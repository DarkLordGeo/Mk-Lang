// verdict: reject E1103
// rule: R19
// note: field access never auto-dereferences a raw pointer outside unsafe
Get(p: *Point): i32 {
    return p.x // <- E1103
}
