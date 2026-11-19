// verdict: ok
// rule: EV536
// rule: R2
// note: the fallible call runs first, so no linear temporary is live at its error exit
Fallible(): OutOfMemory!i32 {
    return 1
}

Both(r: Res, n: i32) {
    Use(&n)
    r.Close()
}

Early(): OutOfMemory!void {
    n := try Fallible()
    Both(Acquire("a"), n)
}
