// Why: Limit, Failing and Fallback sit between a container and the
// allocator that holds its memory; a request counted wrong, failed at the
// wrong number or freed to the wrong side leaks or corrupts.
// What: tests of the three through raw requests and through the
// containers, as user code uses them; std's Go tests compare whole random
// runs with models.
// How: run by `mk test --std=std.mem` with the counting test heap: a block
// that leaves heap memory live fails with exit 102. A Failing that fails
// nothing serves as a probe: its Requests says whether the allocator above
// it asked it at all. Helper names start with Tw; ThFill, ThFilled, ThNext
// and ThLead are tests_fixedheap.mk's.

import std.core
import std.raw

// TwFillList pushes 0, 1, ... into a HeapList over a until a push is
// refused or n are held, checks them, frees the list and returns how many
// it held.
TwFillList(a: &Alloc, n: int): int {
    var xs := HeapList[int].New(a)
    var held := 0
    for held < n {
        xs.Push(a, held) else {
            break
        }
        held += 1
    }
    Assert(xs.Len() == held)
    for i in 0..held {
        Assert(xs[i] == i)
    }
    xs.Free(a)
    return held
}

test "Limit: requests pass while the bytes live stay within the limit" (sys: &System) {
    l := Limit.New(sys.heap.Alloc(), 100)
    a := l.Alloc()
    Assert(l.Bytes() == 0 && l.Left() == 100)
    p := core.AllocBytes(a, 40, 8)
    q := core.AllocBytes(a, 40, 8)
    Assert(!raw.IsNull(p) && !raw.IsNull(q) && l.Bytes() == 80 && l.Left() == 20)
    // The request that would pass the limit is refused, and changes nothing.
    Assert(raw.IsNull(core.AllocBytes(a, 21, 1)) && l.Bytes() == 80)
    r := core.AllocBytes(a, 20, 1)
    Assert(!raw.IsNull(r) && l.Left() == 0 && raw.IsNull(core.AllocBytes(a, 1, 1)))
    // A zero-byte request takes nothing, so it still passes.
    z := core.AllocBytes(a, 0, 1)
    Assert(!raw.IsNull(z) && l.Left() == 0)
    core.FreeBytes(a, z, 0, 1)
    // Freed bytes are available again, whatever the order of the frees.
    core.FreeBytes(a, p, 40, 8)
    Assert(l.Bytes() == 60 && l.Left() == 40)
    s := core.AllocBytes(a, 40, 8)
    Assert(!raw.IsNull(s) && l.Left() == 0)
    core.FreeBytes(a, q, 40, 8)
    core.FreeBytes(a, r, 20, 1)
    core.FreeBytes(a, s, 40, 8)
    Assert(l.Bytes() == 0 && l.Left() == 100)
}

test "Limit: a resize is charged the difference, and a refused one changes nothing" (sys: &System) {
    l := Limit.New(sys.heap.Alloc(), 64)
    a := l.Alloc()
    p := core.AllocBytes(a, 16, 8)
    Assert(!raw.IsNull(p) && l.Bytes() == 16)
    // The heap's class of a 16-byte block holds 16 bytes: it shrinks in
    // place, and the bytes come back.
    Assert(core.ResizeBytes(a, p, 16, 4, 8) && l.Bytes() == 4)
    Assert(core.ResizeBytes(a, p, 4, 16, 8) && l.Bytes() == 16)
    // The heap refuses to grow it past its class: nothing is charged.
    Assert(!core.ResizeBytes(a, p, 16, 48, 8) && l.Bytes() == 16)
    // Growing by more than what is left is refused before the heap is
    // asked, and so is a negative size.
    Assert(!core.ResizeBytes(a, p, 16, 65, 8) && !core.ResizeBytes(a, p, 16, -1, 8) && l.Bytes() == 16)
    core.FreeBytes(a, p, 16, 8)
    Assert(l.Bytes() == 0)
}

