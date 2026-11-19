// Why: run, info and compare, each with its own error reporting.
// What: Run, RunIn, PrintSummary, Compare, Load and the Report* helpers.
// How: each command reports its failures on standard error and returns the
// exit code; only a failed write to the terminal leaves as an error.

import src.kernel
import src.pipeline
import src.pnm
import std.fs
import std.math
import std.mem
import std.sim
import std.simd
import std.time

/// Reads and decodes `path` into `a`, or makes the test pattern that
/// `synth:WxH` names, or reports why not and returns the exit code.
Load(sys: &System, a: &Alloc, path: str): WriteError!(img: ?pnm.Image, code: int) from a {
    if path.StartsWith("synth:") {
        return try LoadSynthetic(sys, a, path)
    }
    data := fs.ReadBytes(sys.arena, sys.io, path) else e {
        try sys.PrintErr("imgproc: cannot read {path}: {e}\n", (path, e))
        code := match e {
            OutOfMemory { 5 }
            _ { 4 }
        }
        return (img: none, code: code)
    }
    img := pnm.Decode(a, data) else e {
        try ReportDecode(sys, path, e)
        code := match e {
            OutOfMemory { 5 }
            _ { 3 }
        }
        return (img: none, code: code)
    }
    return (img: some(img), code: 0)
}

/// The test pattern `synth:WxH` names, or the exit code of why not.
LoadSynthetic(sys: &System, a: &Alloc, path: str): WriteError!(img: ?pnm.Image, code: int) from a {
    size := pipeline.ParseSize(path[6..]) else {
        try sys.PrintErr("imgproc: {path}: a test pattern is synth:WxH, each side from 1 to {m}\n", (path, m: pnm.MaxSide))
        return (img: none, code: 2)
    }
    if size[0] == 0 || size[1] == 0 || size[0] * size[1] * 3 > pnm.MaxSamples {
        try sys.PrintErr("imgproc: {path}: a test pattern is synth:WxH, each side from 1 to {m}\n", (path, m: pnm.MaxSide))
        return (img: none, code: 2)
    }
    img := pnm.Synthetic(a, size[0], size[1]) else {
        try sys.PrintErr("imgproc: out of memory making {path}\n", (path))
        return (img: none, code: 5)
    }
    return (img: some(img), code: 0)
}

ReportDecode(sys: &System, path: str, e: pnm.Error): WriteError!void {
    match e {
        pnm.BadMagic {
            try sys.PrintErr("imgproc: {path}: not a PGM or PPM file\n", (path))
        }
        pnm.BadHeader(offset) {
            try sys.PrintErr("imgproc: {path}: bad header field at byte {offset}\n", (path, offset))
        }
        pnm.BadSample(offset) {
            try sys.PrintErr("imgproc: {path}: bad sample at byte {offset}\n", (path, offset))
        }
        pnm.Truncated(expected, got) {
            try sys.PrintErr("imgproc: {path}: truncated after {got} of {expected} samples\n", (path, got, expected))
        }
        pnm.WideSamples(maxval) {
            try sys.PrintErr("imgproc: {path}: maxval {maxval} needs 16-bit samples, which imgproc does not read\n", (path, maxval))
        }
        pnm.TooLarge(width, height) {
            try sys.PrintErr("imgproc: {path}: {width}x{height} is larger than imgproc accepts\n", (path, width, height))
        }
        OutOfMemory {
            try sys.PrintErr("imgproc: out of memory decoding {path}\n", (path))
        }
    }
}

/// Reports why an operation did not parse.
ReportOp(sys: &System, text: str, e: pipeline.OpError): WriteError!void {
    match e {
        pipeline.UnknownOp {
            try sys.PrintErr("imgproc: unknown operation {text}\n", (text))
        }
        pipeline.BadArgument {
            try sys.PrintErr("imgproc: bad argument in {text}\n", (text))
        }
        pipeline.BadKernel {
            try sys.PrintErr("imgproc: {text}: a kernel is an odd square of integer weights, at most 15 by 15\n", (text))
        }
        pipeline.HeavyKernel {
            try sys.PrintErr("imgproc: {text}: the weights' magnitudes add up past 65793, so a sum could lose precision\n", (text))
        }
        OutOfMemory {
            try sys.PrintErr("imgproc: out of memory parsing {text}\n", (text))
        }
    }
}

/// Reports why an operation stopped, and returns the exit code.
ReportRun(sys: &System, e: pipeline.RunError): WriteError!int {
    match e {
        pipeline.TimedOut(ms) {
            try sys.PrintErr("imgproc: timed out after {ms} ms\n", (ms))
            return 6
        }
        Canceled {
            try sys.PrintErr("imgproc: cancelled\n")
            return 6
        }
        pipeline.ScratchFull(bytes) {
            try sys.PrintErr("imgproc: a tile's scratch does not fit in {bytes} bytes\n", (bytes))
            return 5
        }
        OutOfMemory {
            try sys.PrintErr("imgproc: out of memory\n")
            return 5
        }
    }
}

