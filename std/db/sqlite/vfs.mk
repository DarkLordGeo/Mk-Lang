// Why: sqlite is built SQLITE_OS_OTHER, with no OS layer of its own, so
// that its files, locks and randomness go through std.sys rather than a C
// runtime; this file is that OS layer.
//
// What: the "mk" VFS: the sqlite3_vfs callbacks (VOpen, VDelete, VAccess,
// VFullPathname, VRandomness...), the sqlite3_io_methods callbacks (FRead,
// FWrite, FTruncate, FSync, FLock, FUnlock...), the in-process lock table
// (FindInode, DropInode, CleanName) and sqlite3_os_init.
//
// How: sqlite's test_demovfs.c gives the minimal shape, os_unix.c and
// os_win.c the semantics. Every call blocks the carrier (a C call runs on
// the carrier's system stack), through std.sys's synchronous calls.
//
// The tables are Mk structs whose fn fields are C function pointers
// (every non-generic function is a C callback), laid out as C's
// sqlite3_vfs (168 bytes) and sqlite3_io_methods (152 bytes) and cast to
// the binding's types; sqlite3_os_init refuses to register the VFS when
// the sizes differ, so every test that opens a database checks them.
//
// Locks. sqlite's protocol (SHARED, RESERVED, PENDING, EXCLUSIVE) runs
// over a lock table in this process, one record per file name, shared by
// every connection that opens that name: os_unix.c's unixInodeInfo, the
// in-process half of its locking, with the same transitions. So two
// connections of one process on one database exclude each other and see
// Busy, as two processes do under os_unix.c. Another process is not
// excluded: std.sys has no file lock yet, so a database file must be used
// by one process at a time.
//
// Names. os_unix.c keys its table by device and inode; std.sys reports
// neither yet, so the key is the name xFullPathname makes:
// CleanName drops "." components and repeated separators, and on Windows
// spells every separator '\' and compares ASCII letters without case. So
// "s.db", "./s.db" and ".//s.db" are one record. ".." is kept as given,
// since resolving it by text would open another file when the component
// before it is a link; so "d/../s.db" and "s.db" are two records, as are a
// relative and an absolute spelling (no working directory is known here)
// and two links to one file.
//
// What std.sys cannot do yet:
// - No truncation but to zero (FTruncate): a database that shrinks keeps
//   its old bytes past the new end, unread, because sqlite reads a file
//   only up to the page count in its header.
// - The wall clock and sleep are absent, and locks are only this process's.

import c.sqlite3
import std.core
import std.raw
import std.rt
import std.sys

// File is the sqlite3_file subclass: C's pMethods first, null when the
// file is not open (sqlite then skips xClose). name is the name sqlite
// opened it by, which sqlite keeps unchanged until xClose (sqlite3.h,
// xOpen). journal marks a rollback journal or super-journal; dirsync asks
// the next xSync to sync the directory too (os_unix.c's UNIXFILE_DIRSYNC).
struct File {
    methods: *IoMethods
    handle: uint
    inode: *Inode
    name: *u8
    lock: i32
    readonly: bool
    journal: bool
    dirsync: bool
}

// Inode is one record of the lock table: the connections that have the
// name open (refs), how many hold SHARED or more (shared), and the
// strongest lock any of them holds (level). The name's len bytes follow
// the record in the same heap block. The table is a list whose head is
// State.inodes, read and written only under sqlite.mk's process-wide lock,
// which every VFS call runs under.
struct Inode {
    next: *Inode
    refs: i32
    shared: i32
    level: i32
    len: int
}

// IoMethods is sqlite3_io_methods, version 1: no shared memory (so no
// WAL across connections) and no memory map.
struct IoMethods {
    version: i32
    close: fn(*File): i32
    read: fn(*File, *u8, i32, i64): i32
    write: fn(*File, *u8, i32, i64): i32
    truncate: fn(*File, i64): i32
    sync: fn(*File, i32): i32
    file_size: fn(*File, *i64): i32
    lock: fn(*File, i32): i32
    unlock: fn(*File, i32): i32
    check_reserved_lock: fn(*File, *i32): i32
    file_control: fn(*File, i32, *void): i32
    sector_size: fn(*File): i32
    device_characteristics: fn(*File): i32
    shm_map: ?fn(*File): i32
    shm_lock: ?fn(*File): i32
    shm_barrier: ?fn(*File)
    shm_unmap: ?fn(*File): i32
    fetch: ?fn(*File): i32
    unfetch: ?fn(*File): i32
}

