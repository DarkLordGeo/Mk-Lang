// Why: std.fs and std.io need handles, files and paths to behave on
// Windows as they do on Linux: renames that replace, removes, byte-range
// locks, consoles that speak UTF-8, and paths longer than MAX_PATH.
//
// What: the Windows handle and file calls of std.sys: StdHandle,
// HandleKind, IsConsole, ReadSync, ReadOnce, WriteSync, WriteOnce,
// OpenFile, Close, FileSize, SyncFile, Truncate, FileId, LockRange,
// UnlockRange, Rename, Remove, HandleCount and ReOpenSync.
//
// How: paths go to UTF-16 in a stack buffer; a path of LongPath units or
// more takes the \\?\ form of its full path in pages mapped for the call
// (WidePath), so neither MAX_PATH nor the stack buffer limits it. A console
// is read and written as UTF-16 and converted (ConsoleRead), and positioned
// reads and writes on an overlapped handle wait on pooled events.

import std.core
import std.raw

const GenericRead: u32 = 0x8000_0000
const GenericWrite: u32 = 0x4000_0000
const ShareAll: u32 = 7
const CreateAlways: u32 = 2
const OpenExisting: u32 = 3
const OpenAlways: u32 = 4
const CreateNew: u32 = 1
const TruncateExisting: u32 = 5
const FileAppendData: u32 = 4
const FileWriteEa: u32 = 0x10
const FileReadAttributesOpen: u32 = 0x80
const FileWriteAttributesOpen: u32 = 0x100
const Synchronize: u32 = 0x10_0000
const AttributeReadOnlyOpen: u32 = 1
const AttributeNormal: u32 = 0x80
const FlagOverlapped: u32 = 0x4000_0000
const AttributeDirectory: u32 = 0x10
const InvalidAttributes: u32 = 0xFFFF_FFFF
const FlagBackupSemantics: u32 = 0x0200_0000
const FlagOpenReparsePoint: u32 = 0x0020_0000
const AccessDelete: u32 = 0x0001_0000
const FileRenameInfoEx: u32 = 22
const RenameReplacePosix: u32 = 0x1 | 0x2
const MoveFileReplaceExisting: u32 = 1
// MaxIo is the largest single ReadFile or WriteFile request.
const MaxIo: int = 0x4000_0000

/// StdHandle is the handle of standard input (0), output (1) or error (2):
/// GetStdHandle (STD_INPUT_HANDLE is -10).
/// O(1); allocates nothing.
export StdHandle(i: int): uint {
    unsafe "a kernel32 call that passes no memory, only values and handles" {
        return GetStdHandle(Trunc[u32](-10 - i))
    }
}

/// HandleKind is what h refers to: 1 a file, 2 a console or other
/// character device, 3 a pipe, 4 a socket, 0 unknown. GetFileType says FILE_TYPE_PIPE for a socket
/// too: a pipe answers GetNamedPipeInfo (anonymous pipes are
/// named pipes underneath) and a socket refuses it, so a socket is told
/// apart with kernel32 alone, without loading ws2_32.dll for every piped
/// standard handle (std.rt asks for the kind of each one at start).
/// O(1) system calls; allocates nothing.
export HandleKind(h: uint): u32 {
    unsafe "a kernel32 call that passes no memory, only values and handles" {
        t := GetFileType(h)
        if t == 1 {
            return 1
        }
        if t == 2 {
            return 2
        }
        if t == 3 {
            if GetNamedPipeInfo(h, raw.Null[u32](), raw.Null[u32](), raw.Null[u32](), raw.Null[u32]()) == 0 {
                return 4
            }
            return 3
        }
        return 0
    }
}

/// IsConsole reports whether h is a console: GetConsoleMode succeeds only on
/// a console handle.
/// O(1) system calls; allocates nothing.
export IsConsole(h: uint): bool {
    unsafe "kernel32; mode is a u32 in this frame" {
        var mode: u32 = 0
        return GetConsoleMode(h, raw.OfMut(mut mode)) != 0
    }
}

// Overlapped is an OVERLAPPED whose offset is pos, for a positioned
// synchronous read or write.
Overlapped(pos: i64): [4]u64 {
    var ov: [4]u64 = [0; 4]
    ov[2] = Trunc[u64](pos)
    return ov
}

/// ReadSync reads at most len(buf) bytes of h into buf, blocking the thread;
/// pos < 0 reads at the stream position. End of input (ERROR_HANDLE_EOF,
/// ERROR_BROKEN_PIPE) is a result of 0. A console reads UTF-16 and returns
/// UTF-8 (ConsoleRead). A positioned read leaves the file pointer where it
/// was and works on an overlapped handle too (PosIo), as pread does on
/// Linux.
/// O(len(buf)): one read call, made again only after a console read that a
/// stray cancel ended with no unit; allocates nothing.
export ReadSync(h: uint, buf: mut []u8, pos: i64): OsResult {
    for {
        r := ReadOnce(h, buf, pos)
        // A console read that a stray CancelSynchronousIo (or, as Rust reads
        // it, a Ctrl+C) ended with no unit is repeated, as Linux ReadSync
        // repeats EINTR; only ReadOnce reports it (ConsoleRead).
        if r.err != core.SysErr.Canceled || pos >= 0 || !IsConsole(h) {
            return r
        }
    }
}

