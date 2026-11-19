// verdict: reject E0602
// note: a fn type that names a source, in a generic body. MkG(v: &i32): &Mutex[?&i32] names no source (an &i32 cannot supply a Mutex) and returns &GM; E0608 lets it stand behind `fn(v: &i32): T from v`, so Call[T]'s `mk(d)` would hold only d and Call would return it under d. A fn value's result is judged with storesFirstGen even when its type names a source, unless a parameter of the fn type holds the result's type (here &i32 does not): E0602 at the return. If accepted, a dev build prints `before 5 after 1515870810`. Acceptance means the hole reopened (mutant M-D756-gen-sfn)
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

MkG(v: &i32): &Mutex[?&i32] {
    _ = v
    return &GM
}

Call[T](d: &i32, mk: fn(v: &i32): T from v): T from d {
    return mk(d) // <- E0602
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    k: i32 = 0
    m := Call[&Mutex[?&i32]](&k, MkG)
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
