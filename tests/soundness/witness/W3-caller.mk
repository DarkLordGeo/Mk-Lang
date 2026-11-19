// verdict: reject E0505
// note: at the caller: a field the clause leaves out holds every source the clause names, here b alone, so the call's result gives s the set of b, the loan on y; given nothing, s would let keep outlive y (M-F8-W3-caller-union). W3.mk guards the callee's side only
struct Pair {
    r: &i32
    s: &i32
}

F(a: &i32, b: &i32): Pair from (r: b) {
    return Pair(r: b, s: b)
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        Pair(r, s) := F(&x, &y)
        Use(r)
        keep = s // <- E0505
    }
    Use(keep)
}
