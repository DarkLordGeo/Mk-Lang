// verdict: reject E0505
// note: `*q into out` with q's referent a shallow `[]str`, but `c into q` chains a `mut &i32` into it in the same call: the loan read through q keeps the deep mark it came with, so the walk of `v into out` starts from it and reaches p. Accepted, p would point at a dead local
Keep(v: &i32 into out, c: mut &i32 into q, q: mut []str, out: mut List[str]): void *q into out {
}

Run(sys: &System): OutOfMemory!void {
    long: i32 = 1
    var p := &long
    if Cond() {
        short: i32 = 2
        var ar := try Arena.New(sys.heap)
        defer ar.Free()
        var out := List[str].New(&ar)
        var xs: [2]str = ["a", "b"]
        Keep(&short, mut p, mut xs[..], mut out) // <- E0505
    }
    Use(p)
}

export Main(sys: &System) {
    Run(sys) else {}
}
