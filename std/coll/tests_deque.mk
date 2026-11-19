// Why: Deque.GetMut must address the ring by logical index and never panic.
// What: tests that writes land on the i-th value from the front, across the
// ring's wrap, and that an index out of range is none.
// How: run by `mk test --std=std.coll` with the counting heap. Helper names
// start with Dq, because every file of std/coll is one module.

test "Deque.GetMut writes the i-th value from the front, wrapped, and is none out of range" (sys: &System) {
    var d := Deque[int].New(sys.arena)
    Assert(d.GetMut(0).IsNone())
    Assert(d.GetMut(-1).IsNone())
    // Wrap the ring: pushes at the front land at the top of the buffer.
    for i in 0..5 {
        try d.Push(i)
    }
    for i in 1..4 {
        try d.PushFront(-i)
    }
    // Front to back: -3 -2 -1 0 1 2 3 4.
    Assert(d.Len() == 8)
    for i in 0..8 {
        *d.GetMut(i).Must() *= 10
    }
    for i in 0..8 {
        Assert(*d.Get(i).Must() == (i - 3) * 10)
    }
    Assert(d.GetMut(8).IsNone())
    Assert(d.GetMut(-1).IsNone())
    Assert(d.GetMut(1 << 62).IsNone())
    // Pop at both ends; indices follow the front.
    _ = d.PopFront()
    _ = d.Pop()
    *d.GetMut(0).Must() = 99
    Assert(*d.Get(0).Must() == 99 && d.Len() == 6)
    Assert(*d.Get(5).Must() == 30)
}

struct DqPair {
    a: int
    b: int
}

test "Deque.GetMut of a struct writes one field" (sys: &System) {
    var d := Deque[DqPair].New(sys.arena)
    for i in 0..20 {
        try d.PushFront(DqPair(a: i, b: -i))
    }
    for i in 0..20 {
        p := d.GetMut(i).Must()
        p.b = p.a * 2
    }
    for i in 0..20 {
        q := d.Get(i).Must()
        Assert(q.a == 19 - i && q.b == 2 * (19 - i))
    }
}
