// Why: `swap` reorders a colour pixel's channels and `posterize` keeps the
// top bits of each.
// What: SwapScalar, SwapN, Swap16, PosterizeScalar, PosterizeN and the order
// constants.
// How: vector bodies rotate and select lanes by channel; every body gives
// the same bytes.

import std.simd

// The channel orders `swap` writes, named by the source channel each output
// channel takes.

/// Blue, green, red: red and blue exchanged.
export const OrderBgr: int = 0
/// Green, blue, red: each channel takes the next one.
export const OrderGbr: int = 1
/// Blue, red, green: each channel takes the one before.
export const OrderBrg: int = 2

/// The source channel of output channel `c` in `order`. O(1).
export SourceChannel(order: int, c: int): int {
    return if order == OrderBgr { 2 - c } else if order == OrderGbr { (c + 1) % 3 } else { (c + 2) % 3 }
}

/// A colour row with its channels reordered. O(w).
export SwapScalar(src: []u8, dst: mut []u8, order: int) {
    for i in 0..dst.Len() {
        c := i % 3
        dst[i] = src[i - c + SourceChannel(order, c)]
    }
}

/// The same row a whole number of pixels a step, from one load each: the
/// lanes rotated one or two places either way, and the rotation each lane
/// takes chosen by its channel. A step stores the 3 * (N / 3) lanes whose
/// sources lie in the same load. O(w / N).
SwapN[const N: int](src: []u8, dst: mut []u8, order: int)
    requires N == 16 || N == 32 || N == 64
{
    lane := simd.Vec[u8, N].Iota()
    three := simd.Vec[u8, N].Splat(3)
    phase := lane -% lane.DivTrunc(three) *% three
    first := phase == simd.Vec[u8, N].Zero()
    second := phase == simd.Vec[u8, N].Splat(1)
    step := N - N % 3
    keep := TailMask[u8, N](step)
    var i := 0
    for i + N <= src.Len() {
        v := simd.Vec[u8, N].FromSlice(src, i)
        // Rotate(k) gives lane i lane i + k
        out := if order == OrderBgr {
            first.Select(v.Rotate(2), second.Select(v, v.Rotate(N - 2)))
        } else if order == OrderGbr {
            first.Select(v.Rotate(1), second.Select(v.Rotate(1), v.Rotate(N - 2)))
        } else {
            first.Select(v.Rotate(2), second.Select(v.Rotate(N - 1), v.Rotate(N - 1)))
        }
        out.CopyToMasked(dst, i, keep)
        i += step
    }
    SwapScalar(src[i..], mut dst[i..], order)
}

/// The same row five pixels a step at 16 lanes, each order one Shuffle with
/// fixed indices; the 16th lane is never stored. O(w).
export Swap16(src: []u8, dst: mut []u8, order: int) {
    keep := TailMask[u8, 16](15)
    var i := 0
    for i + 16 <= src.Len() {
        v := simd.Vec[u8, 16].FromSlice(src, i)
        out := if order == OrderBgr {
            v.Shuffle(v, [2, 1, 0, 5, 4, 3, 8, 7, 6, 11, 10, 9, 14, 13, 12, 15])
        } else if order == OrderGbr {
            v.Shuffle(v, [1, 2, 0, 4, 5, 3, 7, 8, 6, 10, 11, 9, 13, 14, 12, 15])
        } else {
            v.Shuffle(v, [2, 0, 1, 5, 3, 4, 8, 6, 7, 11, 9, 10, 14, 12, 13, 15])
        }
        out.CopyToMasked(dst, i, keep)
        i += 15
    }
    SwapScalar(src[i..], mut dst[i..], order)
}

/// Keeps the top bits of each sample: `shifts[c]` low bits of channel `c`
/// are cleared. O(n).
export PosterizeScalar(src: []u8, dst: mut []u8, shifts: &[3]u8, channels: int) {
    for i, v in src {
        k := shifts[i % channels]
        dst[i] = (v >> k) << k
    }
}

/// The same, N samples a step by ShrEach and ShlEach with a shift per lane:
/// the shifts repeat with the channels, so a step starting at sample `i`
/// loads them from `i % channels` in a table of the repeated pattern. O(n / N).
PosterizeN[const N: int](src: []u8, dst: mut []u8, shifts: &[3]u8, channels: int)
    requires N == 16 || N == 32 || N == 64
{
    var pattern: [67]u8 = [0; 67]
    for j in 0..67 {
        pattern[j] = shifts[j % channels]
    }
    zero := simd.Vec[u8, N].Zero()
    var i := 0
    for i < src.Len() {
        k := simd.Vec[u8, N].FromSlice(pattern[..], i % channels)
        v := simd.Vec[u8, N].FromSliceOr(src, i, zero)
        Store[u8, N](v.ShrEach(k).ShlEach(k), dst, i)
        i += N
    }
}