// Vfs is sqlite3_vfs, version 2. app_data points at the IoMethods.
struct Vfs {
    version: i32
    sz_os_file: i32
    mx_pathname: i32
    next: *Vfs
    name: *u8
    app_data: *IoMethods
    open: fn(*Vfs, *u8, *File, i32, *i32): i32
    delete: fn(*Vfs, *u8, i32): i32
    access: fn(*Vfs, *u8, i32, *i32): i32
    full_pathname: fn(*Vfs, *u8, i32, *u8): i32
    dl_open: ?fn(*Vfs): *void
    dl_error: ?fn(*Vfs)
    dl_sym: ?fn(*Vfs): *void
    dl_close: ?fn(*Vfs)
    randomness: fn(*Vfs, i32, *u8): i32
    sleep: fn(*Vfs, i32): i32
    current_time: fn(*Vfs, *f64): i32
    get_last_error: fn(*Vfs, i32, *u8): i32
    current_time_int64: fn(*Vfs, *i64): i32
    set_system_call: ?fn(*Vfs): i32
    get_system_call: ?fn(*Vfs): *void
    next_system_call: ?fn(*Vfs): *u8
}

// Result codes this file returns (sqlite3.h).
const Ok: i32 = 0
const ErrorCode: i32 = 1
const CantOpen: i32 = 14
const NotFound: i32 = 12
const IoErrRead: i32 = 266
const IoErrShortRead: i32 = 522
const IoErrWrite: i32 = 778
const IoErrFsync: i32 = 1034
const IoErrTruncate: i32 = 1546
const IoErrFstat: i32 = 1802
const IoErrDirFsync: i32 = 1290
const IoErrDelete: i32 = 2570
const IoErrAccess: i32 = 3338
const BusyCode: i32 = 5
const NoMem: i32 = 7
const MisuseCode: i32 = 21
const Full: i32 = 13
const OpenReadOnly: i32 = 1
const OpenReadWrite: i32 = 2
const OpenCreate: i32 = 4
const OpenMainJournal: i32 = 0x800
const OpenSuperJournal: i32 = 0x4000

// The bytes CleanName and SyncParent look at.
const Slash: u8 = 47
const Backslash: u8 = 92
const Dot: u8 = 46
const Colon: u8 = 58
const Question: u8 = 63

// Lock levels (sqlite3.h SQLITE_LOCK_*).
const NoLock: i32 = 0
const SharedLock: i32 = 1
const ReservedLock: i32 = 2
const PendingLock: i32 = 3
const ExclusiveLock: i32 = 4

// MaxPath is mxPathname.
const MaxPath: i32 = 1024

// CStr views a NUL-terminated name as str; none when it is not UTF-8.
CStr(p: *u8): ?str {
    var n := 0
    unsafe "sqlite passes NUL-terminated names" {
        for raw.Read(raw.Offset(p, n)) != 0 {
            n += 1
        }
        s := str.From(raw.Slice(p, n)) else {
            return none
        }
        return some(s)
    }
}

// Exists reports whether a path names something (sys.StatPath).
Exists(p: str): bool {
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    return sys.StatPath(p, mut st).err == core.SysErr.None
}

