// Why: a FixedHeap keeps its lists and block headers inside the buffer it
// hands out, so one wrong link or size corrupts memory far from the cause.
// What: ThWalk, which checks the invariants H1 to H4 of fixedheap.mk over a
// whole buffer, random runs of raw requests that call it after every
// operation, and tests of the FixedHeap through the containers, as user
// code uses it.
// How: run by `mk test --std=std.mem` with the counting test heap. Every
// block of a random run is filled with a byte of its own and read back
// before it is freed or resized, so two blocks that overlap are seen.
// Helper names start with Th.

import std.core
import std.raw

// ThWalk is 0 when h's buffer holds a well-formed heap, else the number of
// the first check that failed: 1 to 5 are H1, H2 and H4 over the blocks in
// address order, 6 to 12 are H3 over the lists and maps.
ThWalk(h: &FixedHeap): int {
    if h.fls == 0 {
        return 0
    }
    b := h.base
    var g := h.first
    var prev := 0
    var prev_free := false
    var free := 0
    var free_granules := 0
    for g < h.last {
        s := FhR(b, (g << 3) + 4)
        n := s & FhSize
        is_free := (s & FhFree) != 0
        if FhR(b, g << 3) != prev {
            return 1
        }
        if n < 2 || g + n > h.last {
            return 2
        }
        if is_free && prev_free {
            return 3
        }
        if is_free {
            free += 1
            free_granules += n
        }
        prev = n
        prev_free = is_free
        g += n
    }
    if g != h.last || FhR(b, h.last << 3) != prev {
        return 4
    }
    if FhR(b, (h.last << 3) + 4) != 1 {
        return 5
    }
    var listed := 0
    var listed_granules := 0
    var flmap := 0
    for f in 0..h.fls {
        var slmap := 0
        for s in 0..16 {
            i := (f << 4) + s
            var x := FhR(b, h.heads + (i << 2))
            var back := 0
            if x != 0 {
                slmap = slmap | (1 << s)
            }
            for x != 0 {
                if x < h.first || x >= h.last {
                    return 6
                }
                sz := FhR(b, (x << 3) + 4)
                if (sz & FhFree) == 0 || FhList(sz & FhSize) != i {
                    return 7
                }
                if FhR(b, (x << 3) + 12) != back {
                    return 8
                }
                listed += 1
                listed_granules += sz & FhSize
                if listed > free {
                    return 9
                }
                back = x
                x = FhR(b, (x << 3) + 8)
            }
        }
        if FhR(b, 4 + (f << 2)) != slmap {
            return 10
        }
        if slmap != 0 {
            flmap = flmap | (1 << f)
        }
    }
    if FhR(b, 0) != flmap {
        return 11
    }
    if listed != free || listed_granules != free_granules {
        return 12
    }
    return 0
}

// ThWhole says whether h's buffer is one free block from first to last:
// everything that was allocated came back and merged.
ThWhole(h: &FixedHeap): bool {
    return h.fls != 0 && FhR(h.base, (h.first << 3) + 4) == ((h.last - h.first) | FhFree)
}

// A seeded generator, so a block's inputs are fixed.
ThNext(s: mut u64): int {
    *s = *s *% 6364136223846793005 +% 1442695040888963407
    return Trunc[int](*s >> 33)
}

// ThBlk is one live block of a random run: its pointer, size and alignment,
// and the byte every one of its bytes holds.
struct ThBlk {
    p: *u8
    size: int
    align: int
    fill: u8
}

// ThFilled says whether the first n bytes at p all hold fill.
ThFilled(p: *u8, n: int, fill: u8): bool {
    unsafe "p is a live block of at least n bytes" {
        for i in 0..n {
            if raw.Read[u8](raw.Offset(p, i)) != fill {
                return false
            }
        }
    }
    return true
}

ThFill(p: *u8, n: int, fill: u8) {
    unsafe "p is a live block of at least n bytes" {
        raw.Fill(p, fill, n)
    }
}

