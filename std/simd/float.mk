// Why: a dot product or a sum over floats is only reproducible if its order of
// additions is fixed, yet a vector kernel must reassociate to be fast.
//
// What: simd.Dot and simd.Sum, with their generic bodies DotOf and SumOf and
// their avx2 forms.
//
// How: the reassociation is stated and the same on every CPU: 16 partial
// sums, the jth taking elements j, j + 16, j + 32 and so on up to the last
// multiple of 16, each starting at +0.0 and each product rounded before its
// sum (no Mk float operation is fused), the 16 added as ReduceSum's pairwise
// tree, then the tail added in order. The 16 sums are one Vec[T, 16], which
// LLVM keeps in four 128-bit registers at the baseline and two 256-bit ones
// under avx2 (eight and four for f64): the same arithmetic in the same
// order, so a dispatched run and a baseline run give the same bits. This is
// also the order of tests/bench/simd/dot. O(n), no allocation. Integer lanes
// are refused at the call (E0701).
//
// Dot's avx2 form starts its loads of a on a 32-byte boundary without changing
// the order (Sum's does not, see SumOf). With h the elements before the
// boundary, lane k of the accumulator gathers the elements congruent to h + k
// mod 16, so it holds partial sum (h + k) mod 16, and every partial sum still
// takes its own elements in index order, the first added to +0.0. The h leading
// elements seed their sums' lanes, and the elements past the aligned loop up to
// the last multiple of 16 are added to lanes 0 to 15 - h. The lanes need no
// putting back: ReduceSum's tree adds lane j to lane j + 8, then j + 4, j + 2
// and j + 1 (mod each width), and a rotation of the lanes keeps every one of
// those pairs, so with commuting adds the tree gives the same bits (so a
// differential test cannot tell a rotation by one lane more or less from this
// one: only this argument can). On freestanding no avx2 form is taken: a cpu
// function there would have to save the FPU state itself, and a bin built
// general-registers-only does not.

import std.raw

/// Returns the dot product of a and b, the sum of a[i] * b[i], in a fixed
/// order of additions that is the same on every CPU, so every run gives
/// the same bits. It panics with code 2 when the lengths differ. T is f32
/// or f64 (E0701 for integer lanes). O(n); it allocates nothing.
///
/// The order: 16 partial sums, the jth taking the elements j, j + 16, j +
/// 32 and so on up to the last multiple of 16, each starting at +0.0 and
/// each product rounded before it is added. The 16 sums are added as a
/// pairwise tree, as ReduceSum does, and then the tail in index order.
export Dot[T: Lane](a: []T, b: []T): T {
    if a.Len() != b.Len() {
        raw.Fail(2, "simd.Dot: the slices' lengths differ")
    }
    if T is f32 {
        if Target.os != Os.Freestanding && a.Len() >= DispatchMin && Cpu().Has("avx2") {
            unsafe "Cpu() reports avx2, which DotAvx2F32's clause names" {
                return DotAvx2F32(a, b)
            }
        }
        return DotOf[f32](a, b, 0)
    }
    if T is f64 {
        if Target.os != Os.Freestanding && a.Len() >= DispatchMin && Cpu().Has("avx2") {
            unsafe "Cpu() reports avx2, which DotAvx2F64's clause names" {
                return DotAvx2F64(a, b)
            }
        }
        return DotOf[f64](a, b, 0)
    }
    raw.Fail(2, "simd.Dot: lanes are not f32 or f64")
}

/// Returns the sum of xs, in the same fixed order of additions as Dot, so
/// every CPU gives the same bits. A sum of -0.0 elements alone is +0.0. T
/// is f32 or f64 (E0701 for integer lanes). O(n); it allocates nothing.
export Sum[T: Lane](xs: []T): T {
    if T is f32 {
        if Target.os != Os.Freestanding && xs.Len() >= DispatchMin && Cpu().Has("avx2") {
            unsafe "Cpu() reports avx2, which SumAvx2F32's clause names" {
                return SumAvx2F32(xs)
            }
        }
        return SumOf[f32](xs)
    }
    if T is f64 {
        if Target.os != Os.Freestanding && xs.Len() >= DispatchMin && Cpu().Has("avx2") {
            unsafe "Cpu() reports avx2, which SumAvx2F64's clause names" {
                return SumAvx2F64(xs)
            }
        }
        return SumOf[f64](xs)
    }
    raw.Fail(2, "simd.Sum: lanes are not f32 or f64")
}

// The avx2 forms. The length is at least DispatchMin, so Dot's h <= 7
// leading elements lie inside the first 16.
DotAvx2F32(a: []f32, b: []f32): f32 cpu "avx2" {
    return DotOf[f32](a, b, HeadElems[f32](a))
}

DotAvx2F64(a: []f64, b: []f64): f64 cpu "avx2" {
    return DotOf[f64](a, b, HeadElems[f64](a))
}

SumAvx2F32(xs: []f32): f32 cpu "avx2" {
    return SumOf[f32](xs)
}

SumAvx2F64(xs: []f64): f64 cpu "avx2" {
    return SumOf[f64](xs)
}

