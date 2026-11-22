// verdict: reject E0505
// note: twin of SHM-start-ok with a value that is not shallow: a Box1 holds a `mut &i32`, so the walk of `v into out` goes on through b's loan to p, and storing `&short` into p, which outlives short, is E0505. Accepted, p would point at a dead local
struct Box1 {
    r: mut &i32
}

Keep(v: &i32 into out, b: Box1 into out, out: mut List[Box1]): OutOfMemory!void {
    try out.Push(b)
}

Run(sys: &System): OutOfMemory!void {
    long: i32 = 1
    var p := &long
    if Cond() {
        short: i32 = 2
        var ar := try Arena.New(sys.heap)
        defer ar.Free()
        var out := List[Box1].New(&ar)
        try Keep(&short, Box1(r: mut p), mut out) // <- E0505
    }
    Use(p)
}

export Main(sys: &System) {
    Run(sys) else {}
}
