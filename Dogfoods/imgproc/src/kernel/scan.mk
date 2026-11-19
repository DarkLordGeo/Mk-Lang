// Why: the equalisation table needs a cumulative histogram.
// What: RunningSum.
// How: a vector prefix sum, slides of 1, 2 and 4 within a step.

import std.simd

/// `out[i]` is the sum of `counts[0..i + 1]`, N counts a step in i32 lanes:
/// each step adds its lanes to the lanes after them by slides of 1, 2 and 4,
/// adds the total so far broadcast from the step before, and leaves the lanes
/// through ToArray to be widened into `out`. The counts' sum must stay below
/// 2^31, which an image of at most 2^26 samples keeps. O(n).
export RunningSum[const N: int](counts: []i32, out: mut []int)
    requires N == 4 || N == 8
{
    zero := simd.Vec[i32, N].Zero()
    var carry := zero
    var i := 0
    for i + N <= counts.Len() {
        var v := simd.Vec[i32, N].FromSlice(counts, i)
        v = v +% v.Slide(zero, 1)
        v = v +% v.Slide(zero, 2)
        if N == 8 {
            v = v +% v.Slide(zero, 4)
        }
        v = v +% carry
        carry = v.Broadcast(N - 1)
        lanes := v.ToArray()
        for j in 0..N {
            out[i + j] = int(lanes[j])
        }
        i += N
    }
    var total := if i > 0 { int(carry.Lane(0)) } else { 0 }
    for j in i..counts.Len() {
        total += int(counts[j])
        out[j] = total
    }
}
