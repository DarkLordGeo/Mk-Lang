// Why: dilate and erode, grey-scale morphology over a 3x3 square.
// What: MorphScalar, MorphN, MorphAt, Extremes and Pick.
// How: dilate takes the largest sample of each neighbourhood, erode the
// smallest, edges repeated. Gray images only: the pipeline refuses colour.

import std.math
import std.simd

/// The largest (or, unless `dilate`, the smallest) of `a` and `b`. O(1).
Pick(a: u8, b: u8, dilate: bool): u8 {
    return if dilate { math.Max(a, b) } else { math.Min(a, b) }
}

/// Output sample `x` of row `y`. O(1).
MorphAt(f: &Frame, y: int, x: int, dilate: bool): u8 {
    var best := f.samples[y * f.width + x]
    for dy in -1..2 {
        base := f.ClampY(y + dy) * f.width
        for dx in -1..2 {
            best = Pick(best, f.samples[base + f.ClampX(x + dx)], dilate)
        }
    }
    return best
}

/// Row `y` of the dilation or erosion of a gray frame. O(w).
export MorphScalar(f: &Frame, y: int, dst: mut []u8, dilate: bool) {
    for x in 0..dst.Len() {
        dst[x] = MorphAt(f, y, x, dilate)
    }
}

/// The three rows' extreme at samples `i` to `i + N` of rows `a`, `b` and
/// `c`, the lanes past the row end repeating its last sample. O(1).
Extremes[const N: int](a: []u8, b: []u8, c: []u8, i: int, dilate: bool): simd.Vec[u8, N]
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
{
    last := a.Len() - 1
    x := simd.Vec[u8, N].FromSliceOr(a, i, simd.Vec[u8, N].Splat(a[last]))
    y := simd.Vec[u8, N].FromSliceOr(b, i, simd.Vec[u8, N].Splat(b[last]))
    z := simd.Vec[u8, N].FromSliceOr(c, i, simd.Vec[u8, N].Splat(c[last]))
    return if dilate { x.Max(y).Max(z) } else { x.Min(y).Min(z) }
}

/// The same row N samples a step, the neighbours in registers: the column
/// extremes of a step are rotated one lane either way, and the lane that
/// wraps around is replaced by the neighbour from the step before or after,
/// which `Lane` reads and `With` puts in. O(w / N).
MorphN[const N: int](f: &Frame, y: int, dst: mut []u8, dilate: bool)
    requires N == 16 || N == 32 || N == 64
{
    w := f.width
    a := f.samples[f.ClampY(y - 1) * w..(f.ClampY(y - 1) + 1) * w]
    b := f.samples[y * w..(y + 1) * w]
    c := f.samples[f.ClampY(y + 1) * w..(f.ClampY(y + 1) + 1) * w]
    edge := simd.Vec[u8, N].Splat(Pick(Pick(a[w - 1], b[w - 1], dilate), c[w - 1], dilate))
    var cur := Extremes[N](a, b, c, 0, dilate)
    var before := cur.Lane(0)
    var i := 0
    for i < w {
        next := if i + N < w { Extremes[N](a, b, c, i + N, dilate) } else { edge }
        // Rotate(k) gives lane i lane i + k
        right := cur.Rotate(1).With(N - 1, next.Lane(0))
        left := cur.Rotate(N - 1).With(0, before)
        out := if dilate { cur.Max(left).Max(right) } else { cur.Min(left).Min(right) }
        Store[u8, N](out, dst, i)
        before = cur.Lane(N - 1)
        cur = next
        i += N
    }
}
