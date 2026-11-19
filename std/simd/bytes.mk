// Why: searching and comparing bytes is the inner loop of parsers, string
// functions and protocols, and on targets with vector registers a vector
// kernel runs it faster than a byte loop.
//
// What: IndexByte, IndexAny, Count, Equal and Compare, each with its private
// avx2 and avx512bw forms and its aligned bodies.
//
// How: every routine has one body generic over the lane count N, restating
// the lane-count clause of the Vec functions it calls. The public function
// runs the 16-lane instance (Target.vector_bits = 128 on every supported
// target); from 64 bytes up it calls a `cpu "avx2"` function when Cpu()
// reports avx2, and from 256 bytes up a `cpu "avx512bw"` one for IndexByte,
// IndexAny and Equal (Count and Compare measured slower so, and have none).
// Results never depend on the width taken. No routine allocates; each is O(n).
//
// The wide forms of IndexByte, Count, Equal and Compare start their loads on
// an N-byte boundary (32 for avx2, 64 for avx512bw), as glibc's memchr and
// memcmp do: one unaligned vector covers the bytes before it, so no load of
// the main loop splits a cache line (measured: an unaligned slice ran slower
// at L1). The two-slice routines align a, and gain fully when b shares a's
// alignment. The address is read only there: the evaluator never runs those
// functions, since Cpu() is the baseline at compile time, and raw.Addr would
// be refused there (E0901 pointer-bytes; TestSTDSIMD_CompileTime keeps that
// true).

import std.raw

// DispatchMin is the length from which a routine tests Cpu() for avx2: below
// it the baseline instance finishes in a few vectors, cheaper than the call.
const DispatchMin: int = 64

// WideMin is the length from which a routine tests Cpu() for avx512bw: the
// 64-lane four-vector step covers 256 bytes, and below it the avx2 form
// does as well.
const WideMin: int = 256

// AlignHead is the count of bytes from the start of s to the next a-byte
// boundary (a a power of two), 0 to a - 1. Called only on the avx2 paths,
// with s.Len() >= 64 >= a.
AlignHead(s: []u8, a: int): int {
    m := Cast[uint](a - 1)
    return Cast[int]((Cast[uint](a) - (raw.Addr(raw.Data(s)) & m)) & m)
}

// LowBits is the mask of the lanes below h.
LowBits(h: int): u64 {
    return (Cast[u64](1) << h) - 1
}

/// Returns the offset of the first byte of s equal to b, or none. O(n); it
/// allocates nothing.
///
/// Each step compares four vectors and tests them once; the tail is one
/// overlapping load of the last vector's worth of bytes. avx2 is used from
/// 64 bytes and avx512bw from 256 when the CPU has them.
export IndexByte(s: []u8, b: u8): ?int {
    if Target.os == Os.Freestanding {
        return IndexByteWords(s, b)
    }
    if s.Len() >= WideMin && Cpu().Has("avx512bw") {
        unsafe "Cpu() reports avx512bw, which IndexByteAvx512's clause names" {
            return IndexByteAvx512(s, b)
        }
    }
    if s.Len() >= DispatchMin && Cpu().Has("avx2") {
        unsafe "Cpu() reports avx2, which IndexByteAvx2's clause names" {
            return IndexByteAvx2(s, b)
        }
    }
    return IndexByteN[16](s, b)
}

IndexByteAvx2(s: []u8, b: u8): ?int cpu "avx2" {
    return IndexByteAligned[32](s, b)
}

IndexByteAvx512(s: []u8, b: u8): ?int cpu "avx512bw" {
    return IndexByteAligned[64](s, b)
}

// IndexByteAligned needs s.Len() >= N. A match in the unaligned head
// vector is the first one.
IndexByteAligned[const N: int](s: []u8, b: u8): ?int
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 {
    h := AlignHead(s, N)
    if h != 0 {
        m := (Vec[u8, N].FromSlice(s, 0) == Vec[u8, N].Splat(b)).Bits()
        if m != 0 {
            return some(raw.Ctz64(m))
        }
    }
    if k := IndexByteN[N](s[h..], b) {
        return some(h + k)
    }
    return none
}

