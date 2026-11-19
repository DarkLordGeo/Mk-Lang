// Why: Box and the heap containers must free exactly what they allocate
// and refuse a block handed back to the wrong allocator.
// What: test blocks for Box, Take, Heap.Alloc, the heap containers' owner
// check, &Heap across tasks and the runtime heap calls.
// How: run by `mk test --std=std.core` with the counting heap (a leak fails
// with exit 102). A block whose name ends in "[exit N: text]" must end with
// that status and text, as internal/stdtest's TestStdMkTests judges; under mk
// test alone it fails, because it panics on purpose. Helper names start with
// Th, because every file of std/core is one module.

import std.raw

struct ThPair {
    a: int
    b: i64
}

struct ThUnit {}

linear struct ThTok {
    n: int
}

linear struct ThNode {
    v: int
    next: ?Box[ThNode]
}

test "Box on the heap" (sys: &System) {
    var m := try Box[ThPair].New(sys.heap.Alloc(), ThPair(a: 1, b: 2))
    m.Mut().a += 1
    Assert(m.Ref().a == 2 && m.Ref().b == 2)
    m.Free(sys.heap.Alloc())
    b := try Box[int].New(sys.heap.Alloc(), 41)
    Assert(b.Take(sys.heap.Alloc()) + 1 == 42)
    inner := try Box[int].New(sys.heap.Alloc(), 5)
    bb := Box[Box[int]].New(sys.heap.Alloc(), inner) else {
        inner.Free(sys.heap.Alloc())
        Assert(false)
        return
    }
    back := bb.Take(sys.heap.Alloc())
    Assert(back.Take(sys.heap.Alloc()) == 5)
}

test "Box of a zero-size value takes no memory" (sys: &System) {
    for _ in 0..1_000 {
        b := try Box[ThUnit].New(sys.heap.Alloc(), ThUnit())
        _ = b.Ref()
        b.Free(sys.heap.Alloc())
    }
    c := try Box[[0]int].New(sys.heap.Alloc(), [])
    _ = c.Take(sys.heap.Alloc())
}

// A self-referential linear list through ?Box (the [0]&T marker, no E0907):
// built, summed, and released node by node with Take.
ThSum(a: &Alloc, head: ?Box[ThNode]): int {
    var total := 0
    var cur := head
    for {
        match cur {
            none { return total }
            some(b) {
                ThNode(v, next) := b.Take(a)
                total += v
                cur = next
            }
        }
    }
}

test "Take releases a recursive list of linear nodes" (sys: &System) {
    a := sys.heap.Alloc()
    var head: ?Box[ThNode] = none
    for i in 0..1_000 {
        head = some(Box[ThNode].New(a, ThNode(v: i, next: head)).Must())
    }
    Assert(ThSum(a, head) == 499_500)
}

test "Take moves a linear value out" (sys: &System) {
    b := Box[ThTok].New(sys.heap.Alloc(), ThTok(n: 3)).Must()
    ThTok(n) := b.Take(sys.heap.Alloc())
    Assert(n == 3)
}

test "Heap.Alloc returns one owner" (sys: &System) {
    Assert(raw.Same(raw.Of(sys.heap.Alloc()), raw.Of(sys.heap.Alloc())))
    var xs := HeapList[int].New(sys.heap.Alloc())
    defer xs.Free(sys.heap.Alloc())
    for i in 0..1000 {
        try xs.Push(sys.heap.Alloc(), i)
    }
    Assert(xs.Len() == 1000)
    var t := HeapText.New(sys.heap.Alloc())
    defer t.Free(sys.heap.Alloc())
    try t.Push(sys.heap.Alloc(), "ab")
    try t.Push(sys.heap.Alloc(), "c")
    Assert(t.Str() == "abc")
}

// A Push that does not grow does not compare the allocator, so a wrong
// allocator passes there; growth and Free compare (the blocks below).
test "a Push that does not grow does not check the allocator" (sys: &System) {
    var xs := HeapList[int].New(sys.heap.Alloc())
    defer xs.Free(sys.heap.Alloc())
    try xs.Push(sys.heap.Alloc(), 1)
    try xs.Push(sys.arena.Alloc(), 2)
    Assert(xs.Len() == 2 && xs[1] == 2)
}

// The keys come from an arena that is freed before the lookups.
ThFillKeys(h: &Heap, m: mut TextMap[int]): OutOfMemory!void {
    var scratch := try Arena.New(h)
    defer scratch.Free()
    for i in 0..100 {
        k := try scratch.Fmt("key{i}", (i: i))
        _ = try m.Ensure(h.Alloc(), k, i)
    }
}

test "TextMap keeps its own copies of the keys" (sys: &System) {
    var m := TextMap[int].New(sys.heap.Alloc())
    defer m.Free(sys.heap.Alloc())
    try ThFillKeys(sys.heap, mut m)
    Assert(m.Len() == 100)
    if v := m.Get("key42") {
        Assert(*v == 42)
    } else {
        Assert(false)
    }
    if v := m.Remove(sys.heap.Alloc(), "key7") {
        Assert(v == 7)
    } else {
        Assert(false)
    }
    Assert(m.Len() == 99)
}

