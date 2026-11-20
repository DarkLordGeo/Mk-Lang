// Why: std.fs needs directories made, listed and stat'ed on macOS, where
// libSystem's directory stream (fdopendir and readdir) is the only stable
// way to read a directory's entries.
//
// What: the bindings behind fs.Dir, fs.MakeDir and fs.Stat: mkdir, stat and
// a directory stream (DirBlockBytes, the stream calls), with the helpers
// FsStatAt, FsStatInto, FsDtypeInto, FsNanos and DirKindOnly.
//
// How: a stream's handle is the directory's descriptor itself, which std.rt
// closes with Close and removeat.mk's calls name entries by, so no DIR
// outlives a call: each refill opens the directory afresh (openat of ".",
// with its own offset), makes a DIR of that descriptor, skips the entries
// earlier refills used, reads until the block is full, and closes the DIR,
// as Go's syscall.Getdirentries does on Darwin. Each entry's kind, size
// and time come from one fstatat(AT_SYMLINK_NOFOLLOW) in the refill; a
// stream read for kinds alone (fs.Walk's and RemoveAll's, opened by
// removeat.mk's DirOpenAt) takes the kind from d_type and makes the
// fstatat only for DT_UNKNOWN. struct dirent is the 64-bit-inode layout on
// arm64 and the legacy one on x86_64, whose unsuffixed fdopendir and
// readdir use it. Paths are copied NUL-terminated into a PATH_MAX (1,024)
// stack buffer (CPath).

import std.core
import std.raw
import std.sys.lc

/// DirBlockBytes is the size of the block a directory stream reads into: a
/// 48-byte head (the next record's offset at 0, the bytes filled at 8, the
/// state at 16: 0 live, 1 the directory's end read by the last refill, 2
/// the end; at 24 1 for a stream read for kinds alone, DirKindOnly's; at
/// 32 how many readdir entries earlier refills used), then the records
/// DirFill writes: each 8-byte aligned, its length and the name's length
/// in the first word, the kind, the size and the time in the next three,
/// then the name. . and .. and entries gone before their fstatat are never
/// written. DirFill makes every system call, DirNext none.
export const DirBlockBytes: int = 16384

/// DirFillOnOpen: false here, so std.rt's DirOpenAt leaves the first fill
/// to the first read, whose EINTR contract (DirFill's doc) is the refill's
/// own. Windows' is true.
export const DirFillOnOpen: bool = false

const FsDirHead: int = 48
const FsRecHead: int = 32
// FsDtUnknown, FsDtDir, FsDtReg and FsDtLnk are Darwin's d_type values.
const FsDtUnknown: u8 = 0
const FsDtDir: u8 = 4
const FsDtReg: u8 = 8
const FsDtLnk: u8 = 10
// FsAtFdcwd and FsAtSymlinkNoFollow are Darwin's AT_FDCWD and
// AT_SYMLINK_NOFOLLOW.
const FsAtFdcwd: i32 = -2
const FsAtSymlinkNoFollow: i32 = 0x20

// FsGet and FsPut read and write the 8-byte word at byte off of p, which
// is 8-byte aligned there.
FsGet(p: *u8, off: int): u64 {
    unsafe "the caller's block holds 8 aligned bytes at off" {
        return raw.Read(raw.PtrCast[u8, u64](raw.Offset(p, off)))
    }
}

FsPut(p: *u8, off: int, v: u64) {
    unsafe "the caller's block holds 8 aligned bytes at off" {
        raw.Write(raw.PtrCast[u8, u64](raw.Offset(p, off)), v)
    }
}

