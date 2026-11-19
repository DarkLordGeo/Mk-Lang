// Why: the command line of imgproc.
// What: Main, Options, Usage and the path choice (Kernels, ArchName).
// How: parses a command, its files and the operations, as below.

import src.dispatch
import src.kernel
import std.cli
import std.proc

// The command line: a command, then its files, then the operations.
//
//   imgproc run IN OUT OP...     decode IN, apply each OP in order, encode OUT
//   imgproc info IN [OP...]      print the shape and the samples' summary,
//                                after the operations
//
// IN may be synth:WxH, a test pattern made in memory, for timing a pipeline.
//   imgproc compare A B          exit 0 when A and B are the same image, else 1
//
// Exit codes: 0 done, 1 the images differ, 2 a usage error, 3 bad image data,
// 4 a file error, 5 out of memory, 6 timed out or cancelled, 7 the path needs
// a CPU feature this machine lacks.

struct Options {
    /// Print this help and exit
    export help: bool
    /// Kernel path: scalar, v128, w256, w512, v256, v512 or auto (default)
    export path: ?str
    /// Most tile tasks alive at once (default 16)
    export tasks: ?int
    /// Rows per tile (default 32)
    export tile_rows: ?int
    /// Bytes allowed for image samples, intermediate images included
    export mem_limit: ?int
    /// Give up after this many milliseconds
    export timeout_ms: ?int
    /// Write P2 or P3 text instead of binary P5 or P6
    export ascii: bool
    /// Write a P4 bitmap: a sample at most half of maxval is black
    export pbm: bool
    /// Write OUT to a simulated disk instead, failing as asked: none, nospace or denied
    export sim_fault: ?str
    /// The simulated disk's seed (default 1)
    export seed: ?int
    /// The command, its files and its operations
    export words: []str
}

gen cli.Args(Options)

const Usage: str = """
    usage: imgproc [options] run IN OUT [OP...]
           imgproc [options] info IN [OP...]
           imgproc [options] compare A B
    IN and OUT are PGM or PPM files; OUT may be - for standard output.
    IN may be synth:WxH, a colour test pattern made in memory.
    Operations: invert, brighten=D, threshold[=T], equalize, levels,
    blur, sharpen, edge, emboss, box=N, conv=W:W:...[/D][+B],
    mirror, flip, gray, half, double, gain=F, sobel[=l1|max],
    resize=WxH, dilate, erode, swap=bgr|gbr|brg, posterize=B[:G:B], gamma=G

    """

ExitWith(code: int): never {
    proc.Exit(code)
}

export Main(sys: &System): WriteError!void {
    opts := Options.Parse(sys.arena, sys.args[1..]) else e {
        try ReportArgs(sys, e)
        ExitWith(2)
    }
    if opts.help {
        try sys.Print(Usage)
        var out := sys.Stdout()
        try Options.Usage(mut out)
        return
    }
    if opts.words.Len() == 0 {
        try sys.PrintErr(Usage)
        ExitWith(2)
    }
    code := try Command(sys, &opts)
    if code != 0 {
        ExitWith(code)
    }
}

/// Reports a command-line error with the argument it names.
ReportArgs(sys: &System, e: cli.Error): WriteError!void {
    match e {
        cli.Unknown(arg) {
            try sys.PrintErr("imgproc: unknown option {a}\n", (a: sys.args[arg + 1]))
        }
        cli.Invalid(arg) {
            try sys.PrintErr("imgproc: bad value in {a}\n", (a: sys.args[arg + 1]))
        }
        cli.Missing | OutOfMemory {
            try sys.PrintErr("imgproc: {e}\n", (e))
        }
        _ {
            Panic("cli.Error gained a member this match does not handle")
        }
    }
    try sys.PrintErr("run imgproc --help for the options\n")
}

/// Runs the command `opts.words[0]` and returns the exit code.
Command(sys: &System, opts: &Options): WriteError!int {
    cmd := opts.words[0]
    args := opts.words[1..]
    if cmd == "run" {
        if args.Len() < 2 {
            try sys.PrintErr("imgproc: run needs IN and OUT\n")
            return 2
        }
        return try Run(sys, opts, args[0], args[1], args[2..], false)
    }
    if cmd == "info" {
        if args.Len() < 1 {
            try sys.PrintErr("imgproc: info needs a file\n")
            return 2
        }
        return try Run(sys, opts, args[0], "", args[1..], true)
    }
    if cmd == "compare" {
        if args.Len() != 2 {
            try sys.PrintErr("imgproc: compare needs two files\n")
            return 2
        }
        return try Compare(sys, opts, args[0], args[1])
    }
    try sys.PrintErr("imgproc: unknown command {cmd}\n", (cmd))
    return 2
}

/// The architecture as a toolchain names it, for messages.
ArchName(a: Arch): str {
    return match a {
        Arch.X64 { "x86_64" }
        Arch.Arm64 { "aarch64" }
        Arch.Riscv64 { "riscv64" }
        _ { "unknown" }
    }
}

/// The kernel table `--path` names, or an exit code.
Kernels(sys: &System, opts: &Options): WriteError!(k: kernel.Kernels, code: int) from sys, opts {
    name := opts.path else { "auto" }
    if name == "auto" {
        return (k: dispatch.Auto(), code: 0)
    }
    if name == "scalar" {
        return (k: kernel.Scalar(), code: 0)
    }
    if name == "v128" {
        return (k: kernel.V128(), code: 0)
    }
    if name == "w256" {
        return (k: kernel.W256(), code: 0)
    }
    if name == "w512" {
        return (k: kernel.W512(), code: 0)
    }
    if name == "v256" || name == "v512" {
        found := if name == "v256" { dispatch.V256() } else { dispatch.V512() }
        if k := found {
            return (k: k, code: 0)
        }
        try sys.PrintErr("imgproc: this {arch} CPU cannot run the {name} path\n", (arch: ArchName(Target.arch), name))
        return (k: kernel.Scalar(), code: 7)
    }
    try sys.PrintErr("imgproc: unknown path {name}\n", (name))
    return (k: kernel.Scalar(), code: 2)
}