test "Limit: a refused request never reaches the inner allocator" (sys: &System) {
    probe := Failing.New(sys.heap.Alloc(), -1)
    l := Limit.New(probe.Alloc(), 32)
    a := l.Alloc()
    p := core.AllocBytes(a, 32, 8)
    Assert(!raw.IsNull(p) && probe.Requests() == 1)
    Assert(raw.IsNull(core.AllocBytes(a, 1, 1)) && raw.IsNull(core.AllocBytes(a, -1, 1)) && raw.IsNull(core.AllocBytes(a, 1 << 40, 8)))
    Assert(!core.ResizeBytes(a, p, 32, 33, 8))
    Assert(probe.Requests() == 1 && l.Bytes() == 32)
    core.FreeBytes(a, p, 32, 8)
}

test "Limit: when the inner allocator refuses, nothing is charged" {
    l := Limit.New(Null(), 1_000)
    a := l.Alloc()
    Assert(raw.IsNull(core.AllocBytes(a, 10, 1)) && l.Bytes() == 0 && l.Left() == 1_000)
    var buf: [72]u8 = [0; 72]
    lead := ThLead(buf[..])
    f := Fixed.New(mut buf[lead..lead + 64])
    k := Limit.New(f.Alloc(), 1_000)
    p := core.AllocBytes(k.Alloc(), 64, 8)
    Assert(!raw.IsNull(p) && raw.IsNull(core.AllocBytes(k.Alloc(), 8, 8)) && k.Bytes() == 64)
    core.FreeBytes(k.Alloc(), p, 64, 8)
    Assert(k.Bytes() == 0)
}

test "Limit: a limit below zero is zero" (sys: &System) {
    l := Limit.New(sys.heap.Alloc(), -5)
    Assert(l.Left() == 0 && raw.IsNull(core.AllocBytes(l.Alloc(), 1, 1)))
    Assert(TwFillList(l.Alloc(), 10) == 0)
}

test "Limit over the heap bounds a HeapList, and churn under it never runs out" (sys: &System) {
    l := Limit.New(sys.heap.Alloc(), 4096)
    // A list of ints doubles its buffer, and a growth holds the old buffer
    // and the new one at once: 256 elements (2,048 bytes) are the most
    // that 4,096 bytes allow.
    Assert(TwFillList(l.Alloc(), 100_000) == 256 && l.Bytes() == 0)
    // What a Fixed of 4,096 bytes could not do: every round's memory comes
    // back, in whatever order it was freed.
    for _ in 0..200 {
        Assert(TwFillList(l.Alloc(), 200) == 200)
    }
    Assert(l.Bytes() == 0 && l.Left() == 4096)
}

test "Limit bounds an arena made in it" (sys: &System) {
    l := Limit.New(sys.heap.Alloc(), 200_000)
    var ar := try Arena.NewIn(l.Alloc())
    var xs := List[int].New(&ar)
    var held := 0
    for i in 0..1_000_000 {
        xs.Push(i) else {
            break
        }
        held += 1
    }
    Assert(held > 1_000 && held < 1_000_000 && l.Bytes() <= 200_000)
    ar.Free()
    Assert(l.Bytes() == 0)
}

test "Failing.New fails exactly the request it names" (sys: &System) {
    f := Failing.New(sys.heap.Alloc(), 2)
    a := f.Alloc()
    var got: [5]*u8 = [raw.Null[u8](); 5]
    for i in 0..5 {
        got[i] = core.AllocBytes(a, 24, 8)
    }
    Assert(!raw.IsNull(got[0]) && !raw.IsNull(got[1]) && raw.IsNull(got[2]) && !raw.IsNull(got[3]) && !raw.IsNull(got[4]))
    Assert(f.Requests() == 5 && f.Failed() == 1)
    for i in 0..5 {
        if !raw.IsNull(got[i]) {
            core.FreeBytes(a, got[i], 24, 8)
        }
    }
    // Frees are not requests.
    Assert(f.Requests() == 5)
}

