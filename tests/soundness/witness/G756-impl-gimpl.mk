// verdict: reject E0602
// note: a generic impl of a sourced interface method (automatic B(self)) returns the result of a source-less fn value it keeps; at T = &Mutex[?&i32] the fn returns &GM. A generic forwarder returns s.Get() under `from s`, and the caller stores a local through it. The generic impl's own result names self (R9, automatically: W[T] mentions T), so its boundary refuses the global it hands back, judged for every instance (E0602 at the impl's return); the generic caller trusts the interface's source, so this boundary is what refuses the program. If accepted, a dev build prints `before 5 after 1515870810` (a dead read). Acceptance means the boundary rule is cut (mutants M-D756-boundary and, for the fn value's result, M-D756-gen-boundary)
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

GetM(): &Mutex[?&i32] {
    return &GM
}

interface Src[R] {
    Get(&self): R
}

struct W[T] {
    f: fn(): T
}

impl[T] Src[T] for W[T] {
    Get(&self): T {
        g := self.f
        return g() // <- E0602
    }
}

Fwd[T, S: Src[T]](s: &S): T from s {
    return s.Get()
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    w := W[&Mutex[?&i32]](f: GetM)
    var n: i32 = 5
    m := Fwd[&Mutex[?&i32], W[&Mutex[?&i32]]](&w)
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
