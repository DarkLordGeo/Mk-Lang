// verdict: reject E0101
// note: foreign form, go F() outside a scope; tasks start with s.go inside a scope block
Work() {
}

Start() {
    go Work() // <- E0101
}
