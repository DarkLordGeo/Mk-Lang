// verdict: reject E0402
// rule: EV536
// rule: R2
// note: arguments evaluate left to right (R13), so the `Res` temporary is live when the `try` exits; E0402 at the statement
Fallible(): OutOfMemory!i32 {
    return 1
}

Both(r: Res, n: i32) {
    Use(&n)
    r.Close()
}

Early(): OutOfMemory!void {
    Both(Acquire("a"), try Fallible()) // <- E0402
}