IndexByteN[const N: int](s: []u8, b: u8): ?int
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 {
    n := s.Len()
    if n < N {
        for k in 0..n {
            if s[k] == b {
                return some(k)
            }
        }
        return none
    }
    nd := Vec[u8, N].Splat(b)
    zero := Vec[u8, N].Zero()
    var i := 0
    for i + 4 * N <= n {
        // A lane of x is zero where the byte is b; the lanewise minimum
        // of the four is zero where any is, so one compare tests 4N
        // bytes (glibc's AVX2 and EVEX memchr: one compare instead of
        // four, fewer mask operations at 64 lanes).
        w := s[i..i + 4 * N]
        x0 := Vec[u8, N].FromSlice(w, 0) ^ nd
        x1 := Vec[u8, N].FromSlice(w, N) ^ nd
        x2 := Vec[u8, N].FromSlice(w, 2 * N) ^ nd
        x3 := Vec[u8, N].FromSlice(w, 3 * N) ^ nd
        if (x0.Min(x1).Min(x2.Min(x3)) == zero).Any() {
            break
        }
        i += 4 * N
    }
    for i + N <= n {
        m := (Vec[u8, N].FromSlice(s, i) == nd).Bits()
        if m != 0 {
            return some(i + raw.Ctz64(m))
        }
        i += N
    }
    if i < n {
        // The last N bytes, of which the first N - (n - i) were seen.
        m := (Vec[u8, N].FromSlice(s, n - N) == nd).Bits() >> (N - (n - i))
        if m != 0 {
            return some(i + raw.Ctz64(m))
        }
    }
    return none
}

/// Returns the offset of the first byte of s that is in set, or none (also
/// when set is empty). O(n + set.Len()); it allocates nothing.
///
/// A set of one byte is IndexByte. Two or three bytes are compared lane by
/// lane. Four or more are looked up in two 16-byte tables built on the
/// stack once per call, in O(set.Len()), so a large set costs no more per
/// byte of s than a set of four.
export IndexAny(s: []u8, set: []u8): ?int {
    if Target.os == Os.Freestanding {
        return IndexAnyWords(s, set)
    }
    k := set.Len()
    if k == 0 {
        return none
    }
    if k == 1 {
        return IndexByte(s, set[0])
    }
    if k <= 3 {
        c := set[k - 1]
        if s.Len() >= WideMin && Cpu().Has("avx512bw") {
            unsafe "Cpu() reports avx512bw, which IndexAnyFewAvx512's clause names" {
                return IndexAnyFewAvx512(s, set[0], set[1], c)
            }
        }
        if s.Len() >= DispatchMin && Cpu().Has("avx2") {
            unsafe "Cpu() reports avx2, which IndexAnyFewAvx2's clause names" {
                return IndexAnyFewAvx2(s, set[0], set[1], c)
            }
        }
        return IndexAnyFewN[16](s, set[0], set[1], c)
    }
    var lo: [16]u8 = [0; 16]
    var hi: [16]u8 = [0; 16]
    for x in set {
        if x < 0x80 {
            lo[x & 15] |= Trunc[u8](1 << (x >> 4))
        } else {
            hi[x & 15] |= Trunc[u8](1 << ((x >> 4) - 8))
        }
    }
    if s.Len() >= WideMin && Cpu().Has("avx512bw") {
        unsafe "Cpu() reports avx512bw, which IndexSetAvx512's clause names" {
            return IndexSetAvx512(s, lo, hi)
        }
    }
    if s.Len() >= DispatchMin && Cpu().Has("avx2") {
        unsafe "Cpu() reports avx2, which IndexSetAvx2's clause names" {
            return IndexSetAvx2(s, lo, hi)
        }
    }
    return IndexSetN[16](s, Vec[u8, 16].FromArray(lo), Vec[u8, 16].FromArray(hi))
}

// IndexAnyFewAvx2 and IndexSetAvx2 do not align their loads: the set test
// is compute-bound, and measured with an aligned head it gained nothing on
// three bytes and made Truffle slower at L1.
IndexAnyFewAvx2(s: []u8, a: u8, b: u8, c: u8): ?int cpu "avx2" {
    return IndexAnyFewN[32](s, a, b, c)
}

