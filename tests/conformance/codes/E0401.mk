// verdict: reject E0401
// prelude: yes
Twice() {
    r := Acquire("a")
    Take(r)
    r.Close() // <- E0401
}
