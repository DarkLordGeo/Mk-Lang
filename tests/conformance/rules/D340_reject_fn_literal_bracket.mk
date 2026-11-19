// verdict: reject E0101
// rule: D-340
// note: in a bracket argument `fn(` begins a fn type, so a literal's body there is a syntax error at its `{`, capturing or not
struct MinHeap[T: Plain, const less: fn(&T, &T): bool] {
    items: List[T]
}

MinHeap[T, less].New(a: &Arena): MinHeap[T, less] {
    return MinHeap[T, less](items: List[T].New(a))
}

Run(a: &Arena) {
    h := MinHeap[i64, fn(x: &i64, y: &i64): bool { return *x < *y }].New(a) // <- E0101
    _ = h
}
