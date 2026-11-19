// Why: Atomic[T] must behave the same for every width, order and target.
// What: tests of atomic.Atomic[T]'s operations and atomic.Fence.
// How: run by `mk test --std=std.atomic -run "AtomicT "`
// (std/std-sync_atomic_test.go: dev and release, 1 and 8 carriers, several
// jitter seeds, both targets). The orders an operation cannot have are E0706
// at compile time (TestSTDSYNC_AtomicOrderRejected); a pointer FetchAdd is
// E0701.

import std.raw

// One value round trip over every operation that takes any T.
RoundTrip[T: Plain + Eq](a: T, b: T): bool {
    x := Atomic[T].New(a)
    if !x.Load(Relaxed).Eq(&a) || !x.Load(Acquire).Eq(&a) || !x.Load(SeqCst).Eq(&a) {
        return false
    }
    x.Store(b, Relaxed)
    if !x.Load(SeqCst).Eq(&b) {
        return false
    }
    x.Store(a, Release)
    x.Store(b, SeqCst)
    if !x.Swap(a, Relaxed).Eq(&b) || !x.Swap(b, Acquire).Eq(&a) || !x.Swap(a, Release).Eq(&b) {
        return false
    }
    if !x.Swap(b, AcqRel).Eq(&a) || !x.Swap(a, SeqCst).Eq(&b) {
        return false
    }
    // The word holds a. A failing exchange writes nothing and returns it.
    var r := x.CompareExchange(b, b, SeqCst, Relaxed)
    if r.ok || !r.value.Eq(&a) || !x.Load(Relaxed).Eq(&a) {
        return false
    }
    r = x.CompareExchange(a, b, AcqRel, Acquire)
    if !r.ok || !r.value.Eq(&a) || !x.Load(Relaxed).Eq(&b) {
        return false
    }
    r = x.CompareExchange(b, a, Release, Relaxed)
    if !r.ok || !r.value.Eq(&b) {
        return false
    }
    r = x.CompareExchange(a, b, Relaxed, SeqCst)
    if !r.ok || !r.value.Eq(&a) {
        return false
    }
    // A weak exchange may fail spuriously, so it is retried.
    for {
        w := x.CompareExchangeWeak(b, a, Acquire, Relaxed)
        if w.ok {
            break
        }
        if !w.value.Eq(&b) {
            return false
        }
    }
    return x.Load(Acquire).Eq(&a)
}

test "AtomicT every integer width and a pointer round trip" {
    Assert(RoundTrip[u8](3, 250))
    Assert(RoundTrip[i8](-128, 127))
    Assert(RoundTrip[u16](1, 65535))
    Assert(RoundTrip[i16](-32768, 32767))
    Assert(RoundTrip[u32](7, 4294967295))
    Assert(RoundTrip[i32](-2147483648, 2147483647))
    Assert(RoundTrip[u64](0, 18446744073709551615))
    Assert(RoundTrip[i64](-9223372036854775808, 9223372036854775807))
    Assert(RoundTrip[int](-1, 1))
    Assert(RoundTrip[uint](0, 18446744073709551615))
    var p: u8 = 1
    var q: u8 = 2
    Assert(RoundTripPtr(raw.Of(&p), raw.Of(&q)))
    Assert(RoundTripPtr(raw.Null[u8](), raw.Of(&q)))
}

// RoundTrip over a raw pointer, compared by address (raw pointers have no
// Eq).
RoundTripPtr(a: *u8, b: *u8): bool {
    x := Atomic[*u8].New(a)
    if !raw.Same(x.Load(Acquire), a) {
        return false
    }
    x.Store(b, Release)
    if !raw.Same(x.Swap(a, AcqRel), b) {
        return false
    }
    var r := x.CompareExchange(b, b, SeqCst, Acquire)
    if r.ok || !raw.Same(r.value, a) {
        return false
    }
    r = x.CompareExchange(a, b, AcqRel, Relaxed)
    if !r.ok || !raw.Same(r.value, a) {
        return false
    }
    for {
        w := x.CompareExchangeWeak(b, a, Release, Relaxed)
        if w.ok {
            break
        }
        if !raw.Same(w.value, b) {
            return false
        }
    }
    return raw.Same(x.Load(SeqCst), a)
}

