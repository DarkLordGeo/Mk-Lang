// Why: Draws a function's graph as an image.
//
// What: The plotter: samples an expression once per pixel column, in f64 or
// f32.
//
// How: Draws the curve over a grid of round-numbered lines and the axes.

import std.math

/// What to plot and how large.
export struct Options {
    export width: int
    export height: int
    export from: f64
    export to: f64
    export low: ?f64
    export high: ?f64
    export single: bool
}

/// The y range spans more than the largest f64, so no row can be computed.
export error RangeTooWide(low: f64, high: f64)

/// What the plot found: the sampled y range and how many samples were not
/// finite (NaN or infinite, left out of the curve).
export struct Summary {
    export low: f64
    export high: f64
    export missing: int
    export step: f64
}

const MaxLines: int = 64

// A round step for grid lines: 1, 2 or 5 times a power of ten, giving about
// five lines across `span`.
GridStep(span: f64): f64 {
    raw := span / 5.0
    power := math.Pow(10.0, math.Floor(math.Log10(raw)))
    lead := raw / power
    k := if lead < 1.5 { 1.0 } else if lead < 3.5 { 2.0 } else if lead < 7.5 { 5.0 } else { 10.0 }
    return k * power
}

// The pixel row of `y`, clamped a little outside the image so a steep
// segment still reaches its edge.
Row(y: f64, low: f64, high: f64, height: int): int {
    r := (high - y) / (high - low) * Cast[f64](height - 1)
    return Cast[int](math.Round(math.Max(-1.0, math.Min(Cast[f64](height), r))))
}

Paint(pixels: mut []f64, width: int, height: int, x: int, y: int, c: (r: f64, g: f64, b: f64)) {
    if x < 0 || x >= width || y < 0 || y >= height {
        return
    }
    i := (y * width + x) * 3
    pixels[i] = c.r
    pixels[i + 1] = c.g
    pixels[i + 2] = c.b
}

/// Plots `e` into `pixels` (width times height times 3 values, linear RGB).
/// Fails with `RangeTooWide` when the y range's width is not finite, and
/// `OutOfMemory` when `a` runs out. O(width times the expression's nodes plus the pixels); allocates one
/// value a node and one a column in `a`.
export Plot(a: &Arena, e: &Expr, o: &Options, pixels: mut []f64): RangeTooWide|OutOfMemory!Summary {
    w := o.width
    h := o.height
    var ys := try a.Slice[f64](w)
    var vals := try a.Slice[f64](e.Slots())
    var vals32 := try a.Slice[f32](e.Slots())
    var missing := 0
    var low := math.Inf
    var high := -math.Inf
    for i in 0..w {
        x := o.from + (o.to - o.from) * (Cast[f64](i) + 0.5) / Cast[f64](w)
        y := if o.single { Cast[f64](e.EvalF32(ToF32(x), vals32)) } else { e.Eval(x, vals) }
        ys[i] = y
        if !math.IsFinite(y) {
            missing += 1
            continue
        }
        low = math.Min(low, y)
        high = math.Max(high, y)
    }
    if missing == w {
        low = -1.0
        high = 1.0
    }
    if low == high {
        low -= 1.0
        high += 1.0
    }
    // A margin of a twentieth of the range above and below the curve.
    margin := (high - low) / 20.0
    var lo := o.low else { low - margin }
    var hi := o.high else { high + margin }
    if !(hi > lo) {
        lo = low - margin
        hi = high + margin
    }
    if !math.IsFinite(hi - lo) {
        fail RangeTooWide(low: lo, high: hi)
    }
    pixels.Fill(1.0)
    // Grid lines, then axes over them, then the curve over everything.
    xstep := GridStep(o.to - o.from)
    // Lines by index, at most MaxLines, so a step below the spacing of the
    // floats near a huge range cannot loop forever.
    xstart := math.Ceil(o.from / xstep) * xstep
    for k in 0..MaxLines {
        gx := xstart + Cast[f64](k) * xstep
        if gx > o.to {
            break
        }
        col := Cast[int](math.Floor((gx - o.from) / (o.to - o.from) * Cast[f64](w)))
        for y in 0..h {
            Paint(pixels, w, h, col, y, (r: 0.85, g: 0.85, b: 0.85))
        }
    }
    ystep := GridStep(hi - lo)
    ystart := math.Ceil(lo / ystep) * ystep
    for k in 0..MaxLines {
        gy := ystart + Cast[f64](k) * ystep
        if gy > hi {
            break
        }
        row := Row(gy, lo, hi, h)
        for x in 0..w {
            Paint(pixels, w, h, x, row, (r: 0.85, g: 0.85, b: 0.85))
        }
    }
    if lo < 0.0 && hi > 0.0 {
        row := Row(0.0, lo, hi, h)
        for x in 0..w {
            Paint(pixels, w, h, x, row, (r: 0.3, g: 0.3, b: 0.3))
        }
    }
    if o.from < 0.0 && o.to > 0.0 {
        col := Cast[int](math.Floor(-o.from / (o.to - o.from) * Cast[f64](w)))
        for y in 0..h {
            Paint(pixels, w, h, col, y, (r: 0.3, g: 0.3, b: 0.3))
        }
    }
    var prev: ?int = none
    for i, y in ys {
        if !math.IsFinite(y) {
            prev = none
            continue
        }
        row := Row(y, lo, hi, h)
        // Join to the previous column with a vertical run, so a steep curve
        // stays connected.
        top := math.Min(row, prev else { row })
        bottom := math.Max(row, prev else { row })
        for r in top..bottom + 1 {
            Paint(pixels, w, h, i, r, (r: 0.1, g: 0.2, b: 0.8))
        }
        prev = some(row)
    }
    return Summary(low: lo, high: hi, missing: missing, step: ystep)
}

test "GridStep picks 1, 2 or 5 times a power of ten" {
    Assert(GridStep(10.0) == 2.0)
    Assert(GridStep(1.0) == 0.2)
    Assert(GridStep(30.0) == 5.0)
}