// VOpen is xOpen. A null name is a temporary file, which this VFS does not
// make: the package builds sqlite with SQLITE_TEMP_STORE=3, so temporary
// tables and journals live in memory. A journal sqlite asks to create is
// created afresh, truncating any file of that name: sqlite asks only after
// it has found that file not hot (pager_open_journal, under RESERVED), so
// its bytes are dead, and sqlite writes a new header at offset 0 anyway.
// Any other file sqlite asks for read-write opens read-write without
// truncation (sys.OpenFile's mode 2: O_CREAT without O_TRUNC,
// OPEN_ALWAYS), so a database an earlier open wrote is written again,
// or read-only when the OS refuses a write open (Denied), as os_unix.c
// retries;
// a missing one is created only when CREATE is asked, else CantOpen, as
// a missing file asked for read-only is.
VOpen(vfs: *Vfs, name: *u8, f: *File, flags: i32, out: *i32): i32 {
    unsafe "sqlite passes its own vfs, a zeroed file of sz_os_file bytes and out or null" {
        (*f).methods = raw.Null[IoMethods]()
        if raw.IsNull(name) {
            return CantOpen
        }
        p := CStr(name) else {
            return CantOpen
        }
        journal := (flags & (OpenMainJournal | OpenSuperJournal)) != 0
        create := (flags & OpenCreate) != 0
        var mode: u32 = 0
        if journal && create {
            mode = 1
        } else if !create && !Exists(p) {
            return CantOpen
        } else if (flags & OpenReadWrite) != 0 {
            mode = 2
        }
        node := FindInode(name, p.Len())
        if raw.IsNull(node) {
            return NoMem
        }
        var got := flags
        var r := sys.OpenFile(p, mode, false)
        if mode == 2 && r.err == core.SysErr.Denied {
            // A file the OS lets this process only read opens read-only,
            // reported to sqlite as such (os_unix.c's unixOpen retry).
            r = sys.OpenFile(p, 0, false)
            got = (flags & ~(OpenReadWrite | OpenCreate)) | OpenReadOnly
        }
        if r.err != core.SysErr.None {
            DropInode(node)
            return CantOpen
        }
        (*f).handle = Trunc[uint](r.value)
        (*f).inode = node
        (*f).name = name
        (*f).lock = NoLock
        (*f).readonly = (got & OpenReadOnly) != 0
        (*f).journal = journal
        // os_unix.c syncs the directory after it creates a journal, so the
        // new entry survives a power loss; Windows needs no such sync
        // (os_win.c has none), and a directory handle there cannot flush.
        (*f).dirsync = Target.os == core.Os.Linux && journal && create
        (*f).methods = (*vfs).app_data
        if !raw.IsNull(out) {
            raw.Write(out, got)
        }
        return Ok
    }
}

// VDelete is xDelete. A missing file is no error (os_unix.c returns
// SQLITE_IOERR_DELETE_NOENT, which the pager ignores; plain OK is simpler).
// With sync_dir set, the directory is synced after the remove on Linux, as
// os_unix.c's unixDelete does, so a deleted journal stays deleted after a
// power loss (else a committed transaction could come back as hot and be
// rolled back).
VDelete(vfs: *Vfs, name: *u8, sync_dir: i32): i32 {
    p := CStr(name) else {
        return IoErrDelete
    }
    r := sys.Remove(p)
    if r.err != core.SysErr.None && r.err != core.SysErr.NotFound {
        return IoErrDelete
    }
    if (sync_dir & 1) != 0 && Target.os == core.Os.Linux {
        if SyncParent(p) == 2 {
            return IoErrDirFsync
        }
    }
    return Ok
}

// SyncParent fsyncs the directory that holds the file named p, as
// os_unix.c's osOpenDirectory and full_fsync: 0 synced, 1 the directory
// would not open (os_unix.c ignores that), 2 the fsync failed. The
// directory is the text before p's last '/', or "." without one.
SyncParent(p: str): int {
    var i := p.Len() - 1
    for i >= 0 && p.Byte(i) != Slash {
        i -= 1
    }
    var dir := "."
    if i == 0 {
        dir = "/"
    } else if i > 0 {
        dir = p[0..i]
    }
    r := sys.OpenFile(dir, 0, false)
    if r.err != core.SysErr.None {
        return 1
    }
    h := Trunc[uint](r.value)
    s := sys.SyncFile(h)
    _ = sys.Close(h)
    if s.err != core.SysErr.None {
        return 2
    }
    return 0
}

// VAccess is xAccess: EXISTS, READWRITE and READ all answer whether the
// path exists (std.sys reports no permission bits).
VAccess(vfs: *Vfs, name: *u8, flags: i32, out: *i32): i32 {
    unsafe "out is sqlite's" {
        p := CStr(name) else {
            raw.Write(out, 0)
            return Ok
        }
        var v: i32 = 0
        if Exists(p) {
            v = 1
        }
        raw.Write(out, v)
        return Ok
    }
}

