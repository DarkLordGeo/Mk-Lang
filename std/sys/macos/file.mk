// Why: every file, pipe and standard stream in std ends in a file
// descriptor; this is the layer that opens, reads, writes, locks and closes
// them through libSystem.
//
// What: ReadSync, ReadOnce, the writes, OpenFile, Close, the byte-range
// locks, Remove, IsConsole and ConsoleTestType (Windows' console path,
// absent here), and the helpers CPath, StatFd, IoFail and the stat readers.
//
// How: read, write, pread and pwrite retry EINTR, except ReadOnce and
// WriteOnce, which a pool thread's interrupt may end. Paths are copied
// NUL-terminated into a PATH_MAX stack buffer. struct stat is Darwin's
// 64-bit-inode layout (144 bytes: st_mode at 4, st_ino at 8, st_size at
// 96), from fstat on arm64 and fstat64 on x86_64. Byte-range locks are
// POSIX fcntl locks (Darwin has no open file description locks), which
// belong to the process: closing any descriptor of the file drops them.

import std.core
import std.raw
import std.sys.lc

// Darwin's open flags (<fcntl.h>).
const ORdOnly: i32 = 0
const OWrOnly: i32 = 0x1
const ORdWr: i32 = 0x2
const OAppend: i32 = 0x8
const OCreat: i32 = 0x200
const OTrunc: i32 = 0x400
const OExcl: i32 = 0x800
const OCloExec: i32 = 0x0100_0000
const ODirectory: i32 = 0x0010_0000
const ONoFollow: i32 = 0x100
// MaxIo is the most one read or write moves: Darwin refuses a count over
// INT_MAX with EINVAL.
const MaxIo: int = 0x7FFF_F000
// The byte offsets in struct stat.
const StMode: int = 4
const StIno: int = 8
const StMtime: int = 48
const StSize: int = 96
const StBytes: int = 144

// CPath copies s NUL-terminated into buf; false when s holds a NUL or does
// not fit.
CPath(s: str, buf: mut []u8): bool {
    b := s.AsBytes()
    if b.Len() + 1 > buf.Len() {
        return false
    }
    for i, x in b {
        if x == 0 {
            return false
        }
        buf[i] = x
    }
    buf[b.Len()] = 0
    return true
}

// StatFd fills st (struct stat) for h; the libSystem result, 0 or -1.
StatFd(h: uint, st: mut [18]u64): i32 {
    unsafe "st holds 144 bytes, a struct stat" {
        p := raw.PtrCast[u64, u8](raw.DataMut(mut st[..]))
        return if Target.arch == Arch.Arm64 { fstat(Trunc[i32](h), p) } else { fstat64(Trunc[i32](h), p) }
    }
}

// PathStat is stat (or lstat with nofollow) of the NUL-terminated path c.
PathStat(c: []u8, nofollow: bool, st: mut [18]u64): i32 {
    unsafe "c is NUL-terminated and st holds 144 bytes" {
        path := raw.Data(c)
        p := raw.PtrCast[u64, u8](raw.DataMut(mut st[..]))
        if Target.arch == Arch.Arm64 {
            return if nofollow { lstat(path, p) } else { stat(path, p) }
        }
        return if nofollow { lstat64(path, p) } else { stat64(path, p) }
    }
}

// StatMode is st_mode of a filled stat.
StatMode(st: &[18]u64): u32 {
    return Trunc[u32]((st[0] >> 32) & 0xFFFF)
}

/// StdHandle is the handle of standard input (0), output (1) or error (2):
/// the descriptor i itself.
/// O(1); allocates nothing.
export StdHandle(i: int): uint {
    return Trunc[uint](i)
}