test "Failing.NewFrom fails the request it names and every later one" (sys: &System) {
    f := Failing.NewFrom(sys.heap.Alloc(), 2)
    a := f.Alloc()
    p := core.AllocBytes(a, 24, 8)
    q := core.AllocBytes(a, 24, 8)
    Assert(!raw.IsNull(p) && !raw.IsNull(q))
    for _ in 0..10 {
        Assert(raw.IsNull(core.AllocBytes(a, 24, 8)))
    }
    Assert(f.Requests() == 12 && f.Failed() == 10)
    // What was allocated before is still freed through it.
    core.FreeBytes(a, p, 24, 8)
    core.FreeBytes(a, q, 24, 8)
}

test "Failing: a negative number fails nothing" (sys: &System) {
    f := Failing.New(sys.heap.Alloc(), -1)
    g := Failing.NewFrom(sys.heap.Alloc(), -7)
    Assert(TwFillList(f.Alloc(), 1_000) == 1_000 && TwFillList(g.Alloc(), 1_000) == 1_000)
    Assert(f.Failed() == 0 && g.Failed() == 0 && f.Requests() > 0 && f.Requests() == g.Requests())
}

test "Failing: a resize to more bytes is a request, a resize to fewer and a free are not" (sys: &System) {
    probe := Failing.New(sys.heap.Alloc(), -1)
    f := Failing.New(probe.Alloc(), 1)
    a := f.Alloc()
    p := core.AllocBytes(a, 16, 8)
    Assert(!raw.IsNull(p) && f.Requests() == 1)
    // Request 1 is this growth: it fails, and the inner allocator is not
    // asked.
    Assert(!core.ResizeBytes(a, p, 8, 16, 8) && f.Requests() == 2 && f.Failed() == 1 && probe.Requests() == 1)
    // The same growth again is request 2, which passes.
    Assert(core.ResizeBytes(a, p, 8, 16, 8) && f.Requests() == 3)
    Assert(core.ResizeBytes(a, p, 16, 8, 8) && core.ResizeBytes(a, p, 8, 8, 8) && f.Requests() == 3)
    core.FreeBytes(a, p, 8, 8)
    Assert(f.Requests() == 3 && f.Failed() == 1)
}

// TwBuild fills a HeapMap and a TextMap over a, fails with OutOfMemory when
// a refuses, and frees both on every path.
TwBuild(a: &Alloc): OutOfMemory!int {
    var m := HeapMap[int, int].New(a)
    onerr m.Free(a)
    var t := TextMap[int].New(a)
    onerr t.Free(a)
    keys := ["one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"]
    for i in 0..40 {
        try m.Set(a, i, i * i)
        _ = try t.Ensure(a, keys[i % 10], i)
    }
    n := m.Len() + t.Len()
    m.Free(a)
    t.Free(a)
    return n
}

test "Failing: one run per request number fails every allocation of a function once, and none leaks" (sys: &System) {
    c := Counting.New(sys.heap.Alloc())
    var at := 0
    var failed := 0
    for at < 10_000 {
        f := Failing.New(c.Alloc(), at)
        _ = TwBuild(f.Alloc()) else {
            failed += 1
        }
        // Whether the run failed or went on past the failure, it gave
        // everything back.
        Assert(c.Live() == 0 && c.Bytes() == 0)
        if f.Failed() == 0 {
            break
        }
        at += 1
    }
    // The run that met no failure made exactly at requests, and some of the
    // failures were fatal to the function.
    Assert(at > 10 && at < 10_000 && failed > 0 && failed <= at)
    // An allocator that stays exhausted fails the function at every number.
    for k in 0..at {
        f := Failing.NewFrom(c.Alloc(), k)
        var oom := false
        _ = TwBuild(f.Alloc()) else {
            oom = true
        }
        Assert(oom && c.Live() == 0)
    }
}