// VFullPathname is xFullPathname: the name as CleanName spells it (sys
// resolves a relative one against the working directory), with its NUL.
// sqlite opens the database and names its journal by this result, so every
// spelling CleanName merges finds one lock record and one journal.
VFullPathname(vfs: *Vfs, name: *u8, n: i32, out: *u8): i32 {
    unsafe "name is NUL-terminated and out holds n bytes" {
        var len := 0
        for raw.Read(raw.Offset(name, len)) != 0 {
            len += 1
        }
        if len + 1 > int(n) {
            return CantOpen
        }
        o := CleanName(raw.Slice(name, len), raw.SliceMut(out, len), Target.os == core.Os.Windows)
        raw.Write(raw.Offset(out, o), 0)
    }
    return Ok
}

// IsSep reports whether x separates components: '/', and '\' on Windows.
IsSep(x: u8, win: bool): bool {
    return x == Slash || (win && x == Backslash)
}

// IsLetter reports whether x is an ASCII letter.
IsLetter(x: u8): bool {
    return (x | 0x20) >= 97 && (x | 0x20) <= 122
}

// CleanName writes s to d with "." components and repeated separators
// dropped, and on Windows every separator spelt '\', and returns the
// length written, never more than s.Len() (each byte written stands for a
// byte of s, but for the "." that stands for a name of only "." and
// separators, which has at least two). The root ("/", "\", a drive "C:"
// with or without a separator, a UNC prefix of two separators) and a
// trailing separator are kept; ".." is kept (see the file comment). On
// Windows a name that starts "\\?\" or "\\.\" is copied as it is, since
// Windows does not parse those either.
CleanName(s: []u8, d: mut []u8, win: bool): int {
    n := s.Len()
    if n == 0 {
        return 0
    }
    sep := if win { Backslash } else { Slash }
    if win && n >= 4 && IsSep(s[0], win) && IsSep(s[1], win) && (s[2] == Question || s[2] == Dot) && IsSep(s[3], win) {
        for k in 0..n {
            d[k] = s[k]
        }
        return n
    }
    var i := 0
    var o := 0
    if win && n >= 2 && s[1] == Colon && IsLetter(s[0]) {
        d[0] = s[0]
        d[1] = Colon
        i = 2
        o = 2
    }
    if i < n && IsSep(s[i], win) {
        d[o] = sep
        o += 1
        i += 1
        if win && o == 1 && i < n && IsSep(s[i], win) {
            d[o] = sep
            o += 1
            i += 1
        }
    }
    start := o
    for i < n {
        if IsSep(s[i], win) {
            i += 1
            continue
        }
        var j := i
        for j < n && !IsSep(s[j], win) {
            j += 1
        }
        if j - i != 1 || s[i] != Dot {
            if o > start {
                d[o] = sep
                o += 1
            }
            for i < j {
                d[o] = s[i]
                o += 1
                i += 1
            }
        }
        i = j
    }
    if o > start && IsSep(s[n - 1], win) {
        d[o] = sep
        o += 1
    }
    if o == 0 {
        d[0] = Dot
        o = 1
    }
    return o
}

// SameName compares two cleaned names: byte for byte, and on Windows ASCII
// letters without case (NTFS's default; two names that differ only in the
// case of a letter above ASCII are two records, see the file comment).
SameName(a: []u8, b: []u8, win: bool): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        x := a[i]
        y := b[i]
        if x != y && !(win && IsLetter(x) && (x | 0x20) == (y | 0x20)) {
            return false
        }
    }
    return true
}

// VRandomness is xRandomness: n bytes from the OS source.
VRandomness(vfs: *Vfs, n: i32, out: *u8): i32 {
    unsafe "out holds n bytes" {
        b := raw.SliceMut(out, int(n))
        if !sys.Random(b) {
            raw.Fill(out, 0, int(n))
        }
    }
    return n
}

// VSleep is xSleep: std.sys has no thread sleep yet, so it yields once
// and reports no time slept; a busy handler then gives up at once instead
// of waiting.
VSleep(vfs: *Vfs, micros: i32): i32 {
    sys.ThreadYield()
    return 0
}

// VCurrentTime and VCurrentTimeInt64: std.sys has no wall clock yet, so
// they fail and date('now') is NULL.
VCurrentTime(vfs: *Vfs, out: *f64): i32 {
    return ErrorCode
}

VCurrentTimeInt64(vfs: *Vfs, out: *i64): i32 {
    return ErrorCode
}

VGetLastError(vfs: *Vfs, n: i32, out: *u8): i32 {
    return 0
}

