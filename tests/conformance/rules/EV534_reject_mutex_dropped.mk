// verdict: reject E0402
// rule: EV534
// rule: R1
// note: `Mutex[Res]` is linear because `Res` is, so it must be consumed
Drop() {
    m := Mutex.New(Acquire("a")) // <- E0402
    Use(&m)
}
