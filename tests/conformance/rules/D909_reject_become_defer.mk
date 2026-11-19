// verdict: reject E0406
// rule: D-909
// note: nothing in the frame may be left to run after a `become`: a pending `defer` is E0406, since the frame it would run in is replaced
Log(n: int) {
}

Down(n: int): int {
    defer Log(n)
    if n == 0 {
        return 0
    }
    become Down(n - 1) // <- E0406
}
