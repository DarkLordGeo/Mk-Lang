// verdict: reject E0402
// prelude: yes
Forget() {
    r := Acquire("a") // <- E0402
    Use(&r)
}
