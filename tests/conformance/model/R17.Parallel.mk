// verdict: ok
// case: R17.Parallel
// note: `for x in xs` copies each element, so the body reads `x`, not `*x`
Sum(xs: []i32, out: mut i64) {
    for x in xs {
        *out += i64(x)
    }
}

Parallel(io: Io, a: []i32, b: []i32): i64 {
    var left: i64 = 0
    var right: i64 = 0
    scope s := io.Scope() {
        s.go Sum(a, mut left)
        s.go Sum(b, mut right)
    }
    return left + right
}
