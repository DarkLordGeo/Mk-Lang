// verdict: ok
// note: field-level precision: the per-field call taken apart at once gives r the loan on x alone, so keeping r past the scope of y is accepted; with the clause flattened to `from a, b` it is E0505 (the flattened twin)
struct Pair {
    r: &i32
    s: &i32
}

G(a: &i32, b: &i32): OutOfMemory!Pair from (r: a, s: b) {
    return Pair(r: a, s: b)
}

Caller(): OutOfMemory!void {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        Pair(r, s) := try G(&x, &y)
        Use(s)
        keep = r
    }
    Use(keep)
}
