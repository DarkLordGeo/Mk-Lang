// verdict: reject E0505
// note: loops join field-wise to a fixpoint; a field written on the back edge holds what the loop's later iterations read
struct Two {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    var keep := &x
    for {
        keep = t.a
        if Cond() {
            break
        }
        y: i32 = 2
        t.a = t.b
        t.b = &y // <- E0505
    }
    Use(keep)
}
