// Why: `imgproc info` prints a summary of an image's samples.
// What: Summary and Summarize, with CountMax and Spread.
// How: 16 lanes a step at the baseline's byte width, the tail one sample at
// a time.

import std.math
import std.simd

/// The lanes of one baseline vector of bytes, and of f32.
const ByteLanes: int = simd.Lanes[u8]()
const FloatLanes: int = simd.Lanes[f32]()

/// What `Summarize` finds.
export struct Summary {
    export min: int
    export max: int
    /// The sum of every sample, exact
    export sum: i64
    /// How many samples equal `max`, and the first of them
    export at_max: int
    export first_max: int
    /// The OR and the AND of every sample: the bits used and those always set
    export bits_or: u8
    export bits_and: u8
    /// The XOR of every sample, and their sum modulo 2^32: two checksums
    export xor: u8
    export sum32: u32
    /// Every sample is 0 or maxval
    export binary: bool
    /// Some sample exceeds maxval, which a decoded image never has
    export over: bool
    /// The standard deviation of the samples
    export spread: f64
}

/// Summarizes `xs`, which is not empty. O(n).
export Summarize(xs: []u8, maxval: u8): Summary {
    var s := Summary(
        min: 255,
        max: 0,
        sum: 0,
        at_max: 0,
        first_max: -1,
        bits_or: 0,
        bits_and: 0xFF,
        xor: 0,
        sum32: 0,
        binary: true,
        over: false,
        spread: 0.0,
    )
    zero := simd.Vec[u8, ByteLanes].Zero()
    top := simd.Vec[u8, ByteLanes].Splat(maxval)
    var wide := simd.Vec[u32, ByteLanes].Zero()
    var i := 0
    for i + ByteLanes <= xs.Len() {
        v := simd.Vec[u8, ByteLanes].FromSlice(xs, i)
        s.min = math.Min(s.min, int(v.ReduceMin()))
        s.max = math.Max(s.max, int(v.ReduceMax()))
        s.sum += v.ReduceSumWide()
        s.bits_or |= v.ReduceOr()
        s.bits_and &= v.ReduceAnd()
        s.xor ^= v.ReduceXor()
        wide = wide +% v.Cast[u32]()
        s.binary = s.binary && ((v == zero) | (v == top)).All()
        s.over = s.over || (v > top).Any()
        i += ByteLanes
    }
    s.sum32 = wide.ReduceSumWrap()
    for j in i..xs.Len() {
        v := xs[j]
        s.min = math.Min(s.min, int(v))
        s.max = math.Max(s.max, int(v))
        s.sum += i64(v)
        s.bits_or |= v
        s.bits_and &= v
        s.xor ^= v
        s.sum32 = s.sum32 +% u32(v)
        s.binary = s.binary && (v == 0 || v == maxval)
        s.over = s.over || v > maxval
    }
    CountMax(xs, mut s)
    s.spread = Spread(xs, Cast[f64](s.sum) / Cast[f64](xs.Len()))
    return s
}

/// Counts the samples equal to `s.max` and finds the first, by the mask of
/// lanes that equal it.
CountMax(xs: []u8, s: mut Summary) {
    top := simd.Vec[u8, ByteLanes].Splat(Trunc[u8](s.max))
    var i := 0
    for i + ByteLanes <= xs.Len() {
        hit := simd.Vec[u8, ByteLanes].FromSlice(xs, i) == top
        if s.first_max < 0 {
            if at := hit.FirstSet() {
                s.first_max = i + at
            }
        }
        s.at_max += hit.CountSet()
        i += ByteLanes
    }
    for j in i..xs.Len() {
        if int(xs[j]) == s.max {
            if s.first_max < 0 {
                s.first_max = j
            }
            s.at_max += 1
        }
    }
}

/// The standard deviation around `mean`: squared deviations in f32 lanes,
/// each vector's lanes added by ReduceSum as a tree, the vectors' sums added
/// in f64 in order. O(n).
Spread(xs: []u8, mean: f64): f64 {
    m := simd.Vec[f32, FloatLanes].Splat(Cast[f32](mean))
    var total: f64 = 0.0
    var i := 0
    for i + FloatLanes <= xs.Len() {
        d := simd.Vec[u8, FloatLanes].FromSlice(xs, i).Cast[f32]() - m
        total += f64((d * d).ReduceSum())
        i += FloatLanes
    }
    for j in i..xs.Len() {
        d := Cast[f64](xs[j]) - mean
        total += d * d
    }
    return math.Sqrt(total / Cast[f64](xs.Len()))
}
