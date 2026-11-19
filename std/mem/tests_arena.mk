// Why: an arena over a program's allocator must take every chunk from it and
// give every chunk back.
// What: tests of Arena.NewIn over a Counting and over a Fixed: a Counting or
// a Fixed under an arena sees every chunk the arena and its Subs take and
// refuses the arena at its budget.
// How: a Counting must get every byte back at Free; a Fixed, which frees
// only its last block, gets back what was freed in last-in first-out order:
// all of it for an arena of ordinary chunks, but not the bytes of an aligned
// chunk (a value aligned over 8) or of a Sub freed before its parent's later
// chunks, until Fixed.Reset.
// Helper names start with Ta. std's Go tests check the loan rules: the arena
// holds the allocator's loan (std-arena_newin_test.go), and it is not
// Send (std-arena_alloc_test.go).

import std.raw

align(64) struct TaLine {
    a: u64
}

TaOff[T](p: *T, n: uint): uint {
    return raw.Addr(p) & (n - 1)
}

// TaGrow grows an arena over c, a Sub of it, and resets it, checking c's
// counts at each step; the deferred Free gives the rest back.
TaGrow(c: &Counting): OutOfMemory!void {
    var a := try Arena.NewIn(c.Alloc())
    defer a.Free()
    // The first chunk, which holds the header.
    Assert(c.Live() == 1 && c.Bytes() == 4096)
    _ = try a.Slice[u8](10000)
    Assert(c.Live() == 2 && c.Bytes() > 4096 + 10000)
    try TaSub(&a, c)
    Assert(c.Live() == 2)
    for _ in 0..50 {
        _ = try a.Slice[u8](3000)
    }
    held := c.Live()
    Assert(held > 2)
    a.Reset()
    // Reset keeps the first chunk and the current one.
    Assert(c.Live() == 2)
    _ = try a.Dup("after the reset")
}

// TaSub makes a Sub of a, whose chunks are c's too.
TaSub(a: &Arena, c: &Counting): OutOfMemory!void {
    live := c.Live()
    var s := try a.Sub()
    defer s.Free()
    Assert(c.Live() == live + 1)
    _ = try s.Dup("a Sub's bytes are the same source's")
}

test "Arena.NewIn: every chunk, a Sub's too, is a request to a Counting, and Free gives every one back" (sys: &System) {
    c := Counting.New(sys.heap.Alloc())
    try TaGrow(&c)
    Assert(c.Live() == 0 && c.Bytes() == 0 && c.Peak() > 4096 + 10000)
}

// TaFill bumps 1000-byte slices in an arena over f until f refuses one,
// and returns how many fit; Free then gives every chunk back to f.
TaFill(f: &Fixed): OutOfMemory!int {
    var a := try Arena.NewIn(f.Alloc())
    defer a.Free()
    var n := 0
    for n < 1000 {
        _ = a.Slice[u8](1000) else {
            break
        }
        n += 1
    }
    return n
}

test "Arena.NewIn over a Fixed fails cleanly at the budget, and an arena of ordinary chunks leaves the buffer whole at Free" (sys: &System) {
    buf := try sys.arena.Slice[u8](65536)
    f := Fixed.New(buf)
    first := try TaFill(&f)
    second := try TaFill(&f)
    // 64 KiB holds the 4 KiB first chunk and doubling chunks up to the
    // budget; with only ordinary chunks, Free returns them newest first,
    // so the Fixed takes every one back and a second arena reaches exactly
    // where the first did. (An aligned chunk stays at the head of the
    // list, above chunks made after it, so it would not go back in order.)
    Assert(first > 30 && first < 65 && first == second)
}

test "Arena.NewIn refuses an allocator that refuses the first chunk" {
    var a := Arena.NewIn(Null()) else {
        return
    }
    a.Free()
    Assert(false)
}

test "Arena.NewIn refuses a Fixed smaller than the first chunk" {
    var small: [4000]u8 = [0; 4000]
    f := Fixed.New(mut small[..])
    var a := Arena.NewIn(f.Alloc()) else {
        return
    }
    a.Free()
    Assert(false)
}

// TaAligned puts align(64) values in an arena over c.
TaAligned(c: &Counting): OutOfMemory!void {
    var a := try Arena.NewIn(c.Alloc())
    defer a.Free()
    for i in 0..100 {
        _ = try a.Put(u8(1))
        q := try a.Put(TaLine(a: Trunc[u64](i)))
        Assert(TaOff(raw.Of(&*q), 64) == 0 && q.a == Trunc[u64](i))
    }
    Assert(c.Live() >= 2)
}