// FClose is xClose: the lock goes (sqlite unlocks first; this covers a
// path that does not), then the table reference, then the handle.
FClose(f: *File): i32 {
    _ = FUnlock(f, NoLock)
    unsafe "f is open" {
        DropInode((*f).inode)
        _ = sys.Close((*f).handle)
        (*f).methods = raw.Null[IoMethods]()
    }
    return Ok
}

// FRead is xRead: n bytes at off, a short read zero-filled and reported
// as SQLITE_IOERR_SHORT_READ, which the pager requires.
FRead(f: *File, buf: *u8, n: i32, off: i64): i32 {
    var got := 0
    want := int(n)
    unsafe "f is open and buf holds n bytes" {
        for got < want {
            r := sys.ReadSync((*f).handle, raw.SliceMut(raw.Offset(buf, got), want - got), off + i64(got))
            if r.err != core.SysErr.None {
                return IoErrRead
            }
            if r.value == 0 {
                raw.Fill(raw.Offset(buf, got), 0, want - got)
                return IoErrShortRead
            }
            got += Trunc[int](r.value)
        }
    }
    return Ok
}

// FWrite is xWrite: all n bytes at off (sys.WriteSync loops over short
// writes and EINTR), SQLITE_FULL on a full disk as os_unix.c and os_win.c
// report it, else SQLITE_IOERR_WRITE.
FWrite(f: *File, buf: *u8, n: i32, off: i64): i32 {
    unsafe "f is open and buf holds n bytes" {
        if (*f).readonly {
            return IoErrWrite
        }
        r := sys.WriteSync((*f).handle, raw.Slice(buf, int(n)), off)
        if r.err == core.SysErr.NoSpace {
            return Full
        }
        if r.err != core.SysErr.None {
            return IoErrWrite
        }
    }
    return Ok
}

// FTruncate is xTruncate. A size at or past the end is done already.
// std.sys has no truncate yet, so:
// - to zero, the file is opened once more create-truncate and closed,
//   which empties it under the open handle (TRUNCATE journal mode's commit,
//   and the rollback of a database's first transaction, whose journal
//   holds no page 1 to restore);
// - a shrink to a size above zero leaves the bytes past it in place and
//   succeeds. For a database that is safe because sqlite reads it only up
//   to the page count in its header (offset 28), which every sqlite since
//   3.7.0 keeps valid, and the header is what a rollback restores (a file
//   last written by an older sqlite, whose header count is not valid,
//   would read the old bytes as pages: the module doc says so). For a
//   journal it is safe because only the records its header counts, salted
//   with that header's nonce, are played back.
// Failing the shrink instead would make a ROLLBACK larger than the page
// cache fail in playback and leave the journal hot and the database
// unopenable.
FTruncate(f: *File, size: i64): i32 {
    unsafe "f is open; its name lives until xClose" {
        r := sys.FileSize((*f).handle)
        if r.err != core.SysErr.None || size < 0 {
            return IoErrTruncate
        }
        if r.value <= size {
            return Ok
        }
        if (*f).readonly {
            return IoErrTruncate
        }
        if size == 0 {
            p := CStr((*f).name) else {
                return IoErrTruncate
            }
            t := sys.OpenFile(p, 1, false)
            if t.err != core.SysErr.None {
                return IoErrTruncate
            }
            _ = sys.Close(Trunc[uint](t.value))
        }
    }
    return Ok
}

// FSync is xSync: FlushFileBuffers or fsync, whatever the flags; then,
// once after a journal was created on Linux, its directory, as os_unix.c's
// unixSync (which ignores a failure of that directory sync).
FSync(f: *File, flags: i32): i32 {
    unsafe "f is open; its name lives until xClose" {
        if (*f).readonly {
            return Ok
        }
        if sys.SyncFile((*f).handle).err != core.SysErr.None {
            return IoErrFsync
        }
        if (*f).dirsync {
            (*f).dirsync = false
            p := CStr((*f).name) else {
                return Ok
            }
            _ = SyncParent(p)
        }
    }
    return Ok
}

// FFileSize is xFileSize.
FFileSize(f: *File, out: *i64): i32 {
    unsafe "f is open and out is sqlite's" {
        r := sys.FileSize((*f).handle)
        if r.err != core.SysErr.None {
            return IoErrFstat
        }
        raw.Write(out, r.value)
    }
    return Ok
}

