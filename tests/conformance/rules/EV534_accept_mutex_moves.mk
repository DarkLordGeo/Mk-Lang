// verdict: ok
// rule: EV534
// rule: R1
// note: `Mutex[Res]` is linear because `Res` is: it moves, and `Into` consumes it
Once() {
    m := Mutex.New(Acquire("a"))
    n := m
    r := n.Into()
    r.Close()
}