IndexAnyFewAvx512(s: []u8, a: u8, b: u8, c: u8): ?int cpu "avx512bw" {
    return IndexAnyFewN[64](s, a, b, c)
}

// IndexAnyFewN finds the first byte equal to a, b or c (c repeats b when the
// set has two bytes, and the compare is then redundant but harmless).
IndexAnyFewN[const N: int](s: []u8, a: u8, b: u8, c: u8): ?int
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 {
    n := s.Len()
    if n < N {
        for k in 0..n {
            x := s[k]
            if x == a || x == b || x == c {
                return some(k)
            }
        }
        return none
    }
    na := Vec[u8, N].Splat(a)
    nb := Vec[u8, N].Splat(b)
    nc := Vec[u8, N].Splat(c)
    var i := 0
    for i + 2 * N <= n {
        w := s[i..i + 2 * N]
        v0 := Vec[u8, N].FromSlice(w, 0)
        v1 := Vec[u8, N].FromSlice(w, N)
        m0 := (v0 == na) | (v0 == nb) | (v0 == nc)
        m1 := (v1 == na) | (v1 == nb) | (v1 == nc)
        if (m0 | m1).Any() {
            break
        }
        i += 2 * N
    }
    for i + N <= n {
        v := Vec[u8, N].FromSlice(s, i)
        m := ((v == na) | (v == nb) | (v == nc)).Bits()
        if m != 0 {
            return some(i + raw.Ctz64(m))
        }
        i += N
    }
    if i < n {
        v := Vec[u8, N].FromSlice(s, n - N)
        m := ((v == na) | (v == nb) | (v == nc)).Bits() >> (N - (n - i))
        if m != 0 {
            return some(i + raw.Ctz64(m))
        }
    }
    return none
}

// IndexSetAvx2 takes the tables as arrays: a cpu function takes no vector.
IndexSetAvx2(s: []u8, lo: [16]u8, hi: [16]u8): ?int cpu "avx2" {
    return IndexSetN[32](s, Vec[u8, 16].FromArray(lo), Vec[u8, 16].FromArray(hi))
}

IndexSetAvx512(s: []u8, lo: [16]u8, hi: [16]u8): ?int cpu "avx512bw" {
    return IndexSetN[64](s, Vec[u8, 16].FromArray(lo), Vec[u8, 16].FromArray(hi))
}

// SetMask is Truffle's membership test of every lane of v.
SetMask[const N: int](v: Vec[u8, N], tlo: Vec[u8, 16], thi: Vec[u8, 16]): Mask[u8, N]
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
    requires N == 16 || N == 32 || N == 64 {
    nib := v & Vec[u8, N].Splat(15)
    bit := Lookup16[N](v >> 4, Vec[u8, 16].FromArray([1, 2, 4, 8, 16, 32, 64, 128, 1, 2, 4, 8, 16, 32, 64, 128]))
    row := (v >= Vec[u8, N].Splat(0x80)).Select(Lookup16[N](nib, thi), Lookup16[N](nib, tlo))
    return (row & bit) != Vec[u8, N].Zero()
}

IndexSetN[const N: int](s: []u8, tlo: Vec[u8, 16], thi: Vec[u8, 16]): ?int
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
    requires N == 16 || N == 32 || N == 64 {
    n := s.Len()
    if n < N {
        lo := tlo.ToArray()
        hi := thi.ToArray()
        for k in 0..n {
            x := s[k]
            row := if x >= 0x80 { hi[x & 15] } else { lo[x & 15] }
            if (row >> ((x >> 4) & 7)) & 1 != 0 {
                return some(k)
            }
        }
        return none
    }
    var i := 0
    for i + N <= n {
        m := SetMask[N](Vec[u8, N].FromSlice(s, i), tlo, thi).Bits()
        if m != 0 {
            return some(i + raw.Ctz64(m))
        }
        i += N
    }
    if i < n {
        m := SetMask[N](Vec[u8, N].FromSlice(s, n - N), tlo, thi).Bits() >> (N - (n - i))
        if m != 0 {
            return some(i + raw.Ctz64(m))
        }
    }
    return none
}