test "AtomicT FetchAdd and FetchSub wrap at every width" {
    a := Atomic[u8].New(250)
    Assert(a.FetchAdd(10, Relaxed) == 250)
    Assert(a.Load(Relaxed) == 4)
    Assert(a.FetchSub(5, AcqRel) == 4)
    Assert(a.Load(Relaxed) == 255)
    b := Atomic[i16].New(32767)
    Assert(b.FetchAdd(1, SeqCst) == 32767)
    Assert(b.Load(SeqCst) == -32768)
    Assert(b.FetchSub(1, Release) == -32768)
    Assert(b.Load(Acquire) == 32767)
    c := Atomic[u32].New(0)
    Assert(c.FetchSub(1, Acquire) == 0)
    Assert(c.Load(Relaxed) == 4294967295)
    d := Atomic[i64].New(-9223372036854775808)
    Assert(d.FetchSub(1, Relaxed) == -9223372036854775808)
    Assert(d.Load(Relaxed) == 9223372036854775807)
    Assert(d.FetchAdd(1, Relaxed) == 9223372036854775807)
    Assert(d.Load(Relaxed) == -9223372036854775808)
    e := Atomic[uint].New(18446744073709551615)
    Assert(e.FetchAdd(2, Relaxed) == 18446744073709551615)
    Assert(e.Load(Relaxed) == 1)
}

test "AtomicT FetchAnd, FetchOr and FetchXor return the old bits" {
    a := Atomic[u8].New(0b1100_1010)
    Assert(a.FetchAnd(0b0110_0110, Relaxed) == 0b1100_1010)
    Assert(a.Load(Relaxed) == 0b0100_0010)
    Assert(a.FetchOr(0b1000_0001, AcqRel) == 0b0100_0010)
    Assert(a.Load(Relaxed) == 0b1100_0011)
    Assert(a.FetchXor(0b1111_1111, SeqCst) == 0b1100_0011)
    Assert(a.Load(Relaxed) == 0b0011_1100)
    b := Atomic[i32].New(-1)
    Assert(b.FetchAnd(0x7fff_ffff, Release) == -1)
    Assert(b.FetchXor(-1, Acquire) == 0x7fff_ffff)
    Assert(b.Load(Relaxed) == -2147483648)
    Assert(b.FetchOr(1, Relaxed) == -2147483648)
    Assert(b.Load(Relaxed) == -2147483647)
    c := Atomic[u64].New(0)
    for i in 0..64 {
        bit: u64 = 1 << Trunc[u64](i)
        Assert(c.FetchOr(bit, Relaxed) == bit -% 1)
    }
    Assert(c.Load(Relaxed) == 18446744073709551615)
}

test "AtomicT a fence of every order that orders something" {
    a := Atomic[i64].New(0)
    a.Store(1, Relaxed)
    Fence(Release)
    Fence(Acquire)
    Fence(AcqRel)
    Fence(SeqCst)
    Assert(a.Load(Relaxed) == 1)
}

// 8 tasks: FetchAdd and FetchSub (relaxed: the scope's join is the edge
// that makes the totals visible), a CompareExchangeWeak loop on a u16, and
// one bit each through FetchOr.
AtCount(io: Io, n: &Atomic[i64], m: &Atomic[i64], h: &Atomic[u16], bits: &Atomic[u64], k: int) {
    for i in 0..2000 {
        _ = n.FetchAdd(i64(k), Relaxed)
        _ = m.FetchSub(1, Relaxed)
        var old := h.Load(Relaxed)
        for {
            r := h.CompareExchangeWeak(old, old +% 1, AcqRel, Relaxed)
            if r.ok {
                break
            }
            old = r.value
        }
        if i % 97 == 0 {
            io.Yield() else {}
        }
    }
    bit: u64 = 1 << Trunc[u64](k)
    _ = bits.FetchOr(bit, Relaxed)
}

test "AtomicT 8 tasks count every read-modify-write once" (sys: &System) {
    n := Atomic[i64].New(0)
    m := Atomic[i64].New(0)
    h := Atomic[u16].New(65000)
    bits := Atomic[u64].New(0)
    scope s := sys.io.Scope() {
        for k in 0..8 {
            s.go AtCount(sys.io, &n, &m, &h, &bits, k)
        }
    }
    Assert(n.Load(Relaxed) == 2000 * 28)
    Assert(m.Load(Relaxed) == -16000)
    // 65000 + 16000 wraps at 65536.
    Assert(h.Load(Relaxed) == 15464)
    Assert(bits.Load(Relaxed) == 255)
}

