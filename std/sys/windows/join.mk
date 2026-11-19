// Why: runtime.Thread and Mk libraries start OS threads that must be
// waited for and cleaned up, not left detached.
//
// What: ThreadStartJoin, a thread started to be joined, and ThreadJoin.
//
// How: on Windows ThreadStart already keeps the thread's HANDLE open (its
// value), so ThreadStartJoin is ThreadStart, and the join waits on the
// handle and closes it.

/// ThreadStartJoin is ThreadStart for a thread ThreadJoin will join: value
/// is the handle ThreadJoin takes. O(1) system calls, as ThreadStart.
/// Allocates nothing on the heap.
export ThreadStartJoin(entry: fn(*u8), arg: *u8, stack: int): OsResult {
    return ThreadStart(entry, arg, stack)
}

/// ThreadJoin waits until the thread h (ThreadStartJoin's value) has ended,
/// then frees what it kept (its handle). Blocks the calling OS thread: a
/// task joins through the blocking pool (runtime.Joined.Join). O(1) system
/// calls besides the wait. Allocates nothing.
export ThreadJoin(h: i64) {
    unsafe "kernel32; h is the open handle ThreadStartJoin gave, joined once" {
        _ = WaitForSingleObject(Trunc[uint](h), 0xFFFF_FFFF)
        _ = CloseHandle(Trunc[uint](h))
    }
}
