// Why: every file, pipe and standard stream in std ends in a Linux file
// descriptor; this is the layer that opens, reads, writes, locks and closes
// them without libc.
//
// What: ReadSync, ReadOnce, the writes, OpenFile, Close, the byte-range
// locks, Remove, IsConsole and ConsoleTestType (Windows' console path, absent
// here).
//
// How: direct system calls; read and write retry EINTR, except ReadOnce,
// which a pool thread's interrupt may end. Files open by openat, with paths
// copied NUL-terminated into a PATH_MAX stack buffer. Close treats EINTR as
// success, since Linux has released the descriptor by then.

import std.core
import std.raw

const ORdOnly: uint = 0
const ORdWrCreateTrunc: uint = 0o2 | 0o100 | 0o1000
const ORdWrCreate: uint = 0o2 | 0o100
const OCloExec: uint = 0o2000000
const AtRemoveDir: uint = 0x200
const Eintr: int = -4
const MaxIo: int = 0x7FFF_F000

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

/// StdHandle is the handle of standard input (0), output (1) or error (2):
/// on Linux the descriptor i itself.
/// O(1); allocates nothing.
export StdHandle(i: int): uint {
    return Trunc[uint](i)
}

// Stat fills st (struct stat) for fd; false on failure.
Stat(h: uint, st: mut [18]u64): bool {
    unsafe "system call; st holds 144 bytes" {
        return !IsErr(Syscall(Nrs().fstat, h, raw.Addr(raw.DataMut(mut st[..])), 0, 0, 0, 0))
    }
}

