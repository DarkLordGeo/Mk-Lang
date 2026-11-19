// Why: std.fs offers a data-only sync (rt.FileDataSync), and Windows needs
// its own answer to it.
// What: DataSync, run on the pool.
// How: kernel32 has no data-only flush (the data-sync flag is
// NtFlushBuffersFileEx's, in ntdll, which std does not import), so it is
// FlushFileBuffers, which flushes the data and the metadata: never less
// than asked.

/// DataSync flushes h's written data and the metadata a read of it needs:
/// FlushFileBuffers (SyncFile). O(1) system calls besides the device's
/// flush; allocates nothing.
export DataSync(h: uint): OsResult {
    return SyncFile(h)
}