// ThRun makes steps random requests of up to max_size bytes over h (half
// allocations at alignments 1 to 4096, the rest frees and resizes of a
// random live block), checks the invariants and the contents after each,
// then frees everything. It returns 0, or 100 times the step that failed
// plus what failed there: ThWalk's number, 20 a misaligned block, 21 a
// block outside the buffer, 22 contents another request changed, 30 a
// buffer that is not one free block at the end.
ThRun(h: &FixedHeap, seed: u64, steps: int, max_size: int): int {
    a := h.Alloc()
    var s := seed
    var blks: [256]ThBlk = [ThBlk(p: raw.Null[u8](), size: 0, align: 1, fill: 0); 256]
    var n := 0
    lo := raw.Addr(h.base)
    hi := lo + Trunc[uint](h.last << 3)
    for step in 0..steps {
        r := ThNext(mut s) % 100
        if r < 50 && n < 256 {
            size := ThNext(mut s) % (max_size + 1)
            var align := 1 << (ThNext(mut s) % 4)
            if ThNext(mut s) % 8 == 0 {
                align = 16 << (ThNext(mut s) % 9)
            }
            p := core.AllocBytes(a, size, align)
            if !raw.IsNull(p) {
                at := raw.Addr(p)
                if at % Trunc[uint](align) != 0 || at % 8 != 0 {
                    return step * 100 + 20
                }
                if at < lo || at + Trunc[uint](size) > hi {
                    return step * 100 + 21
                }
                fill := Trunc[u8](step % 251 + 1)
                ThFill(p, size, fill)
                blks[n] = ThBlk(p: p, size: size, align: align, fill: fill)
                n += 1
            }
        } else if n > 0 {
            j := ThNext(mut s) % n
            k := blks[j]
            if !ThFilled(k.p, k.size, k.fill) {
                return step * 100 + 22
            }
            if r < 75 {
                new := ThNext(mut s) % (max_size * 2 + 1)
                if core.ResizeBytes(a, k.p, k.size, new, k.align) {
                    // The bytes kept are the block's; the bytes gained are
                    // filled like them.
                    var kept := k.size
                    if new < kept {
                        kept = new
                    }
                    if !ThFilled(k.p, kept, k.fill) {
                        return step * 100 + 22
                    }
                    ThFill(k.p, new, k.fill)
                    blks[j] = ThBlk(p: k.p, size: new, align: k.align, fill: k.fill)
                }
            } else {
                core.FreeBytes(a, k.p, k.size, k.align)
                blks[j] = blks[n - 1]
                n -= 1
            }
        }
        w := ThWalk(h)
        if w != 0 {
            return step * 100 + w
        }
    }
    for n > 0 {
        n -= 1
        k := blks[n]
        if !ThFilled(k.p, k.size, k.fill) {
            return steps * 100 + 22
        }
        core.FreeBytes(a, k.p, k.size, k.align)
        w := ThWalk(h)
        if w != 0 {
            return steps * 100 + w
        }
    }
    if !ThWhole(h) {
        return steps * 100 + 30
    }
    return 0
}

// ThLead is how far b's first 8-byte address boundary lies past its start:
// a FixedHeap never hands out the bytes before it.
ThLead(b: []u8): int {
    return Trunc[int]((0 -% raw.Addr(raw.Data(b))) & 7)
}

struct ThPair {
    a: int
    b: i64
}

test "FixedHeap: a fresh heap is one free block, and holds H1 to H4" {
    var buf: [4104]u8 = [0; 4104]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 4096])
    Assert(ThWalk(&h) == 0 && ThWhole(&h))
    // 4 KiB is 512 granules: 6 first levels, 416 bytes of control words,
    // and the largest request is the rest less the end marker and one
    // header.
    Assert(h.fls == 6 && h.first == 52 && h.last == 511 && h.max == 4096 - 416 - 16)
}