/// ReadOnce is ReadSync (Windows retries nothing: an interrupted read,
/// CancelSynchronousIo, is ERROR_OPERATION_ABORTED, Canceled), exported in
/// both variants for the pool's interruptible calls.
/// O(1) system calls; allocates nothing.
export ReadOnce(h: uint, buf: mut []u8, pos: i64): OsResult {
    var n := buf.Len()
    if n > MaxIo {
        n = MaxIo
    }
    // GetFileType first: it is cheaper than GetConsoleMode, which a pipe or
    // file read would pay on every call (PosCost).
    if pos < 0 && IsCharDevice(h) && IsConsole(h) {
        return ConsoleRead(h, buf)
    }
    var done: u32 = 0
    var e: u32 = 0
    unsafe "kernel32; buf holds n bytes" {
        if pos >= 0 {
            r := PosIo(h, raw.DataMut(buf), Trunc[u32](n), pos, false)
            e = r.err
            done = r.count
        } else if ReadFile(h, raw.DataMut(buf), Trunc[u32](n), raw.OfMut(mut done), raw.Null[u8]()) == 0 {
            e = GetLastError()
        }
    }
    if e != 0 {
        return if e == ErrorHandleEof || e == ErrorBrokenPipe { Ok(0) } else { ReadFail(h, e) }
    }
    return Ok(i64(done))
}

/// WriteSync writes all of data or fails; value is the bytes written.
/// O(len(data)), in one write call per partial write; allocates nothing.
export WriteSync(h: uint, data: []u8, pos: i64): OsResult {
    var off := 0
    for off < data.Len() {
        p := if pos >= 0 { pos + i64(off) } else { pos }
        r := WriteOnce(h, data[off..], p)
        if r.err != core.SysErr.None {
            return r
        }
        if r.value == 0 {
            return FailWith(core.SysErr.Other)
        }
        off += Trunc[int](r.value)
    }
    return Ok(i64(off))
}

/// WriteOnce is one WriteFile: value is the bytes written, which may be
/// fewer than data holds. Positioned writes keep the file pointer (PosIo).
/// O(1) system calls; allocates nothing.
export WriteOnce(h: uint, data: []u8, pos: i64): OsResult {
    var n := data.Len()
    if n > MaxIo {
        n = MaxIo
    }
    var done: u32 = 0
    var e: u32 = 0
    unsafe "kernel32; data holds n bytes" {
        if pos >= 0 {
            r := PosIo(h, raw.Data(data), Trunc[u32](n), pos, true)
            e = r.err
            done = r.count
        } else if WriteFile(h, raw.Data(data), Trunc[u32](n), raw.OfMut(mut done), raw.Null[u8]()) == 0 {
            e = GetLastError()
        }
    }
    return if e != 0 { Fail(e) } else { Ok(i64(done)) }
}

// PosIo reads (write false) or writes n bytes at p at offset pos; it
// returns 0 or the Win32 error, and the count. It gives one contract on
// every handle, that of pread and pwrite:
//
// - The file pointer is kept: a synchronous handle's ReadFile with an
//   OVERLAPPED moves it to pos + done, so it is read first
//   (SetFilePointerEx, FILE_CURRENT) and put back after, as Go's
//   internal/poll FD.Pread does. A handle without a pointer (a pipe) fails
//   the read of it and is left alone.
// - An overlapped handle works: the OVERLAPPED carries an event whose low
//   bit is set, so a completion port the handle is attached to
//   gets no packet for it (the OVERLAPPED is on this stack and dies on
//   return), and ERROR_IO_PENDING waits with GetOverlappedResult(TRUE) on
//   that event, as Go's internal/poll does for synchronous calls on
//   overlapped handles.
//
// Cost: three system calls where a plain ReadFile is one (the event comes
// from a pool, EvTake; measured by PosCost); only pos >= 0 pays it.
PosIo(h: uint, p: *u8, n: u32, pos: i64, write: bool): (err: u32, count: u32) {
    unsafe "kernel32; p holds n bytes" {
        var cur: i64 = 0
        saved := SetFilePointerEx(h, 0, raw.OfMut(mut cur), 1) != 0
        t := EvTake()
        ev := t.ev
        if ev == 0 {
            return (err: GetLastError(), count: 0)
        }
        var done: u32 = 0
        var ov := Overlapped(pos)
        ov[3] = u64(ev) | 1
        ovp := raw.PtrCast[u64, u8](raw.DataMut(mut ov[..]))
        var ok: i32 = 0
        if write {
            ok = WriteFile(h, p, n, raw.OfMut(mut done), ovp)
        } else {
            ok = ReadFile(h, p, n, raw.OfMut(mut done), ovp)
        }
        var e: u32 = 0
        if ok == 0 {
            e = GetLastError()
            if e == WsaIoPending {
                e = 0
                if GetOverlappedResult(h, ovp, raw.OfMut(mut done), 1) == 0 {
                    e = GetLastError()
                }
            }
        } else {
            // The count from the OVERLAPPED's status block (no system call:
            // the operation is complete), which is right for overlapped
            // handles too, where the count argument may not be.
            _ = GetOverlappedResult(h, ovp, raw.OfMut(mut done), 0)
        }
        if saved {
            _ = SetFilePointerEx(h, cur, raw.Null[i64](), 0)
        }
        EvGive(t.slot, ev)
        return (err: e, count: done)
    }
}

// IsDir reports whether the UTF-16 path names a directory.
IsDir(w: *u16): bool {
    unsafe "kernel32; w is the caller's NUL-terminated UTF-16 path" {
        a := GetFileAttributesW(w)
        return a != InvalidAttributes && (a & AttributeDirectory) != 0
    }
}

