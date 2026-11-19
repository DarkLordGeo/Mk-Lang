// Why: The `tracer` command: a ray tracer and a function plotter writing PPM
// images, and table modes that print every std.math function over its edge
// inputs.
//
// What: Main, the exit codes, the help text, and the commands Render,
// PlotCommand, TableCommand and BitsCommand.
//
// How: Each command parses its options, does its work in an arena, and hands
// the pixels to output.mk; memory and time limits end a run with their own exit
// codes.

import std.math
import std.mem
import src.grid
import src.plot
import src.ppm
import src.render
import src.scene

// Exit statuses: 1 for bad input or a failed write, 2 for a bad command
// line, 3 for memory, 4 for a render stopped by its deadline.
const ExitInput: int = 1
const ExitUsage: int = 2
const ExitMemory: int = 3
const ExitDeadline: int = 4

const Help: str = """
    usage: tracer <command> [options] [arguments]

    commands:
      render SCENE OUT    trace the scene file SCENE into the PPM image OUT
      plot EXPR OUT       plot y = EXPR over x into the PPM image OUT
      table [NAME...]     print each math function over its edge inputs
      bits                print the integer bit operations over edge inputs
      help                print this text

    OUT may be - for standard output.

    options:
      --ascii             write P3 (text) instead of P6
      --16                write 16-bit samples
      --size WxH          image size (plot: 256x160; render: the scene's)
      --samples N         samples per pixel, overriding the scene's
      --band N            rows per render task (default 4)
      --max-time MS       stop a render after MS milliseconds
      --mem-limit BYTES   cap the memory for the scene and its grid
      --from X, --to X    the plotted range of x (default -5 to 5)
      --ymin Y, --ymax Y  the plotted range of y (default: fitted)
      --f32               plot or tabulate in f32 (table: f32 only)
      --f64               tabulate in f64 only
      --seed N            write through a simulated disk with seed N
      --fault F           with --seed, fail the first write with F
                          (nospace or denied)
      --help              print this text

    exit status: 0 done, 1 bad input or failed write, 2 bad command line,
    3 out of memory, 4 stopped by --max-time.
    """

export Main(sys: &System): WriteError!int {
    o := Parse(sys.arena, sys.args[1..]) else e {
        match e {
            Usage(arg, problem) {
                try sys.PrintErr("tracer: argument {arg}: {p}\n", (arg, p: problem.Text()))
            }
            OutOfMemory {
                try sys.PrintErr("tracer: out of memory\n")
                return ExitMemory
            }
        }
        try sys.PrintErr("run tracer --help for usage\n")
        return ExitUsage
    }
    if o.help || o.command == "help" {
        try sys.Print(Help)
        return 0
    }
    want := match o.command {
        "render" | "plot" { 2 }
        "bits" { 0 }
        "table" { o.positional.Len() }
        "" {
            try sys.PrintErr("tracer: no command given\nrun tracer --help for usage\n")
            return ExitUsage
        }
        _ {
            try sys.PrintErr("tracer: unknown command {c}\nrun tracer --help for usage\n", (c: o.command))
            return ExitUsage
        }
    }
    if o.positional.Len() != want {
        try sys.PrintErr("tracer: {c} takes {want} arguments\nrun tracer --help for usage\n", (c: o.command, want))
        return ExitUsage
    }
    return match o.command {
        "render" { try Render(sys, &o) }
        "plot" { try PlotCommand(sys, &o) }
        "table" { try TableCommand(sys, &o) }
        _ { try BitsCommand(sys) }
    }
}

// The image encoding the options choose.
Encoding(o: &Options): ppm.Encoding {
    return if o.ascii { ppm.Encoding.Ascii } else if o.wide { ppm.Encoding.Binary16 } else { ppm.Encoding.Binary8 }
}

// Encodes, reports and writes an image; the status Main returns.
Finish(sys: &System, o: &Options, width: int, height: int, pixels: []f64): WriteError!int {
    file := ppm.Encode(sys.arena, width, height, pixels, Encoding(o)) else {
        try sys.PrintErr("tracer: out of memory\n")
        return ExitMemory
    }
    path := o.positional[1]
    // Gcd reduces the size to its aspect ratio, as 64x48 is 4:3.
    g := math.Gcd(Cast[u64](width), Cast[u64](height))
    var err := sys.Stderr()
    try Print(mut err, "{width}x{height} ({aw}:{ah}), {n} bytes, fnv1a {sum:016x}\n", (width, height, aw: Cast[u64](width) / g, ah: Cast[u64](height) / g, n: file.Len(), sum: ppm.Checksum(file)))
    if seed := o.seed {
        Simulate(sys.arena, seed, o.fault, path, file) else e {
            try sys.PrintErr("tracer: {path}: {e} on the simulated disk\n", (path, e))
            return ExitInput
        }
        try sys.PrintErr("simulated disk: {path} is whole or absent after a crash\n", (path))
        return 0
    }
    Save(sys, sys.arena, path, file) else e {
        try sys.PrintErr("tracer: {path}: {e}\n", (path, e))
        return ExitInput
    }
    return 0
}

// The `render` command.
Render(sys: &System, o: &Options): WriteError!int {
    // With --mem-limit the scene and its grid live in a fixed buffer, so an
    // oversized scene fails cleanly instead of taking the machine's memory.
    limit := o.mem_limit else { 0 }
    buffer := sys.arena.Slice[u8](limit) else {
        try sys.PrintErr("tracer: out of memory\n")
        return ExitMemory
    }
    fixed := mem.Fixed.New(buffer)
    a := if limit > 0 { fixed.Alloc() } else { sys.heap.Alloc() }
    return try RenderIn(sys, a, o, limit)
}

