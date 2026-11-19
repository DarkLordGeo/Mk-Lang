// verdict: ok
// note: X12's first unit. `t` is a local struct, so it holds one set per field (R7): `t.a` holds only what `p` gave it, which the automatic source covers, so `return t.a` returns `p`'s loan and is sound, not E0603 at the `return`. Behind a reference a struct still holds one set (tests/conformance/memory/X12.mk)
struct Two {
    a: &i32
    b: &i32
}

Pick(p: &i32): &i32 {
    n: i32 = 1
    t := Two(a: p, b: &n)
    Use(t.b)
    return t.a
}
