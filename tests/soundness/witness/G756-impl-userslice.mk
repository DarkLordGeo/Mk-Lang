// verdict: reject E0602
// note: a user impl of the compiler-known Slice[T] whose Items names no source (K holds only an int) and hands back a slice of a global array of Mutexes; std's default Slice[T].Get returns some(&self.Items()[i]) from self, so a caller that trusted it would judge a store through the element against kk. E0602 at the impl's return (implSourced), with std's default methods unchanged. If accepted, a dev build prints `before 5 after 1515870810`. Acceptance means the hole reopened (mutant M-D756-impl)
var GA: [2]Mutex[?&i32] = [Mutex[?&i32].New(none), Mutex[?&i32].New(none)]

struct K {
    k: int
}

impl Slice[Mutex[?&i32]] for K {
    Items(&self): []Mutex[?&i32] {
        return GA[..] // <- E0602
    }
    ItemsMut(mut self): mut []Mutex[?&i32] {
        for {
        }
    }
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    kk := K(k: 1)
    if m := kk.Get(0) {
        var g := m.Lock(io) else {
            return -2
        }
        g.Store(some(&n))
        g.Unlock()
    }
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
    var g := GA[0].Lock(io) else {
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
