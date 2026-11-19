// verdict: reject E0503
// prelude: yes
Moved() {
    r := Acquire("a")
    a := &r
    Take(r) // <- E0503
    Use(a)
}