test "FixedHeap: every buffer of 96 bytes or more holds a heap, and a longer one never a smaller block" {
    var buf: [70_008]u8 = [0; 70_008]
    lead := ThLead(buf[..])
    var before := 0
    var n := 0
    for n <= 70_000 {
        h := FixedHeap.New(mut buf[lead..lead + n])
        if n < 96 {
            Assert(h.fls == 0 && h.max == 0)
        } else {
            // The largest block needs every first level the heap has, and
            // no more: none is wasted, none is missing.
            Assert(h.fls == (FhList(h.last - h.first) >> 4) + 1)
            Assert(h.max >= 8 && h.max >= before && ThWalk(&h) == 0 && ThWhole(&h))
            p := core.AllocBytes(h.Alloc(), h.max, 1)
            Assert(!raw.IsNull(p) && ThWalk(&h) == 0)
            core.FreeBytes(h.Alloc(), p, h.max, 1)
            Assert(ThWhole(&h))
        }
        before = h.max
        // Every length to 2,200 bytes, then steps that are no multiple of
        // 8 across each doubling up to 64 KiB.
        if n < 2_200 {
            n += 1
        } else {
            n += 61
        }
    }
}

test "FixedHeap: the fit a request is promised, at 248 bytes and at 256" {
    var buf: [8200]u8 = [0; 8200]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 8192])
    a := h.Alloc()
    // Free blocks of exactly 248 and 256 bytes of payload, each between
    // two used blocks, and nothing else free.
    k1 := core.AllocBytes(a, 248, 8)
    s1 := core.AllocBytes(a, 8, 8)
    k2 := core.AllocBytes(a, 256, 8)
    s2 := core.AllocBytes(a, 8, 8)
    rest := core.AllocBytes(a, h.max - 248 - 256 - 16 - 4 * 8, 8)
    Assert(!raw.IsNull(k1) && !raw.IsNull(s1) && !raw.IsNull(k2) && !raw.IsNull(s2) && !raw.IsNull(rest))
    Assert(raw.IsNull(core.AllocBytes(a, 0, 1)))
    core.FreeBytes(a, k2, 256, 8)
    core.FreeBytes(a, k1, 248, 8)
    Assert(ThWalk(&h) == 0)
    // A request of up to 248 bytes takes the block of its own size.
    p := core.AllocBytes(a, 248, 8)
    Assert(raw.Same(p, k1))
    core.FreeBytes(a, p, 248, 8)
    // The two free blocks share a list, and the smaller heads it now: a
    // request of 256 bytes is promised a block a sixteenth larger, finds
    // none, and is refused though a block of its size is free.
    Assert(raw.IsNull(core.AllocBytes(a, 256, 8)))
    // Once the smaller block is taken, the block of its size heads the
    // list, and it is served.
    p2 := core.AllocBytes(a, 248, 8)
    q := core.AllocBytes(a, 256, 8)
    Assert(raw.Same(p2, k1) && raw.Same(q, k2) && ThWalk(&h) == 0)
    core.FreeBytes(a, q, 256, 8)
    core.FreeBytes(a, p2, 248, 8)
    core.FreeBytes(a, s1, 8, 8)
    core.FreeBytes(a, s2, 8, 8)
    core.FreeBytes(a, rest, h.max - 248 - 256 - 16 - 4 * 8, 8)
    Assert(ThWhole(&h))
}

test "FixedHeap: random requests keep the invariants and the contents, small blocks" {
    var buf: [8200]u8 = [0; 8200]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 8192])
    Assert(ThRun(&h, 1, 4_000, 96) == 0)
    Assert(ThRun(&h, 2, 4_000, 24) == 0)
}

test "FixedHeap: random requests keep the invariants and the contents, large blocks" {
    var buf: [65544]u8 = [0; 65544]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 65536])
    Assert(ThRun(&h, 3, 4_000, 2_000) == 0)
    Assert(ThRun(&h, 4, 2_000, 12_000) == 0)
}

test "FixedHeap: random requests at every start address of the buffer" {
    var buf: [2064]u8 = [0; 2064]
    for shift in 0..9 {
        h := FixedHeap.New(mut buf[shift..shift + 2048])
        Assert(ThRun(&h, Trunc[u64](shift) + 10, 1_500, 120) == 0)
    }
}

