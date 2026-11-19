// Why: edge detection by gradient magnitude.
// What: SobelScalar, SobelN, SobelAt and the three combinations.
// How: the Sobel derivative kernels, combined in f32 exactly, as below.

import std.math
import std.simd

// The Sobel gradient magnitude (I. Sobel and G. Feldman, 1968): the 3x3
// derivative kernels across and down, combined as sqrt(gx^2 + gy^2) (mode
// 0), |gx| + |gy| (mode 1) or max(|gx|, |gy|) (mode 2), clamped to maxval.
// |gx| and |gy| are at most 4 * 255, so their squares and sums are integers
// below 2^22 and exact in f32; the f32 square root is correctly rounded, as
// the f64 one rounded to f32 is, so every path gives the same bytes.

/// The Sobel combinations.
export const SobelL2: int = 0
export const SobelL1: int = 1
export const SobelMax: int = 2

/// One output sample of the gradient at row `y`, byte `i`, edges repeated.
/// O(1).
export SobelAt(f: &Frame, y: int, i: int, mode: int): u8 {
    x := i / f.channels
    c := i % f.channels
    row := f.Row()
    up := f.ClampY(y - 1) * row + c
    mid := y * row + c
    down := f.ClampY(y + 1) * row + c
    l := f.ClampX(x - 1) * f.channels
    m := x * f.channels
    r := f.ClampX(x + 1) * f.channels
    p := f.samples
    gx := int(p[up + r]) + 2 * int(p[mid + r]) + int(p[down + r]) - int(p[up + l]) - 2 * int(p[mid + l]) - int(p[down + l])
    gy := int(p[down + l]) + 2 * int(p[down + m]) + int(p[down + r]) - int(p[up + l]) - 2 * int(p[up + m]) - int(p[up + r])
    ax := math.Max(gx, -gx)
    ay := math.Max(gy, -gy)
    if mode == SobelL1 {
        return Trunc[u8](math.Min(ax + ay, f.maxval))
    }
    if mode == SobelMax {
        return Trunc[u8](math.Min(math.Max(ax, ay), f.maxval))
    }
    root := Cast[f32](math.Sqrt(Cast[f64](gx * gx + gy * gy)))
    return RoundSample(math.Min(root, Cast[f32](f.maxval)))
}

/// One row of the gradient one sample at a time. O(row).
export SobelScalar(f: &Frame, y: int, dst: mut []u8, mode: int) {
    for i in 0..dst.Len() {
        dst[i] = SobelAt(f, y, i, mode)
    }
}

/// The column sums `a + 2 b + c` of three rows' vectors, in i32 lanes.
Column[const N: int](a: simd.Vec[u8, N], b: simd.Vec[u8, N], c: simd.Vec[u8, N]): simd.Vec[i32, N]
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
{
    two := simd.Vec[i32, N].Splat(2)
    return a.Cast[i32]() +% two *% b.Cast[i32]() +% c.Cast[i32]()
}

/// The left neighbours of `cur`: the last `channels` lanes of `prev` slid in
/// before it.
Left[const N: int](cur: simd.Vec[u8, N], prev: simd.Vec[u8, N], channels: int): simd.Vec[u8, N]
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
    requires 3 >= 0 && 3 <= N
{
    return if channels == 1 { cur.Slide(prev, 1) } else { cur.Slide(prev, 3) }
}

/// A row of the gradient N samples a step. Each step loads the three rows
/// at the step and one sample-step right, and takes the left neighbours by
/// sliding in the end of the previous step's vectors. O(row / N).
SobelN[const N: int](f: &Frame, y: int, dst: mut []u8, mode: int)
    requires N == 4 || N == 8 || N == 16
{
    row := f.Row()
    ch := f.channels
    up := f.ClampY(y - 1) * row
    mid := y * row
    down := f.ClampY(y + 1) * row
    p := f.samples
    top := simd.Vec[i32, N].Splat(Trunc[i32](f.maxval))
    ftop := simd.Vec[f32, N].Splat(Cast[f32](f.maxval))
    var i := math.Min(N, row)
    for i < row {
        if i + N + ch > row {
            break
        }
        u := simd.Vec[u8, N].FromSlice(p, up + i)
        m := simd.Vec[u8, N].FromSlice(p, mid + i)
        d := simd.Vec[u8, N].FromSlice(p, down + i)
        ul := Left[N](u, simd.Vec[u8, N].FromSlice(p, up + i - N), ch)
        ml := Left[N](m, simd.Vec[u8, N].FromSlice(p, mid + i - N), ch)
        dl := Left[N](d, simd.Vec[u8, N].FromSlice(p, down + i - N), ch)
        ur := simd.Vec[u8, N].FromSlice(p, up + i + ch)
        mr := simd.Vec[u8, N].FromSlice(p, mid + i + ch)
        dr := simd.Vec[u8, N].FromSlice(p, down + i + ch)
        gx := Column[N](ur, mr, dr) -% Column[N](ul, ml, dl)
        gy := Column[N](dl, d, dr) -% Column[N](ul, u, ur)
        var out := simd.Vec[i32, N].Zero()
        if mode == SobelL1 {
            out = (gx.AbsWrap() +% gy.AbsWrap()).Min(top)
        } else if mode == SobelMax {
            out = gx.Cast[f32]().Abs().Max(gy.Cast[f32]().Abs()).Min(ftop).CastSat[i32]()
        } else {
            fx := gx.Cast[f32]()
            fy := gy.Cast[f32]()
            out = (fx * fx + fy * fy).Sqrt().Min(ftop).RoundEven().CastSat[i32]()
        }
        out.Trunc[u8]().CopyTo(dst, i)
        i += N
    }
    for j in 0..math.Min(N, row) {
        dst[j] = SobelAt(f, y, j, mode)
    }
    for j in i..row {
        dst[j] = SobelAt(f, y, j, mode)
    }
}