// InodeName is the name stored after the record.
InodeName(n: *Inode): *u8 {
    unsafe "the name follows the record in its block" {
        return raw.Offset(raw.PtrCast[Inode, u8](n), SizeOf[Inode]())
    }
}

// FindInode returns the lock record of the name (len bytes at p, as
// xFullPathname cleaned it) with one more reference, making it when no
// connection has the name open; null when the heap is out of memory. A
// linear search: a process has few database files open, and the search
// runs once per open.
FindInode(p: *u8, len: int): *Inode {
    unsafe "the table is touched only under the process-wide lock; p holds len bytes" {
        win := Target.os == core.Os.Windows
        var a := State.inodes.Load()
        for a != 0 {
            n := raw.FromAddr[Inode](a)
            if SameName(raw.Slice(InodeName(n), (*n).len), raw.Slice(p, len), win) {
                (*n).refs = (*n).refs + 1
                return n
            }
            a = raw.Addr((*n).next)
        }
        b := rt.HeapAlloc(SizeOf[Inode]() + len, 8)
        if raw.IsNull(b) {
            return raw.Null[Inode]()
        }
        n := raw.PtrCast[u8, Inode](b)
        raw.Write(n, Inode(next: raw.FromAddr[Inode](State.inodes.Load()), refs: 1, shared: 0, level: NoLock, len: len))
        raw.Copy(InodeName(n), p, len)
        State.inodes.Store(raw.Addr(n))
        return n
    }
}

// DropInode gives back one reference; the last one unlinks the record and
// frees its block.
DropInode(n: *Inode) {
    unsafe "the table is touched only under the process-wide lock; n is in it" {
        (*n).refs = (*n).refs - 1
        if (*n).refs > 0 {
            return
        }
        var a := State.inodes.Load()
        var prev := raw.Null[Inode]()
        for a != 0 {
            q := raw.FromAddr[Inode](a)
            if a == raw.Addr(n) {
                if raw.IsNull(prev) {
                    State.inodes.Store(raw.Addr((*q).next))
                } else {
                    (*prev).next = (*q).next
                }
                rt.HeapFree(raw.PtrCast[Inode, u8](q), SizeOf[Inode]() + (*q).len, 8)
                return
            }
            prev = q
            a = raw.Addr((*q).next)
        }
    }
}

// FLock is xLock, os_unix.c's unixLock over the lock table: a connection
// whose lock differs from the record's strongest may take nothing past
// SHARED, and nothing at all while another holds PENDING or more; a new
// SHARED joins the readers; EXCLUSIVE, while other readers remain, takes
// PENDING, so no new reader starts, and fails Busy until it is the only
// reader. sqlite returns Busy to the caller, since no busy handler is set.
FLock(f: *File, level: i32): i32 {
    unsafe "f is open and the table is under the process-wide lock" {
        cur := (*f).lock
        if cur >= level {
            return Ok
        }
        n := (*f).inode
        top := (*n).level
        if cur != top && (top >= PendingLock || level > SharedLock) {
            return BusyCode
        }
        if level == SharedLock {
            (*n).shared = (*n).shared + 1
            (*f).lock = SharedLock
            if top == NoLock {
                (*n).level = SharedLock
            }
            return Ok
        }
        if level == ExclusiveLock && (*n).shared > 1 {
            (*f).lock = PendingLock
            (*n).level = PendingLock
            return BusyCode
        }
        (*f).lock = level
        (*n).level = level
    }
    return Ok
}

// FUnlock is xUnlock, to SHARED or to none (os_unix.c's posixUnlock): from
// above SHARED the record drops to SHARED; the last reader to leave clears
// it.
FUnlock(f: *File, level: i32): i32 {
    unsafe "f is open and the table is under the process-wide lock" {
        cur := (*f).lock
        if cur <= level {
            return Ok
        }
        n := (*f).inode
        if cur > SharedLock {
            (*n).level = SharedLock
        }
        if level == NoLock {
            (*n).shared = (*n).shared - 1
            if (*n).shared == 0 {
                (*n).level = NoLock
            }
        }
        (*f).lock = level
    }
    return Ok
}

// FCheckReservedLock is xCheckReservedLock: whether any connection holds
// RESERVED or more, which tells sqlite a journal is live, not hot.
FCheckReservedLock(f: *File, out: *i32): i32 {
    unsafe "f is open and out is sqlite's; under the process-wide lock" {
        var v: i32 = 0
        if (*(*f).inode).level > SharedLock {
            v = 1
        }
        raw.Write(out, v)
    }
    return Ok
}