/// OpenFile opens path; value is the handle. mode 0 reads, 1 creates or
/// truncates for read-write, 2 opens or creates for read-write without
/// truncating (OPEN_ALWAYS, for db.sqlite); async opens for overlapped I/O.
/// A directory opens for reading
/// (FILE_FLAG_BACKUP_SEMANTICS, as Go and Rust open it) and its reads are
/// IsDirectory, the Linux behaviour; creating over one is IsDirectory.
/// O(1) system calls; the path converts to UTF-16 in a stack buffer, or,
/// from 248 units on, in pages mapped for the call and given back.
export OpenFile(path: str, mode: u32, async: bool): OsResult {
    var small: [1024]u16 = [0; 1024]
    w := WidePath(path, mut small[..])
    if w.err != core.SysErr.None {
        return FailWith(w.err)
    }
    var access := GenericRead
    var disposition := OpenExisting
    var flags := AttributeNormal | FlagBackupSemantics
    if mode == 1 {
        access = GenericRead | GenericWrite
        disposition = CreateAlways
        flags = AttributeNormal
    } else if mode == 2 {
        access = GenericRead | GenericWrite
        disposition = OpenAlways
        flags = AttributeNormal
    } else if (mode & core.IoOpenOptions) != 0 {
        o := OpenOptions(mode)
        access = o.access
        disposition = o.disposition
        flags = o.flags
    }
    if async {
        flags = flags | FlagOverlapped
    }
    var r := Ok(0)
    unsafe "kernel32; w.p is NUL-terminated" {
        h := CreateFileW(w.p, access, ShareAll, raw.Null[u8](), disposition, flags, 0)
        if h == InvalidHandle {
            e := GetLastError()
            if e == 5 && IsDir(w.p) {
                r = OsResult(value: 0, err: core.SysErr.IsDirectory, os: i64(e))
            } else {
                r = Fail(e)
            }
        } else {
            r = Ok(Trunc[i64](h))
        }
    }
    FreePath(w)
    return r
}

// OpenOptions is CreateFileW's access, disposition and flags for the open
// entry's options form (core.IoOpenOptions), as Go's syscall.Open maps
// O_* flags: append is FILE_APPEND_DATA without FILE_WRITE_DATA, so every
// write goes to the end (std.task keeps such a handle off the port); a
// created file is read-only when the owner's write bit is clear.
OpenOptions(mode: u32): (access: u32, disposition: u32, flags: u32) {
    var access: u32 = 0
    if (mode & core.IoOpenRead) != 0 {
        access = access | GenericRead
    }
    if (mode & core.IoOpenWrite) != 0 {
        access = access | GenericWrite
    }
    if (mode & core.IoOpenAppend) != 0 {
        access = (access & ~GenericWrite) | FileAppendData | FileWriteAttributesOpen | Synchronize
        if (mode & core.IoOpenWrite) != 0 {
            access = access | FileWriteEa | FileReadAttributesOpen
        }
    }
    create := (mode & core.IoOpenCreate) != 0
    trunc := (mode & core.IoOpenTruncate) != 0
    disposition := if create && (mode & core.IoOpenExclusive) != 0 {
        CreateNew
    } else if create && trunc {
        CreateAlways
    } else if create {
        OpenAlways
    } else if trunc {
        TruncateExisting
    } else {
        OpenExisting
    }
    var flags := AttributeNormal
    if create && ((mode >> core.IoOpenPermShift) & 0o200) == 0 {
        flags = AttributeReadOnlyOpen
    }
    if (mode & (core.IoOpenWrite | core.IoOpenAppend | core.IoOpenTruncate)) == 0 {
        flags = flags | FlagBackupSemantics
    }
    return (access: access, disposition: disposition, flags: flags)
}

/// Close is std.sys's CloseHandle: kernel32's extern of that name is in this
/// module.
/// O(1): one system call; allocates nothing.
export Close(h: uint): OsResult {
    unsafe "a kernel32 call that passes no memory, only values and handles" {
        if CloseHandle(h) == 0 {
            return LastError()
        }
    }
    return Ok(0)
}

/// FileSize is the end-of-file position, from FILE_STANDARD_INFO (the call
/// GetFileSizeEx makes), whose Directory flag makes a directory IsDirectory
/// in the same call, as on Linux (one answer for directories).
/// O(1): one system call; allocates nothing.
export FileSize(h: uint): OsResult {
    var info: [3]u64 = [0; 3]
    unsafe "kernel32; info is a FILE_STANDARD_INFO (24 bytes): EndOfFile at 8, Directory at 21" {
        b := raw.PtrCast[u64, u8](raw.DataMut(mut info[..]))
        if GetFileInformationByHandleEx(h, 1, b, 24) == 0 {
            return LastError()
        }
        if raw.Read(raw.Offset(b, 21)) != 0 {
            return OsResult(value: 0, err: core.SysErr.IsDirectory, os: 0)
        }
        return Ok(Trunc[i64](Get64(b, 8)))
    }
}

/// SyncFile flushes h's written data and metadata to the device.
/// O(1) system calls besides the device's flush; allocates nothing.
export SyncFile(h: uint): OsResult {
    unsafe "a kernel32 call that passes no memory, only values and handles" {
        if FlushFileBuffers(h) == 0 {
            return LastError()
        }
    }
    return Ok(0)
}

/// Truncate sets the file's size to size (for db.sqlite):
/// SetFileInformationByHandle with FILE_END_OF_FILE_INFO, as Go's
/// os.File.Truncate and Rust's set_len do; a longer size extends the file
/// with zeros. The file pointer does not move. A negative size is Other
/// with os 0 and no call.
/// O(1) system calls; allocates nothing.
export Truncate(h: uint, size: i64): OsResult {
    if size < 0 {
        return OsResult(value: 0, err: core.SysErr.Other, os: 0)
    }
    var eof := size
    unsafe "kernel32; eof is a FILE_END_OF_FILE_INFO (one LARGE_INTEGER)" {
        if SetFileInformationByHandle(h, 6, raw.PtrCast[i64, u8](raw.OfMut(mut eof)), 8) == 0 {
            return LastError()
        }
    }
    return Ok(0)
}

