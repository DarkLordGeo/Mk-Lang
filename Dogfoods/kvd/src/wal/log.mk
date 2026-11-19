// Why: no change a client was told about may be lost in a crash.
// What: Wal, Dir, TakeLock, Recover, Load and the log errors (BadHeader,
// Corrupt, Crashed, Broken, InUse).
// How: a log beside a snapshot, with generations that say which to trust
// after a crash, as below.

// The write-ahead log as the server uses it, beside its snapshot. A data
// directory holds the log (`kvd.wal`), the snapshot of the store when the
// log's generation began (`kvd.snap`), and an instance lock (`kvd.lock`)
// that one server holds while it runs. Recovery loads the snapshot and
// replays the log after it; compaction writes a new snapshot, renames it
// into place and cuts the log back to its header, so a crash at any point
// leaves a snapshot and a log that recover to the same store:
//
//   log generation = snapshot generation    replay the log
//   log generation < snapshot generation    the crash came after the rename:
//                                           the snapshot has the log's changes
//
// A data directory with no snapshot is a log of generation 0 onward, which
// is also what a log from before snapshots is.
import std.fs
import src.snap
import src.store

export error BadHeader
export error Corrupt(offset: i64)
export error Crashed(change: int)
export error Broken
export error InUse

/// What reading or writing the log can fail with. `Crashed` is the
/// simulator's stand-in for a crash (`Crash`); `Broken` means an earlier
/// write failed part way and only a compaction can make the log whole;
/// `InUse` that another server holds the data directory.
export type Error = BadHeader | Corrupt | Crashed | Broken | InUse | snap.Error | fs.Error | WriteError | OutOfMemory

/// Where the simulator stops the log, to stand for a crash: before write
/// number `at`, counted from 0 since the log was opened, where a write is an
/// appended change or a compaction. When `torn`, half of that write reaches
/// the disk first. `at` below 0 never stops.
export struct Crash {
    export at: int
    export torn: bool
}

/// No crash.
export const NoCrash: Crash = Crash(at: -1, torn: false)

/// When an appended change is synced to the disk.
export enum SyncPolicy {
    /// Before the change is answered, so a change a client was told of
    /// survives a crash of the machine.
    Always
    /// After every `n` changes, and at compaction and close.
    Every(n: int)
    /// Only at compaction and close; the system writes the rest when it will.
    Never
}

/// The files of one data directory.
export struct Dir {
    export log: str
    export log_tmp: str
    export snap: str
    export snap_tmp: str
    export lock: str
}

/// The files of the data directory `dir` (`""` is the working directory),
/// with the instance lock at `lock` when it is given, else in `dir`.
export Dir.In(a: &Arena, dir: str, lock: ?str): OutOfMemory!Dir from a, lock {
    sep := if dir == "" { "" } else { "/" }
    return Dir(
        log: try a.Fmt("{dir}{sep}kvd.wal", (dir, sep)),
        log_tmp: try a.Fmt("{dir}{sep}kvd.wal.tmp", (dir, sep)),
        snap: try a.Fmt("{dir}{sep}kvd.snap", (dir, sep)),
        snap_tmp: try a.Fmt("{dir}{sep}kvd.snap.tmp", (dir, sep)),
        lock: lock else { try a.Fmt("{dir}{sep}kvd.lock", (dir, sep)) },
    )
}

/// Takes the data directory's instance lock: makes the lock file if it is
/// missing and holds an exclusive lock on it, which the returned file keeps
/// until it is closed. Fails with `InUse` when another holder has it.
export TakeLock(io: Io, path: str): Error!fs.File {
    var o := fs.ReadWrite
    o.create = true
    var f := try fs.OpenWith(io, path, o)
    onerr f.Abandon()
    if !try f.TryLock(io, fs.LockMode.Exclusive) {
        fail InUse
    }
    return f
}

/// An open log, every write of which goes to its end.
export linear view struct Wal {
    file: fs.File
    lock: fs.File
    dir: Dir
    nbytes: i64
    nrecords: int
    writes: int
    epoch: u64
    crash: Crash
    broken: bool
    policy: SyncPolicy
    nunsynced: int
}

/// What recovery found.
export struct Recovered {
    export generation: u64
    export records: int
    export clean: bool
    export end: End
    export good_bytes: i64
    export snapshot: ?snap.Info
    export stale: bool
}

/// Reads exactly `buf.Len()` bytes of `f` from `pos`.
ReadFull(io: Io, f: &fs.File, pos: i64, buf: mut []u8): Error!void {
    var got := 0
    for got < buf.Len() {
        n := try f.ReadAt(io, pos + i64(got), mut buf[got..])
        if n == 0 {
            fail Corrupt(offset: pos + i64(got))
        }
        got += n
    }
}

/// Closes `f`, abandoning it if the close fails.
CloseQuietly(f: fs.File, io: Io) {
    f.Close(io) else {
        f.Abandon()
    }
}

