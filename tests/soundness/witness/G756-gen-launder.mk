// verdict: reject E0602
// note: a source-less global through a type-parameter result named `from k`: Launder[T](f: fn(): T, k: &i32): T from k returns f()'s result. E0602 at the return, as in G756-gen-call. If accepted, `mk run` prints `before 5 after 1515870810` in dev and release, a dead read. Mutants M-D756-gen-value and M-D756-gen-boundary accept it
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

Get(): &Mutex[?&i32] {
    return &GM
}

Launder[T](f: fn(): T, k: &i32): T from k {
    return f() // <- E0602
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    k: i32 = 0
    m := Launder[&Mutex[?&i32]](Get, &k)
    var n: i32 = 5
    var g := m.Lock(io) else {
        return -2
    }
    g.Store(some(&n))
    g.Unlock()
    return 0
}

Paint(depth: int, p: &i32): i32 {
    var x: [64]i32 = [*p; 64]
    if depth == 0 {
        return x[3]
    }
    return Paint(depth - 1, &x[1]) +% x[2]
}

Read(io: Io): i32 {
    var g := GM.Lock(io) else {
        return -1
    }
    var v: i32 = -1
    if r := *g.Ref() {
        v = *r
    }
    g.Unlock()
    return v
}

export Main(sys: &System): WriteError!void {
    if Stash(sys.io, 3) == -1000 {
        return
    }
    before := Read(sys.io)
    var c: i32 = 1515870810
    _ = Paint(8, &c)
    after := Read(sys.io)
    try sys.Print("before {before} after {after}\n", (before, after))
}