/// FileId is the identity of the open file (for db.sqlite): out[0]
/// the volume serial number and out[1] the 64-bit file index, from
/// GetFileInformationByHandle (BY_HANDLE_FILE_INFORMATION, 52 bytes:
/// dwVolumeSerialNumber at 28, nFileIndexHigh at 44, nFileIndexLow at 48).
/// Two handles of one file give one pair, whatever path opened them, as
/// fstat's st_dev and st_ino do on Linux. (ReFS has 128-bit file ids; the
/// 64-bit index is what Go's os.SameFile compares.)
/// O(1): one system call; allocates nothing.
export FileId(h: uint, out: mut [2]u64): OsResult {
    var info: [7]u64 = [0; 7]
    unsafe "kernel32; info holds a BY_HANDLE_FILE_INFORMATION (52 of its 56 bytes)" {
        b := raw.PtrCast[u64, u8](raw.DataMut(mut info[..]))
        if GetFileInformationByHandle(h, b) == 0 {
            return LastError()
        }
        out[0] = u64(Get32(b, 28))
        out[1] = (u64(Get32(b, 44)) << 32) | u64(Get32(b, 48))
    }
    return Ok(0)
}

// LockFailImmediately and LockExclusive are LockFileEx's flags.
const LockFailImmediately: u32 = 1
const LockExclusive: u32 = 2

// LockLen splits a byte count for LockFileEx and UnlockFileEx; 0 is the
// whole range from off on (all bits set), POSIX's l_len 0. The callers
// refused a negative n.
LockLen(n: i64): (low: u32, high: u32) {
    if n == 0 {
        return (low: 0xFFFF_FFFF, high: 0xFFFF_FFFF)
    }
    u := Trunc[u64](n)
    return (low: Trunc[u32](u), high: Trunc[u32](u >> 32))
}

/// LockRange takes a byte-range lock on bytes off to off + n of the file
/// (n 0: to the end and beyond), shared or exclusive (for db.sqlite):
/// LockFileEx. The lock belongs to the open handle, so two handles of
/// one process exclude each other too, and it is released by UnlockRange or
/// the handle's close. value is 1 when the lock is held, 0 when another
/// holder has a conflicting lock and wait is false (ERROR_LOCK_VIOLATION;
/// one answer on both targets); a negative off or n is Other with os 0 and
/// no call, as on Linux; any other failure is the error. With wait
/// true the call blocks the thread until the lock is free: std code below
/// the scheduler passes false and retries.
/// Not an OFD lock: a handle's new lock on bytes it already
/// locks is judged against its own lock. Shared on shared stacks (one
/// UnlockRange each), and anything on an exclusive range, or exclusive on
/// a shared one, conflicts: value 0, and with wait true the thread waits on
/// itself forever. Linux converts or merges in place instead. So a caller
/// that changes a range's kind unlocks it first and locks again (sqlite's
/// os_win.c does), and never locks a range it holds; under that rule both
/// targets give the same answers.
/// Windows locks are mandatory: while held, another handle's read or write
/// of those bytes fails, so sqlite locks bytes past the end of its data.
/// For a synchronous handle (sys.OpenFile with async false).
/// O(1) system calls besides the wait when wait is true; allocates nothing.
export LockRange(h: uint, off: i64, n: i64, exclusive: bool, wait: bool): OsResult {
    if off < 0 || n < 0 {
        return OsResult(value: 0, err: core.SysErr.Other, os: 0)
    }
    kind: u32 = if exclusive { LockExclusive } else { 0 }
    flags: u32 = if wait { kind } else { kind | LockFailImmediately }
    l := LockLen(n)
    var ov := Overlapped(off)
    unsafe "kernel32; ov is an OVERLAPPED whose offset is off" {
        if LockFileEx(h, flags, 0, l.low, l.high, raw.PtrCast[u64, u8](raw.DataMut(mut ov[..]))) == 0 {
            e := GetLastError()
            return if e == 33 { Ok(0) } else { Fail(e) }
        }
    }
    return Ok(1)
}

/// UnlockRange releases the lock LockRange took on exactly that range
/// (UnlockFileEx; Windows unlocks only a range that was locked whole, one
/// lock per call). A negative off or n is Other with os 0 and no
/// call, as on Linux.
/// O(1): one system call; allocates nothing.
export UnlockRange(h: uint, off: i64, n: i64): OsResult {
    if off < 0 || n < 0 {
        return OsResult(value: 0, err: core.SysErr.Other, os: 0)
    }
    l := LockLen(n)
    var ov := Overlapped(off)
    unsafe "kernel32; ov is an OVERLAPPED whose offset is off" {
        if UnlockFileEx(h, 0, l.low, l.high, raw.PtrCast[u64, u8](raw.DataMut(mut ov[..]))) == 0 {
            return LastError()
        }
    }
    return Ok(0)
}

/// Rename replaces dst: src is opened for DELETE (a directory or
/// a link itself, not its target) and renamed with FileRenameInfoEx and
/// POSIX semantics, so a dst that is open elsewhere is replaced as on Linux
/// (its open handles keep the old file); a volume that refuses the class
/// (ERROR_INVALID_PARAMETER, ERROR_INVALID_FUNCTION, ERROR_NOT_SUPPORTED:
/// FAT, older Windows) gets MoveFileExW(MOVEFILE_REPLACE_EXISTING), as Rust's
/// std::fs::rename does.
/// O(1) system calls; the path converts to UTF-16 in a stack buffer, or,
/// from 248 units on, in pages mapped for the call and given back.
export Rename(src: str, dst: str): OsResult {
    var sa: [1024]u16 = [0; 1024]
    var sb: [1024]u16 = [0; 1024]
    a := WidePath(src, mut sa[..])
    if a.err != core.SysErr.None {
        return FailWith(a.err)
    }
    b := WidePath(dst, mut sb[..])
    if b.err != core.SysErr.None {
        FreePath(a)
        return FailWith(b.err)
    }
    r := RenamePaths(a.p, b.p)
    FreePath(a)
    FreePath(b)
    return r
}