/// MakeDir makes the directory p (mode 0o777 less the umask). Anything
/// already at p is Exists (EEXIST), as Go's os.Mkdir; a missing parent is
/// NotFound.
/// O(1) system calls; allocates nothing (the path is copied into a stack
/// buffer).
export MakeDir(p: str): OsResult {
    var c: [1024]u8 = [0; 1024]
    if !CPath(p, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    unsafe "c is NUL-terminated" {
        if mkdir(raw.Data(c[..]), 0o777) != 0 {
            return Fail(Errno())
        }
    }
    return Ok(0)
}

// FsStatAt is fstatat (fstatat64 on x86_64) of the NUL-terminated name
// inside the directory dir (FsAtFdcwd: the working directory) into st,
// a struct stat; 0, or -1 with errno set.
FsStatAt(dir: i32, name: *u8, flags: i32, st: mut [18]u64): i32 {
    unsafe "name is NUL-terminated and st holds 144 bytes" {
        p := raw.PtrCast[u64, u8](raw.DataMut(mut st[..]))
        return if Target.arch == Arch.Arm64 { fstatat(dir, name, p, flags) } else { fstatat64(dir, name, p, flags) }
    }
}

// FsStatInto fills io from st, a filled struct stat: the kind, the size (0
// for a directory or a link, whose st_size is the file system's, not a
// length) and the modification time.
FsStatInto(st: &[18]u64, io: mut core.IoStat) {
    mode := StatMode(st) & 0o170000
    kind := if mode == 0o100000 {
        core.IoKindFile
    } else if mode == 0o040000 {
        core.IoKindDir
    } else if mode == 0o120000 {
        core.IoKindLink
    } else {
        core.IoKindOther
    }
    io.kind = kind
    io.size = if kind == core.IoKindFile || kind == core.IoKindOther { Trunc[i64](st[StSize / 8]) } else { 0 }
    io.modified = FsNanos(Trunc[i64](st[StMtime / 8]), Trunc[i64](st[StMtime / 8 + 1]))
}

// FsDtypeInto fills io from a d_type alone (DT_REG, DT_DIR, DT_LNK,
// anything else Other), with size and time 0: the facts of an entry whose
// fstatat failed, and of every entry of a stream read for kinds alone.
FsDtypeInto(t: u8, io: mut core.IoStat) {
    io.kind = if t == FsDtReg {
        core.IoKindFile
    } else if t == FsDtDir {
        core.IoKindDir
    } else if t == FsDtLnk {
        core.IoKindLink
    } else {
        core.IoKindOther
    }
    io.size = 0
    io.modified = 0
}

/// StatPath fills st for p, following symbolic links (Go's os.Stat).
/// O(1) system calls; allocates nothing (the path is copied into a stack
/// buffer).
export StatPath(p: str, st: mut core.IoStat): OsResult {
    var c: [1024]u8 = [0; 1024]
    if !CPath(p, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    var sb: [18]u64 = [0; 18]
    if PathStat(c[..], false, mut sb) != 0 {
        return Fail(Errno())
    }
    FsStatInto(&sb, st)
    return Ok(0)
}

/// DirOpen opens p as a directory stream: the descriptor (close-on-exec).
/// A file is NotDirectory (ENOTDIR).
/// O(1) system calls; allocates nothing (the path is copied into a stack
/// buffer; rt allocates the stream's block).
export DirOpen(p: str): OsResult {
    var c: [1024]u8 = [0; 1024]
    if !CPath(p, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    return OpenPath(c[..], ORdOnly | ODirectory | OCloExec, 0)
}

// DirKindOnly marks block, a zeroed DirBlockBytes stream block before its
// first fill, as read for kinds alone: DirFill then takes each entry's
// kind from d_type, with size and time 0, and makes an fstatat only for an
// entry whose d_type is DT_UNKNOWN (a file system without d_type). An
// empty block (none was made) is left alone. O(1); allocates nothing.
DirKindOnly(block: mut []u8) {
    if block.Len() < DirBlockBytes {
        return
    }
    unsafe "block holds DirBlockBytes bytes" {
        FsPut(raw.DataMut(block), 24, 1)
    }
}

/// DirFill makes the next entries of the stream h ready in block, the
/// stream's DirBlockBytes block (zero when the stream opened): the only
/// part of a listing that makes system calls, so std.task runs it on the
/// blocking pool, once per block rather than once per entry. While records
/// written earlier are unread it makes no call and answers 1. Else one
/// refill: openat(h, "."), fdopendir, readdir past the entries earlier
/// refills used, then readdir and one fstatat(AT_SYMLINK_NOFOLLOW) per new
/// entry until the block is full or the directory ends, and closedir; a
/// symbolic link is IoKindLink. value: 1 when entries are ready, 0 at the
/// end. An entry removed between readdir and its fstatat (ENOENT) is
/// skipped, as Go's ReadDir skips it; any other fstatat failure keeps the
/// listing going with the kind from d_type, size and time 0. EINTR from an
/// fstatat is returned with the records written so far kept, so the
/// pool's repeat serves them and the next refill resumes at the entry it
/// stopped on; a readdir error after some records is left for the next
/// refill to meet again. A stream read for kinds alone (DirKindOnly) takes
/// each kind from d_type, size and time 0, and makes the fstatat only for
/// a record whose d_type is DT_UNKNOWN. Because each refill skips again
/// every entry used before, a listing of n entries costs O(n * n /
/// entries per block) readdir steps (in libSystem's buffer, a few hundred
/// entries per block), and entries added or removed between refills may be
/// seen twice or missed, as Go's syscall.Getdirentries on Darwin, except
/// for removals by RemoveAt inside the stream, which it counts.
/// One openat, fdopendir and closedir per refill and one fstatat per new
/// entry (per new DT_UNKNOWN entry for kinds alone); libSystem allocates
/// the DIR and frees it before the call returns; std allocates nothing.
export DirFill(h: uint, block: mut []u8): OsResult {
    if block.Len() < DirBlockBytes {
        return FailWith(core.SysErr.Other)
    }
    unsafe "block holds DirBlockBytes bytes: the head and the records DirFill writes" {
        b := raw.DataMut(block)
        state := FsGet(b, 16)
        if state == 2 {
            return Ok(0)
        }
        if FsGet(b, 0) < FsGet(b, 8) {
            return Ok(1)
        }
        if state == 1 {
            FsPut(b, 16, 2)
            return Ok(0)
        }
        FsPut(b, 0, 0)
        FsPut(b, 8, 0)
        dot: [2]u8 = [46, 0]
        fd := openat(Trunc[i32](h), raw.Data(dot[..]), ORdOnly | ODirectory | OCloExec)
        if fd < 0 {
            return Fail(Errno())
        }
        d := fdopendir(fd)
        if raw.IsNull(d) {
            e := Errno()
            _ = lc.CloseFd(fd)
            return Fail(e)
        }
        kinds := FsGet(b, 24) == 1
        var used := FsGet(b, 32)
        var idx: u64 = 0
        var n := 0
        var res := Ok(1)
        var st := core.IoStat(kind: 0, size: 0, modified: 0)
        var sb: [18]u64 = [0; 18]
        for {
            raw.Write(__error(), 0)
            e := readdir(d)
            if raw.IsNull(e) {
                err := Errno()
                if err != 0 {
                    if n == 0 {
                        res = Fail(err)
                    }
                } else if n == 0 {
                    FsPut(b, 16, 2)
                    res = Ok(0)
                } else {
                    FsPut(b, 16, 1)
                }
                break
            }
            if idx < used {
                idx += 1
                continue
            }
            var nm := raw.Offset(e, 21)
            var len := int(raw.Read(raw.Offset(e, 18))) | (int(raw.Read(raw.Offset(e, 19))) << 8)
            var t := raw.Read(raw.Offset(e, 20))
            if Target.arch != Arch.Arm64 {
                // The legacy layout: d_ino (4 bytes), d_reclen (2), d_type,
                // d_namlen, d_name.
                nm = raw.Offset(e, 8)
                len = int(raw.Read(raw.Offset(e, 7)))
                t = raw.Read(raw.Offset(e, 6))
            }
            dots := (len == 1 && raw.Read(nm) == 46) || (len == 2 && raw.Read(nm) == 46 && raw.Read(raw.Offset(nm, 1)) == 46)
            need := FsRecHead + ((len + 7) / 8) * 8
            if dots || len == 0 || need > DirBlockBytes - FsDirHead {
                idx += 1
                used = idx
                continue
            }
            if FsDirHead + n + need > DirBlockBytes {
                // The entry stays for the next refill.
                break
            }
            if kinds && t != FsDtUnknown {
                FsDtypeInto(t, mut st)
            } else if FsStatAt(Trunc[i32](h), nm, FsAtSymlinkNoFollow, mut sb) == 0 {
                FsStatInto(&sb, mut st)
            } else {
                err := Errno()
                if err == 4 {
                    res = Fail(err)
                    break
                }
                if err == 2 {
                    idx += 1
                    used = idx
                    continue
                }
                FsDtypeInto(t, mut st)
            }
            rec := raw.Offset(b, FsDirHead + n)
            FsPut(rec, 0, Cast[u64](need) | (Cast[u64](len) << 32))
            FsPut(rec, 8, u64(st.kind))
            FsPut(rec, 16, Trunc[u64](st.size))
            FsPut(rec, 24, Trunc[u64](st.modified))
            raw.Copy(raw.Offset(rec, FsRecHead), nm, len)
            n += need
            idx += 1
            used = idx
        }
        _ = closedir(d)
        FsPut(b, 8, Cast[u64](n))
        FsPut(b, 32, used)
        return res
    }
}

/// DirNext serves the next entry already in block (DirFill's), with no
/// system call, into name and st. value: the name's length; 0 at the end;
/// -1 when the block holds no ready entry (call DirFill, then DirNext
/// again). . and .. never reach the block, nor an entry DirFill found
/// gone. A name longer than name is NoSpace with value the length it
/// needs; the entry stays next. name and st are written only for an entry.
/// O(len) of the name; allocates nothing.
export DirNext(block: mut []u8, name: mut []u8, st: mut core.IoStat): OsResult {
    if block.Len() < DirBlockBytes {
        return FailWith(core.SysErr.Other)
    }
    unsafe "block holds DirBlockBytes bytes, laid out by DirFill" {
        b := raw.DataMut(block)
        if FsGet(b, 16) == 2 {
            return Ok(0)
        }
        pos := Trunc[int](FsGet(b, 0))
        fill := Trunc[int](FsGet(b, 8))
        if pos >= fill {
            return Ok(-1)
        }
        rec := raw.Offset(b, FsDirHead + pos)
        w := FsGet(rec, 0)
        reclen := Trunc[int](w & 0xFFFF_FFFF)
        n := Trunc[int](w >> 32)
        if reclen < FsRecHead || pos + reclen > fill || n > reclen - FsRecHead || pos % 8 != 0 {
            // A record DirFill never writes: drop the rest of the block
            // rather than read past it.
            FsPut(b, 0, Cast[u64](fill))
            return FailWith(core.SysErr.Other)
        }
        if n > name.Len() {
            return OsResult(value: i64(n), err: core.SysErr.NoSpace, os: 0)
        }
        raw.Copy(raw.DataMut(name), raw.Offset(rec, FsRecHead), n)
        st.kind = Trunc[u32](FsGet(rec, 8))
        st.size = Trunc[i64](FsGet(rec, 16))
        st.modified = Trunc[i64](FsGet(rec, 24))
        FsPut(b, 0, Cast[u64](pos + reclen))
        return Ok(i64(n))
    }
}

// FsNanos is a struct stat timestamp, sec seconds and ns nanoseconds (0 to
// 999,999,999) since the epoch, as one i64 of nanoseconds saturated at its
// ends: a time past 2262 is the greatest, one before 1677 the least, so no
// time wraps to the other end. O(1).
FsNanos(sec: i64, ns: i64): i64 {
    if sec >= 9_223_372_036 {
        return if sec == 9_223_372_036 && ns <= 854_775_807 { sec * 1_000_000_000 + ns } else { 0x7FFF_FFFF_FFFF_FFFF }
    }
    if sec < -9_223_372_036 {
        return -0x7FFF_FFFF_FFFF_FFFF - 1
    }
    return sec * 1_000_000_000 + ns
}
