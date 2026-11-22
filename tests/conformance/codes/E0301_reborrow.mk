// verdict: reject E0301
// note: a `mut T` argument for a `&T` parameter has no conversion; the diagnostic names the shared reborrow `&*n` and offers the fix that inserts it, compiles rather than proven, since outside a `return` a later use can conflict with the new shared loan
struct Node {
    v: int
}

Read(n: &Node): int {
    return n.v
}

Bump(n: mut Node): int {
    n.v += 1
    return Read(n) // <- E0301
}
