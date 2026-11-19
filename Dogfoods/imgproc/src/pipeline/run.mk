// Why: an operation runs over large images in parallel.
// What: Apply, Job, Settings, Tile and the TimedOut and ScratchFull errors.
// How: the output is cut into runs of rows with ChunksMut, one task each,
// under a scope whose Limits cap the live tasks. Each task takes scratch
// from a mem.Fixed on its own stack, checks the deadline and cancellation
// before each row, and leaves its failure in a slot of its own.

import src.kernel
import src.pnm
import std.math
import std.mem
import std.time

/// How a run is carried out.
export struct Settings {
    /// The kernels of the chosen path
    export kernels: kernel.Kernels
    /// The most tasks alive at once
    export tasks: int
    /// Rows per tile
    export tile_rows: int
    /// When the run started, for `budget_ms`
    export start: time.Instant
    /// The time allowed in milliseconds, or -1 for no limit
    export budget_ms: i64
}

/// The run passed its time budget.
export error TimedOut(ms: i64)
/// A tile's scratch did not fit its buffer.
export error ScratchFull(bytes: int)

export type TileError = TimedOut | ScratchFull | Canceled

export type RunError = TimedOut | ScratchFull | Canceled | OutOfMemory

/// What one tile computes: the operation with its parameters fixed.
struct Job {
    kind: OpKind
    kernels: kernel.Kernels
    frame: kernel.Frame
    filter: kernel.Kernel
    lut: [256]u8
    level: u8
    delta: i32
    shifts: [3]u8
    out_row: int
    out_height: int
    start: time.Instant
    budget_ms: i64
}

/// The bytes of a tile's scratch buffer.
const ScratchBytes: int = 1024

/// Fails when the run is past its budget or its scope was cancelled.
Checkpoint(io: Io, job: &Job): TimedOut|Canceled!void {
    if job.budget_ms >= 0 && time.Now(io).Since(job.start).Nanos() >= job.budget_ms * 1_000_000 {
        fail TimedOut(ms: job.budget_ms)
    }
    try io.Yield()
}

/// Runs `job` over the rows of `dst` from `y0`, with a scratch row of
/// `job.filter.size` values from a mem.Fixed over a buffer on this task's
/// stack, reset when the rows are done.
Rows(io: Io, job: &Job, y0: int, dst: mut []u8): TileError!void {
    var buf: [ScratchBytes]u8 = [0; ScratchBytes]
    var fixed := mem.Fixed.New(mut buf[..])
    a := fixed.Alloc()
    var scratch := HeapList[f32].New(a)
    var failed: ?TileError = none
    for i in 0..job.filter.size {
        _ = i
        scratch.Push(a, 0.0) else {
            failed = some(ScratchFull(bytes: ScratchBytes))
            break
        }
    }
    if failed.IsNone() {
        RowsWith(io, job, y0, dst, scratch.ItemsMut()) else e {
            failed = some(e)
        }
    }
    scratch.Free(a)
    fixed.Reset()
    if e := failed {
        fail e
    }
}

/// The source row `y` of `job`'s frame.
SourceRow(job: &Job, y: int): []u8 {
    row := job.frame.Row()
    return job.frame.samples[y * row..(y + 1) * row]
}

RowsWith(io: Io, job: &Job, y0: int, dst: mut []u8, scratch: mut []f32): TimedOut|Canceled!void {
    maxval := Trunc[u8](job.frame.maxval)
    for dy in 0..dst.Len() / job.out_row {
        try Checkpoint(io, job)
        y := y0 + dy
        out := mut dst[dy * job.out_row..(dy + 1) * job.out_row]
        match job.kind {
            .Invert {
                job.kernels.invert(SourceRow(job, y), out, maxval)
            }
            .Brighten {
                job.kernels.brighten(SourceRow(job, y), out, job.delta, maxval)
            }
            .Threshold {
                job.kernels.threshold(SourceRow(job, y), out, job.level, maxval)
            }
            .Equalize | .Levels {
                job.kernels.lut(SourceRow(job, y), out, &job.lut)
            }
            .Convolve {
                job.kernels.convolve(&job.frame, &job.filter, y, out, scratch)
            }
            .Mirror {
                job.kernels.mirror(SourceRow(job, y), out, job.frame.channels)
            }
            .Flip {
                out.CopyFrom(SourceRow(job, job.frame.height - 1 - y))
            }
            .Gray {
                if job.frame.channels == 1 {
                    out.CopyFrom(SourceRow(job, y))
                } else {
                    job.kernels.gray(SourceRow(job, y), out)
                }
            }
            .Half {
                bottom := math.Min(2 * y + 1, job.frame.height - 1)
                job.kernels.half(SourceRow(job, 2 * y), SourceRow(job, bottom), out, job.frame.channels)
            }
            .Double {
                job.kernels.double(SourceRow(job, y / 2), out, job.frame.channels)
            }
            .Gain {
                job.kernels.gain(SourceRow(job, y), out, Trunc[u16](job.delta), maxval)
            }
            .Sobel {
                job.kernels.sobel(&job.frame, y, out, int(job.delta))
            }
            .Resize {
                job.kernels.resize(&job.frame, y, job.out_height, out)
            }
            .Dilate {
                job.kernels.morph(&job.frame, y, out, true)
            }
            .Erode {
                job.kernels.morph(&job.frame, y, out, false)
            }
            .Swap {
                job.kernels.swap(SourceRow(job, y), out, int(job.delta))
            }
            .Posterize {
                job.kernels.posterize(SourceRow(job, y), out, &job.shifts, job.frame.channels)
            }
            .Gamma {
                job.kernels.lut(SourceRow(job, y), out, &job.lut)
            }
        }
    }
}

