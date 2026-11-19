// cli, Mk: gen cli.Args(Opts)'s Parse over one generated argument vector
// of n arguments (README.md), parsed reps times. Usage: cli <n> <reps>.
// Prints the checksum. The options come first and the positionals after
// them, one contiguous run, so Parse allocates nothing (the files are a
// view of the vector).

import std.cli

struct Opts {
    export verbose: bool
    export count: int
    export name: str
    export level: ?int
    export tag: ?str
    export files: []str
}

gen cli.Args(Opts)

export Main(sys: &System): OutOfMemory|ParseError|WriteError|cli.Error!void {
    n := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    a := sys.arena
    var l := List[str].New(a)
    for c in 0..n / 10 {
        try l.Push("--verbose")
        try l.Push(try a.Fmt("--count={c}", (c: c)))
        try l.Push("--name")
        try l.Push(try a.Fmt("n{c}", (c: c)))
        try l.Push("--level")
        try l.Push(try a.Fmt("{v}", (v: c * 3)))
        try l.Push(try a.Fmt("--tag=t{c}", (c: c)))
    }
    var j := 0
    for l.Len() < n {
        try l.Push(try a.Fmt("f{j}", (j: j)))
        j += 1
    }
    argv := l.Done()[..]
    var total: u64 = 0
    for r in 0..reps {
        o := try Opts.Parse(a, argv)
        level := o.level else { 0 }
        tag := o.tag else { "" }
        var s := o.count + level + o.name.Len() + tag.Len() + o.files.Len()
        if o.verbose {
            s += 1
        }
        if o.files.Len() > 0 {
            s += o.files[r % o.files.Len()].Len()
        }
        total = total +% Cast[u64](s)
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total: total))
}