test "FixedHeap: a buffer too small for one block refuses every request" {
    var buf: [128]u8 = [0; 128]
    for n in 0..97 {
        h := FixedHeap.New(mut buf[0..n])
        if h.fls == 0 {
            a := h.Alloc()
            Assert(raw.IsNull(core.AllocBytes(a, 0, 1)) && raw.IsNull(core.AllocBytes(a, 1, 8)) && raw.IsNull(core.AllocBytes(a, 8, 64)))
            // A free or a resize of any pointer changes nothing.
            core.FreeBytes(a, h.base, 8, 8)
            Assert(!core.ResizeBytes(a, h.base, 8, 4, 8))
        }
        Assert(ThWalk(&h) == 0)
    }
    // 96 bytes at an 8-byte boundary hold the control words of one first
    // level, one block of 16 bytes and the end marker.
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 96])
    Assert(h.fls == 1 && h.max == 8)
    a := h.Alloc()
    p := core.AllocBytes(a, 8, 8)
    Assert(!raw.IsNull(p) && raw.IsNull(core.AllocBytes(a, 1, 1)))
    core.FreeBytes(a, p, 8, 8)
    Assert(ThWhole(&h))
}

test "FixedHeap: the largest request takes the whole buffer, and one byte more is refused" {
    var buf: [4104]u8 = [0; 4104]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 4096])
    a := h.Alloc()
    Assert(raw.IsNull(core.AllocBytes(a, h.max + 1, 1)))
    p := core.AllocBytes(a, h.max, 1)
    Assert(!raw.IsNull(p) && ThWalk(&h) == 0)
    Assert(raw.IsNull(core.AllocBytes(a, 0, 1)))
    ThFill(p, h.max, 0x5A)
    Assert(ThWalk(&h) == 0)
    core.FreeBytes(a, p, h.max, 1)
    Assert(ThWhole(&h))
}

test "FixedHeap: blocks freed in any order merge back into one" {
    var buf: [4104]u8 = [0; 4104]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 4096])
    a := h.Alloc()
    var ps: [40]*u8 = [raw.Null[u8](); 40]
    // Six orders of freeing 40 blocks of 64 bytes: forward, backward, and
    // strides of 3, 7, 11 and 13, each a permutation of 0..40.
    for order in 0..6 {
        for i in 0..40 {
            ps[i] = core.AllocBytes(a, 64, 8)
            Assert(!raw.IsNull(ps[i]))
        }
        Assert(ThWalk(&h) == 0)
        for i in 0..40 {
            var j := i
            if order == 1 {
                j = 39 - i
            } else if order == 2 {
                j = i * 3 % 40
            } else if order == 3 {
                j = i * 7 % 40
            } else if order == 4 {
                j = i * 11 % 40
            } else if order == 5 {
                j = i * 13 % 40
            }
            core.FreeBytes(a, ps[j], 64, 8)
            Assert(ThWalk(&h) == 0)
        }
        Assert(ThWhole(&h))
    }
}

test "FixedHeap: a freed block is used again, so churn never runs the buffer out" {
    var buf: [1032]u8 = [0; 1032]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 1024])
    a := h.Alloc()
    // Four live boxes at a time, the oldest freed for each new one: 100,000
    // allocations through a buffer that holds a few dozen.
    var live: [4]*u8 = [raw.Null[u8](); 4]
    for i in 0..100_000 {
        k := i % 4
        if !raw.IsNull(live[k]) {
            core.FreeBytes(a, live[k], 24 + k * 16, 8)
        }
        live[k] = core.AllocBytes(a, 24 + k * 16, 8)
        Assert(!raw.IsNull(live[k]))
    }
    for k in 0..4 {
        core.FreeBytes(a, live[k], 24 + k * 16, 8)
    }
    Assert(ThWalk(&h) == 0 && ThWhole(&h))
}

