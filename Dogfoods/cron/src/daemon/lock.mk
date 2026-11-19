// Why: two daemons on one crontab would run every job twice.
//
// What: Lock, Unlock and the Held error.
//
// How: an exclusive advisory lock on a lock file, held for the daemon's
// life; a second instance is refused at once, or after waiting
// `--lock-wait` for the first to stop.

import std.fs
import std.time

/// Another instance holds the lock.
export error Held

/// Why the lock could not be taken.
export type LockError = Held | fs.Error

/// Opens the lock file, creating it, and takes its exclusive lock: at once
/// with `TryLock`, or, when `wait` is given, with `Lock` under a deadline
/// of that long. The file stays open, and locked, until the caller closes
/// it. Fails with `Held` when another process has it.
export Lock(io: Io, path: str, wait: ?time.Duration): LockError!fs.File {
    var o := fs.DefaultOptions
    o.write = true
    o.create = true
    var f := try fs.OpenWith(io, path, o)
    onerr f.Abandon()
    d := wait else {
        if !try f.TryLock(io, fs.LockMode.Exclusive) {
            fail Held
        }
        return f
    }
    scope s := io.Scope() {
        s.CancelAfter(d)
        f.Lock(io, fs.LockMode.Exclusive) else e {
            if s.Expired() {
                fail Held
            }
            fail e
        }
    }
    return f
}

/// Releases the lock and closes the file.
export Unlock(io: Io, f: fs.File) {
    f.Unlock(io) else {}
    f.Close(io) else {
        f.Abandon()
    }
}
