// verdict: ok
// rule: R2
Both(c: bool) {
    r := Acquire("a")
    if c {
        r.Close()
    } else {
        Take(r)
    }
}
