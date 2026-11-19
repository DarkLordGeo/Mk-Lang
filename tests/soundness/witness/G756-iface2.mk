// verdict: reject E0602 E0605
// note: an impl method whose result names no source behind an interface method whose result is from self (E0608 lets the impl have fewer flows); the generic caller declares x into t, so judging the store against t only would miss the global. An interface call's carrier result also holds the global node's loan: E0605 at the store. The impl method names no source while its interface method names one, so its boundary is judged as a sourced one (implSourced): E0602 at the impl's return too. If accepted, `mk run` reads a dead frame (canary 1515870810 after Paint, dev and release). Mutant M-D756-indirect accepts it; its row also cuts implSourced
// An impl method with no source behind an interface method whose result is from self; the store is declared into t.
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

interface Src {
    Get(&self): &Mutex[?&i32]
}

struct K {
    k: int
}

impl Src for K {
    Get(&self): &Mutex[?&i32] {
        return &GM // <- E0602
    }
}

Via[T: Src](t: &T, x: &i32 into t, io: Io): int {
    m := t.Get()
    var g := m.Lock(io) else {
        return -2
    }
    g.Store(some(x)) // <- E0605
    g.Unlock()
    return 0
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    kk := K(k: 1)
    return Via(&kk, &n, io)
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