/// HandleKind is what h refers to: 1 a regular file, 2 a tty or other
/// character device, 3 a pipe, 4 a socket, 0 unknown (or fstat failed).
/// O(1) system calls; allocates nothing.
export HandleKind(h: uint): u32 {
    var st: [18]u64 = [0; 18]
    if StatFd(h, mut st) != 0 {
        return 0
    }
    t := StatMode(&st) & 0o170000
    return if t == 0o100000 {
        1
    } else if t == 0o020000 {
        2
    } else if t == 0o010000 {
        3
    } else if t == 0o140000 {
        4
    } else {
        0
    }
}

/// IsConsole is false: macOS writes bytes exactly everywhere, so no handle
/// needs the console path.
/// O(1); allocates nothing.
export IsConsole(h: uint): bool {
    return false
}

/// ConsoleTestType types into the process's console (tests only; the
/// Windows variant's ConsoleRead test): always -1, refused.
/// O(1); allocates nothing.
export ConsoleTestType(units: []u16): int {
    return -1
}

// IoFail maps errno from an operation of kind on handle h: the socket kinds
// through SockFail; a read or write's EPIPE by what h is (Closed on a pipe,
// Reset on a socket); anything else through MapError.
IoFail(h: uint, kind: u32, errno: i64): OsResult {
    if kind == OpAccept || kind == OpRecv || kind == OpSend || kind == OpSendTo || kind == OpRecvFrom {
        return SockFail(errno)
    }
    if errno == 32 && (kind == OpRead || kind == OpWrite) && HandleKind(h) == 4 {
        return OsResult(value: 0, err: core.SysErr.Reset, os: errno)
    }
    return Fail(errno)
}

// ReadCall is one read (pos < 0) or pread of at most n bytes into p.
ReadCall(h: uint, p: *u8, n: int, pos: i64): int {
    unsafe "p holds n bytes of the caller's buffer" {
        return if pos < 0 { read(Trunc[i32](h), p, Trunc[uint](n)) } else { pread(Trunc[i32](h), p, Trunc[uint](n), pos) }
    }
}

// WriteCall is one write (pos < 0) or pwrite of n bytes at p.
WriteCall(h: uint, p: *u8, n: int, pos: i64): int {
    unsafe "p holds n bytes of the caller's data" {
        return if pos < 0 { write(Trunc[i32](h), p, Trunc[uint](n)) } else { pwrite(Trunc[i32](h), p, Trunc[uint](n), pos) }
    }
}

/// ReadSync reads at most len(buf) bytes of h into buf, blocking the thread;
/// pos < 0 reads at the stream position. value is the bytes read, 0 at the
/// end. EINTR is repeated.
/// O(len(buf)): one read call, made again after each EINTR; allocates
/// nothing.
export ReadSync(h: uint, buf: mut []u8, pos: i64): OsResult {
    var n := buf.Len()
    if n > MaxIo {
        n = MaxIo
    }
    for {
        var r := 0
        unsafe "buf holds n bytes" {
            r = ReadCall(h, raw.DataMut(buf), n, pos)
        }
        if r >= 0 {
            return Ok(i64(r))
        }
        e := Errno()
        if e != 4 {
            return IoFail(h, OpRead, e)
        }
    }
}

/// WriteSync writes all of data or fails; value is the bytes written.
/// O(len(data)), in one write call per partial write; allocates nothing.
export WriteSync(h: uint, data: []u8, pos: i64): OsResult {
    var off := 0
    for off < data.Len() {
        var n := data.Len() - off
        if n > MaxIo {
            n = MaxIo
        }
        var r := 0
        unsafe "data holds off + n bytes" {
            r = WriteCall(h, raw.Offset(raw.Data(data), off), n, if pos < 0 { -1 } else { pos + i64(off) })
        }
        if r < 0 {
            e := Errno()
            if e == 4 {
                continue
            }
            return IoFail(h, OpWrite, e)
        }
        if r == 0 {
            return FailWith(core.SysErr.Other)
        }
        off += r
    }
    return Ok(i64(off))
}

