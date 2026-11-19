// verdict: reject E0501
// note: a mut-self call with explicit generic arguments, s.Flip[0](), inside `for x in s.buf` over a reassignable mut []int place makes the loop hold a shared view, so the call conflicts with it. If lower's reassigned() saw only an x.M callee, the program would be accepted, and under `mk run -release` the loop would read past the shorter slice swapped in. Acceptance means the hole reopened (tests/conformance/rules/CICE2_reject_loop_mut_generic_method.mk)
struct S {
    buf: mut []int
    alt: mut []int
}

S.Flip[const K: int](mut self) {
    Swap(mut self.buf, mut self.alt)
}

Seen(s: mut S, out: mut []int): int {
    var n := 0
    var first := true
    for x in s.buf {
        if first {
            s.Flip[0]() // <- E0501
            first = false
        }
        out[n] = x
        n = n + 1
    }
    return n
}

export Main(sys: &System): WriteError!void {
    var big := [1, 1, 1, 1, 1, 1, 1, 1]
    var small := [100]
    var canary := [7000000, 7000001, 7000002, 7000003, 7000004, 7000005, 7000006, 7000007]
    var out := [0, 0, 0, 0, 0, 0, 0, 0]
    var s := S(buf: mut big[..], alt: mut small[..])
    n := Seen(mut s, mut out[..])
    try sys.Print("read {n}: {a} {b} {c} {d} {e} {f} {g} {h} canary {k}\n", (n: n, a: out[0], b: out[1], c: out[2], d: out[3], e: out[4], f: out[5], g: out[6], h: out[7], k: canary[0]))
}
