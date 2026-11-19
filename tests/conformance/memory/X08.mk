// verdict: reject E0609
// prelude: no
// note: a `fn` literal that uses a local of the enclosing body; the rewrite is a struct plus a `fn`, or a loop
Apply(f: fn(i32): i32): i32 {
    return f(1)
}

Offset(): i32 {
    n: i32 = 3
    return Apply(fn(x: i32): i32 { return x + n }) // <- E0609
}