/// Cuts the log of `d` back to its header and makes it a log of generation
/// `generation`, not shut down cleanly, synced. Writes through a handle of
/// its own: a handle opened to append cannot truncate.
Reset(io: Io, d: &Dir, generation: u64): Error!void {
    var f := try fs.OpenWith(io, d.log, fs.ReadWrite)
    onerr f.Abandon()
    head := Header(generation)
    try f.Truncate(io, i64(HeaderSize))
    _ = try f.Seek(io, 0, fs.Whence.Start)
    try f.WriteAll(io, head[..])
    try f.Sync(io)
    try f.Close(io)
}

/// Writes the clean-shutdown flag of the log at `path`, synced.
MarkClean(io: Io, path: str, clean: bool): Error!void {
    var f := try fs.OpenWith(io, path, fs.ReadWrite)
    onerr f.Abandon()
    flag: [1]u8 = [if clean { 1 } else { 0 }]
    if try f.WriteAt(io, i64(CleanOffset), flag[..]) != 1 {
        fail BadHeader
    }
    try f.Sync(io)
    try f.Close(io)
}

/// Makes a new log of generation `generation` at `path`; it must not exist.
CreateLog(io: Io, path: str, generation: u64): Error!void {
    var f := try fs.OpenWith(io, path, fs.CreateNew)
    onerr f.Abandon()
    head := Header(generation)
    try f.WriteAll(io, head[..])
    try f.Sync(io)
    try f.Close(io)
}

/// Removes what a crash left at `path`, if anything.
RemoveLeftover(io: Io, path: str): fs.Error!void {
    fs.Remove(io, path) else e {
        match e {
            fs.NotFound {}
            _ {
                fail e
            }
        }
    }
}

/// Recovers `s`, which should be empty, from the data directory `d`: loads
/// the snapshot, replays the log after it, drops a torn last record by
/// cutting the file, and marks the log as in use. A missing log is an empty
/// one, made now; damage before the end fails with `Corrupt`. O(snapshot +
/// log); reads the log into `arena`.
export Recover(a: &Alloc into s, arena: &Arena, io: Io, d: &Dir, s: mut store.Store): Error!Recovered {
    try RemoveLeftover(io, d.snap_tmp)
    try RemoveLeftover(io, d.log_tmp)
    taken := try snap.Load(a, arena, io, d.snap, s)
    base := if info := taken { info.generation } else { 0 }
    var f := fs.OpenWith(io, d.log, fs.ReadWrite) else e {
        match e {
            fs.NotFound {
                try CreateLog(io, d.log, base)
                return Recovered(generation: base, records: 0, clean: true, end: End.Clean, good_bytes: i64(HeaderSize), snapshot: taken, stale: false)
            }
            _ {
                fail e
            }
        }
    }
    onerr f.Abandon()
    size := try f.Size(io)
    if size < i64(HeaderSize) {
        fail BadHeader
    }
    data := try arena.Slice[u8](Cast[int](size))
    try ReadFull(io, &f, 0, mut data[..])
    head := ReadHeader(data[..]) else {
        fail BadHeader
    }
    if head.generation < base {
        // the snapshot was renamed into place and the crash came before the
        // log was cut: the snapshot already holds these records
        try f.Close(io)
        try Reset(io, d, base)
        return Recovered(generation: base, records: 0, clean: head.clean, end: End.Clean, good_bytes: i64(HeaderSize), snapshot: taken, stale: true)
    }
    got := try Load(a, data[..], s)
    if got.good_bytes < size {
        try f.Truncate(io, got.good_bytes)
    }
    try f.Close(io)
    try MarkClean(io, d.log, false)
    return Recovered(generation: got.generation, records: got.records, clean: got.clean, end: got.end, good_bytes: got.good_bytes, snapshot: taken, stale: false)
}

/// What `Load` found in a log's bytes.
export struct Loaded {
    export generation: u64
    export records: int
    export clean: bool
    export end: End
    export good_bytes: i64
}

/// Applies the records of a whole log's bytes to `s`. A torn last record is
/// dropped; damage before the end fails with `Corrupt`. O(n); allocates
/// what the store holds.
export Load(a: &Alloc into s, data: []u8, s: mut store.Store): BadHeader|Corrupt|OutOfMemory!Loaded {
    head := ReadHeader(data) else {
        fail BadHeader
    }
    var records := Records.New(data)
    var n := 0
    for r := records.Next() {
        try s.Apply(a, r.op, r.key, r.value)
        n += 1
    }
    end := records.End()
    if off := CorruptAt(end) {
        fail Corrupt(offset: off)
    }
    return Loaded(generation: head.generation, records: n, clean: head.clean, end: end, good_bytes: records.Offset())
}

/// The offset of the damage when a scan ended at some.
export CorruptAt(end: End): ?i64 {
    return match end {
        .Corrupt(offset) { some(offset) }
        _ { none }
    }
}

/// Opens the recovered log of `d` to append to it, keeping `lock`, the
/// instance lock, until the log is closed; on failure `lock` stays with the
/// caller. `crash` is the simulator's; pass
/// `NoCrash`.
export Wal.Open(io: Io, d: Dir, lock: fs.File, rec: &Recovered, policy: SyncPolicy, crash: Crash): Error!Wal from d {
    file := try fs.OpenWith(io, d.log, fs.Append)
    return Wal(file: file, lock: lock, dir: d, nbytes: rec.good_bytes, nrecords: rec.records, writes: 0, epoch: rec.generation, crash: crash, broken: false, policy: policy, nunsynced: 0)
}