// RenamePaths renames the NUL-terminated UTF-16 path a to b.
RenamePaths(a: *u16, b: *u16): OsResult {
    var units := 0
    unsafe "b is NUL-terminated" {
        for raw.Read(raw.Offset(b, units)) != 0 {
            units += 1
        }
    }
    // FILE_RENAME_INFO: flags at 0, RootDirectory at 8, FileNameLength (in
    // bytes, without the NUL) at 16, the name at 20, then the NUL.
    size := 20 + (units + 1) * 2
    var stack: [520]u64 = [0; 520]
    var info := raw.Null[u8]()
    var map := raw.Null[u8]()
    var mbytes := 0
    unsafe "kernel32; info holds size bytes: the stack buffer, or pages mapped here" {
        if size <= 520 * 8 {
            info = raw.PtrCast[u64, u8](raw.DataMut(mut stack[..]))
        } else {
            mbytes = PageRound(size)
            map = Reserve(mbytes, 4096)
            if raw.IsNull(map) || !Commit(map, mbytes) {
                FreeMap(map, mbytes)
                return FailWith(core.SysErr.Other)
            }
            info = map
        }
        h := CreateFileW(a, AccessDelete, ShareAll, raw.Null[u8](), OpenExisting,
            FlagBackupSemantics | FlagOpenReparsePoint, 0)
        if h == InvalidHandle {
            e := GetLastError()
            FreeMap(map, mbytes)
            return Fail(e)
        }
        Put32(info, 0, RenameReplacePosix)
        Put64(info, 8, 0)
        Put32(info, 16, Trunc[u32](units * 2))
        raw.Copy(raw.Offset(info, 20), raw.PtrCast[u16, u8](b), (units + 1) * 2)
        ok := SetFileInformationByHandle(h, FileRenameInfoEx, info, Trunc[u32](size))
        e := GetLastError()
        _ = CloseHandle(h)
        FreeMap(map, mbytes)
        if ok != 0 {
            return Ok(0)
        }
        if e != 87 && e != 1 && e != 50 {
            return RenameFail(e, b)
        }
        if MoveFileExW(a, b, MoveFileReplaceExisting) == 0 {
            return RenameFail(GetLastError(), b)
        }
        return Ok(0)
    }
}

// RenameFail is Fail for a rename to b: a destination that is a directory
// answers ERROR_ACCESS_DENIED, which is IsDirectory, as Linux's EISDIR,
// so both targets give one answer (RenameDir checks it). One attribute
// read, on this error path only.
RenameFail(e: u32, b: *u16): OsResult {
    if e == 5 && IsDir(b) {
        return OsResult(value: 0, err: core.SysErr.IsDirectory, os: i64(e))
    }
    return Fail(e)
}

/// Remove deletes a file, or an empty directory.
/// O(1) system calls; the path converts to UTF-16 in a stack buffer, or,
/// from 248 units on, in pages mapped for the call and given back.
export Remove(p: str): OsResult {
    var small: [1024]u16 = [0; 1024]
    w := WidePath(p, mut small[..])
    if w.err != core.SysErr.None {
        return FailWith(w.err)
    }
    var r := Ok(0)
    unsafe "kernel32; w.p is NUL-terminated" {
        if DeleteFileW(w.p) == 0 {
            e := GetLastError()
            if e == 5 && IsDir(w.p) {
                if RemoveDirectoryW(w.p) == 0 {
                    r = LastError()
                }
            } else {
                r = Fail(e)
            }
        }
    }
    FreePath(w)
    return r
}

/// HandleCount is how many handles the process has open
/// (GetProcessHandleCount of the current process); -1 when Windows does not
/// say. For leak tests. The count includes handles Windows opens for its own
/// use (a first Winsock call opens several), so a test compares counts taken
/// after a warm-up.
/// O(1): one system call; allocates nothing.
export HandleCount(): int {
    var n: u32 = 0
    unsafe "kernel32; the pseudo-handle -1 is the current process" {
        if GetProcessHandleCount(InvalidHandle, raw.OfMut(mut n)) == 0 {
            return -1
        }
    }
    return int(n)
}

// WPath is a path in the form Win32 file calls take: p, NUL-terminated
// UTF-16, in the caller's small buffer or in pages mapped for it (map and
// bytes, given back by FreePath). err is None, or why there is no p.
struct WPath {
    p: *u16
    map: *u8
    bytes: int
    err: core.SysErr
}

// LongPath is where the \\?\ form starts: CreateDirectoryW's limit
// (MAX_PATH - 12), the bound Go's fixLongPath and Rust's maybe_verbatim use.
const LongPath: int = 248
// MaxUnits is the longest path Windows takes, in UTF-16 units.
const MaxUnits: int = 32_767