/// `run IN OUT OP...`, or `info IN OP...` when `summary` is set, which
/// prints the result's summary instead of writing it.
Run(sys: &System, opts: &Options, input: str, output: str, ops: []str, summary: bool): WriteError!int {
    var parsed := List[pipeline.Op].New(sys.arena)
    for text in ops {
        op := pipeline.ParseOp(sys.arena, text) else e {
            try ReportOp(sys, text, e)
            return 2
        }
        parsed.Push(op) else {
            try sys.PrintErr("imgproc: out of memory\n")
            return 5
        }
    }
    (k, code) := try Kernels(sys, opts)
    if code != 0 {
        return code
    }
    if opts.pbm && opts.ascii {
        try sys.PrintErr("imgproc: --pbm writes binary P4 and cannot be --ascii\n")
        return 2
    }
    if fault := opts.sim_fault {
        if fault != "none" && fault != "nospace" && fault != "denied" || output == "-" {
            try sys.PrintErr("imgproc: --sim-fault takes none, nospace or denied, and a file for OUT\n")
            return 2
        }
    }
    limit := opts.mem_limit else { 0 }
    if limit < 0 {
        try sys.PrintErr("imgproc: --mem-limit must not be negative\n")
        return 2
    }
    budget := sys.arena.Slice[u8](limit) else {
        try sys.PrintErr("imgproc: cannot reserve {limit} bytes for images\n", (limit))
        return 5
    }
    fixed := mem.Fixed.New(budget)
    a := if limit > 0 { fixed.Alloc() } else { sys.heap.Alloc() }
    return try RunIn(sys, a, opts, input, output, parsed.Items(), k, summary, limit)
}

// The rest of `Run`, with the images in `a`. A function of its own, so the
// images' stores reach the allocator `a` and stop there, whatever buffer a
// budget's allocator was made over.
RunIn(sys: &System, a: &Alloc, opts: &Options, input: str, output: str, parsed: []pipeline.Op, k: kernel.Kernels, summary: bool, limit: int): WriteError!int {
    (img: loaded, code: load_code) := try Load(sys, a, input)
    var img := loaded else {
        return load_code
    }
    defer img.Free(a)
    var spare := img.Like(a) else {
        try sys.PrintErr("imgproc: out of memory: the images need more than --mem-limit {limit}\n", (limit))
        return 5
    }
    defer spare.Free(a)
    settings := pipeline.Settings(
        kernels: k,
        tasks: opts.tasks else { 16 },
        tile_rows: opts.tile_rows else { 32 },
        start: time.Now(sys.io),
        budget_ms: Cast[i64](opts.timeout_ms else { -1 }),
    )
    if settings.tasks < 1 || settings.tile_rows < 1 {
        try sys.PrintErr("imgproc: --tasks and --tile-rows must be at least 1\n")
        return 2
    }
    for op in parsed {
        if why := pipeline.Unsupported(&op, img.channels) {
            try sys.PrintErr("imgproc: {why}\n", (why))
            return 2
        }
        shape := pipeline.OutShape(&op, img.width, img.height, img.channels)
        if shape.width > pnm.MaxSide || shape.height > pnm.MaxSide || shape.width * shape.height * shape.channels > pnm.MaxSamples {
            try sys.PrintErr("imgproc: the result would be {w}x{h}, larger than imgproc makes\n", (w: shape.width, h: shape.height))
            return 2
        }
        if spare.width != shape.width || spare.height != shape.height || spare.channels != shape.channels {
            fresh := pnm.Image.New(a, shape.width, shape.height, shape.channels, img.maxval) else {
                try sys.PrintErr("imgproc: out of memory: a {w}x{h} image does not fit\n", (w: shape.width, h: shape.height))
                return 5
            }
            old := Replace(mut spare, fresh)
            old.Free(a)
        }
        pipeline.Apply(sys.arena, sys.io, &settings, &op, &img, mut spare) else e {
            return try ReportRun(sys, e)
        }
        Swap(mut img, mut spare)
    }
    if summary {
        try PrintSummary(sys, input, &img)
        return 0
    }
    if opts.pbm && img.channels != 1 {
        try sys.PrintErr("imgproc: --pbm needs a gray image; put gray or threshold last\n")
        return 2
    }
    bytes := EncodeAs(sys.arena, &img, opts) else {
        try sys.PrintErr("imgproc: out of memory encoding {output}\n", (output))
        return 5
    }
    if fault := opts.sim_fault {
        return try WriteSimulated(sys, opts.seed else { 1 }, fault, output, bytes)
    }
    if output == "-" {
        var out := sys.Stdout()
        try out.Bytes(bytes)
        return 0
    }
    fs.WriteAll(sys.io, output, bytes) else e {
        try sys.PrintErr("imgproc: cannot write {output}: {e}\n", (output, e))
        return 4
    }
    return 0
}

/// `img` in the form the options ask for: P4, P2 or P3, or P5 or P6.
EncodeAs(a: &Arena, img: &pnm.Image, opts: &Options): OutOfMemory![]u8 from a {
    if opts.pbm {
        return try pnm.EncodeBitmap(a, img)
    }
    return try pnm.Encode(a, img, opts.ascii)
}