/// ReadOnce is ReadSync without the EINTR retry, for a pool thread that
/// ThreadInterrupt may interrupt: one call; an interruption is Canceled
/// with os 4.
/// O(1) system calls; allocates nothing.
export ReadOnce(h: uint, buf: mut []u8, pos: i64): OsResult {
    var n := buf.Len()
    if n > MaxIo {
        n = MaxIo
    }
    var r := 0
    unsafe "buf holds n bytes" {
        r = ReadCall(h, raw.DataMut(buf), n, pos)
    }
    return if r < 0 { IoFail(h, OpRead, Errno()) } else { Ok(i64(r)) }
}

/// WriteOnce is one write call with no EINTR retry: value is the bytes
/// written, which may be fewer than data holds; an interruption before any
/// byte is Canceled with os 4.
/// O(1) system calls; allocates nothing.
export WriteOnce(h: uint, data: []u8, pos: i64): OsResult {
    var n := data.Len()
    if n > MaxIo {
        n = MaxIo
    }
    var r := 0
    unsafe "data holds n bytes" {
        r = WriteCall(h, raw.Data(data), n, pos)
    }
    return if r < 0 { IoFail(h, OpWrite, Errno()) } else { Ok(i64(r)) }
}

// OpenFlags is open(2)'s flags for an open entry's mode (core.IoOpenOptions
// for the options form), close-on-exec always; OpenPerm the mode bits a
// file it creates gets (less the umask).
OpenFlags(mode: u32): i32 {
    if (mode & core.IoOpenOptions) == 0 {
        return if mode == 1 {
            ORdWr | OCreat | OTrunc | OCloExec
        } else if mode == 2 {
            ORdWr | OCreat | OCloExec
        } else {
            ORdOnly | OCloExec
        }
    }
    rd := (mode & core.IoOpenRead) != 0
    wr := (mode & (core.IoOpenWrite | core.IoOpenAppend)) != 0
    var f: i32 = if rd && wr { ORdWr } else if wr { OWrOnly } else { ORdOnly }
    if (mode & core.IoOpenAppend) != 0 {
        f = f | OAppend
    }
    if (mode & core.IoOpenCreate) != 0 {
        f = f | OCreat
        if (mode & core.IoOpenExclusive) != 0 {
            f = f | OExcl
        }
    }
    if (mode & core.IoOpenTruncate) != 0 {
        f = f | OTrunc
    }
    return f | OCloExec
}

OpenPerm(mode: u32): i32 {
    return if (mode & core.IoOpenOptions) == 0 { 0o666 } else { Trunc[i32]((mode >> core.IoOpenPermShift) & 0o7777) }
}

// OpenPath is open(2) of the NUL-terminated c with flags and perm (the
// variadic mode, passed as the C int open reads), repeated on EINTR; the
// descriptor, or the failure.
OpenPath(c: []u8, flags: i32, perm: i32): OsResult {
    for {
        var fd: i32 = -1
        unsafe "c is a NUL-terminated path in the caller's frame" {
            fd = open(raw.Data(c), flags, perm)
        }
        if fd >= 0 {
            return Ok(i64(fd))
        }
        e := Errno()
        if e != 4 {
            return Fail(e)
        }
    }
}