// WidePath converts path for a Win32 file call. Up to LongPath - 1 units,
// and resolving to no more (ShortFull: a relative path under a deep
// current directory does not), it is the plain UTF-16 in small. A longer
// path becomes the \\?\ form of its
// full path (GetFullPathNameW resolves . and .. and the current directory
// first, which \\?\ would not), \\?\UNC\ for \\server\share, so the
// MAX_PATH limit does not apply; a path already in \\?\ or \\.\ form is
// kept. Pages are mapped only for such paths: the full path's buffer, and
// the UTF-16 source when it does not fit small. A NUL byte, bytes that are
// not WTF-8 (Wide) or more than MaxUnits units is InvalidPath; a
// mapping that fails is Other.
WidePath(path: str, small: mut []u16): WPath {
    var r := WPath(p: raw.Null[u16](), map: raw.Null[u8](), bytes: 0, err: core.SysErr.InvalidPath)
    b := path.AsBytes()
    for x in b {
        if x == 0 {
            return r
        }
    }
    if b.Len() >= MaxUnits {
        return r
    }
    n := Wide(path, small)
    if n == WideBad {
        return r
    }
    if n > 0 && n - 1 < LongPath && ShortFull(small[..]) {
        r.p = raw.DataMut(small)
        r.err = core.SysErr.None
        return r
    }
    // The UTF-16 source: small, or pages (one unit per byte at most).
    var src := raw.Null[u16]()
    var smap := raw.Null[u8]()
    var sbytes := 0
    unsafe "small, or the pages mapped here, hold the source" {
        if n > 0 {
            src = raw.DataMut(small)
        } else {
            sbytes = PageRound((b.Len() + 1) * 2)
            smap = Reserve(sbytes, 4096)
            if raw.IsNull(smap) || !Commit(smap, sbytes) {
                FreeMap(smap, sbytes)
                r.err = core.SysErr.Other
                return r
            }
            src = raw.PtrCast[u8, u16](smap)
            // Checked: the bytes are WTF-8 (above) with no NUL, and the
            // pages hold a unit per byte and the NUL.
            if Wide(path, raw.SliceMut(src, sbytes / 2)) <= 0 {
                FreeMap(smap, sbytes)
                return r
            }
        }
        if Verbatim(src) {
            r.p = src
            r.map = smap
            r.bytes = sbytes
            r.err = core.SysErr.None
            return r
        }
        need := GetFullPathNameW(src, 0, raw.Null[u16](), raw.Null[u8]())
        if need == 0 || int(need) > MaxUnits {
            e := GetLastError()
            FreeMap(smap, sbytes)
            return if need == 0 { FailPath(e) } else { r }
        }
        // Room for the 7-unit \?\UNC prefix before the full path.
        obytes := PageRound((int(need) + 8) * 2)
        omap := Reserve(obytes, 4096)
        if raw.IsNull(omap) || !Commit(omap, obytes) {
            FreeMap(omap, obytes)
            FreeMap(smap, sbytes)
            r.err = core.SysErr.Other
            return r
        }
        full := raw.Offset(raw.PtrCast[u8, u16](omap), 8)
        got := GetFullPathNameW(src, need, full, raw.Null[u8]())
        e := GetLastError()
        FreeMap(smap, sbytes)
        if got == 0 || got >= need {
            FreeMap(omap, obytes)
            return FailPath(e)
        }
        var start := raw.Offset(full, -4)
        if raw.Read(full) == 92 && raw.Read(raw.Offset(full, 1)) == 92 {
            if Verbatim(full) {
                start = full
            } else {
                // \\server\share: \\?\UNC replaces the first backslash.
                start = raw.Offset(full, -6)
                PutUnits(start, "\\\\?\\UNC")
            }
        } else {
            PutUnits(start, "\\\\?\\")
        }
        r.p = start
        r.map = omap
        r.bytes = obytes
        r.err = core.SysErr.None
        return r
    }
}

// ShortFull: the plain form of w, a NUL-terminated path of fewer than
// LongPath units, also resolves to fewer than LongPath units, so Win32
// takes it as it is. An absolute path (a drive letter, a colon and a
// separator) or a UNC or device path (two separators) is short as it
// stands; any other path is relative to the current directory, of the
// process or of a drive, and is measured by GetFullPathNameW with no
// buffer, which writes nothing (Rust's get_long_path makes the same split;
// Go's fixLongPath does not measure a relative path, so a short relative
// path under a deep current directory fails there). O(len) in user mode,
// no system call; reads w only up to its NUL.
ShortFull(w: []u16): bool {
    if w.Len() >= 3 {
        a := w[0]
        b := w[1]
        if (a == 92 || a == 47) && (b == 92 || b == 47) {
            return true
        }
        if a != 92 && a != 47 && a != 0 && b == 58 && (w[2] == 92 || w[2] == 47) {
            return true
        }
    }
    unsafe "kernel32; w is NUL-terminated (WidePath's Wide), and a zero length writes nothing" {
        need := GetFullPathNameW(raw.Data(w), 0, raw.Null[u16](), raw.Null[u8]())
        // need counts the NUL; 0 is a failure, which the long form reports.
        return need != 0 && int(need) - 1 < LongPath
    }
}

// FailPath is a WPath with no p for the Win32 error e.
FailPath(e: u32): WPath {
    return WPath(p: raw.Null[u16](), map: raw.Null[u8](), bytes: 0, err: MapError(e))
}

// Verbatim: p starts with \\?\ or \\.\ (a form Windows passes through).
Verbatim(p: *u16): bool {
    unsafe "p is NUL-terminated: each unit is read only after the one before it is not NUL" {
        if raw.Read(p) != 92 || raw.Read(raw.Offset(p, 1)) != 92 {
            return false
        }
        c := raw.Read(raw.Offset(p, 2))
        return (c == 63 || c == 46) && raw.Read(raw.Offset(p, 3)) == 92
    }
}

// PutUnits writes the ASCII text s as UTF-16 units at p.
PutUnits(p: *u16, s: str) {
    for i, x in s.AsBytes() {
        unsafe "p has room for s, checked by the caller" {
            raw.Write(raw.Offset(p, i), u16(x))
        }
    }
}

PageRound(n: int): int {
    return (n + 4095) / 4096 * 4096
}

// FreeMap gives back a mapping WidePath made (null: none).
FreeMap(p: *u8, bytes: int) {
    if !raw.IsNull(p) {
        Release(p, bytes)
    }
}

FreePath(w: WPath) {
    FreeMap(w.map, w.bytes)
}

// IsDirHandle: h is open on a directory (FileBasicInfo's attributes).
IsDirHandle(h: uint): bool {
    var info: [5]u64 = [0; 5]
    unsafe "kernel32; info is a FILE_BASIC_INFO (40 bytes)" {
        b := raw.PtrCast[u64, u8](raw.DataMut(mut info[..]))
        if GetFileInformationByHandleEx(h, 0, b, 40) == 0 {
            return false
        }
        return (Get32(b, 32) & AttributeDirectory) != 0
    }
}

