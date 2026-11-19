// Why: mirror, gray, half, double and gain move or combine samples.
// What: the Scalar and N bodies of each.
// How: each takes whole rows starting at a pixel boundary, so a lane's
// channel is its sample index modulo the channel count. The widening steps
// need the half lane count M beside N, fixed by `requires M == N / 2`, so
// each widening line's clause is judged over the listed pairs.

import std.math
import std.simd

/// One row reversed pixel by pixel. O(w).
export MirrorScalar(src: []u8, dst: mut []u8, channels: int) {
    w := src.Len() / channels
    for x in 0..w {
        for c in 0..channels {
            dst[x * channels + c] = src[(w - 1 - x) * channels + c]
        }
    }
}

/// A gray row reversed N samples at a time; a colour row gathered, each lane
/// reading the sample its pixel mirrors to. O(w / N).
MirrorN[const N: int](src: []u8, dst: mut []u8, channels: int)
    requires N == 16 || N == 32 || N == 64
{
    n := src.Len()
    var i := 0
    if channels == 1 {
        for i + N <= n {
            simd.Vec[u8, N].FromSlice(src, n - i - N).Reverse().CopyTo(dst, i)
            i += N
        }
    } else {
        lane := simd.Vec[i32, N].Iota()
        three := simd.Vec[i32, N].Splat(3)
        last := simd.Vec[i32, N].Splat(Trunc[i32](n / 3 - 1))
        for i + N <= n {
            at := simd.Vec[i32, N].Splat(Trunc[i32](i)) +% lane
            px := at.DivTrunc(three)
            from := (last -% px) *% three +% (at -% px *% three)
            simd.Vec[u8, N].Gather(src, from).CopyTo(dst, i)
            i += N
        }
    }
    for j in i..n {
        dst[j] = src[(n / channels - 1 - j / channels) * channels + j % channels]
    }
}

/// The luma of one colour row, ITU-R BT.601 weights in eighths of 256:
/// (77 R + 150 G + 29 B + 128) / 256, which stays below 2^16. O(w).
export GrayScalar(src: []u8, dst: mut []u8) {
    for x in 0..dst.Len() {
        sum := 77 * int(src[3 * x]) + 150 * int(src[3 * x + 1]) + 29 * int(src[3 * x + 2]) + 128
        dst[x] = Trunc[u8](sum >> 8)
    }
}

/// N pixels a step: each channel gathered into N lanes, multiplied into u16
/// halves by MulWidenLo and MulWidenHi, added, and narrowed back. O(w / N).
GrayN[const N: int, const M: int](src: []u8, dst: mut []u8)
    requires N == 16 || N == 32 || N == 64
    requires M == N / 2
{
    lane := simd.Vec[i32, N].Iota() *% simd.Vec[i32, N].Splat(3)
    half := simd.Vec[u16, M].Splat(128)
    var x := 0
    for x + N <= dst.Len() {
        at := simd.Vec[i32, N].Splat(Trunc[i32](3 * x)) +% lane
        r := simd.Vec[u8, N].Gather(src, at)
        g := simd.Vec[u8, N].Gather(src, at +% simd.Vec[i32, N].Splat(1))
        b := simd.Vec[u8, N].Gather(src, at +% simd.Vec[i32, N].Splat(2))
        kr := simd.Vec[u8, N].Splat(77)
        kg := simd.Vec[u8, N].Splat(150)
        kb := simd.Vec[u8, N].Splat(29)
        lo := r.MulWidenLo[u16, M](kr) +% g.MulWidenLo[u16, M](kg) +% b.MulWidenLo[u16, M](kb) +% half
        hi := r.MulWidenHi[u16, M](kr) +% g.MulWidenHi[u16, M](kg) +% b.MulWidenHi[u16, M](kb) +% half
        (lo >> 8).NarrowSat[u8, N](hi >> 8).CopyTo(dst, x)
        x += N
    }
    GrayScalar(src[3 * x..], mut dst[x..])
}

/// One output row of the 2x2 box average: pixel x of `top` and `bottom`
/// averaged with pixel x + 1, the last pixel standing in for a missing one,
/// rounded half up. O(w).
export HalfScalar(top: []u8, bottom: []u8, dst: mut []u8, channels: int) {
    w := top.Len() / channels
    for i in 0..dst.Len() {
        x := i / channels
        c := i % channels
        left := 2 * x * channels + c
        right := math.Min(2 * x + 1, w - 1) * channels + c
        sum := int(top[left]) + int(top[right]) + int(bottom[left]) + int(bottom[right])
        dst[i] = Trunc[u8]((sum + 2) >> 2)
    }
}

