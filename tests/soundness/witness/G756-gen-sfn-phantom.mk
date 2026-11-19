// verdict: reject E0602
// note: the sourced fn rule's narrowing does not trust a phantom argument. Apply[T: Plain](f: fn(w: &Wrap[T]): T, w: &Wrap[T]): T, where Wrap holds no T, so no parameter of the fn type holds its result and a source-less G(w: &Wrap[&Mutex[?&i32]]) returning &GM may stand behind it, so Apply would return its result under w. E0602 at Apply's return. If accepted, a dev build prints `before 5 after 1515870810`. Acceptance means the hole reopened (mutant M-D756-gen-sfn)
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

struct Wrap[T: Plain] {
    n: int
}

G(w: &Wrap[&Mutex[?&i32]]): &Mutex[?&i32] {
    _ = w
    return &GM
}

Apply[T: Plain](f: fn(w: &Wrap[T]): T, w: &Wrap[T]): T {
    return f(w) // <- E0602
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    w := Wrap[&Mutex[?&i32]](n: 1)
    m := Apply[&Mutex[?&i32]](G, &w)
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
