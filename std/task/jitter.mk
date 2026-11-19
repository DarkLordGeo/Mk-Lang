// Why: races in the scheduler hide in windows a plain run almost never
// hits; stress tests need a way to widen those windows on demand.
//
// What: the jitter point ids (JitterPush to JitterTimer), Mix (splitmix64's
// finalizer) and Jitter, which perturbs one point.
//
// How: with MK_TEST_SEED=<n> (n > 0, read at start)
// every jitter point below may spin for a pseudo-random time or yield the
// thread, so a stress run over many seeds reaches the interleavings a plain
// run rarely does: a push and its wake; a store of bottom and the load of
// top; a thief's two loads; the idle publication and its recheck; the moment
// before a park's switch; the gap between a wait's first cancellation check
// and its registration; the gap between a poller submission and its park,
// where the completion often wins; and the end of a task after its live
// decrement (JitterEnd); and a timer's two windows, between the
// read of its heap and that heap's lock, and between a pop and its fire
// (JitterTimer). Without the variable a point is one load of a word
// written once at start and a branch.

import std.raw
import std.sys

const JitterPush: u64 = 1
const JitterTake: u64 = 2
const JitterSteal: u64 = 3
const JitterReady: u64 = 4
const JitterIdle: u64 = 5
const JitterPark: u64 = 6
const JitterCancel: u64 = 7
const JitterSubmit: u64 = 8
const JitterSignal: u64 = 9
const JitterEnd: u64 = 10
const JitterTimer: u64 = 11

// Mix is splitmix64's finalizer (S. Vigna; Steele, Lea and Flood, OOPSLA
// 2014), a bijection with full avalanche.
Mix(var z: u64): u64 {
    z = (z ^ (z >> 30)) *% 0xBF58_476D_1CE4_E5B9
    z = (z ^ (z >> 27)) *% 0x94D0_49BB_1331_11EB
    return z ^ (z >> 31)
}

// Jitter perturbs one point: one in eight passes spins for up to 1,023
// atomic loads, and one in sixteen of those yields the thread instead.
Jitter(point: u64) {
    st := TS()
    unsafe "the TaskState page; jitter_seed is written once before any thread" {
        if raw.IsNull(st) || (*st).jitter_seed == 0 {
            return
        }
        // Edge: none needed; the counter only has to differ between calls.
        x := Mix((*st).jitter.Add(0x9E37_79B9_7F4A_7C15) ^ (*st).jitter_seed ^ point)
        if x & 7 != 0 {
            return
        }
        if (x >> 3) & 15 == 0 {
            sys.ThreadYield()
            return
        }
        n := Trunc[int]((x >> 8) & 1023)
        for _ in 0..n {
            // Edge: none; a delay.
            _ = (*st).jitter.Load()
        }
    }
}