/// The bytes in the log.
export Wal.Size(&self): i64 {
    return self.nbytes
}

/// The records in the log since its generation began.
export Wal.Records(&self): int {
    return self.nrecords
}

/// How many times the log has been compacted, plus the generation recovered.
export Wal.Generation(&self): u64 {
    return self.epoch
}

/// Takes the simulator's next write, and reports whether it is the crash.
Wal.CrashHere(mut self): bool {
    n := self.writes
    self.writes += 1
    return n == self.crash.at
}

/// Syncs the log when the policy says an append must be.
Wal.SyncByPolicy(mut self, io: Io): Error!void {
    self.nunsynced += 1
    due := match self.policy {
        .Always { true }
        .Every(n) { self.nunsynced >= n }
        .Never { false }
    }
    if due {
        try self.file.Sync(io)
        self.nunsynced = 0
    }
}

/// Appends one change and syncs it as the policy says. After a failed write
/// the log is `Broken` until `Compact`, since a partial record may sit at
/// its end.
export Wal.Append(mut self, io: Io, op: store.Op, key: str, value: str): Error!void {
    if self.broken {
        fail Broken
    }
    start := RecordStart(op, key, value)
    if self.CrashHere() {
        self.broken = true
        if self.crash.torn {
            // what a crash in the middle of the write leaves: the record's
            // start and half its key, synced
            _ = self.file.Write(io, start[..]) else {}
            _ = self.file.Write(io, key[..key.Len() / 2].AsBytes()) else {}
            self.file.Sync(io) else {}
        }
        fail Crashed(change: self.writes - 1)
    }
    self.broken = true
    try self.file.WriteAll(io, start[..])
    try self.file.WriteAll(io, key.AsBytes())
    try self.file.WriteAll(io, value.AsBytes())
    try self.SyncByPolicy(io)
    self.broken = false
    self.nbytes += i64(RecordSize(key, value))
    self.nrecords += 1
}

/// Writes `s` as the snapshot of the next generation, renames it into
/// place, and cuts the log back to its header. On failure before the rename
/// the old snapshot and log stay in use. O(store log store); the snapshot's
/// sorted keys go in `arena`.
export Wal.Compact(mut self, arena: &Arena, io: Io, s: &store.Store): Error!void {
    if self.CrashHere() {
        self.broken = true
        if self.crash.torn {
            // the new snapshot is written but the crash comes before the rename
            _ = try snap.Write(arena, io, self.dir.snap_tmp, self.epoch + 1, s)
        }
        fail Crashed(change: self.writes - 1)
    }
    try self.file.Sync(io)
    _ = try snap.Write(arena, io, self.dir.snap_tmp, self.epoch + 1, s)
    try fs.Rename(io, self.dir.snap_tmp, self.dir.snap)
    self.epoch += 1
    // from here a crash recovers from the new snapshot alone
    self.broken = true
    try Reset(io, &self.dir, self.epoch)
    self.nbytes = i64(HeaderSize)
    self.nrecords = 0
    self.nunsynced = 0
    self.broken = false
}

/// Syncs the log, marks it as shut down cleanly and closes it, then lets
/// go of the instance lock. Everything is closed whatever happens; the
/// result is the first error, if any.
export Wal.Close(self, io: Io): ?Error {
    Wal(file, lock, dir, ..) := self
    var first: ?Error = none
    var f := file
    f.Sync(io) else e {
        first = some(e)
    }
    f.Close(io) else e {
        f.Abandon()
        if first.IsNone() {
            first = some(e)
        }
    }
    if first.IsNone() {
        MarkClean(io, dir.log, true) else e {
            first = some(e)
        }
    }
    CloseQuietly(lock, io)
    return first
}

/// Closes the log without marking it clean, as after a crash, and lets go
/// of the instance lock.
export Wal.Abandon(self) {
    Wal(file, lock, ..) := self
    file.Abandon()
    lock.Abandon()
}

/// Writes `s` as a whole log of generation `generation` at `path`, one
/// record per key, through `tmp` renamed into place: `kvd wal compact`.
/// Returns its size in bytes. O(store); allocates nothing.
export WriteLog(io: Io, path: str, tmp: str, generation: u64, s: &store.Store): Error!i64 {
    var f := try fs.Create(io, tmp)
    onerr f.Abandon()
    head := Header(generation)
    try f.WriteAll(io, head[..])
    var size := i64(HeaderSize)
    var buf: [8192]u8 = [0; 8192]
    var w := f.Writer(io, mut buf[..])
    for (key, value) in s.Entries() {
        start := RecordStart(store.Op.Set, key, value)
        try w.Bytes(start[..])
        try w.Str(key)
        try w.Str(value)
        size += i64(RecordSize(key, value))
    }
    try w.Flush()
    try f.Sync(io)
    try f.Close(io)
    try fs.Rename(io, tmp, path)
    try MarkClean(io, path, true)
    return size
}