/// Writes `bytes` to `output` on a simulated disk seeded with `seed` whose
/// next write fails with `fault` (`nospace` or `denied`), or none (`none`),
/// and reports as a real write would: a test of the error path that needs no
/// full disk. Returns the exit code.
WriteSimulated(sys: &System, seed: int, fault: str, output: str, bytes: []u8): WriteError!int {
    var disk := sim.Sim.New(sys.arena, Cast[u64](seed)) else {
        try sys.PrintErr("imgproc: out of memory making the simulated disk\n")
        return 5
    }
    defer disk.Free()
    if fault == "nospace" {
        disk.FailNext(sim.Op.Write, sim.Fault.NoSpace)
    } else if fault == "denied" {
        disk.FailNext(sim.Op.Write, sim.Fault.Denied)
    }
    fs.WriteAll(disk.Io(), output, bytes) else e {
        try sys.PrintErr("imgproc: cannot write {output} (simulated disk, seed {seed}): {e}\n", (output, seed, e))
        return 4
    }
    back := fs.ReadBytes(sys.arena, disk.Io(), output) else e {
        try sys.PrintErr("imgproc: cannot read {output} back (simulated disk, seed {seed}): {e}\n", (output, seed, e))
        return 4
    }
    if !simd.Equal(back, bytes) {
        try sys.PrintErr("imgproc: {output} reads back different (simulated disk, seed {seed})\n", (output, seed))
        return 4
    }
    try sys.PrintErr("imgproc: wrote {n} bytes to {output} (simulated disk, seed {seed})\n", (n: bytes.Len(), output, seed))
    return 0
}

/// Prints the shape of `img`, then its darkest, brightest and mean sample
/// and the bits it uses, as `info` does.
PrintSummary(sys: &System, input: str, img: &pnm.Image): WriteError!void {
    s := kernel.Summarize(img.Samples(), Trunc[u8](img.maxval))
    row := img.RowBytes()
    mean := Cast[f64](s.sum) / Cast[f64](img.Samples().Len())
    try sys.Print("{input}: {magic} {w}x{h}, {c} channel(s), maxval {m}\n", (input, magic: img.Magic(), w: img.width, h: img.height, c: img.channels, m: img.maxval))
    try sys.Print("samples: min {s.min} max {s.max} mean {mean:.3} deviation {s.spread:.3}\n", (s, mean))
    try sys.Print("brightest: {s.at_max} samples, the first at x {x} y {y}\n", (s, x: s.first_max % row / img.channels, y: s.first_max / row))
    try sys.Print("bits used {s.bits_or:08b}, always set {s.bits_and:08b}; xor {s.xor:02x}, sum32 {s.sum32:08x}\n", (s))
    if s.binary {
        try sys.Print("binary: every sample is 0 or {m}\n", (m: img.maxval))
    }
}

/// `compare A B`: 0 when the two have one shape and the same samples, else
/// 1 with where they first differ and by how much.
Compare(sys: &System, opts: &Options, left: str, right: str): WriteError!int {
    _ = opts
    a := sys.heap.Alloc()
    (img: first, code: c1) := try Load(sys, a, left)
    x := first else {
        return c1
    }
    defer x.Free(a)
    (img: second, code: c2) := try Load(sys, a, right)
    y := second else {
        return c2
    }
    defer y.Free(a)
    if x.width != y.width || x.height != y.height || x.channels != y.channels {
        try sys.Print("differ: {l} is {lw}x{lh}x{lc}, {r} is {rw}x{rh}x{rc}\n", (l: left, lw: x.width, lh: x.height, lc: x.channels, r: right, rw: y.width, rh: y.height, rc: y.channels))
        return 1
    }
    xs := x.Samples()
    ys := y.Samples()
    if simd.Equal(xs, ys) {
        try sys.Print("identical\n")
        return 0
    }
    var first_diff := 0
    for xs[first_diff] == ys[first_diff] {
        first_diff += 1
    }
    var count := 0
    var worst := 0
    var squares: f64 = 0.0
    for i in 0..xs.Len() {
        d := math.Max(int(xs[i]), int(ys[i])) - math.Min(int(xs[i]), int(ys[i]))
        if d != 0 {
            count += 1
            worst = math.Max(worst, d)
            squares += Cast[f64](d * d)
        }
    }
    rmse := math.Sqrt(squares / Cast[f64](xs.Len()))
    order := if simd.Compare(xs, ys) < 0 { "darker" } else { "brighter" }
    row := x.RowBytes()
    try sys.Print("differ: {n} of {total} samples, largest by {worst}, rms {rmse:.4}\n", (n: count, total: xs.Len(), worst, rmse))
    try sys.Print("first at x {px} y {py} channel {ch}: {l} is {order}\n", (px: first_diff % row / x.channels, py: first_diff / row, ch: first_diff % x.channels, l: left, order))
    return 1
}