/// The 2x2 average of a gray row, N output pixels a step: DeinterleaveEven
/// and DeinterleaveOdd split each row's 2N samples into left and right
/// pixels, WidenLo and WidenHi take them to u16 to add. Colour rows and the
/// last pixels go through HalfScalar. O(w / N).
HalfN[const N: int, const M: int](top: []u8, bottom: []u8, dst: mut []u8, channels: int)
    requires N == 16 || N == 32 || N == 64
    requires M == N / 2
{
    var x := 0
    if channels == 1 {
        two := simd.Vec[u16, M].Splat(2)
        for 2 * (x + N) <= top.Len() {
            t0 := simd.Vec[u8, N].FromSlice(top, 2 * x)
            t1 := simd.Vec[u8, N].FromSlice(top, 2 * x + N)
            b0 := simd.Vec[u8, N].FromSlice(bottom, 2 * x)
            b1 := simd.Vec[u8, N].FromSlice(bottom, 2 * x + N)
            te := t0.DeinterleaveEven(t1)
            to := t0.DeinterleaveOdd(t1)
            be := b0.DeinterleaveEven(b1)
            bo := b0.DeinterleaveOdd(b1)
            lo := te.WidenLo[u16, M]() +% to.WidenLo[u16, M]() +% be.WidenLo[u16, M]() +% bo.WidenLo[u16, M]() +% two
            hi := te.WidenHi[u16, M]() +% to.WidenHi[u16, M]() +% be.WidenHi[u16, M]() +% bo.WidenHi[u16, M]() +% two
            (lo >> 2).NarrowSat[u8, N](hi >> 2).CopyTo(dst, x)
            x += N
        }
    }
    // the rest in whole rows' terms: shift the start to pixel x
    HalfScalar(top[2 * x..], bottom[2 * x..], mut dst[x..], channels)
}

/// One output row of the 2x nearest-neighbour enlargement from source row
/// `src`. O(w).
export DoubleScalar(src: []u8, dst: mut []u8, channels: int) {
    for i in 0..dst.Len() {
        dst[i] = src[i / channels / 2 * channels + i % channels]
    }
}

/// A gray row doubled by InterleaveLo and InterleaveHi of a vector with
/// itself; a colour row gathered. O(w / N).
DoubleN[const N: int](src: []u8, dst: mut []u8, channels: int)
    requires N == 16 || N == 32 || N == 64
{
    var i := 0
    if channels == 1 {
        for 2 * i + 2 * N <= dst.Len() {
            v := simd.Vec[u8, N].FromSlice(src, i)
            v.InterleaveLo(v).CopyTo(dst, 2 * i)
            v.InterleaveHi(v).CopyTo(dst, 2 * i + N)
            i += N
        }
        i *= 2
    } else {
        lane := simd.Vec[i32, N].Iota()
        three := simd.Vec[i32, N].Splat(3)
        six := simd.Vec[i32, N].Splat(6)
        for i + N <= dst.Len() {
            at := simd.Vec[i32, N].Splat(Trunc[i32](i)) +% lane
            px := at.DivTrunc(three)
            from := at.DivTrunc(six) *% three +% (at -% px *% three)
            simd.Vec[u8, N].Gather(src, from).CopyTo(dst, i)
            i += N
        }
    }
    for j in i..dst.Len() {
        dst[j] = src[j / channels / 2 * channels + j % channels]
    }
}

/// `dst[i]` is `src[i] * gain / 256`, clamped to maxval. O(n).
export GainScalar(src: []u8, dst: mut []u8, gain: u16, maxval: u8) {
    for i, v in src {
        dst[i] = Trunc[u8](math.Min(int(v) * int(gain) >> 8, int(maxval)))
    }
}

/// The gain in u16 lanes: each sample shifted up 8 bits, so MulHigh's upper
/// half of the 32-bit product is `v * gain / 256`. O(n / N).
GainN[const N: int, const M: int](src: []u8, dst: mut []u8, gain: u16, maxval: u8)
    requires N == 16 || N == 32 || N == 64
    requires M == N / 2
{
    g := simd.Vec[u16, M].Splat(gain)
    top := simd.Vec[u8, N].Splat(maxval)
    var i := 0
    for i + N <= src.Len() {
        v := simd.Vec[u8, N].FromSlice(src, i)
        lo := (v.WidenLo[u16, M]() << 8).MulHigh(g)
        hi := (v.WidenHi[u16, M]() << 8).MulHigh(g)
        lo.NarrowSat[u8, N](hi).Min(top).CopyTo(dst, i)
        i += N
    }
    GainScalar(src[i..], mut dst[i..], gain, maxval)
}
