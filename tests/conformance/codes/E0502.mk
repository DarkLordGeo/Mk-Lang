// verdict: reject E0502
// prelude: yes
Write() {
    var p := MakePoint()
    a := &p.x
    p.x = 3 // <- E0502
    Use(a)
}
