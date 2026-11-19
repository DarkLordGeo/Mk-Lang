// Why: std.fs's data-only sync (rt.FileDataSync) flushes a file's data
// without its metadata.
//
// What: DataSync.
//
// How: one fdatasync system call, run on the pool.

/// DataSync flushes h's written data and the metadata a read of it needs
/// (the size; not the times): fdatasync. EINTR is Canceled for the pool to
/// repeat (MapError); EINVAL for a handle that cannot sync (a pipe, a
/// socket) is the OS's. O(1) system calls besides the device's flush;
/// allocates nothing.
export DataSync(h: uint): OsResult {
    unsafe "a system call that passes no memory, only values and handles" {
        return Result(Syscall(Nrs().fdatasync, h, 0, 0, 0, 0, 0))
    }
}