/// Returns the number of bytes of s equal to b. O(n); it allocates nothing.
///
/// Matches are counted in byte-wide vector counters, which are added into
/// the total every 255 steps; the tail is one overlapping compare. avx2 is
/// used from 64 bytes when the CPU has it.
export Count(s: []u8, b: u8): int {
    if Target.os == Os.Freestanding {
        return CountWords(s, b)
    }
    if s.Len() >= DispatchMin && Cpu().Has("avx2") {
        unsafe "Cpu() reports avx2, which CountAvx2's clause names" {
            return CountAvx2(s, b)
        }
    }
    return CountN[16](s, b)
}

CountAvx2(s: []u8, b: u8): int cpu "avx2" {
    return CountAligned[32](s, b)
}

// CountAligned needs s.Len() >= N, and counts the head vector's lanes below
// the boundary only. Generic over N as the other aligned bodies are; only
// the 32-lane instance exists, since the 64-lane form measured slower.
CountAligned[const N: int](s: []u8, b: u8): int
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 {
    h := AlignHead(s, N)
    head := if h != 0 { raw.Popcount64((Vec[u8, N].FromSlice(s, 0) == Vec[u8, N].Splat(b)).Bits() & LowBits(h)) } else { 0 }
    return head + CountN[N](s[h..], b)
}

CountN[const N: int](s: []u8, b: u8): int
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 {
    n := s.Len()
    var total := 0
    if n < N {
        for k in 0..n {
            if s[k] == b {
                total += 1
            }
        }
        return total
    }
    nd := Vec[u8, N].Splat(b)
    ones := Vec[u8, N].Splat(255)
    zero := Vec[u8, N].Zero()
    var i := 0
    for i + 4 * N <= n {
        // At most 255 steps, so no byte counter wraps; one slice for the
        // run, so each load is checked against it alone.
        steps := if (n - i) / (4 * N) > 255 { 255 } else { (n - i) / (4 * N) }
        run := s[i..i + steps * (4 * N)]
        var c0 := zero
        var c1 := zero
        var c2 := zero
        var c3 := zero
        for j in 0..steps {
            w := run[j * (4 * N)..j * (4 * N) + 4 * N]
            c0 = c0 -% (Vec[u8, N].FromSlice(w, 0) == nd).Select(ones, zero)
            c1 = c1 -% (Vec[u8, N].FromSlice(w, N) == nd).Select(ones, zero)
            c2 = c2 -% (Vec[u8, N].FromSlice(w, 2 * N) == nd).Select(ones, zero)
            c3 = c3 -% (Vec[u8, N].FromSlice(w, 3 * N) == nd).Select(ones, zero)
        }
        i += steps * (4 * N)
        total += Cast[int](c0.ReduceSumWide() + c1.ReduceSumWide() + c2.ReduceSumWide() + c3.ReduceSumWide())
    }
    for i + N <= n {
        total += (Vec[u8, N].FromSlice(s, i) == nd).CountSet()
        i += N
    }
    if i < n {
        m := (Vec[u8, N].FromSlice(s, n - N) == nd).Bits() >> (N - (n - i))
        total += raw.Popcount64(m)
    }
    return total
}

/// Reports whether a and b hold the same bytes. O(n); it allocates nothing.
///
/// Unequal lengths answer at once. Otherwise each step compares four
/// vectors and tests them once, and the tail is one overlapping load;
/// below one vector's width, two overlapping loads of 8, 4 or 2 bytes.
/// avx2 is used from 64 bytes and avx512bw from 256 when the CPU has them.
export Equal(a: []u8, b: []u8): bool {
    n := a.Len()
    if n != b.Len() {
        return false
    }
    if Target.os == Os.Freestanding {
        return EqualWords(a, b)
    }
    if n >= WideMin && Cpu().Has("avx512bw") {
        unsafe "Cpu() reports avx512bw, which EqualAvx512's clause names" {
            return EqualAvx512(a, b)
        }
    }
    if n >= DispatchMin && Cpu().Has("avx2") {
        unsafe "Cpu() reports avx2, which EqualAvx2's clause names" {
            return EqualAvx2(a, b)
        }
    }
    return EqualN[16](a, b)
}

