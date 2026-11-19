// verdict: ok
// rule: R15
// note: assigning r ends the loan under *r; x still holds what r held
Retarget(a: &i32, b: &i32) {
    var r := a
    x := &*r
    r = b
    Use(x)
    Use(r)
}
