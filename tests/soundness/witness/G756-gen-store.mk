// verdict: reject E0605
// note: a source-less global with the store in the generic body: Stash2[T: Sink](f: fn(): &T, io: Io) stores a local's loan through f()'s result with Put's `v into self`. As written, the result's type &T takes no store, so the store would land nowhere, while at T = W it reaches the global GW. f()'s result holds the global node (storesFirstGen), so the store is E0605. If accepted, a run reads a dead frame. Mutant M-D756-gen-value accepts it
interface Sink {
    Put(&self, io: Io, v: &i32 into self): int
}

struct W {
    m: Mutex[?&i32]
}

var GW: W = W(m: Mutex[?&i32].New(none))

impl Sink for W {
    Put(&self, io: Io, v: &i32 into self): int {
        var g := self.m.Lock(io) else {
            return -2
        }
        g.Store(some(v))
        g.Unlock()
        return 0
    }
}

GetW(): &W {
    return &GW
}

Stash2[T: Sink](f: fn(): &T, io: Io): int {
    var n: i32 = 5
    r := f()
    return r.Put(io, &n) // <- E0605
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    return Stash2[W](GetW, io)
}

Paint(depth: int, p: &i32): i32 {
    var x: [64]i32 = [*p; 64]
    if depth == 0 {
        return x[3]
    }
    return Paint(depth - 1, &x[1]) +% x[2]
}

Read(io: Io): i32 {
    var g := GW.m.Lock(io) else {
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
