// verdict: reject E0807
// prelude: yes
error Bad

Step(): Bad!void {
}

Two(): Bad!void {
    a := Acquire("a")
    b := Acquire("b")
    onerr { // <- E0807
        a.Close()
        b.Close()
    }
    try Step()
    a.Close()
    b.Close()
}
