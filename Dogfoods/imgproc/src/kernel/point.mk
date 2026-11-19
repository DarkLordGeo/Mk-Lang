// Why: invert, brighten, threshold and table lookups touch one sample each.
// What: the Scalar and N bodies of each, TailMask and Store.
// How: a vector body handles N samples a step; the last step of a span not a
// multiple of N loads with FromSliceOr and stores under a mask.

import std.math
import std.simd

/// The mask of the first `left` lanes, for the last step of a span. O(1).
TailMask[T: simd.Lane, const N: int](left: int): simd.Mask[T, N]
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
{
    if left >= N {
        return simd.Mask[T, N].FromBits(0xFFFFFFFFFFFFFFFF)
    }
    return simd.Mask[T, N].FromBits((u64(1) << left) - 1)
}

/// Stores `v` at `dst[i..]`, all N lanes or the first `dst.Len() - i`. O(1).
Store[T: simd.Lane, const N: int](v: simd.Vec[T, N], dst: mut []T, i: int)
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
{
    left := dst.Len() - i
    if left >= N {
        v.CopyTo(dst, i)
    } else {
        v.CopyToMasked(dst, i, TailMask[T, N](left))
    }
}

/// `dst[i]` is `hi` where `src[i] >= t`, else 0. O(n).
export ThresholdScalar(src: []u8, dst: mut []u8, t: u8, hi: u8) {
    for i, v in src {
        dst[i] = if v >= t { hi } else { 0 }
    }
}

/// The threshold through two 16-entry tables indexed by a sample's high
/// nibble: `decided` gives the answer for the nibbles whose 16 samples all
/// fall on one side of `t`, and `boundary` marks the one nibble that holds
/// `t`, where the sample is compared whole. O(n).
ThresholdN[const N: int](src: []u8, dst: mut []u8, t: u8, hi: u8)
    requires N == 16 || N == 32 || N == 64
{
    var decided: [16]u8 = [0; 16]
    var boundary: [16]u8 = [0; 16]
    for n in 0..16 {
        low := n * 16
        if low >= int(t) {
            decided[n] = hi
        } else if low + 15 >= int(t) {
            boundary[n] = 0xFF
        }
    }
    dtab := simd.Vec[u8, 16].FromArray(decided)
    btab := simd.Vec[u8, 16].FromArray(boundary)
    zero := simd.Vec[u8, N].Zero()
    tv := simd.Vec[u8, N].Splat(t)
    hv := simd.Vec[u8, N].Splat(hi)
    marked := simd.Vec[u8, N].Splat(0xFF)
    var i := 0
    for i < src.Len() {
        v := simd.Vec[u8, N].FromSliceOr(src, i, zero)
        nib := v >> 4
        exact := (v >= tv).Select(hv, zero)
        on_edge := simd.Lookup16(nib, btab) == marked
        Store[u8, N](on_edge.Select(exact, simd.Lookup16(nib, dtab)), dst, i)
        i += N
    }
}

/// `dst[i]` is `maxval - src[i]`. O(n).
export InvertScalar(src: []u8, dst: mut []u8, maxval: u8) {
    for i, v in src {
        dst[i] = maxval - v
    }
}

InvertN[const N: int](src: []u8, dst: mut []u8, maxval: u8)
    requires N == 16 || N == 32 || N == 64
{
    zero := simd.Vec[u8, N].Zero()
    top := simd.Vec[u8, N].Splat(maxval)
    var i := 0
    for i < src.Len() {
        Store[u8, N](top -% simd.Vec[u8, N].FromSliceOr(src, i, zero), dst, i)
        i += N
    }
}

/// `dst[i]` is `src[i] + delta` clamped to 0..maxval. O(n).
export BrightenScalar(src: []u8, dst: mut []u8, delta: i32, maxval: u8) {
    for i, v in src {
        dst[i] = Trunc[u8](math.Min(math.Max(i32(v) + delta, 0), i32(maxval)))
    }
}

BrightenN[const N: int](src: []u8, dst: mut []u8, delta: i32, maxval: u8)
    requires N == 16 || N == 32 || N == 64
{
    zero := simd.Vec[u8, N].Zero()
    top := simd.Vec[u8, N].Splat(maxval)
    step := simd.Vec[u8, N].Splat(Trunc[u8](math.Min(if delta < 0 { -delta } else { delta }, 255)))
    var i := 0
    for i < src.Len() {
        v := simd.Vec[u8, N].FromSliceOr(src, i, zero)
        moved := if delta < 0 { v.SubSat(step) } else { v.AddSat(step).Min(top) }
        Store[u8, N](moved, dst, i)
        i += N
    }
}

/// `dst[i]` is `lut[src[i]]`. O(n).
export LutScalar(src: []u8, dst: mut []u8, lut: &[256]u8) {
    for i, v in src {
        dst[i] = lut[v]
    }
}

/// The table lookup as sixteen 16-entry lookups by the low nibble, each kept
/// where the high nibble selects its sixteenth of the table. O(16 n / N).
LutN[const N: int](src: []u8, dst: mut []u8, lut: &[256]u8)
    requires N == 16 || N == 32 || N == 64
{
    zero := simd.Vec[u8, N].Zero()
    low := simd.Vec[u8, N].Splat(15)
    var i := 0
    for i < src.Len() {
        v := simd.Vec[u8, N].FromSliceOr(src, i, zero)
        lo := v & low
        hi := v >> 4
        var out := zero
        for h in 0..16 {
            part := simd.Vec[u8, 16].FromSlice(lut[..], h * 16)
            out = (hi == simd.Vec[u8, N].Splat(Trunc[u8](h))).Select(simd.Lookup16(lo, part), out)
        }
        Store[u8, N](out, dst, i)
        i += N
    }
}
