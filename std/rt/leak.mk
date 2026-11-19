// Why: a test should fail when it leaks heap memory, yet a normal build must
// pay nothing for counting.
//
// What: TrackLeaks, LiveAllocations and KeptForever.
//
// How: every build counts nothing until TrackLeaks, which the generated
// test runner calls first; it zeroes the counters and swaps the Heap's table
// to CountingTable. Finish reports what is still live.

import std.core
import std.raw

/// TrackLeaks zeroes the counters, turns counting on and swaps the Heap's
/// table to CountingTable, before the test's tasks exist.
/// O(1); allocates nothing.
export TrackLeaks() {
    // Edge: before the test's tasks exist; the Adds of later tasks follow
    // these stores in S (a spawn publishes through the run queues).
    Rt.live_count.Store(0)
    Rt.live_bytes.Store(0)
    Rt.counting.Store(1)
    unsafe "Init ran, so R() is the page it mapped, which lives until exit; CountingTable is a constant" {
        core.SetHeapTable(raw.Of(&(*R()).heap), raw.Of(&CountingTable))
    }
}

/// LiveAllocations is the number of blocks and bytes allocated from the Heap
/// since TrackLeaks and not freed; both 0 before it. O(1); allocates
/// nothing.
export LiveAllocations(): (count: int, bytes: int) {
    // Edge: read after the test's scope joined, so its tasks' Adds precede.
    return (count: Trunc[int](Rt.live_count.Load()), bytes: Trunc[int](Rt.live_bytes.Load()))
}

/// KeptForever takes blocks blocks of bytes bytes in all, which a and the
/// process heap gave and nothing will free, out of the test build's leak
/// count (std.x509's SystemPool keeps its pool for the process). It
/// changes the counters only when a is the process heap's allocator and a
/// test build counts; it frees and reads no memory. O(1); allocates
/// nothing.
export KeptForever(a: &core.Alloc, blocks: int, bytes: int) {
    // Edge: counting is stored once, by TrackLeaks before the test's tasks
    // exist (a spawn publishes it through the run queues), and never again.
    if !raw.Same(raw.Of(a), raw.Of(ProcessHeap().Alloc())) || Rt.counting.Load() != 1 {
        return
    }
    // Edge: none needed; counters summed at Finish, after every task ended
    // (as CountingAlloc's).
    _ = Rt.live_count.Add(-i64(blocks))
    _ = Rt.live_bytes.Add(-i64(bytes))
}
