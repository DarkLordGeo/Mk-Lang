// verdict: reject E0605
// note: a source-less global through a view result: LockS hands back GS.Lock(), a SpinGuard of a global Spin. SpinGuard[T] is a view whose only field is a raw pointer, so no first-level reference takes stores; storesFirst counts a view whose type arguments hold loans, so the guard holds the global node, and Store's `x: T into self` of a local's loan is E0605. If accepted, `mk run` prints `before 5 after 1515870810` in dev and release, GS reading Stash's dead frame. Mutant M-D756-view accepts it
import std.atomic

var GS: atomic.Spin[?&i32] = atomic.Spin[?&i32].New(none)

LockS(): atomic.SpinGuard[?&i32] {
    return GS.Lock()
}

Stash(depth: int): int {
    if depth > 0 {
        return depth - Stash(depth - 1)
    }
    var n: i32 = 5
    var g := LockS()
    g.Store(some(&n)) // <- E0605
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

Read(): i32 {
    var g := GS.Lock()
    var v: i32 = -1
    if r := *g.Ref() {
        v = *r
    }
    g.Unlock()
    return v
}

export Main(sys: &System): WriteError!void {
    if Stash(3) == -1000 {
        return
    }
    before := Read()
    var c: i32 = 1515870810
    _ = Paint(8, &c)
    after := Read()
    try sys.Print("before {before} after {after}\n", (before, after))
}