EqualAvx2(a: []u8, b: []u8): bool cpu "avx2" {
    return EqualAligned[32](a, b)
}

EqualAvx512(a: []u8, b: []u8): bool cpu "avx512bw" {
    return EqualAligned[64](a, b)
}

// EqualAligned needs a.Len() == b.Len() >= N.
EqualAligned[const N: int](a: []u8, b: []u8): bool
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 {
    h := AlignHead(a, N)
    if h != 0 && (Vec[u8, N].FromSlice(a, 0) ^ Vec[u8, N].FromSlice(b, 0)).ReduceOr() != 0 {
        return false
    }
    return EqualN[N](a[h..], b[h..])
}

// EqualN needs a.Len() == b.Len().
EqualN[const N: int](a: []u8, b: []u8): bool
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 {
    n := a.Len()
    if n < N {
        return EqualShort(a, b)
    }
    var i := 0
    for i + 4 * N <= n {
        wa := a[i..i + 4 * N]
        wb := b[i..i + 4 * N]
        x0 := Vec[u8, N].FromSlice(wa, 0) ^ Vec[u8, N].FromSlice(wb, 0)
        x1 := Vec[u8, N].FromSlice(wa, N) ^ Vec[u8, N].FromSlice(wb, N)
        x2 := Vec[u8, N].FromSlice(wa, 2 * N) ^ Vec[u8, N].FromSlice(wb, 2 * N)
        x3 := Vec[u8, N].FromSlice(wa, 3 * N) ^ Vec[u8, N].FromSlice(wb, 3 * N)
        if (x0 | x1 | x2 | x3).ReduceOr() != 0 {
            return false
        }
        i += 4 * N
    }
    for i + N <= n {
        if (Vec[u8, N].FromSlice(a, i) ^ Vec[u8, N].FromSlice(b, i)).ReduceOr() != 0 {
            return false
        }
        i += N
    }
    if i < n {
        return (Vec[u8, N].FromSlice(a, n - N) ^ Vec[u8, N].FromSlice(b, n - N)).ReduceOr() == 0
    }
    return true
}

// EqualShort compares two slices of one length below 16 with two
// overlapping loads of the widest lane count that fits.
EqualShort(a: []u8, b: []u8): bool {
    n := a.Len()
    if n >= 8 {
        return (Vec[u8, 8].FromSlice(a, 0) == Vec[u8, 8].FromSlice(b, 0)).All() &&
            (Vec[u8, 8].FromSlice(a, n - 8) == Vec[u8, 8].FromSlice(b, n - 8)).All()
    }
    if n >= 4 {
        return (Vec[u8, 4].FromSlice(a, 0) == Vec[u8, 4].FromSlice(b, 0)).All() &&
            (Vec[u8, 4].FromSlice(a, n - 4) == Vec[u8, 4].FromSlice(b, n - 4)).All()
    }
    if n >= 2 {
        return (Vec[u8, 2].FromSlice(a, 0) == Vec[u8, 2].FromSlice(b, 0)).All() &&
            (Vec[u8, 2].FromSlice(a, n - 2) == Vec[u8, 2].FromSlice(b, n - 2)).All()
    }
    return n == 0 || a[0] == b[0]
}

/// Orders a and b by their bytes as unsigned numbers: -1, 0 or 1 by the
/// first byte that differs, and a proper prefix before the longer slice.
/// O(n) in the shorter length; it allocates nothing. avx2 is used when
/// both slices hold at least 64 bytes and the CPU has it.
export Compare(a: []u8, b: []u8): int {
    if Target.os == Os.Freestanding {
        return CompareWords(a, b)
    }
    m := a.Len()
    if m >= DispatchMin && b.Len() >= DispatchMin && Cpu().Has("avx2") {
        unsafe "Cpu() reports avx2, which CompareAvx2's clause names" {
            return CompareAvx2(a, b)
        }
    }
    return CompareN[16](a, b)
}