test "Arena.NewIn over a Fixed serves align(64) requests aligned" (sys: &System) {
    buf := try sys.arena.Slice[u8](262144)
    f := Fixed.New(buf)
    c := Counting.New(f.Alloc())
    try TaAligned(&c)
    Assert(c.Live() == 0 && c.Bytes() == 0)
}

// TaInner makes an arena over outer's Alloc and a List in it.
TaInner(outer: &Arena): OutOfMemory!int {
    var inner := try Arena.NewIn(outer.Alloc())
    defer inner.Free()
    s := try inner.Dup("inner bytes live in the outer arena")
    var xs := List[int].New(&inner)
    for i in 0..2000 {
        try xs.Push(i)
    }
    return xs.Len() + s.Len()
}

test "Arena.NewIn over another arena's Alloc bumps in it, and frees before it" (sys: &System) {
    var outer := try Arena.New(sys.heap)
    defer outer.Free()
    Assert(try TaInner(&outer) == 2035)
    _ = try outer.Dup("the outer arena goes on")
}

// TaSliceAligned serves an over-aligned SliceAligned from an arena over al:
// at run time no evaluation runs, whatever al's ctx holds (std.mem's
// allocators store their own address there).
TaSliceAligned(al: &Alloc): OutOfMemory!bool {
    var a := try Arena.NewIn(al)
    defer a.Free()
    var ok := true
    for k in 0..13 {
        n := 1 << k
        _ = try a.Put(u8(1))
        b := try a.SliceAligned[u8](3 + k, n)
        ok = ok && b.Len() == 3 + k && TaOff(raw.Data(b[..]), Trunc[uint](n)) == 0
    }
    return ok
}

test "Arena.NewIn over a Fixed and over a Counting serves SliceAligned over the element's alignment at run time" (sys: &System) {
    buf := try sys.arena.Slice[u8](1048576)
    f := Fixed.New(buf)
    Assert(try TaSliceAligned(f.Alloc()))
    c := Counting.New(sys.heap.Alloc())
    Assert(try TaSliceAligned(c.Alloc()))
    Assert(c.Live() == 0)
}

// TaLarge bumps size-byte slices in an arena over f until f refuses one,
// and returns how many fit.
TaLarge(f: &Fixed, size: int): OutOfMemory!int {
    var a := try Arena.NewIn(f.Alloc())
    defer a.Free()
    var n := 0
    for n < 100 {
        s := a.Slice[u8](size) else {
            break
        }
        Assert(s[size - 1] == 0)
        n += 1
    }
    return n
}

test "Arena.NewIn asks a Fixed for no more than its slices need: no large slice takes a line" (sys: &System) {
    // Only an arena on a Heap puts a Slice of 4 KiB or more on a cache line
    // (core's LineBit); one over a caller's allocator asks for its chunks at
    // 8 bytes, so a Fixed fits slices without line padding: 64 KiB holds 11
    // of 4 KiB, 5 of 8 KiB, 2 of 16 KiB after the 4 KiB first chunk, LIFO
    // frees leave it whole, and 4096 bytes at 8 mod 64 hold the first chunk.
    buf := try sys.arena.Slice[u8](65536)
    f := Fixed.New(buf)
    Assert(try TaLarge(&f, 4096) == 11 && try TaLarge(&f, 8192) == 5 && try TaLarge(&f, 16384) == 2)
    Assert(try TaLarge(&f, 4096) == 11)
    g := Fixed.New(mut buf[8..8 + 4096])
    var a := try Arena.NewIn(g.Alloc())
    a.Free()
    h := Fixed.New(mut buf[0..4096 + 16 + 8192])
    var b := try Arena.NewIn(h.Alloc())
    defer b.Free()
    s := try b.Slice[u8](8192)
    Assert(TaOff(raw.Data(s[..]), 64) == 16 && s[8191] == 0)
}

// TaMixed makes a 4 KiB slice in an arena over f, then 1000-byte slices,
// which open chunks after it.
TaMixed(f: &Fixed): OutOfMemory!void {
    var a := try Arena.NewIn(f.Alloc())
    defer a.Free()
    _ = try a.Slice[u8](4096)
    for _ in 0..20 {
        _ = try a.Slice[u8](1000)
    }
}

test "Arena.NewIn over a Fixed: an arena that made a 4 KiB slice and then grew leaves the buffer whole at Free" (sys: &System) {
    // A large slice starts on a cache line in ordinary chunks (core's
    // BumpLine), and Free returns them newest first.
    buf := try sys.arena.Slice[u8](262144)
    f := Fixed.New(buf)
    before := try TaFill(&f)
    try TaMixed(&f)
    Assert(try TaFill(&f) == before)
}