test "Fallback: a container that fits the buffer never reaches the other allocator" (sys: &System) {
    probe := Failing.New(sys.heap.Alloc(), -1)
    var buf: [1032]u8 = [0; 1032]
    lead := ThLead(buf[..])
    b := Fallback.New(probe.Alloc(), mut buf[lead..lead + 1024])
    for _ in 0..100 {
        // 4, 8, ... 128 ints, each step grown in place: 1,024 bytes.
        Assert(TwFillList(b.Alloc(), 128) == 128)
    }
    Assert(probe.Requests() == 0)
}

test "Fallback: a container that outgrows the buffer moves to the other allocator and back" (sys: &System) {
    c := Counting.New(sys.heap.Alloc())
    probe := Failing.New(c.Alloc(), -1)
    var buf: [1032]u8 = [0; 1032]
    lead := ThLead(buf[..])
    b := Fallback.New(probe.Alloc(), mut buf[lead..lead + 1024])
    Assert(TwFillList(b.Alloc(), 10_000) == 10_000)
    asked := probe.Requests()
    Assert(asked > 0 && c.Live() == 0 && c.Peak() >= 80_000)
    // The list's block in the buffer went back when it moved out, so the
    // next small list fits the buffer again.
    Assert(TwFillList(b.Alloc(), 128) == 128 && probe.Requests() == asked)
}

test "Fallback: each block goes back to where it came from, in any order" (sys: &System) {
    c := Counting.New(sys.heap.Alloc())
    var buf: [264]u8 = [0; 264]
    lead := ThLead(buf[..])
    lo := raw.Addr(raw.Data(buf[..]))
    b := Fallback.New(c.Alloc(), mut buf[lead..lead + 256])
    a := b.Alloc()
    for order in 0..2 {
        inside := core.AllocBytes(a, 200, 8)
        outside := core.AllocBytes(a, 200, 8)
        small := core.AllocBytes(a, 40, 8)
        Assert(!raw.IsNull(inside) && !raw.IsNull(outside) && !raw.IsNull(small))
        // The first and the third fit the buffer; the second does not.
        Assert(raw.Addr(inside) -% lo < 264 && raw.Addr(small) -% lo < 264 && raw.Addr(outside) -% lo >= 264)
        Assert(c.Live() == 1 && c.Bytes() == 200)
        ThFill(inside, 200, 1)
        ThFill(outside, 200, 2)
        ThFill(small, 40, 3)
        Assert(ThFilled(inside, 200, 1) && ThFilled(outside, 200, 2) && ThFilled(small, 40, 3))
        if order == 0 {
            core.FreeBytes(a, small, 40, 8)
            core.FreeBytes(a, outside, 200, 8)
            core.FreeBytes(a, inside, 200, 8)
        } else {
            core.FreeBytes(a, outside, 200, 8)
            core.FreeBytes(a, small, 40, 8)
            core.FreeBytes(a, inside, 200, 8)
        }
        Assert(c.Live() == 0 && c.Bytes() == 0)
    }
}

test "Fallback: a zero-byte request goes to the other allocator" (sys: &System) {
    probe := Failing.New(sys.heap.Alloc(), -1)
    var buf: [72]u8 = [0; 72]
    lead := ThLead(buf[..])
    b := Fallback.New(probe.Alloc(), mut buf[lead..lead + 64])
    a := b.Alloc()
    // Fill the buffer to its last byte: a zero-byte block of the Fixed
    // would now sit at the buffer's end, an address the heap may own.
    p := core.AllocBytes(a, 64, 8)
    z := core.AllocBytes(a, 0, 1)
    Assert(!raw.IsNull(p) && !raw.IsNull(z) && !raw.Same(p, z) && probe.Requests() == 1)
    core.FreeBytes(a, z, 0, 1)
    core.FreeBytes(a, p, 64, 8)
    // The buffer is whole again.
    q := core.AllocBytes(a, 64, 8)
    Assert(raw.Same(p, q) && probe.Requests() == 1)
    core.FreeBytes(a, q, 64, 8)
}

