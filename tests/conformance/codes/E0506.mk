// verdict: reject E0506
// prelude: yes
Held() {
    r := &MakePoint().x // <- E0506
    Use(r)
}
