// verdict: ok
// rule: R11
// note: a store into an allocator's own memory drops the allocator's own loans: once Set stores `a into r`, Load's local cur holds a's loan, so the view text, a loan of a, reaches a's memory through it, and a loan of the arena stored into the arena's memory is no store of a into a. The same with a itself as the argument (LoadKeep)
struct R {
    s: str
    t: str
}

Set(a: &Arena into r, r: mut R, rest: str into r): OutOfMemory!void {
    r.s = try a.Dup("x")
    r.t = rest
}

Load(a: &Arena): OutOfMemory!str from a {
    text := try a.Dup("a b c")
    var cur := R(s: "", t: "")
    try Set(a, mut cur, text)
    return cur.s
}

Keep(a: &Arena, rest: str into a) {
}

LoadKeep(a: &Arena): OutOfMemory!str from a {
    text := try a.Dup("a b c")
    Keep(a, text)
    return text
}
