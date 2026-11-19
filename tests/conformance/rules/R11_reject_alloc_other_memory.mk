// verdict: reject E0604
// rule: R11
// note: only the allocator's own loans are dropped from a store into its memory: a view made in another arena b stored into a's memory is a store of b into a, and a node standing for two arenas (Ctx's a and b) keeps the store of one into the other's memory, c into c
struct R {
    s: str
    t: str
}

struct Ctx {
    a: &Arena
    b: &Arena
}

Set(a: &Arena into r, r: mut R, rest: str into r): OutOfMemory!void {
    r.s = try a.Dup("x")
    r.t = rest
}

Other(a: &Arena, b: &Arena): OutOfMemory!void {
    text := try b.Dup("a b c")
    var cur := R(s: "", t: "")
    try Set(a, mut cur, text) // <- E0604
}

Both(c: Ctx): OutOfMemory!str from c {
    text := try c.b.Dup("a b c")
    var cur := R(s: "", t: "")
    try Set(c.a, mut cur, text) // <- E0604
    return cur.s
}