/// OpenFile opens path, close-on-exec; value is the descriptor. mode 0
/// reads, 1 creates or truncates for read-write, 2 opens or creates for
/// read-write without truncating (for db.sqlite), or the options form
/// (core.IoOpenOptions: read, write, append, create, truncate, exclusive
/// and the permission bits of a file it creates, for fs.OpenWith). async
/// is ignored: kqueue does not wait on files.
/// O(1) system calls; allocates nothing (the path is copied into a stack
/// buffer).
export OpenFile(path: str, mode: u32, async: bool): OsResult {
    var c: [1024]u8 = [0; 1024]
    if !CPath(path, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    return OpenPath(c[..], OpenFlags(mode), OpenPerm(mode))
}

/// Close closes h. Darwin releases the descriptor even when close reports
/// EINTR, so EINTR is success here, as on Linux: a second close could hit
/// a descriptor another thread was given in between.
/// O(1): one system call; allocates nothing.
export Close(h: uint): OsResult {
    if lc.CloseFd(Trunc[i32](h)) != 0 {
        e := Errno()
        if e != 4 {
            return Fail(e)
        }
    }
    return Ok(0)
}

/// FileSize is st_size from fstat; a directory is IsDirectory, the answer
/// the other targets give.
/// O(1): one system call; allocates nothing.
export FileSize(h: uint): OsResult {
    var st: [18]u64 = [0; 18]
    if StatFd(h, mut st) != 0 {
        return Fail(Errno())
    }
    if (StatMode(&st) & 0o170000) == 0o040000 {
        return OsResult(value: 0, err: core.SysErr.IsDirectory, os: 21)
    }
    return Ok(Cast[i64](st[StSize / 8]))
}

/// Truncate sets the file's size to size: ftruncate; a longer size extends
/// the file with zeros, and the offset does not move. A negative size is
/// Other with os 0 and no call.
/// O(1) system calls; allocates nothing.
export Truncate(h: uint, size: i64): OsResult {
    if size < 0 {
        return OsResult(value: 0, err: core.SysErr.Other, os: 0)
    }
    unsafe "ftruncate passes no memory" {
        if ftruncate(Trunc[i32](h), size) != 0 {
            return Fail(Errno())
        }
    }
    return Ok(0)
}

/// FileId is the identity of the open file: out[0] st_dev and out[1]
/// st_ino, from fstat. Two descriptors of one file give one pair.
/// O(1): one system call; allocates nothing.
export FileId(h: uint, out: mut [2]u64): OsResult {
    var st: [18]u64 = [0; 18]
    if StatFd(h, mut st) != 0 {
        return Fail(Errno())
    }
    out[0] = st[0] & 0xFFFF_FFFF
    out[1] = st[StIno / 8]
    return Ok(0)
}

// fcntl's POSIX record locks, and struct flock's l_type values (Darwin's
// <fcntl.h>).
const FSetlk: i32 = 8
const FSetlkw: i32 = 9
const FRdlck: u64 = 1
const FUnlck: u64 = 2
const FWrlck: u64 = 3

// LockOp is one F_SETLK(W) of type kind on bytes off to off + n (n 0: to
// the end and beyond). Darwin's struct flock is 24 bytes: l_start, l_len,
// l_pid (i32 at 16), l_type and l_whence (i16 at 20 and 22, SEEK_SET 0).
LockOp(h: uint, cmd: i32, kind: u64, off: i64, n: i64): i32 {
    var fl: [3]u64 = [Cast[u64](off), Cast[u64](n), kind << 32]
    unsafe "fl is a struct flock, passed as fcntl's pointer argument" {
        return fcntl(Trunc[i32](h), cmd, raw.PtrCast[u64, u8](raw.DataMut(mut fl[..])))
    }
}

/// LockRange takes a byte-range lock on bytes off to off + n of the file
/// (n 0: to the end and beyond), shared or exclusive: a POSIX record lock
/// (fcntl F_SETLK, or F_SETLKW to wait). It belongs to the process, not
/// the descriptor: closing any descriptor of the file drops every lock the
/// process holds on it, the trap sqlite's os_unix.c works around. value is
/// 1 when the lock is held, 0 when another holder has a conflicting lock
/// and wait is false (EAGAIN or EACCES); a negative off or n is Other with
/// os 0 and no call; any other failure is the error.
/// O(1) system calls besides the wait when wait is true; allocates nothing.
export LockRange(h: uint, off: i64, n: i64, exclusive: bool, wait: bool): OsResult {
    if off < 0 || n < 0 {
        return OsResult(value: 0, err: core.SysErr.Other, os: 0)
    }
    kind := if exclusive { FWrlck } else { FRdlck }
    cmd := if wait { FSetlkw } else { FSetlk }
    if LockOp(h, cmd, kind, off, n) == 0 {
        return Ok(1)
    }
    e := Errno()
    return if e == 35 || e == 13 { Ok(0) } else { Fail(e) }
}

/// UnlockRange releases the lock on bytes off to off + n (F_UNLCK).
/// O(1): one system call; allocates nothing.
export UnlockRange(h: uint, off: i64, n: i64): OsResult {
    if off < 0 || n < 0 {
        return OsResult(value: 0, err: core.SysErr.Other, os: 0)
    }
    return if LockOp(h, FSetlk, FUnlck, off, n) == 0 { Ok(0) } else { Fail(Errno()) }
}

/// SyncFile flushes h's written data and metadata to the device: DataSync's
/// F_FULLFSYNC, which also writes the metadata.
/// O(1) system calls besides the device's flush; allocates nothing.
export SyncFile(h: uint): OsResult {
    return DataSync(h)
}

/// Rename replaces dst (rename(2)).
/// O(1) system calls; allocates nothing (the paths are copied into stack
/// buffers).
export Rename(src: str, dst: str): OsResult {
    var a: [1024]u8 = [0; 1024]
    var b: [1024]u8 = [0; 1024]
    if !CPath(src, mut a[..]) || !CPath(dst, mut b[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    unsafe "a and b are NUL-terminated copies of the paths in this frame" {
        if lc.RenamePath(raw.Data(a[..]), raw.Data(b[..])) != 0 {
            return Fail(Errno())
        }
    }
    return Ok(0)
}

/// Remove deletes a file, then an empty directory on EISDIR or EPERM
/// (Darwin's unlink of a directory answers EPERM).
/// O(1) system calls; allocates nothing (the path is copied into a stack
/// buffer).
export Remove(p: str): OsResult {
    var c: [1024]u8 = [0; 1024]
    if !CPath(p, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    unsafe "c is a NUL-terminated copy of the path in this frame" {
        path := raw.Data(c[..])
        if unlink(path) == 0 {
            return Ok(0)
        }
        e := Errno()
        if e != 21 && e != 1 {
            return Fail(e)
        }
        if rmdir(path) != 0 {
            e2 := Errno()
            // A path that is not a directory after all keeps unlink's answer.
            return Fail(if e2 == 20 { e } else { e2 })
        }
    }
    return Ok(0)
}

/// WriteConsole writes data to h as WriteSync does: macOS writes the bytes
/// exactly, so carry stays empty.
/// O(len(data)), as WriteSync; allocates nothing.
export WriteConsole(h: uint, data: []u8, carry: mut ConsoleCarry): OsResult {
    return WriteSync(h, data, -1)
}

// FGetFd is F_GETFD of Darwin's <fcntl.h>.
const FGetFd: i32 = 1

/// HandleCount is how many descriptors the process has open: those below
/// the first 4,096 that answer F_GETFD. For leak tests.
/// O(4,096) system calls; allocates nothing.
export HandleCount(): int {
    var n := 0
    for fd in 0..4096 {
        unsafe "fcntl F_GETFD passes no memory" {
            if fcntl(Trunc[i32](fd), FGetFd) >= 0 {
                n += 1
            }
        }
    }
    return n
}

/// TimerHandles: 0; macOS's timed waits make no descriptor of their own.
/// For leak tests (task.KeptHandles).
/// O(1); allocates nothing.
export TimerHandles(): int {
    return 0
}

/// ReOpenSync is Windows's: no descriptor is opened for overlapped I/O
/// here, so a lent file needs no new one. Unsupported. O(1); allocates
/// nothing.
export ReOpenSync(h: uint, write: bool): OsResult {
    return FailWith(core.SysErr.Unsupported)
}
