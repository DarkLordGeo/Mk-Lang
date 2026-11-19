// verdict: ok
// rule: EV535
// note: `var` on a by-value parameter permits `mut` borrows and assignment
Bump(var p: Point): i32 {
    UseMut(mut p)
    p.x = 1
    return p.x
}
