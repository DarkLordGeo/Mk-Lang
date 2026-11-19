// verdict: ok
// case: R4.Fields
Disjoint() {
    var p := Point(x: 1, y: 2)
    a := mut p.x
    b := mut p.y
    *a += 1
    *b += 1
}

struct Both {
    a: Res
    b: Res
}

Release(p: Both) {
    Both(a, b) := p
    a.Close()
    b.Close()
}
