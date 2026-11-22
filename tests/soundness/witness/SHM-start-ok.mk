// verdict: ok
// note: a call that stores views of `sys.args` (a shallow `str` value) and an arena into one list: the str loans are marked shallow where they land, and the store walk of the call's other clause does not start from them, as a later call's walk does not hop through them. Rejected, the arena is E0505 into `*sys`, though nothing can be written through a str; the deep twin is SHM-start-deep
Keep(a: &Arena into out, names: []str, out: mut List[str]): OutOfMemory!void *names into out {
    for n in names {
        try out.Push(try a.Dup(n))
    }
}

KeepT[T](a: &Arena into out, names: []T, out: mut List[T]): void *names into out {
}

Run(sys: &System): OutOfMemory!int {
    var arena := try Arena.New(sys.heap)
    defer arena.Free()
    a := &arena
    var out := List[str].New(a)
    try Keep(a, sys.args[1..], mut out)
    KeepT(a, sys.args[1..], mut out)
    return out.Len()
}

export Main(sys: &System): int {
    return Run(sys) else { 2 }
}
