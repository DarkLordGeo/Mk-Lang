// Why: runtime.Thread and Mk libraries need threads that can be joined,
// which the runtime's own detached threads are not.
//
// What: ThreadStartJoin and ThreadJoin, behind sys.ThreadJoin.
//
// How: the thread is made joinable by pthread_create (thread.mk's Start),
// its handle the pthread_t; ThreadJoin is pthread_join, after which
// libSystem frees the thread's stack. The alternate signal stack
// ThreadInit mapped stays mapped (64 KiB per joined thread).

import std.raw

/// ThreadStartJoin is ThreadStart for a thread ThreadJoin will join: value
/// is the pthread_t (the handle ThreadJoin takes), not the id. O(1) system
/// calls, as ThreadStart.
export ThreadStartJoin(entry: fn(*u8), arg: *u8, stack: int): OsResult {
    return Start(entry, arg, stack, true)
}

/// ThreadJoin waits until the thread h (ThreadStartJoin's value) has ended.
/// Blocks the calling OS thread: a task joins through the blocking pool
/// (runtime.Joined.Join). O(1) system calls besides the wait. Allocates
/// nothing.
export ThreadJoin(h: i64) {
    unsafe "h is a joinable pthread_t that ThreadStartJoin made and nobody joined" {
        _ = pthread_join(Cast[u64](h), raw.Null[u8]())
    }
}