// ReadFail maps a failed read: a directory opened for reading (OpenFile
// allows it, as Linux does) refuses ReadFile with ERROR_INVALID_FUNCTION,
// which is IsDirectory there, as EISDIR is on Linux.
ReadFail(h: uint, e: u32): OsResult {
    if e == 1 && IsDirHandle(h) {
        return OsResult(value: 0, err: core.SysErr.IsDirectory, os: i64(e))
    }
    return Fail(e)
}

// ConIn is the console's read carry: one per process, because a process
// has one console input buffer. pend holds the UTF-8 bytes that did not fit
// the last read's buffer (bits 0 to 55, first byte lowest, their count at
// 56 to 59); hi a high surrogate that ended the last read without its low
// half (0 for none). lock admits one reader at a time (a Cas from 0 to 1,
// released by Store(0): the release/acquire edge that hands pend and hi
// from one reader to the next; both are only read and written under it).
//
// At most 5 bytes are ever pending (ConsoleRead's bound), so 7 fit: a 1-
// or 2-byte read of an unpaired surrogate followed by a 3-byte scalar keeps
// 4 or 5, which a 3-byte store would run into the count's bits (ConRead
// step 4).
struct ConInState {
    lock: raw.Atomic[u32]
    pend: raw.Atomic[u64]
    hi: raw.Atomic[u32]
}

var ConIn: ConInState = ConInState(lock: raw.Atomic[u32].New(0), pend: raw.Atomic[u64].New(0),
    hi: raw.Atomic[u32].New(0))

const ConPendMax: int = 7

// ConUnits bounds one ReadConsoleW (2 KiB of stack).
const ConUnits: int = 1024

// ConsoleRead reads a console as UTF-8: ReadFile on a console returns bytes
// in the input code page (GetConsoleCP, OEM 437 or 850 by default), so its
// non-ASCII input would not be UTF-8. This reads UTF-16 with
// ReadConsoleW and converts it, as Rust's std::sys::windows::stdio and
// Go's os.File on a console do:
//
// - At most buf.Len() / 3 units per call (a BMP unit is at most 3 bytes),
//   at least 1. One call decodes at most want + 1 units (a kept high
//   surrogate, or the one more unit read after a lone one), so it makes at
//   most 3 * want + 3 bytes: at most 3 more than buf holds when buf.Len()
//   >= 3, at most 5 more for a 1-byte buffer. The rest is kept (ConIn.pend)
//   and returned first, alone, by the next read.
// - A read that ends with success and no unit is ERROR_OPERATION_ABORTED:
//   CancelSynchronousIo (ThreadInterrupt) ends a console read that way
//   (measured on Windows 11: ReadConsoleW returns TRUE, 0
//   units, last error 995), and Rust's stdio.rs retries the same answer as
//   a Ctrl+C (not measured here). It is Canceled, never end of input (a 0
//   would make an interrupted pool read of standard input look like end of
//   input); ReadSync repeats it, as Linux ReadSync repeats EINTR, and the
//   pool repeats it when its job was not cancelled.
// - A high surrogate that ends a read waits for its low half in the next
//   read (when it is the only unit, one more unit is read now); an
//   unpaired surrogate becomes U+FFFD, as in every other UTF-16 decode of
//   std.sys.
// - Ctrl+Z as the first unit of a read is end of input (0), as Go and Rust
//   read it.
// - CancelSynchronousIo ends the wait with ERROR_OPERATION_ABORTED
//   (Canceled), so a pool read of standard input can be interrupted.
ConsoleRead(h: uint, buf: mut []u8): OsResult {
    if buf.Len() == 0 {
        return Ok(0)
    }
    // Edge: the Cas from 0 is the acquire of pend and hi (ConInState); the
    // Store(0) below the release.
    for !(ConIn.lock.Load() == 0 && ConIn.lock.Cas(0, 1)) {
        ThreadYield()
    }
    r := ConsoleReadLocked(h, buf)
    ConIn.lock.Store(0)
    return r
}

ConsoleReadLocked(h: uint, buf: mut []u8): OsResult {
    // Edge: none; pend and hi are read and written only under ConIn.lock, whose
    // Cas and Store order them (ConInState).
    pw := ConIn.pend.Load()
    np := Trunc[int](pw >> 56)
    if np > 0 {
        // Bytes left from the last read come first, alone.
        var n := 0
        for n < np && n < buf.Len() {
            buf[n] = Trunc[u8](pw >> (n * 8))
            n += 1
        }
        rest := (pw & 0xFF_FFFF_FFFF_FFFF) >> (n * 8)
        // Edge: none; under ConIn.lock (ConInState).
        ConIn.pend.Store(rest | (Trunc[u64](np - n) << 56))
        return Ok(i64(n))
    }
    hi := ConIn.hi.Load()
    var units: [ConUnits]u16 = [0; ConUnits]
    var start := 0
    if hi != 0 {
        units[0] = Trunc[u16](hi)
        start = 1
    }
    var want := buf.Len() / 3
    if want < 1 {
        want = 1
    }
    if want > ConUnits - 2 - start {
        want = ConUnits - 2 - start
    }
    var got: u32 = 0
    unsafe "kernel32; units has room for want units after start" {
        // Cleared first, so a 0-unit success is judged by this call's own
        // error, not a stale one.
        SetLastError(0)
        if ReadConsoleW(h, raw.Offset(raw.DataMut(mut units[..]), start), Trunc[u32](want), raw.OfMut(mut got),
            raw.Null[u8]()) == 0 {
            return Fail(GetLastError())
        }
        if got == 0 && GetLastError() == 995 {
            // Interrupted (above): hi stays kept.
            return Fail(995)
        }
    }
    var total := start + int(got)
    if start == 0 && got > 0 && units[0] == 0x1A {
        return Ok(0)
    }
    if total == 1 && units[0] >= 0xD800 && units[0] < 0xDC00 {
        // A lone high surrogate: its low half is the next unit.
        var more: u32 = 0
        unsafe "kernel32; units[1] is free" {
            SetLastError(0)
            ok := ReadConsoleW(h, raw.Offset(raw.DataMut(mut units[..]), 1), 1, raw.OfMut(mut more), raw.Null[u8]())
            if ok == 0 || (more == 0 && GetLastError() == 995) {
                e := GetLastError()
                // Edge: none; under ConIn.lock (ConInState).
                ConIn.hi.Store(u32(units[0]))
                return Fail(e)
            }
        }
        total += int(more)
    }
    var keep: u64 = 0
    if total > 1 && units[total - 1] >= 0xD800 && units[total - 1] < 0xDC00 {
        keep = u64(units[total - 1])
        total -= 1
    }
    // Decode16 reads one unit past a high surrogate: units[total] is 0
    // there (a kept surrogate is overwritten with the sentinel).
    units[total] = 0
    var n := 0
    var pend: u64 = 0
    var npend := 0
    var i := 0
    unsafe "units holds total units and a 0 after them" {
        p := raw.Data(units[..])
        for i < total {
            (c, w) := Decode16(p, i)
            i += w
            var tmp: [4]u8 = [0; 4]
            m := PutScalar(mut tmp[..], 0, c)
            for j in 0..m {
                if n < buf.Len() {
                    buf[n] = tmp[j]
                    n += 1
                } else if npend < ConPendMax {
                    // npend <= 5 by the bound in ConsoleRead's comment; the
                    // test only keeps a broken bound from overwriting the
                    // count.
                    pend = pend | (u64(tmp[j]) << (npend * 8))
                    npend += 1
                }
            }
        }
    }
    // Edge: none; under ConIn.lock, released by ConsoleRead's Store(0) after
    // these.
    ConIn.pend.Store(pend | (Trunc[u64](npend) << 56))
    ConIn.hi.Store(Trunc[u32](keep))
    return Ok(i64(n))
}

