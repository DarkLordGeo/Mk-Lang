// Why: blur, sharpen, edge and custom kernels are all convolutions.
// What: ConvolveScalar, ConvolveN and ConvolveFusedN.
// How: interior columns go through the vector body, the edges through
// ConvolveAt; sums are of integers exact in f32.

import std.math
import std.simd

/// Convolves the rows of `f` from `y0` into `dst`, which holds whole rows,
/// one sample at a time through `ConvolveAt`. `scratch` holds at least
/// `k.size` values. O(rows * row * size^2).
export ConvolveScalar(f: &Frame, k: &Kernel, y0: int, dst: mut []u8, scratch: mut []f32) {
    row := f.Row()
    for dy in 0..dst.Len() / row {
        for i in 0..row {
            dst[dy * row + i] = ConvolveAt(f, k, y0 + dy, i, scratch)
        }
    }
}

/// The interior of a row, `[lo, hi)`: the bytes whose taps all lie inside it.
struct Span {
    lo: int
    hi: int
}

InteriorOf(f: &Frame, k: &Kernel): Span {
    row := f.Row()
    lo := math.Min(k.size / 2 * f.channels, row)
    return Span(lo: lo, hi: math.Max((f.width - k.size / 2) * f.channels, lo))
}

/// Writes the edge bytes of output row `dy`, outside `s`, by the scalar rule.
Edges(f: &Frame, k: &Kernel, y0: int, dy: int, s: Span, dst: mut []u8, scratch: mut []f32) {
    row := f.Row()
    for i in 0..s.lo {
        dst[dy * row + i] = ConvolveAt(f, k, y0 + dy, i, scratch)
    }
    for i in s.hi..row {
        dst[dy * row + i] = ConvolveAt(f, k, y0 + dy, i, scratch)
    }
}

/// Clamps, rounds and stores N convolved samples at `dst[at..]`, the first
/// `left` of them when fewer than N remain in the interior.
Finish[const N: int](acc: simd.Vec[f32, N], k: &Kernel, maxval: int, dst: mut []u8, at: int, left: int)
    requires N == 4 || N == 8 || N == 16
{
    zero := simd.Vec[f32, N].Zero()
    q := acc / simd.Vec[f32, N].Splat(k.divisor) + simd.Vec[f32, N].Splat(k.bias)
    top := simd.Vec[f32, N].Splat(Cast[f32](maxval))
    bytes := q.Max(zero).Min(top).RoundEven().Cast[i32]().Trunc[u8]()
    if left >= N {
        bytes.CopyTo(dst, at)
    } else {
        bytes.CopyToMasked(dst, at, simd.Mask[u8, N].FromBits((u64(1) << left) - 1))
    }
}

/// The convolution with N samples a step across the interior of each row;
/// the edge columns go through `ConvolveAt`. Each lane adds its products in
/// the order `ConvolveAt` does. O(rows * row * size^2 / N).
ConvolveN[const N: int](f: &Frame, k: &Kernel, y0: int, dst: mut []u8, scratch: mut []f32)
    requires N == 4 || N == 8 || N == 16
{
    row := f.Row()
    s := InteriorOf(f, k)
    r := k.size / 2
    zero8 := simd.Vec[u8, N].Zero()
    for dy in 0..dst.Len() / row {
        Edges(f, k, y0, dy, s, dst, scratch)
        var i := s.lo
        for i < s.hi {
            var acc := simd.Vec[f32, N].Zero()
            for ky in 0..k.size {
                base := f.ClampY(y0 + dy + ky - r) * row + i - s.lo
                for kx in 0..k.size {
                    p := simd.Vec[u8, N].FromSliceOr(f.samples, base + kx * f.channels, zero8).Cast[f32]()
                    acc = acc + p * simd.Vec[f32, N].Splat(k.weights[ky * k.size + kx])
                }
            }
            Finish[N](acc, k, f.maxval, dst, dy * row + i, s.hi - i)
            i += N
        }
    }
}

/// `ConvolveN` with each product added by `Fma`, which rounds once: every
/// product and sum here is an integer below 2^24, so the bytes are the same.
ConvolveFusedN[const N: int](f: &Frame, k: &Kernel, y0: int, dst: mut []u8, scratch: mut []f32)
    requires N == 4 || N == 8 || N == 16
    cpu "x86-64-v3"
{
    row := f.Row()
    s := InteriorOf(f, k)
    r := k.size / 2
    zero8 := simd.Vec[u8, N].Zero()
    for dy in 0..dst.Len() / row {
        Edges(f, k, y0, dy, s, dst, scratch)
        var i := s.lo
        for i < s.hi {
            var acc := simd.Vec[f32, N].Zero()
            for ky in 0..k.size {
                base := f.ClampY(y0 + dy + ky - r) * row + i - s.lo
                for kx in 0..k.size {
                    p := simd.Vec[u8, N].FromSliceOr(f.samples, base + kx * f.channels, zero8).Cast[f32]()
                    acc = p.Fma(simd.Vec[f32, N].Splat(k.weights[ky * k.size + kx]), acc)
                }
            }
            Finish[N](acc, k, f.maxval, dst, dy * row + i, s.hi - i)
            i += N
        }
    }
}
