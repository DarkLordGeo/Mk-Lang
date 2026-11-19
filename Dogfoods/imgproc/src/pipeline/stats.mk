// Why: some operations choose their parameters from the whole image.
// What: Histogram, Otsu, EqualizeLut and LevelsLut.
// How: integer or f64 arithmetic in one fixed order, so every path and
// target computes the same table.

import src.kernel
import std.math
import std.simd

/// How many samples take each value. O(n).
export Histogram(samples: []u8): [256]int {
    var h: [256]int = [0; 256]
    for v in samples {
        h[v] += 1
    }
    return h
}

/// Otsu's threshold (N. Otsu, "A threshold selection method from gray-level
/// histograms", IEEE SMC 9(1), 1979): the `t` that maximises the
/// between-class variance of the samples below `t` and those at or above it.
/// Ties take the smallest `t`. O(256).
export Otsu(h: &[256]int, maxval: int): int {
    var total := 0
    var weighted: f64 = 0.0
    for v in 0..256 {
        total += h[v]
        weighted += Cast[f64](v) * Cast[f64](h[v])
    }
    var best := 1
    var best_score: f64 = -1.0
    var below := 0
    var below_sum: f64 = 0.0
    for t in 1..maxval + 1 {
        below += h[t - 1]
        below_sum += Cast[f64](t - 1) * Cast[f64](h[t - 1])
        above := total - below
        if below == 0 || above == 0 {
            continue
        }
        mean_below := below_sum / Cast[f64](below)
        mean_above := (weighted - below_sum) / Cast[f64](above)
        gap := mean_below - mean_above
        score := Cast[f64](below) * Cast[f64](above) * gap * gap
        if score > best_score {
            best_score = score
            best = t
        }
    }
    return best
}

/// The equalisation table: each value maps to its rank among the samples,
/// scaled to 0..maxval and rounded half up, with the lowest present value at
/// 0. A one-valued image maps to itself. The cumulative counts come from
/// kernel.RunningSum at the target's own i32 lane count. O(256).
export EqualizeLut(h: &[256]int, maxval: int): [256]u8 {
    var lut: [256]u8 = [0; 256]
    var counts: [256]i32 = [0; 256]
    var lowest := -1
    for v in 0..256 {
        counts[v] = Trunc[i32](h[v])
        if lowest < 0 && h[v] > 0 {
            lowest = h[v]
        }
    }
    var cdf: [256]int = [0; 256]
    kernel.RunningSum[simd.Lanes[i32]()](counts[..], mut cdf[..])
    span := cdf[255] - lowest
    for v in 0..256 {
        seen := cdf[v]
        if span <= 0 {
            lut[v] = Trunc[u8](math.Min(v, maxval))
            continue
        }
        rank := math.Max(seen - lowest, 0)
        lut[v] = Trunc[u8]((2 * rank * maxval + span) / (2 * span))
    }
    return lut
}

/// The levels table: the darkest present value maps to 0, the brightest to
/// maxval, linearly between, rounded half up. A one-valued image maps to
/// itself. O(256).
export LevelsLut(h: &[256]int, maxval: int): [256]u8 {
    var lut: [256]u8 = [0; 256]
    var lo := 0
    for lo < 255 && h[lo] == 0 {
        lo += 1
    }
    var hi := 255
    for hi > 0 && h[hi] == 0 {
        hi -= 1
    }
    for v in 0..256 {
        if hi <= lo {
            lut[v] = Trunc[u8](math.Min(v, maxval))
            continue
        }
        x := math.Min(math.Max(v, lo), hi) - lo
        lut[v] = Trunc[u8]((2 * x * maxval + (hi - lo)) / (2 * (hi - lo)))
    }
    return lut
}