test "HeapText grows and keeps its bytes" (sys: &System) {
    var t := HeapText.New(sys.heap.Alloc())
    defer t.Free(sys.heap.Alloc())
    for _ in 0..1_000 {
        try t.Push(sys.heap.Alloc(), "h\u{e9}")
    }
    Assert(t.Str().Len() == 3_000)
}

ThBoxes(io: Io, h: &Heap, n: int) {
    a := h.Alloc()
    for i in 0..n {
        b := Box[ThPair].New(a, ThPair(a: i, b: 2)) else {
            Assert(false)
            return
        }
        Assert(b.Take(a).a == i)
    }
}

// &Heap crosses tasks: 8 tasks allocate and free Boxes on the one
// heap at once, with one owner for all of them; on every carrier (the
// default, one per CPU) and on one (MK_CARRIERS=1, set by the harness).
test "Box on the heap from 8 tasks on every carrier" (sys: &System) {
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go ThBoxes(sys.io, sys.heap, 20_000)
        }
    }
}

test "Box on the heap from 8 tasks on one carrier [env MK_CARRIERS=1]" (sys: &System) {
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go ThBoxes(sys.io, sys.heap, 20_000)
        }
    }
}

test "Box.Take with the wrong allocator [exit 101: wrong allocator: Box.Take]" (sys: &System) {
    b := try Box[int].New(sys.heap.Alloc(), 1)
    _ = b.Take(sys.arena.Alloc())
}

test "Box.Free with the wrong allocator [exit 101: wrong allocator: Box.Free]" (sys: &System) {
    b := try Box[int].New(sys.heap.Alloc(), 1)
    b.Free(sys.arena.Alloc())
}

test "a Box of a linear value gives it back with Take" (sys: &System) {
    b := Box[ThTok].New(sys.heap.Alloc(), ThTok(n: 7)).Must()
    ThTok(n) := b.Take(sys.heap.Alloc())
    Assert(n == 7)
}

test "HeapList growth with the wrong allocator [exit 101: wrong allocator: HeapList.Push]" (sys: &System) {
    var xs := HeapList[int].New(sys.heap.Alloc())
    defer xs.Free(sys.heap.Alloc())
    try xs.Push(sys.arena.Alloc(), 1)
}

test "HeapList.Free with the wrong allocator [exit 101: wrong allocator: HeapList.Free]" (sys: &System) {
    var xs := HeapList[int].New(sys.heap.Alloc())
    xs.Push(sys.heap.Alloc(), 1).Must()
    xs.Free(sys.arena.Alloc())
}

test "HeapList.FreeEmpty of linear elements [exit 101: HeapList.FreeEmpty of a non-empty list]" (sys: &System) {
    var xs := HeapList[ThTok].New(sys.heap.Alloc())
    xs.Push(sys.heap.Alloc(), ThTok(n: 1)).Must()
    xs.FreeEmpty(sys.heap.Alloc())
}

test "HeapList.Clear of linear elements [exit 101: HeapList.Clear of linear elements]" (sys: &System) {
    var xs := HeapList[ThTok].New(sys.heap.Alloc())
    xs.Push(sys.heap.Alloc(), ThTok(n: 1)).Must()
    xs.Clear()
    xs.FreeEmpty(sys.heap.Alloc())
}

test "HeapMap growth with the wrong allocator [exit 101: wrong allocator: HeapMap.Set]" (sys: &System) {
    var m := HeapMap[int, int].New(sys.heap.Alloc())
    defer m.Free(sys.heap.Alloc())
    try m.Set(sys.arena.Alloc(), 1, 1)
}

test "TextMap with the wrong allocator [exit 101: wrong allocator: TextMap.Ensure]" (sys: &System) {
    var m := TextMap[int].New(sys.heap.Alloc())
    defer m.Free(sys.heap.Alloc())
    _ = try m.Ensure(sys.arena.Alloc(), "k", 1)
}

test "HeapText with the wrong allocator [exit 101: wrong allocator: HeapText.Push]" (sys: &System) {
    var t := HeapText.New(sys.heap.Alloc())
    defer t.Free(sys.heap.Alloc())
    try t.Push(sys.arena.Alloc(), "x")
}

// The runtime's heap calls: MakeHeap records its context and table,
// HeapCtx reads the context back, SetHeapTable swaps the table (the counting
// heap of test builds, rt/leak.mk). The process heap's context is null
// (rt/heap_table.mk).
test "MakeHeap, HeapCtx and SetHeapTable" (sys: &System) {
    Assert(raw.IsNull(HeapCtx(sys.heap)))
    var x: u8 = 5
    p := raw.OfMut(mut x)
    var h := MakeHeap(p, raw.Of(&ArenaTable))
    Assert(raw.Same(HeapCtx(&h), p))
    Assert(raw.Same(raw.Of(h.own.table), raw.Of(&ArenaTable)))
    SetHeapTable(raw.OfMut(mut h), raw.Of(&EvalPageTable))
    Assert(raw.Same(raw.Of(h.own.table), raw.Of(&EvalPageTable)) && raw.Same(HeapCtx(&h), p))
}
