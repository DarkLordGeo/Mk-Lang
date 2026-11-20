// Why: std.fs's data-only sync (rt.FileDataSync) flushes a file's data
// without its metadata.
//
// What: DataSync.
//
// How: fcntl(F_FULLFSYNC), which makes the drive write its cache, as
// fdatasync promises on Linux; plain fsync on a file system that refuses
// it. Run on the pool.

// FFullFsync is F_FULLFSYNC of Darwin's <fcntl.h>.
const FFullFsync: i32 = 51

/// DataSync flushes h's written data to the device. EINTR is Canceled for
/// the pool to repeat; ENOTSUP and EINVAL from F_FULLFSYNC fall back to
/// fsync. O(1) system calls besides the device's flush; allocates nothing.
export DataSync(h: uint): OsResult {
    unsafe "fcntl and fsync pass no memory" {
        if fcntl(Trunc[i32](h), FFullFsync) == 0 {
            return Ok(0)
        }
        e := Errno()
        if e != 45 && e != 22 && e != 25 {
            return Fail(e)
        }
        return Checked(Trunc[int](i64(fsync(Trunc[i32](h)))))
    }
}
