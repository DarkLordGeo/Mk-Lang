# relock

A relock measurement: a waiter's blocking whole-file lock against a peer that holds the same file's lock on another handle.

- **handoff1, handoff20.** The peer holds the lock for 1 or 20 ms and releases it once; the waiter, already blocked in its lock call, takes it. The time per round less the hold is the hand-off latency.
- **relock5.** The peer releases and retakes the lock at once every 5 ms, for up to 2 s. The waiter must take it within 1 s. A waiter that never sees the lock free loses the round, and the row's checksum (`rounds won`) fails: that is the result, not a broken cell.

Mk's `File.Lock` holds no thread while it waits: on Windows the kernel hands the lock over at the holder's release (an overlapped `LockFileEx` on the poller); on Linux and off the poller it tries with `Io.Sleep` from 1 ms doubling to 64 ms between tries. Rust's `File::lock` blocks a thread in `flock` or `LockFileEx`, which the kernel wakes at the release; its 1 s bound is a `recv_timeout` on the waiter thread's answer.

C, Zig and Go are skipped (`bench.json`). The peer runs in the same process on its own handle, not in a second process: both locks belong to the handle or the open file description, so the kernel sees two holders.