test "Fallback: a zero-byte block is one byte of the other allocator, so it is never taken for a block of the buffer" {
    // One array, split: the left half is a Fixed that serves as the other
    // allocator, the right half is the Fallback's buffer, which starts at
    // the very address where the Fixed's memory ends. A zero-byte block of
    // a full Fixed sits at that address.
    var arr: [136]u8 = [0; 136]
    lead := ThLead(arr[..])
    (left, right) := (mut arr[lead..lead + 128]).SplitAt(64)
    f := Fixed.New(left)
    c := Counting.New(f.Alloc())
    b := Fallback.New(c.Alloc(), right)
    a := b.Alloc()
    // Fill the other allocator but for 8 bytes, and the buffer whole.
    fill := core.AllocBytes(c.Alloc(), 56, 8)
    held := core.AllocBytes(a, 64, 8)
    Assert(!raw.IsNull(fill) && !raw.IsNull(held) && c.Live() == 1)
    // A zero-byte block takes a byte of the other allocator: it lies
    // inside the left half, not at the buffer's first byte.
    z := core.AllocBytes(a, 0, 1)
    Assert(!raw.IsNull(z) && !raw.Same(z, held) && c.Live() == 2 && c.Bytes() == 57)
    // It grows there, never into the buffer, and its free reaches the
    // allocator it came from.
    Assert(core.ResizeBytes(a, z, 0, 8, 1) && c.Bytes() == 64)
    Assert(core.ResizeBytes(a, z, 8, 0, 1) && c.Bytes() == 57)
    core.FreeBytes(a, z, 0, 1)
    Assert(c.Live() == 1 && c.Bytes() == 56)
    // With the other allocator full, a zero-byte request fails: it is not
    // handed the address after its last byte, which is the buffer's.
    last := core.AllocBytes(c.Alloc(), 8, 8)
    Assert(!raw.IsNull(last) && raw.IsNull(core.AllocBytes(a, 0, 1)))
    core.FreeBytes(c.Alloc(), last, 8, 8)
    core.FreeBytes(a, held, 64, 8)
    core.FreeBytes(c.Alloc(), fill, 56, 8)
    Assert(c.Live() == 0 && c.Bytes() == 0)
}

test "Fallback: a resize stays on the side its block is on" (sys: &System) {
    c := Counting.New(sys.heap.Alloc())
    var buf: [136]u8 = [0; 136]
    lead := ThLead(buf[..])
    b := Fallback.New(c.Alloc(), mut buf[lead..lead + 128])
    a := b.Alloc()
    p := core.AllocBytes(a, 32, 8)
    Assert(!raw.IsNull(p) && c.Live() == 0)
    // The last block of the buffer grows in place to the buffer's end, and
    // is refused past it: it does not move to the heap by itself.
    Assert(core.ResizeBytes(a, p, 32, 128, 8) && !core.ResizeBytes(a, p, 128, 129, 8) && c.Live() == 0)
    q := core.AllocBytes(a, 16, 8)
    Assert(!raw.IsNull(q) && c.Live() == 1 && c.Bytes() == 16)
    // The heap's block shrinks in place, through the heap.
    Assert(core.ResizeBytes(a, q, 16, 8, 8) && c.Bytes() == 8)
    core.FreeBytes(a, q, 8, 8)
    core.FreeBytes(a, p, 128, 8)
    Assert(c.Live() == 0)
}