/// HandleKind is what h refers to: 1 a regular file, 2 a tty or other
/// character device, 3 a pipe, 4 a socket, 0 unknown (or fstat failed).
/// O(1) system calls; allocates nothing.
export HandleKind(h: uint): u32 {
    var st: [18]u64 = [0; 18]
    if !Stat(h, mut st) {
        return 0
    }
    var mode: u32 = 0
    unsafe "st is a struct stat" {
        mode = raw.Read(raw.PtrCast[u8, u32](raw.Offset(raw.PtrCast[u64, u8](raw.Data(st[..])), StatMode())))
    }
    t := mode & 0o170000
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

/// IsConsole is false: Linux writes bytes exactly everywhere, so no
/// handle needs the console path.
/// O(1); allocates nothing.
export IsConsole(h: uint): bool {
    return false
}

/// ConsoleTestType types into the process's console (tests only; the
/// Windows variant's ConsoleRead test). Linux has no console path:
/// always -1, refused.
/// O(1); allocates nothing.
export ConsoleTestType(units: []u16): int {
    return -1
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
        unsafe "system call; buf holds n bytes" {
            p := raw.Addr(raw.DataMut(buf))
            if pos < 0 {
                r = Syscall(Nrs().read, h, p, Trunc[uint](n), 0, 0, 0)
            } else {
                r = Syscall(Nrs().pread64, h, p, Trunc[uint](n), Trunc[uint](pos), 0, 0)
            }
        }
        if r != Eintr {
            return if IsErr(r) { IoFail(h, OpRead, i64(-r)) } else { Ok(i64(r)) }
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
        unsafe "system call; data holds off + n bytes" {
            p := raw.Addr(raw.Offset(raw.Data(data), off))
            if pos < 0 {
                r = Syscall(Nrs().write, h, p, Trunc[uint](n), 0, 0, 0)
            } else {
                r = Syscall(Nrs().pwrite64, h, p, Trunc[uint](n), Trunc[uint](pos + i64(off)), 0, 0)
            }
        }
        if r == Eintr {
            continue
        }
        if IsErr(r) {
            return IoFail(h, OpWrite, i64(-r))
        }
        if r == 0 {
            return FailWith(core.SysErr.Other)
        }
        off += r
    }
    return Ok(i64(off))
}

/// ReadOnce is ReadSync without the EINTR retry, for a pool thread that
/// ThreadInterrupt may interrupt: one call; an interruption
/// is Canceled with os 4, and the pool decides whether it was its own.
/// O(1) system calls; allocates nothing.
export ReadOnce(h: uint, buf: mut []u8, pos: i64): OsResult {
    var n := buf.Len()
    if n > MaxIo {
        n = MaxIo
    }
    var r := 0
    unsafe "system call; buf holds n bytes" {
        p := raw.Addr(raw.DataMut(buf))
        if pos < 0 {
            r = Syscall(Nrs().read, h, p, Trunc[uint](n), 0, 0, 0)
        } else {
            r = Syscall(Nrs().pread64, h, p, Trunc[uint](n), Trunc[uint](pos), 0, 0)
        }
    }
    return if IsErr(r) { IoFail(h, OpRead, i64(-r)) } else { Ok(i64(r)) }
}

/// WriteOnce is one write call with no EINTR retry (ReadOnce's rule): value
/// is the bytes written, which may be fewer than data holds; an
/// interruption before any byte is Canceled with os 4.
/// O(1) system calls; allocates nothing.
export WriteOnce(h: uint, data: []u8, pos: i64): OsResult {
    var n := data.Len()
    if n > MaxIo {
        n = MaxIo
    }
    var r := 0
    unsafe "system call; data holds n bytes" {
        p := raw.Addr(raw.Data(data))
        if pos < 0 {
            r = Syscall(Nrs().write, h, p, Trunc[uint](n), 0, 0, 0)
        } else {
            r = Syscall(Nrs().pwrite64, h, p, Trunc[uint](n), Trunc[uint](pos), 0, 0)
        }
    }
    return if IsErr(r) { IoFail(h, OpWrite, i64(-r)) } else { Ok(i64(r)) }
}

// OpenFlags is open(2)'s flags for an open entry's mode (core.IoOpenOptions
// for the options form), close-on-exec always; OpenPerm the mode bits a
// file it creates gets (less the umask). The ring's OPENAT uses both, so
// the two routes open alike.
OpenFlags(mode: u32): uint {
    if (mode & core.IoOpenOptions) == 0 {
        return if mode == 1 {
            ORdWrCreateTrunc | OCloExec
        } else if mode == 2 {
            ORdWrCreate | OCloExec
        } else {
            ORdOnly | OCloExec
        }
    }
    rd := (mode & core.IoOpenRead) != 0
    wr := (mode & (core.IoOpenWrite | core.IoOpenAppend)) != 0
    var f: uint = if rd && wr { 0o2 } else if wr { 0o1 } else { 0 }
    if (mode & core.IoOpenAppend) != 0 {
        f = f | 0o2000
    }
    if (mode & core.IoOpenCreate) != 0 {
        f = f | 0o100
        if (mode & core.IoOpenExclusive) != 0 {
            f = f | 0o200
        }
    }
    if (mode & core.IoOpenTruncate) != 0 {
        f = f | 0o1000
    }
    return f | OCloExec
}

OpenPerm(mode: u32): uint {
    return if (mode & core.IoOpenOptions) == 0 { 0o666 } else { uint((mode >> core.IoOpenPermShift) & 0o7777) }
}

/// OpenFile opens path, close-on-exec; value is the descriptor. mode 0
/// reads, 1 creates or truncates for read-write, 2 opens or creates for
/// read-write without truncating (for db.sqlite), or the options form
/// (core.IoOpenOptions: read, write, append, create, truncate, exclusive
/// and the permission bits of a file it creates, for fs.OpenWith). async
/// is ignored: epoll cannot wait on files.
/// O(1) system calls; allocates nothing (the path is copied into a stack
/// buffer).
export OpenFile(path: str, mode: u32, async: bool): OsResult {
    var c: [4096]u8 = [0; 4096]
    if !CPath(path, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    unsafe "system call; c is a NUL-terminated copy of the path in this frame" {
        return Result(Syscall(Nrs().openat, AtFdcwd, raw.Addr(raw.Data(c[..])), OpenFlags(mode), OpenPerm(mode), 0, 0))
    }
}

/// Close closes h: the CloseHandle of the system layer (the Windows variant
/// cannot use that name).
/// Linux releases the descriptor even when close reports EINTR, so EINTR is
/// success here: reporting it would make the caller close again (a record
/// whose close failed is kept), and that second close could hit a
/// descriptor another thread was given in between.
/// O(1): one system call; allocates nothing.
export Close(h: uint): OsResult {
    unsafe "a system call that passes no memory, only values and handles" {
        r := Syscall(Nrs().close, h, 0, 0, 0, 0, 0)
        if IsErr(r) && r != -4 {
            return Result(r)
        }
    }
    return Ok(0)
}

/// FileSize is st_size from fstat; a directory is IsDirectory (its st_size
/// is the file system's, not a length a read could reach), the answer
/// Windows gives too.
/// O(1): one system call; allocates nothing.
export FileSize(h: uint): OsResult {
    var st: [18]u64 = [0; 18]
    unsafe "system call; st holds 144 bytes" {
        r := Syscall(Nrs().fstat, h, raw.Addr(raw.DataMut(mut st[..])), 0, 0, 0, 0)
        if IsErr(r) {
            return Fail(i64(-r))
        }
        if (Get32(raw.PtrCast[u64, u8](raw.Data(st[..])), StatMode()) & 0o170000) == 0o040000 {
            return OsResult(value: 0, err: core.SysErr.IsDirectory, os: 21)
        }
    }
    return Ok(Trunc[i64](st[StatSize / 8]))
}

/// Truncate sets the file's size to size (for db.sqlite): ftruncate;
/// a longer size extends the file with zeros, and the offset does not
/// move. A negative size is Other with os 0 and no call.
/// O(1) system calls; allocates nothing.
export Truncate(h: uint, size: i64): OsResult {
    if size < 0 {
        return OsResult(value: 0, err: core.SysErr.Other, os: 0)
    }
    unsafe "a system call that passes no memory, only values and handles" {
        r := Syscall(Nrs().ftruncate, h, Trunc[uint](size), 0, 0, 0, 0)
        if IsErr(r) {
            return Result(r)
        }
    }
    return Ok(0)
}

/// FileId is the identity of the open file (for db.sqlite): out[0]
/// st_dev and out[1] st_ino, from fstat (both u64, at 0 and 8 in the stat
/// of x86_64 and of aarch64's generic layout). Two descriptors of one file
/// give one pair, whatever path or link opened them; Windows answers with
/// the volume serial and the file index.
/// O(1): one system call; allocates nothing.
export FileId(h: uint, out: mut [2]u64): OsResult {
    var st: [18]u64 = [0; 18]
    unsafe "system call; st holds 144 bytes" {
        r := Syscall(Nrs().fstat, h, raw.Addr(raw.DataMut(mut st[..])), 0, 0, 0, 0)
        if IsErr(r) {
            return Result(r)
        }
    }
    out[0] = st[0]
    out[1] = st[1]
    return Ok(0)
}

// fcntl's open file description locks, and struct flock's l_type values.
const FOfdSetlk: uint = 37
const FOfdSetlkw: uint = 38
const FRdlck: u64 = 0
const FWrlck: u64 = 1
const FUnlck: u64 = 2

// LockOp is one F_OFD_SETLK(W) of type kind on bytes off to off + n (n 0:
// to the end and beyond). struct flock is 32 bytes on x86_64 and aarch64:
// l_type and l_whence (i16, SEEK_SET 0) in the first word, l_start,
// l_len, then l_pid, which an OFD lock needs 0.
LockOp(h: uint, cmd: uint, kind: u64, off: i64, n: i64): int {
    var fl: [4]u64 = [kind, Trunc[u64](off), Trunc[u64](n), 0]
    unsafe "system call; fl is a struct flock" {
        return Syscall(Nrs().fcntl, h, cmd, raw.Addr(raw.DataMut(mut fl[..])), 0, 0, 0)
    }
}

/// LockRange takes a byte-range lock on bytes off to off + n of the file
/// (n 0: to the end and beyond), shared or exclusive (for db.sqlite): an
/// open file description lock (fcntl F_OFD_SETLK, or F_OFD_SETLKW
/// to wait; Linux 3.15). It belongs to the open description, not the
/// process, so two descriptors opened apart exclude each other as two
/// handles do on Windows, and closing another descriptor of the same file
/// does not drop it (the POSIX F_SETLK trap sqlite's os_unix.c works
/// around); it ends with UnlockRange or the description's last close.
/// value is 1 when the lock is held, 0 when another holder has a
/// conflicting lock and wait is false (EAGAIN or EACCES; one answer on both
/// targets); a negative off or n is Other with os 0 and no call; any other
/// failure is the error. With wait true the call blocks the thread: std
/// code below the scheduler passes false and retries.
/// The one difference from Windows: a new lock from this
/// description on bytes it already locks converts or merges in place
/// (shared to exclusive atomically; two shared locks are one, gone after
/// one UnlockRange), where Windows judges it against the handle's own lock.
/// So a caller that changes a range's kind unlocks it first and locks
/// again, and never locks a range it holds; under that rule both targets
/// give the same answers.
/// Linux locks are advisory: reads and writes of locked bytes still work.
/// O(1) system calls besides the wait when wait is true; allocates nothing.
export LockRange(h: uint, off: i64, n: i64, exclusive: bool, wait: bool): OsResult {
    if off < 0 || n < 0 {
        return OsResult(value: 0, err: core.SysErr.Other, os: 0)
    }
    kind := if exclusive { FWrlck } else { FRdlck }
    cmd := if wait { FOfdSetlkw } else { FOfdSetlk }
    r := LockOp(h, cmd, kind, off, n)
    if r == -11 || r == -13 {
        return Ok(0)
    }
    return if IsErr(r) { Result(r) } else { Ok(1) }
}

/// UnlockRange releases the lock on bytes off to off + n (F_UNLCK; POSIX
/// may split a held range, Windows only unlocks a range locked whole, so
/// callers unlock exactly what they locked).
/// O(1): one system call; allocates nothing.
export UnlockRange(h: uint, off: i64, n: i64): OsResult {
    if off < 0 || n < 0 {
        return OsResult(value: 0, err: core.SysErr.Other, os: 0)
    }
    r := LockOp(h, FOfdSetlk, FUnlck, off, n)
    return if IsErr(r) { Result(r) } else { Ok(0) }
}

/// SyncFile flushes h's written data and metadata to the device.
/// O(1) system calls besides the device's flush; allocates nothing.
export SyncFile(h: uint): OsResult {
    unsafe "a system call that passes no memory, only values and handles" {
        r := Syscall(Nrs().fsync, h, 0, 0, 0, 0, 0)
        if IsErr(r) {
            return Result(r)
        }
    }
    return Ok(0)
}

/// Rename replaces dst (renameat2 with no flags).
/// O(1) system calls; allocates nothing (the paths are copied into stack
/// buffers).
export Rename(src: str, dst: str): OsResult {
    var a: [4096]u8 = [0; 4096]
    var b: [4096]u8 = [0; 4096]
    if !CPath(src, mut a[..]) || !CPath(dst, mut b[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    unsafe "system call; a and b are NUL-terminated copies of the paths in this frame" {
        r := Syscall(Nrs().renameat2, AtFdcwd, raw.Addr(raw.Data(a[..])), AtFdcwd, raw.Addr(raw.Data(b[..])), 0, 0)
        if IsErr(r) {
            return Result(r)
        }
    }
    return Ok(0)
}

/// Remove deletes a file, then an empty directory on EISDIR.
/// O(1) system calls; allocates nothing (the path is copied into a stack
/// buffer).
export Remove(p: str): OsResult {
    var c: [4096]u8 = [0; 4096]
    if !CPath(p, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    unsafe "system call; c is a NUL-terminated copy of the path in this frame" {
        path := raw.Addr(raw.Data(c[..]))
        var r := Syscall(Nrs().unlinkat, AtFdcwd, path, 0, 0, 0, 0)
        if r == -21 {
            r = Syscall(Nrs().unlinkat, AtFdcwd, path, AtRemoveDir, 0, 0, 0)
        }
        if IsErr(r) {
            return Result(r)
        }
    }
    return Ok(0)
}

/// WriteConsole writes data to h as WriteSync does: Linux writes the bytes
/// exactly, so carry stays empty.
/// O(len(data)), as WriteSync; allocates nothing.
export WriteConsole(h: uint, data: []u8, carry: mut ConsoleCarry): OsResult {
    return WriteSync(h, data, -1)
}

/// HandleCount is how many descriptors the process has open: the entries of
/// /proc/self/fd, less the one this call opens to read it; -1 when /proc
/// cannot be read. For leak tests.
/// O(open descriptors): getdents64 into a 4 KiB stack buffer, one pass;
/// allocates nothing.
export HandleCount(): int {
    var buf: [512]u64 = [0; 512]
    unsafe "system calls; buf holds 4,096 bytes of linux_dirent64 records" {
        fd := Syscall(Nrs().openat, AtFdcwd, raw.Addr(raw.StrData("/proc/self/fd\u{0}")), ORdOnly | OCloExec, 0, 0,
            0)
        if IsErr(fd) {
            return -1
        }
        b := raw.PtrCast[u64, u8](raw.DataMut(mut buf[..]))
        var n := 0
        for {
            r := Syscall(Nrs().getdents64, Trunc[uint](fd), raw.Addr(b), 4096, 0, 0, 0)
            if r == -4 {
                continue
            }
            if IsErr(r) {
                _ = Close(Trunc[uint](fd))
                return -1
            }
            if r == 0 {
                break
            }
            var off := 0
            for off < r {
                // d_reclen at 16, d_name at 19: "." and ".." are not
                // descriptors.
                name := raw.Offset(b, off + 19)
                dot := raw.Read(name) == 46
                one := dot && raw.Read(raw.Offset(name, 1)) == 0
                two := dot && raw.Read(raw.Offset(name, 1)) == 46 && raw.Read(raw.Offset(name, 2)) == 0
                if !one && !two {
                    n += 1
                }
                off += int(Get16(b, off + 16))
            }
        }
        _ = Close(Trunc[uint](fd))
        return n - 1
    }
}

/// TimerHandles: 0; Linux's timed waits make no descriptor of their own.
/// On Windows it counts the high-resolution timer slots' handles
/// (sys/windows/hrtimer.mk). For leak tests (task.KeptHandles).
/// O(1); allocates nothing.
export TimerHandles(): int {
    return 0
}

/// ReOpenSync is Windows's: no Linux descriptor is opened for overlapped
/// I/O, so a lent file needs no new one, and std.rt never calls it here.
/// Unsupported. O(1); allocates nothing.
export ReOpenSync(h: uint, write: bool): OsResult {
    return FailWith(core.SysErr.Unsupported)
}