// Message passing: the release store of the round number happens before
// the acquire load that reads it, so the relaxed data words written before
// it are seen; the ack edge runs back the same way.
AtProducer(io: Io, data: &[4]Atomic[i64], flag: &Atomic[u32], ack: &Atomic[u32], rounds: u32) {
    for r in 1..rounds + 1 {
        for j in 0..4 {
            data[j].Store(i64(r) * 1000 + i64(j), Relaxed)
        }
        flag.Store(r, Release)
        for ack.Load(Acquire) != r {
            io.Yield() else {}
        }
    }
}

AtConsumer(io: Io, data: &[4]Atomic[i64], flag: &Atomic[u32], ack: &Atomic[u32], bad: &Atomic[i64], rounds: u32) {
    for r in 1..rounds + 1 {
        for flag.Load(Acquire) != r {
            io.Yield() else {}
        }
        for j in 0..4 {
            if data[j].Load(Relaxed) != i64(r) * 1000 + i64(j) {
                _ = bad.FetchAdd(1, Relaxed)
            }
        }
        ack.Store(r, Release)
    }
}

test "AtomicT release and acquire publish relaxed data" (sys: &System) {
    data := [Atomic[i64].New(0), Atomic[i64].New(0), Atomic[i64].New(0), Atomic[i64].New(0)]
    flag := Atomic[u32].New(0)
    ack := Atomic[u32].New(0)
    bad := Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        s.go AtProducer(sys.io, &data, &flag, &ack, 3000)
        s.go AtConsumer(sys.io, &data, &flag, &ack, &bad, 3000)
    }
    Assert(bad.Load(Relaxed) == 0)
}

// The fence form of the same edge: relaxed stores, a release fence, a
// relaxed flag; a relaxed flag load that reads it, an acquire fence, then
// the data (C++20 [atomics.fences] p2).
AtFenceProducer(io: Io, data: &Atomic[i64], flag: &Atomic[u32], ack: &Atomic[u32], rounds: u32) {
    for r in 1..rounds + 1 {
        data.Store(i64(r) * 7, Relaxed)
        Fence(Release)
        flag.Store(r, Relaxed)
        for ack.Load(Relaxed) != r {
            io.Yield() else {}
        }
        Fence(Acquire)
    }
}

AtFenceConsumer(io: Io, data: &Atomic[i64], flag: &Atomic[u32], ack: &Atomic[u32], bad: &Atomic[i64], rounds: u32) {
    for r in 1..rounds + 1 {
        for flag.Load(Relaxed) != r {
            io.Yield() else {}
        }
        Fence(Acquire)
        if data.Load(Relaxed) != i64(r) * 7 {
            _ = bad.FetchAdd(1, Relaxed)
        }
        Fence(Release)
        ack.Store(r, Relaxed)
    }
}

test "AtomicT fences make relaxed operations publish" (sys: &System) {
    data := Atomic[i64].New(0)
    flag := Atomic[u32].New(0)
    ack := Atomic[u32].New(0)
    bad := Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        s.go AtFenceProducer(sys.io, &data, &flag, &ack, 3000)
        s.go AtFenceConsumer(sys.io, &data, &flag, &ack, &bad, 3000)
    }
    Assert(bad.Load(Relaxed) == 0)
}

// Store buffering (Dekker): with SeqCst stores and loads, the two loads of
// one round cannot both miss the other side's store, because all four fall
// in one total order. With release stores and acquire loads x86-64 lets
// both miss (its store buffer), so this fails if SeqCst lowers to a plain
// mov store. Each round starts when both sides have seen start == r.
AtDekker(io: Io, mine: &Atomic[u32], other: &Atomic[u32], start: &Atomic[u32], seen: &Atomic[u32], done: &Atomic[u32], rounds: u32) {
    for r in 1..rounds + 1 {
        var spins := 0
        for start.Load(Acquire) != r {
            spins += 1
            if spins % 64 == 0 {
                io.Yield() else {}
            }
        }
        mine.Store(r, SeqCst)
        if other.Load(SeqCst) == r {
            _ = seen.FetchAdd(1, Relaxed)
        }
        _ = done.FetchAdd(1, Release)
    }
}