// IsCharDevice: GetFileType says FILE_TYPE_CHAR (a console is one).
IsCharDevice(h: uint): bool {
    unsafe "a kernel32 call that passes no memory, only values and handles" {
        return GetFileType(h) == 2
    }
}

// The event pool of PosIo: EvSlots manual-reset events made on first use
// and kept, in a page made on first use (a Cas publishes it; a loser
// releases its own page). Slot i: the busy word at i * 16 (0 free, 1
// taken: the Cas takes it and EvGive's Store(0) hands it on, the
// release/acquire edge of a lock, which also publishes the event handle
// at i * 16 + 8). ReadFile and WriteFile reset the event before they start
// (the OVERLAPPED contract), so a slot's event is reused as is. With every
// slot taken, a temporary event is made and closed.
struct EvState {
    page: raw.Atomic[u64]
}

var Ev: EvState = EvState(page: raw.Atomic[u64].New(0))

const EvSlots: int = 64

EvPage(): *u8 {
    unsafe "the pool page, published by the Cas" {
        // Edge: the winner's Cas publishes the committed page; a Load that
        // reads it sees it.
        p := Ev.page.Load()
        if p != 0 {
            return raw.FromAddr[u8](Trunc[uint](p))
        }
        q := Reserve(4096, 4096)
        if raw.IsNull(q) || !Commit(q, 4096) {
            FreeMap(q, 4096)
            return raw.Null[u8]()
        }
        // Edge: the Cas from 0 publishes this page to every later Load.
        if Ev.page.Cas(0, u64(raw.Addr(q))) {
            return q
        }
        Release(q, 4096)
        return raw.FromAddr[u8](Trunc[uint](Ev.page.Load()))
    }
}

// EvTake is an event for one PosIo: a pool slot (slot >= 0), else a
// temporary one (slot -1); ev 0 when none could be made.
EvTake(): (slot: int, ev: uint) {
    pg := EvPage()
    unsafe "kernel32; the pool page's slots" {
        if !raw.IsNull(pg) {
            for i in 0..EvSlots {
                b := raw.PtrCast[u8, raw.Atomic[u32]](raw.Offset(pg, i * 16))
                // Edge: the Cas gives the slot to one thread (acquire); the
                // Store(0) that frees it is the release.
                if (*b).Load() != 0 || !(*b).Cas(0, 1) {
                    continue
                }
                var e := Trunc[uint](Get64(pg, i * 16 + 8))
                if e == 0 {
                    e = CreateEventW(raw.Null[u8](), 1, 0, raw.Null[u16]())
                    if e == 0 {
                        // Edge: the Store(0) frees the slot (release).
                        (*b).Store(0)
                        break
                    }
                    Put64(pg, i * 16 + 8, u64(e))
                }
                return (slot: i, ev: e)
            }
        }
        return (slot: -1, ev: CreateEventW(raw.Null[u8](), 1, 0, raw.Null[u16]()))
    }
}

// EvGive returns EvTake's event.
EvGive(slot: int, ev: uint) {
    unsafe "kernel32; the slot this thread took" {
        if slot < 0 {
            _ = CloseHandle(ev)
            return
        }
        (*raw.PtrCast[u8, raw.Atomic[u32]](raw.Offset(EvPage(), slot * 16))).Store(0)
    }
}

/// ReOpenSync opens the file h names again as a new file object for
/// synchronous I/O, at position 0, with read access when write is false
/// and write access when it is true, sharing everything as OpenFile does:
/// value the new handle, which the caller closes. std.rt lends it to a
/// child in place of a disk file this process opened for overlapped I/O,
/// since the child's ReadFile and WriteFile pass no OVERLAPPED. Errors:
/// the OS's (ReOpenFile). O(1): one system call; allocates nothing.
export ReOpenSync(h: uint, write: bool): OsResult {
    access := if write { GenericWrite } else { GenericRead }
    unsafe "kernel32; passes only values and a handle" {
        n := ReOpenFile(h, access, ShareAll, 0)
        if n == InvalidHandle {
            return LastError()
        }
        return Ok(Trunc[i64](n))
    }
}
