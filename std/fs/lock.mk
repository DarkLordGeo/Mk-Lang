// Why: processes that share a file (a cache, a database) need an advisory
// lock over the whole file.
//
// What: File.Lock, File.TryLock, File.Unlock and LockMode.
//
// How: flock(2) on Linux and LockFileEx on Windows, as Go's internal
// lockedfile and Rust's File::lock take it. The lock is the open file's, so
// it is released by Unlock, by Close or Abandon, or when the process ends.
// On Windows the lock is mandatory for other handles' reads and writes of
// the locked range; on Linux it binds only other lockers.

import std.core
import std.rt

/// The kind of lock File.Lock and File.TryLock take.
export enum LockMode {
    /// Many holders at once, none of them Exclusive.
    Shared
    /// One holder.
    Exclusive
}

/// Takes the lock on the whole file in mode, waiting while another
/// holder's lock conflicts. Canceled when the scope is cancelled or its
/// deadline passes while it waits; nothing is taken, and the call can be
/// made again. Os for a file that cannot be locked. O(1) system calls per
/// try (one more first on Windows, the unlock); allocates nothing.
///
/// Locking a file that already holds the lock converts it, as flock(2)
/// does on Linux. On Windows, where LockFileEx would stack a second shared
/// lock or wait on the handle's own exclusive one, the file is unlocked
/// before it locks. A conversion that is cancelled, or that another holder
/// wins, can leave nothing held (always so on Windows; flock(2) does not
/// promise an atomic conversion), so lock again before relying on it.
///
/// A wait holds no thread. On Windows, for a file on the poller, the
/// kernel completes one overlapped wait at the holder's release. Elsewhere
/// (Linux, a file off the poller, a simulated file) the call tries again
/// with Io.Sleep between tries, from 1 ms doubling to 64 ms, so the lock
/// can be taken up to 64 ms after its release.
export File.Lock(&self, io: Io, mode: LockMode): Error!void {
    try self.Release(io)
    _ = try Check(rt.FileLock(io, self.cur.h, mode == LockMode.Exclusive, false))
}

/// Takes the lock on the whole file in mode if no other holder's lock
/// conflicts, and returns whether it did; it never waits. A file that
/// holds the lock gives it up first, as Lock does, so the answer is about
/// other holders only, and false can leave nothing held, as a lost
/// conversion of Lock can. Os for a file that cannot be locked. O(1): one
/// system call (two on Windows); allocates nothing.
export File.TryLock(&self, io: Io, mode: LockMode): Error!bool {
    try self.Release(io)
    got := try Check(rt.FileLock(io, self.cur.h, mode == LockMode.Exclusive, true))
    return got == 1
}

// Release drops the lock the file holds before it locks again, on Windows
// only: LockFileEx judges a handle's new lock against its own,
// where flock(2) converts. Nothing held is no failure. Not for a
// simulated file (a Sim's handle is foreign), whose lock converts on every
// target: the extra call would count as an operation of the Sim on one
// target only, and a seed must give one run whatever the target.
File.Release(&self, io: Io): Error!void {
    if Target.os == core.Os.Windows && !rt.IsForeign(self.cur.h) {
        _ = try Check(rt.FileUnlock(io, self.cur.h))
    }
}

/// Releases the file's lock; a file that holds none succeeds. Close,
/// Abandon and the end of the process release it too. Any failure is the
/// OS's. O(1): one system call; allocates nothing.
export File.Unlock(&self, io: Io): Error!void {
    _ = try Check(rt.FileUnlock(io, self.cur.h))
}