CompareAvx2(a: []u8, b: []u8): int cpu "avx2" {
    return CompareAligned[32](a, b)
}

// CompareAligned needs both lengths >= N, so cutting h bytes from both
// keeps their order.
CompareAligned[const N: int](a: []u8, b: []u8): int
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 {
    h := AlignHead(a, N)
    if h != 0 {
        d := (Vec[u8, N].FromSlice(a, 0) != Vec[u8, N].FromSlice(b, 0)).Bits()
        if d != 0 {
            return ByteOrder(a, b, raw.Ctz64(d))
        }
    }
    return CompareN[N](a[h..], b[h..])
}

CompareN[const N: int](a: []u8, b: []u8): int
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 {
    m := if b.Len() < a.Len() { b.Len() } else { a.Len() }
    if m < N {
        return CompareShort(a, b, m)
    }
    var i := 0
    for i + 4 * N <= m {
        wa := a[i..i + 4 * N]
        wb := b[i..i + 4 * N]
        x0 := Vec[u8, N].FromSlice(wa, 0) ^ Vec[u8, N].FromSlice(wb, 0)
        x1 := Vec[u8, N].FromSlice(wa, N) ^ Vec[u8, N].FromSlice(wb, N)
        x2 := Vec[u8, N].FromSlice(wa, 2 * N) ^ Vec[u8, N].FromSlice(wb, 2 * N)
        x3 := Vec[u8, N].FromSlice(wa, 3 * N) ^ Vec[u8, N].FromSlice(wb, 3 * N)
        if (x0 | x1 | x2 | x3).ReduceOr() != 0 {
            break
        }
        i += 4 * N
    }
    for i + N <= m {
        d := (Vec[u8, N].FromSlice(a, i) != Vec[u8, N].FromSlice(b, i)).Bits()
        if d != 0 {
            return ByteOrder(a, b, i + raw.Ctz64(d))
        }
        i += N
    }
    if i < m {
        d := (Vec[u8, N].FromSlice(a, m - N) != Vec[u8, N].FromSlice(b, m - N)).Bits() >> (N - (m - i))
        if d != 0 {
            return ByteOrder(a, b, i + raw.Ctz64(d))
        }
    }
    return LenOrder(a.Len(), b.Len())
}

// CompareShort is Compare when the common length m is below 16: two
// overlapping not-equal masks of 8, 4 or 2 lanes, the low one first.
CompareShort(a: []u8, b: []u8, m: int): int {
    if m >= 8 {
        d0 := (Vec[u8, 8].FromSlice(a, 0) != Vec[u8, 8].FromSlice(b, 0)).Bits()
        if d0 != 0 {
            return ByteOrder(a, b, raw.Ctz64(d0))
        }
        d1 := (Vec[u8, 8].FromSlice(a, m - 8) != Vec[u8, 8].FromSlice(b, m - 8)).Bits()
        if d1 != 0 {
            return ByteOrder(a, b, m - 8 + raw.Ctz64(d1))
        }
    } else if m >= 4 {
        d0 := (Vec[u8, 4].FromSlice(a, 0) != Vec[u8, 4].FromSlice(b, 0)).Bits()
        if d0 != 0 {
            return ByteOrder(a, b, raw.Ctz64(d0))
        }
        d1 := (Vec[u8, 4].FromSlice(a, m - 4) != Vec[u8, 4].FromSlice(b, m - 4)).Bits()
        if d1 != 0 {
            return ByteOrder(a, b, m - 4 + raw.Ctz64(d1))
        }
    } else {
        for j in 0..m {
            if a[j] != b[j] {
                return ByteOrder(a, b, j)
            }
        }
    }
    return LenOrder(a.Len(), b.Len())
}

// ByteOrder is -1 or 1 by the bytes at j, which differ.
ByteOrder(a: []u8, b: []u8, j: int): int {
    return if a[j] < b[j] { -1 } else { 1 }
}

// LenOrder is -1, 0 or 1 by the lengths, once the common prefix is equal.
LenOrder(m: int, n: int): int {
    return if m < n { -1 } else if m > n { 1 } else { 0 }
}