/// A tile task: runs its rows, and on failure keeps the error in `slot` and
/// cancels the other tiles.
Tile(s: &Scope, io: Io, job: &Job, y0: int, dst: mut []u8, slot: mut []?TileError) {
    Rows(io, job, y0, dst) else e {
        slot[0] = some(e)
        s.Cancel()
    }
}

/// Applies `op` to `src`, writing `dst`, which has the shape `OutShape` gives. Fails with
/// the first tile's error, a timeout before a cancellation. O(n) work for a
/// point operation, O(n * size^2) for a convolution; allocates one slot per
/// tile in `a`.
export Apply(a: &Arena, io: Io, s: &Settings, op: &Op, src: &pnm.Image, dst: mut pnm.Image): RunError!void {
    frame := kernel.Frame(
        samples: src.Samples(),
        width: src.width,
        height: src.height,
        channels: src.channels,
        maxval: src.maxval,
    )
    var lut: [256]u8 = [0; 256]
    var level: u8 = 0
    if op.kind == OpKind.Equalize || op.kind == OpKind.Levels || op.kind == OpKind.Threshold {
        h := Histogram(frame.samples)
        if op.kind == OpKind.Equalize {
            lut = EqualizeLut(&h, src.maxval)
        } else if op.kind == OpKind.Levels {
            lut = LevelsLut(&h, src.maxval)
        } else {
            level = Trunc[u8](if op.amount < 0 { Otsu(&h, src.maxval) } else { op.amount })
        }
    }
    if op.kind == OpKind.Gamma {
        s.kernels.gamma(1000.0 / Cast[f32](op.amount), src.maxval, mut lut[..])
    }
    job := Job(
        kind: op.kind,
        kernels: s.kernels,
        frame: frame,
        filter: op.filter,
        lut: lut,
        level: level,
        delta: Trunc[i32](op.amount),
        shifts: [Trunc[u8](op.args[0]), Trunc[u8](op.args[1]), Trunc[u8](op.args[2])],
        out_row: dst.RowBytes(),
        out_height: dst.height,
        start: s.start,
        budget_ms: s.budget_ms,
    )
    if op.kind == OpKind.Flip {
        try FlipHalves(io, &job, dst)
        return
    }
    row := dst.RowBytes()
    rows := math.Max(s.tile_rows, 1)
    tiles := (dst.height + rows - 1) / rows
    var slots := try a.Slice[?TileError](tiles)
    stack := if op.filter.size > 7 { StackSize.Large } else { StackSize.Default }
    scope sc := io.ScopeWith(Limits(tasks: s.tasks, stack: stack)) {
        var places := slots.ChunksMut(1)
        var y0 := 0
        for piece in dst.SamplesMut().ChunksMut(rows * row) {
            slot := places.Next() else {
                break
            }
            sc.go Tile(&sc, io, &job, y0, piece, slot)
            y0 += rows
        }
    }
    var canceled := false
    for r in slots {
        if e := r {
            match e {
                TimedOut(ms) {
                    fail TimedOut(ms: ms)
                }
                ScratchFull(bytes) {
                    fail ScratchFull(bytes: bytes)
                }
                Canceled {
                    canceled = true
                }
            }
        }
    }
    if canceled {
        fail Canceled
    }
}

/// The vertical flip as two tasks: SplitAt cuts the output at its middle
/// row, and each half takes its rows from the other end of the source.
FlipHalves(io: Io, job: &Job, dst: mut pnm.Image): TimedOut|Canceled!void {
    middle := dst.height / 2
    (left: top, right: bottom) := dst.SamplesMut().SplitAt(middle * job.out_row)
    var first: ?TileError = none
    var second: ?TileError = none
    scope sc := io.Scope() {
        sc.go FlipTask(io, job, 0, top, mut first)
        sc.go FlipTask(io, job, middle, bottom, mut second)
    }
    for r in [first, second] {
        if e := r {
            match e {
                TimedOut(ms) {
                    fail TimedOut(ms: ms)
                }
                Canceled | ScratchFull {
                    fail Canceled
                }
            }
        }
    }
}

FlipTask(io: Io, job: &Job, y0: int, dst: mut []u8, failed: mut ?TileError) {
    // a flip reads no scratch
    var unused: [1]f32 = [0.0]
    RowsWith(io, job, y0, dst, mut unused[..]) else e {
        *failed = some(e)
    }
}
