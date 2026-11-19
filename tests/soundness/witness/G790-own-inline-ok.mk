// verdict: ok
// note: a precision witness: a generic method that writes within its own `mut` parameter, a part selected by field, variant or array index steps whose type parameter is bounded by Plain. R11 says a store into the first level needs no clause, and a Plain instance is no view and holds none (R1's kinds table: every view is Unique), so the selected part is first-level storage, as for a concrete type (store.go ownInline, inlineParam). PairD.Dup selects by field, RingD.Rotate by array index, and RevD writes a `mut []T` parameter; the program prints `v 4`, `v 2`, `v 4`. With the Plain exemption cut (mutant M-D790-own-inline), each is E0604, asking for `*self into self` or `*xs into xs`
struct PairD[T: Plain] {
    a: T
    b: T
}

PairD[T].Dup(mut self) {
    self.b = self.a
}

struct RingD[T: Plain] {
    xs: [4]T
    n: int
}

RingD[T].Rotate(mut self) {
    first := self.xs[0]
    for i in 0..3 {
        self.xs[i] = self.xs[i + 1]
    }
    self.xs[3] = first
}

RevD[T: Plain](xs: mut []T) {
    var i := 0
    var j := xs.Len() - 1
    for i < j {
        t := xs[i]
        xs[i] = xs[j]
        xs[j] = t
        i += 1
        j -= 1
    }
}

export Main(sys: &System): WriteError!void {
    x: i32 = 4
    y: i32 = 5
    var p := PairD[&i32](a: &x, b: &y)
    p.Dup()
    v1 := *p.b
    var r := RingD[int](xs: [1, 2, 3, 4], n: 4)
    r.Rotate()
    v2 := r.xs[0]
    var a: [4]int = [1, 2, 3, 4]
    RevD[int](mut a[..])
    v3 := a[0]
    try sys.Print("v {v1} {v2} {v3}\n", (v1, v2, v3))
}
