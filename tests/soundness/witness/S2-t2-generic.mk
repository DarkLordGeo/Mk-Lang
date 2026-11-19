// verdict: reject E0505
// note: a callee whose parameter is a type parameter: `mut t` to it makes t whole, as every exclusive whole borrow does, whatever the callee, so t.a holds y; M-RV2-1-excl-collapse flips this file
struct Two {
    a: &i32
    b: &i32
}

Touch[T](x: mut T) {
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        t.b = &y // <- E0505
        Touch(mut t)
    }
    Use(t.a)
}
