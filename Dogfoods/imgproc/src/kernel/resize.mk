// Why: resizing to any size.
// What: ResizeScalar, ResizeN, ResizeSample and Tap.
// How: bilinear with pixel centres aligned, in integer arithmetic as below,
// so every path gives the same bytes.

import std.math
import std.simd

// Bilinear resizing with pixel centres aligned: output position o of a side
// resized from n to m samples reads the source at s = (o + 0.5) * n / m - 0.5,
// between floor(s) and ceil(s), each clamped to the side. The weight of the
// second neighbour is the fraction of s in 256ths, rounded toward zero, so
// the blend is integer arithmetic: two rows of a * (256 - w) + b * w, below
// 2^16 each, blended again by the row weight, below 2^24, and rounded by
// adding 2^15 before the shift by 16. Every path computes s with the same
// f32 operations in the same order, and the rest is exact, so every path
// gives the same bytes.

/// Where one output position reads its source along one side.
export struct Tap {
    /// The first neighbour, clamped to the side
    export lo: int
    /// The second neighbour, clamped to the side
    export hi: int
    /// The second neighbour's weight in 256ths, 0 to 255
    export w: int
}

/// `s` rounded down, for `s` above -2^31. O(1).
FloorF(s: f32): f32 {
    t := Cast[f32](Cast[i32](s))
    return if t > s { t - 1.0 } else { t }
}

/// `s` rounded up, for `s` below 2^31. O(1).
CeilF(s: f32): f32 {
    t := Cast[f32](Cast[i32](s))
    return if t < s { t + 1.0 } else { t }
}

/// The source coordinate of output position `o` at scale `scale`, the
/// source side over the output side. O(1).
Source(o: int, scale: f32): f32 {
    return (Cast[f32](o) + 0.5) * scale - 0.5
}

/// The tap of output position `o` along a side resized from `n` to `m`. O(1).
export TapAt(o: int, n: int, m: int): Tap {
    s := Source(o, Cast[f32](n) / Cast[f32](m))
    fl := FloorF(s)
    return Tap(
        lo: math.Max(Cast[int](fl), 0),
        hi: math.Min(Cast[int](CeilF(s)), n - 1),
        w: Cast[int]((s - fl) * 256.0),
    )
}

/// Sample `i` of an output row of `out_width` pixels, from the source rows
/// `top` and `bottom` blended with row weight `wy`. O(1).
ResizeSample(top: []u8, bottom: []u8, wy: int, channels: int, out_width: int, i: int): u8 {
    x := i / channels
    c := i % channels
    t := TapAt(x, top.Len() / channels, out_width)
    lo := t.lo * channels + c
    hi := t.hi * channels + c
    upper := int(top[lo]) * (256 - t.w) + int(top[hi]) * t.w
    lower := int(bottom[lo]) * (256 - t.w) + int(bottom[hi]) * t.w
    return Trunc[u8]((upper * (256 - wy) + lower * wy + 32768) >> 16)
}

/// Output row `y` of an image `out_height` rows high and `dst.Len()` samples
/// wide, resized bilinearly from `f`. O(w).
export ResizeScalar(f: &Frame, y: int, out_height: int, dst: mut []u8) {
    ty := TapAt(y, f.height, out_height)
    row := f.Row()
    top := f.samples[ty.lo * row..(ty.lo + 1) * row]
    bottom := f.samples[ty.hi * row..(ty.hi + 1) * row]
    out_width := dst.Len() / f.channels
    for i in 0..dst.Len() {
        dst[i] = ResizeSample(top, bottom, ty.w, f.channels, out_width, i)
    }
}

/// The same row, N samples a step in f32 and i32 lanes: the coordinates by
/// Floor, Ceil and RoundToZero, the left edge clamped by a mask from the f32
/// comparison cast to the i32 lanes, the four neighbours gathered. The last
/// samples go through ResizeSample. O(w / N).
ResizeN[const N: int](f: &Frame, y: int, out_height: int, dst: mut []u8)
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
{
    ty := TapAt(y, f.height, out_height)
    row := f.Row()
    top := f.samples[ty.lo * row..(ty.lo + 1) * row]
    bottom := f.samples[ty.hi * row..(ty.hi + 1) * row]
    out_width := dst.Len() / f.channels
    scale := simd.Vec[f32, N].Splat(Cast[f32](f.width) / Cast[f32](out_width))
    centre := simd.Vec[f32, N].Splat(0.5)
    steps := simd.Vec[f32, N].Splat(256.0)
    lane := simd.Vec[i32, N].Iota()
    channels := simd.Vec[i32, N].Splat(Trunc[i32](f.channels))
    last := simd.Vec[i32, N].Splat(Trunc[i32](f.width - 1))
    zero := simd.Vec[i32, N].Zero()
    full := simd.Vec[i32, N].Splat(256)
    wy := simd.Vec[i32, N].Splat(Trunc[i32](ty.w))
    half := simd.Vec[i32, N].Splat(32768)
    var i := 0
    for i + N <= dst.Len() {
        at := simd.Vec[i32, N].Splat(Trunc[i32](i)) +% lane
        x := at.DivTrunc(channels)
        c := at -% x *% channels
        s := (x.Cast[f32]() + centre) * scale - centre
        fl := s.Floor()
        wx := ((s - fl) * steps).RoundToZero().Cast[i32]()
        before := (fl < simd.Vec[f32, N].Zero()).Cast[i32]()
        lo := before.Select(zero, fl.Cast[i32]()) *% channels +% c
        hi := s.Ceil().Cast[i32]().Min(last) *% channels +% c
        rest := full -% wx
        upper := simd.Vec[u8, N].Gather(top, lo).Cast[i32]() *% rest +% simd.Vec[u8, N].Gather(top, hi).Cast[i32]() *% wx
        lower := simd.Vec[u8, N].Gather(bottom, lo).Cast[i32]() *% rest +% simd.Vec[u8, N].Gather(bottom, hi).Cast[i32]() *% wx
        ((upper *% (full -% wy) +% lower *% wy +% half) >> 16).Trunc[u8]().CopyTo(dst, i)
        i += N
    }
    for j in i..dst.Len() {
        dst[j] = ResizeSample(top, bottom, ty.w, f.channels, out_width, j)
    }
}