// The rest of `render`, with the scene and its grid in `a`. A function of its
// own, so the scene's stores reach the allocator `a` and stop there, whatever
// buffer a budget's allocator was made over.
RenderIn(sys: &System, a: &Alloc, o: &Options, limit: int): WriteError!int {
    var s := scene.Scene.New(a)
    defer s.Free(a)
    var loader := scene.Loader(file: "", line: 0)
    loader.Load(a, sys.arena, sys.io, o.positional[0], mut s) else e {
        match e {
            OutOfMemory {
                try sys.PrintErr("tracer: {f}: out of memory (--mem-limit {limit})\n", (f: loader.file, limit))
                return ExitMemory
            }
            _ {
            }
        }
        if loader.line > 0 {
            try sys.PrintErr("tracer: {f}:{l}: {e}\n", (f: loader.file, l: loader.line, e))
        } else {
            try sys.PrintErr("tracer: {f}: {e}\n", (f: loader.file, e))
        }
        return ExitInput
    }
    s.samples = o.samples else { s.samples }
    s.width = o.width else { s.width }
    s.height = o.height else { s.height }
    res := if s.grid_res > 0 { s.grid_res } else { grid.AutoResolution(s.tris.Len()) }
    g := grid.Grid.Build(a, s.tris.Items(), res) else e {
        match e {
            OutOfMemory {
                try sys.PrintErr("tracer: grid: out of memory (--mem-limit {limit})\n", (limit))
                return ExitMemory
            }
            _ {
                try sys.PrintErr("tracer: grid: {e}\n", (e))
            }
        }
        return ExitInput
    }
    defer g.Free(a)
    try sys.PrintErr("{sp} spheres, {pl} planes, {tr} triangles, grid {res}^3 with {en} entries, {l} lights\n", (sp: s.spheres.Len(), pl: s.planes.Len(), tr: s.tris.Len(), res, en: g.Entries(), l: s.lights.Len()))
    job := render.Job(scene: &s, grid: &g, frame: render.Setup(&s))
    pixels := sys.arena.Slice[f64](s.width * s.height * 3) else {
        try sys.PrintErr("tracer: out of memory\n")
        return ExitMemory
    }
    done := job.Render(sys.arena, sys.io, pixels, o.band, o.max_time) else {
        try sys.PrintErr("tracer: out of memory\n")
        return ExitMemory
    }
    if !done {
        try sys.PrintErr("tracer: stopped by --max-time\n")
        return ExitDeadline
    }
    return try Finish(sys, o, s.width, s.height, pixels[..])
}

// The `plot` command.
PlotCommand(sys: &System, o: &Options): WriteError!int {
    src := o.positional[0]
    e := plot.Parse(sys.arena, src) else err {
        try sys.PrintErr("tracer: expression: {err}\n  {src}\n", (err, src))
        return ExitInput
    }
    if !(o.to > o.from) {
        try sys.PrintErr("tracer: --to must be above --from\n")
        return ExitUsage
    }
    if !math.IsFinite(o.to - o.from) {
        try sys.PrintErr("tracer: the range from --from to --to is wider than the largest f64\n")
        return ExitUsage
    }
    width := o.width else { 256 }
    height := o.height else { 160 }
    opts := plot.Options(width: width, height: height, from: o.from, to: o.to, low: o.low, high: o.high, single: o.single)
    pixels := sys.arena.Slice[f64](width * height * 3) else {
        try sys.PrintErr("tracer: out of memory\n")
        return ExitMemory
    }
    sum := plot.Plot(sys.arena, &e, &opts, pixels) else err {
        match err {
            OutOfMemory {
                try sys.PrintErr("tracer: out of memory\n")
                return ExitMemory
            }
            _ {
                try sys.PrintErr("tracer: plot: {err}\n", (err))
                return ExitInput
            }
        }
    }
    kind := if o.single { "f32" } else { "f64" }
    try sys.PrintErr("y = {src} in {kind} over [{from}, {to}]: y in [{lo}, {hi}], grid step {st}, {m} samples not finite\n", (src, kind, from: o.from, to: o.to, lo: sum.low, hi: sum.high, st: sum.step, m: sum.missing))
    return try Finish(sys, o, width, height, pixels[..])
}

// The `table` command.
TableCommand(sys: &System, o: &Options): WriteError!int {
    width := if o.single && !o.double { plot.Width.F32 } else if o.double && !o.single { plot.Width.F64 } else { plot.Width.Both }
    for name in &o.positional {
        match plot.Find(*name) {
            .None | .Constant {
                try sys.PrintErr("tracer: table: no function {n}\n", (n: *name))
                return ExitUsage
            }
            _ {
            }
        }
    }
    var out := sys.Stdout()
    plot.Table(sys.arena, mut out, width, o.positional.Items()) else e {
        match e {
            OutOfMemory {
                try sys.PrintErr("tracer: out of memory\n")
                return ExitMemory
            }
            WriteError {
                fail WriteError
            }
        }
    }
    return 0
}

// The `bits` command.
BitsCommand(sys: &System): WriteError!int {
    var out := sys.Stdout()
    try plot.Bits(mut out)
    return 0
}