// FFileControl knows no opcode.
FFileControl(f: *File, op: i32, arg: *void): i32 {
    return NotFound
}

FSectorSize(f: *File): i32 {
    return 4096
}

FDeviceCharacteristics(f: *File): i32 {
    return 0
}

// The VFS block: the Vfs, then the IoMethods, then the name "mk". It is
// made once by sqlite3_os_init and lives until sqlite3_os_end.
struct VfsState {
    block: raw.Atomic[uint]
    inodes: raw.Atomic[uint]
}

var State: VfsState = VfsState(block: raw.Atomic[uint].New(0), inodes: raw.Atomic[uint].New(0))

// VfsBytes is the block's size: the two tables and an 8-byte name slot.
VfsBytes(): int {
    return SizeOf[Vfs]() + SizeOf[IoMethods]() + 8
}

/// sqlite's OS start-up hook, called by C name from sqlite3_initialize in
/// a build with SQLITE_OS_OTHER: it registers std's file layer, the "mk"
/// VFS, as sqlite's default. It returns SQLITE_NOMEM when the VFS's memory
/// cannot be allocated, and SQLITE_MISUSE when std's tables do not have
/// the sizes sqlite's C headers give them, which is a bug in std. sqlite
/// calls it once, under no mutex (the build has SQLITE_THREADSAFE=0), and
/// std.db.sqlite's process-wide lock keeps it from running twice at
/// once. O(1). Programs do not call it.
export extern sqlite3_os_init(): i32 {
    if State.block.Load() != 0 {
        return Ok
    }
    // The Mk tables must have C's sizes (168 and 152 bytes on 64-bit
    // targets); a mismatch is a layout bug, refused as SQLITE_MISUSE.
    if SizeOf[Vfs]() != SizeOf[sqlite3.Sqlite3Vfs]() || SizeOf[IoMethods]() != SizeOf[sqlite3.Sqlite3IoMethods]() {
        return MisuseCode
    }
    p := rt.HeapAlloc(VfsBytes(), 8)
    unsafe "p is a fresh block of VfsBytes() bytes, 8-aligned" {
        if raw.IsNull(p) {
            return NoMem
        }
        m := raw.PtrCast[u8, IoMethods](raw.Offset(p, SizeOf[Vfs]()))
        name := raw.Offset(p, SizeOf[Vfs]() + SizeOf[IoMethods]())
        raw.Write(name, 109)
        raw.Write(raw.Offset(name, 1), 107)
        raw.Write(raw.Offset(name, 2), 0)
        raw.Write(m, IoMethods(version: 1, close: FClose, read: FRead, write: FWrite, truncate: FTruncate, sync: FSync,
            file_size: FFileSize, lock: FLock, unlock: FUnlock, check_reserved_lock: FCheckReservedLock,
            file_control: FFileControl, sector_size: FSectorSize, device_characteristics: FDeviceCharacteristics,
            shm_map: none, shm_lock: none, shm_barrier: none, shm_unmap: none, fetch: none, unfetch: none))
        v := raw.PtrCast[u8, Vfs](p)
        raw.Write(v, Vfs(version: 2, sz_os_file: Trunc[i32](SizeOf[File]()), mx_pathname: MaxPath, next: raw.Null[Vfs](),
            name: name, app_data: m, open: VOpen, delete: VDelete, access: VAccess, full_pathname: VFullPathname,
            dl_open: none, dl_error: none, dl_sym: none, dl_close: none, randomness: VRandomness, sleep: VSleep,
            current_time: VCurrentTime, get_last_error: VGetLastError, current_time_int64: VCurrentTimeInt64,
            set_system_call: none, get_system_call: none, next_system_call: none))
        State.block.Store(raw.Addr(p))
        return sqlite3.sqlite3_vfs_register(raw.PtrCast[Vfs, sqlite3.Sqlite3Vfs](v), 1)
    }
}

/// sqlite's OS shut-down hook, the pair of sqlite3_os_init. It does
/// nothing: the VFS stays registered and allocated, since std.db.sqlite
/// never calls sqlite3_shutdown, and a later sqlite3_initialize would
/// register it again. O(1). Programs do not call it.
export extern sqlite3_os_end(): i32 {
    return Ok
}
