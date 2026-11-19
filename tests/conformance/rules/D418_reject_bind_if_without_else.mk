// verdict: reject E0311
// rule: D-418
// note: an if v := w and an if T is X in value position need an else like any if
Unwrap(w: ?int): int {
    x := if v := w { v } // <- E0311
    return x
}

Size[T](v: T): int {
    _ = v
    n := if T is int { 8 } // <- E0311
    return n
}
