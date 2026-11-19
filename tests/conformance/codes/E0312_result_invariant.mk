// verdict: reject E0312
// note: a function returning a struct with invariants of its module checks them when it returns, after its callee
struct Span {
    lo: i32
    hi: i32
    invariant lo <= hi
}

Id(s: Span): Span {
    return s
}

Clamp(s: Span): Span {
    become Id(s) // <- E0312
}