test "Fallback over an empty buffer is the other allocator, and over Null a Fixed" (sys: &System) {
    c := Counting.New(sys.heap.Alloc())
    var empty: [1]u8 = [0; 1]
    b := Fallback.New(c.Alloc(), mut empty[0..0])
    Assert(TwFillList(b.Alloc(), 1_000) == 1_000 && c.Live() == 0 && c.Peak() > 0)
    // A wild pointer near the empty buffer is not taken for a block of it.
    p := core.AllocBytes(b.Alloc(), 8, 8)
    Assert(!raw.IsNull(p) && c.Live() == 1)
    core.FreeBytes(b.Alloc(), p, 8, 8)
    Assert(c.Live() == 0)
    var buf: [264]u8 = [0; 264]
    lead := ThLead(buf[..])
    k := Fallback.New(Null(), mut buf[lead..lead + 256])
    // 4, 8, 16 and 32 ints in place fill the 256 bytes; the next growth has
    // nowhere to go.
    Assert(TwFillList(k.Alloc(), 1_000) == 32)
}

test "Fallback: Reset makes the buffer free again" (sys: &System) {
    probe := Failing.New(sys.heap.Alloc(), -1)
    var buf: [264]u8 = [0; 264]
    lead := ThLead(buf[..])
    var b := Fallback.New(probe.Alloc(), mut buf[lead..lead + 256])
    for _ in 0..3 {
        // Two blocks freed first in, first out strand the first one in the
        // buffer: only Reset gives it back.
        p := core.AllocBytes(b.Alloc(), 100, 8)
        q := core.AllocBytes(b.Alloc(), 100, 8)
        Assert(!raw.IsNull(p) && !raw.IsNull(q))
        core.FreeBytes(b.Alloc(), p, 100, 8)
        core.FreeBytes(b.Alloc(), q, 100, 8)
        b.Reset()
    }
    Assert(probe.Requests() == 0)
    whole := core.AllocBytes(b.Alloc(), 256, 8)
    Assert(!raw.IsNull(whole) && probe.Requests() == 0)
    core.FreeBytes(b.Alloc(), whole, 256, 8)
    b.Reset()
}

test "Fallback: random requests across the buffer and the heap keep every block's contents" (sys: &System) {
    c := Counting.New(sys.heap.Alloc())
    var buf: [2056]u8 = [0; 2056]
    lead := ThLead(buf[..])
    b := Fallback.New(c.Alloc(), mut buf[lead..lead + 2048])
    a := b.Alloc()
    var s: u64 = 99
    var ps: [64]*u8 = [raw.Null[u8](); 64]
    var sizes: [64]int = [0; 64]
    var fills: [64]u8 = [0; 64]
    var n := 0
    var bad := 0
    for step in 0..20_000 {
        r := ThNext(mut s) % 100
        if r < 55 && n < 64 {
            size := ThNext(mut s) % 300
            p := core.AllocBytes(a, size, 8)
            if raw.IsNull(p) {
                bad += 1
                continue
            }
            ps[n] = p
            sizes[n] = size
            fills[n] = Trunc[u8](step % 251 + 1)
            ThFill(p, size, fills[n])
            n += 1
        } else if n > 0 {
            j := ThNext(mut s) % n
            if !ThFilled(ps[j], sizes[j], fills[j]) {
                bad += 1
            }
            if r < 70 {
                new := ThNext(mut s) % 400
                if core.ResizeBytes(a, ps[j], sizes[j], new, 8) {
                    var kept := sizes[j]
                    if new < kept {
                        kept = new
                    }
                    if !ThFilled(ps[j], kept, fills[j]) {
                        bad += 1
                    }
                    ThFill(ps[j], new, fills[j])
                    sizes[j] = new
                }
            } else {
                core.FreeBytes(a, ps[j], sizes[j], 8)
                n -= 1
                ps[j] = ps[n]
                sizes[j] = sizes[n]
                fills[j] = fills[n]
            }
        }
    }
    for n > 0 {
        n -= 1
        if !ThFilled(ps[n], sizes[n], fills[n]) {
            bad += 1
        }
        core.FreeBytes(a, ps[n], sizes[n], 8)
    }
    // The heap never refused, no block was overwritten, and nothing that
    // went to the heap stayed there.
    Assert(bad == 0 && c.Live() == 0 && c.Bytes() == 0 && c.Peak() > 0)
}