test "FixedHeap: a block grows in place into the free block after it, and shrinks in place" {
    var buf: [4104]u8 = [0; 4104]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 4096])
    a := h.Alloc()
    p := core.AllocBytes(a, 100, 8)
    q := core.AllocBytes(a, 100, 8)
    Assert(!raw.IsNull(p) && !raw.IsNull(q))
    // q follows p, so p cannot grow past its own rounding.
    Assert(core.ResizeBytes(a, p, 100, 104, 8) && !core.ResizeBytes(a, p, 104, 105, 8))
    core.FreeBytes(a, q, 100, 8)
    // Now the block after p is free: p grows to everything that is left,
    // and not one byte further.
    Assert(core.ResizeBytes(a, p, 104, 3000, 8) && ThWalk(&h) == 0)
    Assert(core.ResizeBytes(a, p, 3000, h.max, 8) && !core.ResizeBytes(a, p, h.max, h.max + 1, 8))
    Assert(raw.IsNull(core.AllocBytes(a, 1, 1)))
    // Shrinking gives the rest back at once.
    Assert(core.ResizeBytes(a, p, h.max, 16, 8) && ThWalk(&h) == 0)
    r := core.AllocBytes(a, 3000, 8)
    Assert(!raw.IsNull(r))
    core.FreeBytes(a, r, 3000, 8)
    // A resize to nothing keeps the block, which is still freed.
    Assert(core.ResizeBytes(a, p, 16, 0, 8))
    core.FreeBytes(a, p, 0, 8)
    Assert(ThWalk(&h) == 0 && ThWhole(&h))
}

test "FixedHeap: alignments from 1 to 4096, each block where its alignment says" {
    var buf: [32776]u8 = [0; 32776]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 32768])
    a := h.Alloc()
    var ps: [13]*u8 = [raw.Null[u8](); 13]
    for e in 0..13 {
        ps[e] = core.AllocBytes(a, 24, 1 << e)
        Assert(!raw.IsNull(ps[e]) && raw.Addr(ps[e]) % Trunc[uint](1 << e) == 0)
        Assert(ThWalk(&h) == 0)
    }
    for e in 0..13 {
        core.FreeBytes(a, ps[e], 24, 1 << e)
        Assert(ThWalk(&h) == 0)
    }
    Assert(ThWhole(&h))
}

test "FixedHeap: requests no container makes are refused and change nothing" {
    var buf: [4104]u8 = [0; 4104]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 4096])
    a := h.Alloc()
    p := core.AllocBytes(a, 64, 8)
    Assert(!raw.IsNull(p))
    bad := [0, 3, 6, 12, -1, -8, 1 << 31, 1 << 40]
    for al in bad {
        Assert(raw.IsNull(core.AllocBytes(a, 16, al)))
    }
    Assert(raw.IsNull(core.AllocBytes(a, -1, 8)) && raw.IsNull(core.AllocBytes(a, -1, 64)))
    Assert(raw.IsNull(core.AllocBytes(a, 1 << 40, 8)) && raw.IsNull(core.AllocBytes(a, 8, 1 << 30)))
    Assert(!core.ResizeBytes(a, p, 64, -1, 8) && !core.ResizeBytes(a, p, 64, 1 << 40, 8))
    // A null pointer, one outside the buffer, one inside the control
    // words and one off an 8-byte boundary are no blocks.
    var other: [16]u8 = [0; 16]
    unsafe "addresses only: nothing is read or written through them" {
        wild := [raw.Null[u8](), raw.DataMut(mut other[..]), h.base, raw.Offset(p, 1), raw.Offset(p, 4)]
        for w in wild {
            core.FreeBytes(a, w, 64, 8)
            Assert(!core.ResizeBytes(a, w, 64, 32, 8))
        }
    }
    Assert(ThWalk(&h) == 0)
    // Freeing a block twice in a row changes nothing the second time,
    // whether it merged into the block before it or not.
    q := core.AllocBytes(a, 64, 8)
    Assert(!raw.IsNull(q))
    core.FreeBytes(a, p, 64, 8)
    core.FreeBytes(a, p, 64, 8)
    core.FreeBytes(a, q, 64, 8)
    core.FreeBytes(a, q, 64, 8)
    Assert(ThWalk(&h) == 0 && ThWhole(&h))
}

test "FixedHeap: Reset makes the whole buffer free again" {
    var buf: [4104]u8 = [0; 4104]
    lead := ThLead(buf[..])
    var h := FixedHeap.New(mut buf[lead..lead + 4096])
    for round in 0..3 {
        // Leave the buffer in pieces: Reset needs nothing freed first.
        for _ in 0..20 {
            _ = core.AllocBytes(h.Alloc(), 40 + round * 8, 8)
        }
        h.Reset()
        Assert(ThWalk(&h) == 0 && ThWhole(&h))
    }
}