// HeadElems is the count of elements from the start of s to the next
// 32-byte boundary, 0 to 7, or 0 when s's address is not a multiple of the
// element size. Run time only: raw.Addr is refused at compile time, where
// the avx2 forms never run (Cpu() is the baseline there).
HeadElems[T](s: []T): int {
    z := SizeOf[T]()
    hb := Cast[int]((32 - (raw.Addr(raw.Data(s)) & 31)) & 31)
    if hb % z != 0 {
        return 0
    }
    return hb / z
}

// Rot16 is v with lane j moved to lane j - h mod 16 (Rotate by h), for h
// from 0 to 7, the head counts HeadElems gives.
Rot16[T: Lane](v: Vec[T, 16], h: int): Vec[T, 16] {
    return match h {
        1 { v.Rotate(1) }
        2 { v.Rotate(2) }
        3 { v.Rotate(3) }
        4 { v.Rotate(4) }
        5 { v.Rotate(5) }
        6 { v.Rotate(6) }
        7 { v.Rotate(7) }
        _ { v }
    }
}

// DotOf is Dot's body, generic so that a call from a cpu function makes an
// instance compiled with its features, with the accumulator
// rotated by h, 0 to 7 (0 is none); a.Len() == b.Len(), and 16 <= a.Len() when
// h > 0. The partial sums take the elements below q. Rotated, the first vector
// holds the h leading products in its top lanes (lane 16 - h + j holds product
// j) and +0.0 elsewhere, each added to +0.0 as the stated order starts (a -0.0
// product becomes +0.0); the loop then loads from h; and the products from i up
// to q, the last of partial sums h to 15, are added in lanes 0 to q - i - 1 by
// one masked load of a[0..q], whose fill lanes add +0.0 to sums that are never
// -0.0, so change nothing.
DotOf[T: Lane](a: []T, b: []T, h: int): T {
    if T is f32 {
        n := a.Len()
        q := n - n % 16
        var i := 0
        var r: f32 = 0.0
        if h == 0 || q == 0 {
            var acc := Vec[f32, 16].Zero()
            for i + 16 <= q {
                acc = acc + Vec[f32, 16].FromSlice(a, i) * Vec[f32, 16].FromSlice(b, i)
                i += 16
            }
            r = acc.ReduceSum()
        } else {
            z := Vec[f32, 16].Zero()
            top := Mask[f32, 16].FromBits(((Cast[u64](1) << h) - 1) << (16 - h))
            var acc := z + top.Select(Rot16[f32](Vec[f32, 16].FromSlice(a, 0) * Vec[f32, 16].FromSlice(b, 0), h), z)
            i = h
            for i + 16 <= q {
                acc = acc + Vec[f32, 16].FromSlice(a, i) * Vec[f32, 16].FromSlice(b, i)
                i += 16
            }
            acc = acc + Vec[f32, 16].FromSliceOr(a[0..q], i, z) * Vec[f32, 16].FromSliceOr(b[0..q], i, z)
            r = acc.ReduceSum()
            i = q
        }
        for i < n {
            r += a[i] * b[i]
            i += 1
        }
        return r
    }
    if T is f64 {
        n := a.Len()
        q := n - n % 16
        var i := 0
        var r: f64 = 0.0
        if h == 0 || q == 0 {
            var acc := Vec[f64, 16].Zero()
            for i + 16 <= q {
                acc = acc + Vec[f64, 16].FromSlice(a, i) * Vec[f64, 16].FromSlice(b, i)
                i += 16
            }
            r = acc.ReduceSum()
        } else {
            z := Vec[f64, 16].Zero()
            top := Mask[f64, 16].FromBits(((Cast[u64](1) << h) - 1) << (16 - h))
            var acc := z + top.Select(Rot16[f64](Vec[f64, 16].FromSlice(a, 0) * Vec[f64, 16].FromSlice(b, 0), h), z)
            i = h
            for i + 16 <= q {
                acc = acc + Vec[f64, 16].FromSlice(a, i) * Vec[f64, 16].FromSlice(b, i)
                i += 16
            }
            acc = acc + Vec[f64, 16].FromSliceOr(a[0..q], i, z) * Vec[f64, 16].FromSliceOr(b[0..q], i, z)
            r = acc.ReduceSum()
            i = q
        }
        for i < n {
            r += a[i] * b[i]
            i += 1
        }
        return r
    }
    raw.Fail(2, "simd.Dot: lanes are not f32 or f64")
}

// SumOf is Sum's body, generic as DotOf is. It does not rotate: Sum is
// bound by the latency of its two dependent accumulator chains at L1, and
// measured, the rotated head and tail made it slower than plain loads.
SumOf[T: Lane](xs: []T): T {
    n := xs.Len()
    var i := 0
    if T is f32 {
        var acc := Vec[f32, 16].Zero()
        for i + 16 <= n {
            acc = acc + Vec[f32, 16].FromSlice(xs, i)
            i += 16
        }
        var r := acc.ReduceSum()
        for i < n {
            r += xs[i]
            i += 1
        }
        return r
    }
    if T is f64 {
        var acc := Vec[f64, 16].Zero()
        for i + 16 <= n {
            acc = acc + Vec[f64, 16].FromSlice(xs, i)
            i += 16
        }
        var r := acc.ReduceSum()
        for i < n {
            r += xs[i]
            i += 1
        }
        return r
    }
    raw.Fail(2, "simd.Sum: lanes are not f32 or f64")
}
