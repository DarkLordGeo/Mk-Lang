// Why: a Pool must refuse every stale or foreign handle and never panic.
// What: edge tests of Pool's Iter, Sweep and GetPair and its 8-byte Handle:
// an empty pool, stale, retired-slot and equal handles, a sweep of
// everything and of nothing, handles as map keys.
// How: run by `mk test --std=std.mem`; random sequences against a Go model
// of the slot map are std's Go tests. Helper names start with Tp.

test "Handle is 8 bytes, plain, and a map key" (sys: &System) {
    Assert(SizeOf[Handle]() == 8)
    var p := Pool[int].New(sys.arena)
    var m := Map[Handle, int].New(sys.arena)
    for i in 0..100 {
        h := try p.Add(i)
        try m.Set(h, i * 2)
    }
    for (handle, value) in p.Iter() {
        Assert(*m.Get(&handle).Must() == *value * 2)
    }
}

test "Iter visits the live values in slot order, skipping freed slots" (sys: &System) {
    var p := Pool[int].New(sys.arena)
    var it := p.Iter()
    Assert(it.Next().IsNone())
    var hs := List[Handle].New(sys.arena)
    for i in 0..10 {
        try hs.Push(try p.Add(i))
    }
    for i in 0..10 {
        if i % 3 == 0 {
            Assert(p.Free(hs[i]))
        }
    }
    var last := -1
    var n := 0
    for (handle, value) in p.Iter() {
        Assert(*value % 3 != 0 && *value > last)
        Assert(*p.Get(handle).Must() == *value)
        last = *value
        n += 1
    }
    Assert(n == 6 && n == p.Len())
}

test "Sweep drops what keep refuses, makes its handles stale, and reuses the slots" (sys: &System) {
    var p := Pool[int].New(sys.arena)
    Assert(p.Sweep(fn(h: Handle, x: &int): bool { return false }) == 0)
    var hs := List[Handle].New(sys.arena)
    for i in 0..50 {
        try hs.Push(try p.Add(i))
    }
    Assert(p.Sweep(fn(h: Handle, x: &int): bool { return true }) == 0)
    limit := 20
    Assert(p.Sweep(fn(h: Handle, x: &int): bool { return *x >= limit }) == 20)
    Assert(p.Len() == 30)
    for i in 0..50 {
        Assert(p.Get(hs[i]).IsSome() == (i >= 20))
        Assert(p.Free(hs[i]) == (i >= 20))
    }
    Assert(p.Len() == 0)
    for i in 0..50 {
        h := try p.Add(100 + i)
        Assert(p.Get(hs[i]).IsNone())
        Assert(*p.Get(h).Must() == 100 + i)
    }
    Assert(p.Sweep(fn(h: Handle, x: &int): bool { return false }) == 50 && p.Len() == 0)
}

test "GetPair gives two values mut, and none for equal, stale or out-of-range handles" (sys: &System) {
    var p := Pool[int].New(sys.arena)
    h := try p.Add(1)
    k := try p.Add(2)
    Assert(p.GetPair(h, h).IsNone())
    pr := p.GetPair(h, k).Must()
    *pr.a += 10
    *pr.b += 20
    Assert(*p.Get(h).Must() == 11 && *p.Get(k).Must() == 22)
    Assert(p.Free(k))
    Assert(p.GetPair(h, k).IsNone() && p.GetPair(k, h).IsNone())
    k2 := try p.Add(3)
    Assert(p.GetPair(k, k2).IsNone())
    Assert(p.GetPair(h, k2).IsSome())
    var q := Pool[int].New(sys.arena)
    far := try q.Add(0)
    for _ in 0..40 {
        _ = try q.Add(0)
    }
    last := try q.Add(0)
    Assert(p.GetPair(h, last).IsNone() && p.Get(last).IsNone())
    _ = far
}

test "another pool's handle at the same slot is refused" (sys: &System) {
    var p := Pool[int].New(sys.arena)
    var q := Pool[int].New(sys.arena)
    hp := try p.Add(1)
    hq := try q.Add(2)
    Assert(!hp.Eq(&hq))
    Assert(p.Get(hq).IsNone() && q.Get(hp).IsNone())
    Assert(!p.Free(hq) && !q.Free(hp))
}

// TpOrder adds 0 to 11 to a fresh pool, keys a Map by their handles, and
// returns the values in the map's iteration order as base-16 digits.
TpOrder(a: &Arena): OutOfMemory!u64 {
    var p := Pool[int].New(a)
    var m := Map[Handle, int].New(a)
    for i in 0..12 {
        try m.Set(try p.Add(i), i)
    }
    var order: u64 = 0
    for e in m.Iter() {
        order = order << 4 | Cast[u64](*e.value)
    }
    return order
}

test "a handle-keyed map iterates in the same order whatever pools were made before" (sys: &System) {
    first := try TpOrder(sys.arena)
    _ = Pool[int].New(sys.arena)
    second := try TpOrder(sys.arena)
    _ = Pool[int].New(sys.arena)
    _ = Pool[int].New(sys.arena)
    third := try TpOrder(sys.arena)
    Assert(second == first && third == first)
}