test "AtomicT SeqCst forbids the store-buffering outcome" (sys: &System) {
    x := Atomic[u32].New(0)
    y := Atomic[u32].New(0)
    start := Atomic[u32].New(0)
    done := Atomic[u32].New(0)
    sx := Atomic[u32].New(0)
    sy := Atomic[u32].New(0)
    rounds: u32 = 2000
    scope s := sys.io.Scope() {
        s.go AtDekker(sys.io, &x, &y, &start, &sx, &done, rounds)
        s.go AtDekker(sys.io, &y, &x, &start, &sy, &done, rounds)
        for r in 1..rounds + 1 {
            seen_before := sx.Load(Relaxed) + sy.Load(Relaxed)
            start.Store(r, Release)
            for done.Load(Acquire) != 2 * r {
                sys.io.Yield() else {}
            }
            // At least one side saw the other's store this round.
            Assert(sx.Load(Relaxed) + sy.Load(Relaxed) > seen_before)
        }
    }
}

// Pointer exchange: 8 tasks swap their own pointer into one slot 500
// times each, keeping what they get back. No pointer is lost or doubled:
// the addresses taken out plus the one left equal those put in plus the
// first one (a wrapping sum, compared as such).
// A raw pointer is not Send, but an Atomic[*u64] is, so each task
// gets its pointer in one.
AtSwapper(io: Io, slot: &Atomic[*u64], mine: &Atomic[*u64], sum: &Atomic[uint]) {
    var hold := mine.Load(Relaxed)
    for i in 0..500 {
        hold = slot.Swap(hold, AcqRel)
        if i % 31 == 0 {
            io.Yield() else {}
        }
    }
    _ = sum.FetchAdd(raw.Addr(hold), Relaxed)
}

test "AtomicT pointers swapped by 8 tasks are conserved" (sys: &System) {
    var cells: [9]u64 = [0, 1, 2, 3, 4, 5, 6, 7, 8]
    first := raw.Of(&cells[8])
    slot := Atomic[*u64].New(first)
    sum := Atomic[uint].New(0)
    var want: uint = raw.Addr(first)
    var own: [8]Atomic[*u64] = [Atomic[*u64].New(first), Atomic[*u64].New(first), Atomic[*u64].New(first),
        Atomic[*u64].New(first), Atomic[*u64].New(first), Atomic[*u64].New(first), Atomic[*u64].New(first),
        Atomic[*u64].New(first)]
    for k in 0..8 {
        p := raw.Of(&cells[k])
        own[k].Store(p, Relaxed)
        want = want +% raw.Addr(p)
    }
    scope s := sys.io.Scope() {
        for k in 0..8 {
            s.go AtSwapper(sys.io, &slot, &own[k], &sum)
        }
    }
    Assert(sum.Load(Relaxed) +% raw.Addr(slot.Load(Acquire)) == want)
}

// The same operations at compile time (the evaluator: every order is
// seq_cst there, one thread), so a const may be computed with them.
AtEvalOps(): i64 {
    a := Atomic[i64].New(5)
    var t := a.FetchAdd(3, Relaxed)
    t += a.FetchSub(1, AcqRel)
    t += a.FetchOr(16, Release)
    t += a.FetchAnd(0x17, Acquire)
    t += a.FetchXor(1, SeqCst)
    t += a.Swap(100, SeqCst)
    r := a.CompareExchange(100, 7, SeqCst, Acquire)
    f := a.CompareExchange(100, 9, SeqCst, Relaxed)
    if r.ok && !f.ok && f.value == 7 {
        t += a.Load(Acquire)
    }
    h := Atomic[u16].New(65535)
    t += i64(h.FetchAdd(1, Relaxed))
    t += i64(h.Load(Relaxed))
    Fence(SeqCst)
    return t
}

const AtFolded: i64 = AtEvalOps()

test "AtomicT the evaluator agrees with native code" {
    // The old values 5, 8, 7, 23 (or 16), 23 (and 0x17), 22 (xor 1, then
    // the swap), the 7 the exchange left, then 65535 and the wrapped 0.
    Assert(AtFolded == 5 + 8 + 7 + 23 + 23 + 22 + 7 + 65535)
    Assert(AtEvalOps() == AtFolded)
}