test "FixedHeap: a HeapList grows in place while nothing follows it, and its buffer is reused after Free" {
    var buf: [4104]u8 = [0; 4104]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 4096])
    a := h.Alloc()
    for _ in 0..50 {
        var xs := HeapList[i64].New(a)
        var oom := -1
        for i in 0..1_000 {
            if oom < 0 {
                xs.Push(a, Cast[i64](i)) else {
                    oom = i
                }
            }
        }
        // 4, 8, ... 256 elements, each step grown in place: 256 * 8 bytes
        // fit the 3,664 the buffer has, and 512 * 8 do not.
        Assert(oom == 256 && xs.Len() == 256)
        var sum: i64 = 0
        for x in xs.Items() {
            sum += x
        }
        Assert(sum == 32640)
        xs.Free(a)
        Assert(ThWhole(&h))
    }
}

// ThFillMap sets i to i * 3 from 0 up in a HeapMap over a, until a Set is
// refused, and returns how many it holds then; the map is freed.
ThFillMap(a: &Alloc): int {
    var m := HeapMap[int, int].New(a)
    var n := 0
    for n < 100_000 {
        m.Set(a, n, n * 3) else {
            break
        }
        n += 1
    }
    // A refused Set leaves the map as it was.
    Assert(m.Len() == n)
    for i in 0..n {
        if v := m.Get(&i) {
            Assert(*v == i * 3)
        } else {
            Assert(false)
        }
    }
    m.Free(a)
    return n
}

test "FixedHeap: a HeapMap grows until refused, and every round gets as far as the first" {
    var buf: [16392]u8 = [0; 16392]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 16384])
    // Each growth frees the old table, which a FixedHeap uses again: no
    // Reset is needed between rounds, where a Fixed would run out.
    n1 := ThFillMap(h.Alloc())
    Assert(n1 > 100 && ThWhole(&h))
    for _ in 0..20 {
        Assert(ThFillMap(h.Alloc()) == n1 && ThWhole(&h))
    }
}

test "FixedHeap: Boxes, a TextMap and a HeapText share one buffer and leave it whole" {
    var buf: [8200]u8 = [0; 8200]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 8192])
    a := h.Alloc()
    var m := TextMap[int].New(a)
    var t := HeapText.New(a)
    keys := ["alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"]
    for round in 0..200 {
        k := keys[round % 8]
        b := Box[ThPair].New(a, ThPair(a: round, b: 2)) else {
            Assert(false)
            continue
        }
        _ = m.Ensure(a, k, round) else {
            Assert(false)
        }
        t.Push(a, k) else {
            Assert(false)
        }
        if round % 3 == 0 {
            _ = m.Remove(a, keys[(round + 4) % 8])
        }
        if round % 16 == 15 {
            t.Set(a, "") else {
                Assert(false)
            }
        }
        Assert(b.Ref().a == round)
        b.Free(a)
        Assert(ThWalk(&h) == 0)
    }
    Assert(m.Len() > 0 && m.Len() <= 8)
    m.Free(a)
    t.Free(a)
    Assert(ThWhole(&h))
}

test "FixedHeap under a Counting: Live and Bytes are zero once everything is freed" {
    var buf: [8200]u8 = [0; 8200]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 8192])
    c := Counting.New(h.Alloc())
    n := ThFillMap(c.Alloc())
    Assert(n > 0 && c.Live() == 0 && c.Bytes() == 0 && c.Peak() > 0 && c.Peak() <= h.max)
    Assert(ThWhole(&h))
}

test "FixedHeap: an arena made in it takes its chunks from the buffer and gives them back" {
    var buf: [65544]u8 = [0; 65544]
    lead := ThLead(buf[..])
    h := FixedHeap.New(mut buf[lead..lead + 65536])
    for _ in 0..5 {
        var ar := Arena.NewIn(h.Alloc()) else {
            Assert(false)
            continue
        }
        var xs := List[int].New(&ar)
        var held := 0
        for i in 0..100_000 {
            xs.Push(i) else {
                break
            }
            held += 1
        }
        // The arena fails inside the buffer, never reaching the heap.
        Assert(held > 100 && held < 100_000 && !ThWhole(&h))
        ar.Free()
        Assert(ThWalk(&h) == 0 && ThWhole(&h))
    }
}
