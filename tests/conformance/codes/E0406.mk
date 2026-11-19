// verdict: reject E0406
// prelude: yes
Early() {
    r := Acquire("a")
    defer Use(&r)
    Take(r) // <- E0406
}
