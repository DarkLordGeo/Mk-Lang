import std.fs

// Accepted: the pointees hold no Cell, view or raw pointer, or there is
// no pointee (std.proc's DefaultCommand shape: an enum whose variant
// would hold a &fs.File, given a variant that holds none).
enum Stdio {
    Inherit
    File(f: &fs.File)
}

struct Cmd {
    args: []str
    stdin: Stdio
    m: ?&Mutex[int]
}

const Default: Cmd = Cmd(args: ["a", "b"], stdin: Stdio.Inherit, m: none)

MakeN(a: &Arena): &int from a {
    n := a.Put(5) else {
        return MakeN(a)
    }
    return &*n
}

const N: &int = MakeN(EvalArena())

export Main(sys: &System) {
    k := *N + Default.args.Len()
    sys.Print("{k}\n", (k)) else {}
}
