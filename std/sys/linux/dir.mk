// Why: std.fs needs directories made, listed and stat'ed on Linux without
// libc's readdir.
//
// What: the bindings behind fs.Dir, fs.MakeDir and fs.Stat: mkdirat, statx
// and a directory stream over getdents64 (DirBlockBytes, the stream calls).
//
// How: the stream follows glibc's readdir shape and makes one statx per entry
// for its kind, size and time. A stream read for kinds alone (fs.Walk's and
// RemoveAll's, opened by removeat.mk's DirOpenAt) takes each kind from the
// record's d_type and makes a statx only for DT_UNKNOWN, as Go's WalkDir,
// Rust's DirEntry::file_type and Zig's Dir.walk do. Paths are copied
// NUL-terminated into a PATH_MAX stack buffer (CPath).

import std.core
import std.raw

/// DirBlockBytes is the size of the block a directory stream reads into: a
/// 32-byte head (the next record's offset at 0, the bytes filled at 8, the
/// state at 16: 0 live, 2 the end; at 24 1 for a stream read for kinds
/// alone, DirKindOnly's), the getdents64 records from DirHead to
/// DirFacts, and from DirFacts one DirSlot-byte facts slot per record (the
/// kind at 0, 0 when not read yet and DirSkip for an entry gone before its
/// statx; the size at 8; the time at 16), the slot of the record at offset
/// o being o / DirSlot: a record is at least 24 bytes (8-byte aligned
/// 19 + name + NUL), so no two records share one. Same value on both
/// targets: DirFill makes every system call, DirNext none.
export const DirBlockBytes: int = 16384

/// DirFillOnOpen: false here, so std.rt's DirOpenAt leaves the first fill
/// to the first read: DirFill's EINTR contract (its doc) is written for the
/// read's own refill, and a fill in the open's job is neither built nor
/// measured on Linux. Windows' is true.
export const DirFillOnOpen: bool = false

const DirHead: int = 32
const DirFacts: int = 8192
const DirSlot: int = 24
const DirSkip: u32 = 0xFFFF_FFFF
const ODirectory: uint = 0o200000
const AtSymlinkNoFollow: uint = 0x100
const AtNoAutomount: uint = 0x800
// TYPE | MTIME | SIZE (the facts IoStat holds).
const StatxMask: uint = 0x1 | 0x40 | 0x200
const StatxBytes: int = 256

/// MakeDir makes the directory p (mode 0o777 less the umask). Anything
/// already at p is Exists (EEXIST), as Go's os.Mkdir; a missing parent is
/// NotFound.
/// O(1) system calls; allocates nothing (the path is copied into a stack
/// buffer).
export MakeDir(p: str): OsResult {
    var c: [4096]u8 = [0; 4096]
    if !CPath(p, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    unsafe "system call; c is NUL-terminated" {
        r := Syscall(Nrs().mkdirat, AtFdcwd, raw.Addr(raw.Data(c[..])), 0o777, 0, 0, 0)
        if IsErr(r) {
            return Result(r)
        }
    }
    return Ok(0)
}

// StatInto runs statx(dir, name, flags) and fills st from it; the raw
// return (0 or -errno). EINTR is returned, as every blocking call here
// returns it: Canceled, which the pool repeats unless its job was cancelled.
StatInto(dir: uint, name: *u8, flags: uint, st: mut core.IoStat): int {
    var b: [32]u64 = [0; 32]
    unsafe "system call; b holds StatxBytes bytes" {
        p := raw.PtrCast[u64, u8](raw.DataMut(mut b[..]))
        r := Syscall(Nrs().statx, dir, raw.Addr(name), flags, StatxMask, raw.Addr(p), 0)
        if IsErr(r) {
            return r
        }
        mode := u32(Get16(p, 28)) & 0o170000
        kind := if mode == 0o100000 {
            core.IoKindFile
        } else if mode == 0o040000 {
            core.IoKindDir
        } else if mode == 0o120000 {
            core.IoKindLink
        } else {
            core.IoKindOther
        }
        st.kind = kind
        // A directory's or link's st_size is the file system's, not a
        // length (Windows reports 0 for both): 0 on both targets.
        st.size = if kind == core.IoKindFile || kind == core.IoKindOther { Trunc[i64](Get64(p, 40)) } else { 0 }
        st.modified = StatNanos(Trunc[i64](Get64(p, 112)), i64(Get32(p, 120)))
        return 0
    }
}

// DtypeInto fills st from a getdents64 d_type alone (DT_REG 8, DT_DIR 4,
// DT_LNK 10, anything else Other), with size and time 0: the facts of an
// entry whose statx failed, and of every entry of a stream read for kinds
// alone.
DtypeInto(t: u8, st: mut core.IoStat) {
    st.kind = if t == 8 {
        core.IoKindFile
    } else if t == 4 {
        core.IoKindDir
    } else if t == 10 {
        core.IoKindLink
    } else {
        core.IoKindOther
    }
    st.size = 0
    st.modified = 0
}

/// StatPath fills st for p, following symbolic links (Go's os.Stat).
/// O(1) system calls; allocates nothing (the path is copied into a stack
/// buffer).
export StatPath(p: str, st: mut core.IoStat): OsResult {
    var c: [4096]u8 = [0; 4096]
    if !CPath(p, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    var r := 0
    unsafe "c is NUL-terminated" {
        r = StatInto(AtFdcwd, raw.Data(c[..]), AtNoAutomount, st)
    }
    return if r != 0 { Result(r) } else { Ok(0) }
}

/// DirOpen opens p as a directory stream: the descriptor (close-on-exec).
/// A file is NotDirectory (ENOTDIR).
/// O(1) system calls; allocates nothing (the path is copied into a stack
/// buffer; rt allocates the stream's block).
export DirOpen(p: str): OsResult {
    var c: [4096]u8 = [0; 4096]
    if !CPath(p, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    unsafe "system call; c is NUL-terminated" {
        flags := ORdOnly | ODirectory | OCloExec
        r := Syscall(Nrs().openat, AtFdcwd, raw.Addr(raw.Data(c[..])), flags, 0, 0, 0)
        return Result(r)
    }
}

// CNameLen is the length of the NUL-terminated name at nm, at most max.
CNameLen(nm: *u8, max: int): int {
    var n := 0
    unsafe "nm holds max bytes" {
        for n < max && raw.Read(raw.Offset(nm, n)) != 0 {
            n += 1
        }
    }
    return n
}

// DirKindOnly marks block, a zeroed DirBlockBytes stream block before its
// first fill, as read for kinds alone: DirFill then takes each entry's
// kind from d_type, with size and time 0, and makes a statx only for an
// entry whose d_type is DT_UNKNOWN (0; a file system without d_type). An
// empty block (none was made) is left alone. O(1); allocates nothing.
DirKindOnly(block: mut []u8) {
    if block.Len() < DirBlockBytes {
        return
    }
    unsafe "block holds DirBlockBytes bytes" {
        Put64(raw.DataMut(block), 24, 1)
    }
}

// IsDots: the n-byte name at nm is . or ..
IsDots(nm: *u8, n: int): bool {
    unsafe "nm holds n bytes" {
        return (n == 1 && raw.Read(nm) == 46) || (n == 2 && raw.Read(nm) == 46 && raw.Read(raw.Offset(nm, 1)) == 46)
    }
}

/// DirFill makes the next entries of the stream h ready in block, the
/// stream's DirBlockBytes block (zero when the stream opened): the only
/// part of a listing that makes system calls, so std.task runs it on the
/// blocking pool, once per block rather than once per entry.
/// When every record read so far is used, one getdents64 refills the block
/// (its facts zeroed); then one statx(AT_SYMLINK_NOFOLLOW) for every record
/// from the cursor on whose facts are not read yet, so a symbolic link is
/// IoKindLink (Rust's DirEntry::metadata makes the same call). value: 1
/// when entries are ready, 0 at the end. An entry removed between
/// getdents64 and its statx (ENOENT) is marked to be skipped, as Go's
/// ReadDir skips it; any other statx failure (EACCES in a directory that can
/// be read but not searched, EIO on one entry) keeps the listing going with
/// the kind from the record's d_type, size and time 0 (DtypeInto). EINTR
/// from a statx is returned with the records and the facts read so far
/// kept, so the pool's repeat resumes at the next entry. A stream read for
/// kinds alone (DirKindOnly) takes each kind from d_type, size and time 0,
/// and makes the statx only for a record whose d_type is DT_UNKNOWN.
/// One getdents64 per refill and one statx per new entry (per new
/// DT_UNKNOWN entry for kinds alone); allocates nothing
/// (block is the stream's).
export DirFill(h: uint, block: mut []u8): OsResult {
    if block.Len() < DirBlockBytes {
        return FailWith(core.SysErr.Other)
    }
    unsafe "block holds DirBlockBytes bytes: the head, the records the kernel wrote, the facts" {
        b := raw.DataMut(block)
        data := raw.Offset(b, DirHead)
        facts := raw.Offset(b, DirFacts)
        if Get64(b, 16) == 2 {
            return Ok(0)
        }
        var pos := Trunc[int](Get64(b, 0))
        var fill := Trunc[int](Get64(b, 8))
        if pos >= fill {
            _ = TestPoint(12)
            r := Syscall(Nrs().getdents64, h, raw.Addr(data), Trunc[uint](DirFacts - DirHead), 0, 0, 0)
            if IsErr(r) {
                return Result(r)
            }
            if r == 0 {
                Put64(b, 16, 2)
                return Ok(0)
            }
            raw.Fill(facts, 0, DirBlockBytes - DirFacts)
            Put64(b, 0, 0)
            Put64(b, 8, Trunc[u64](i64(r)))
            pos = 0
            fill = r
        }
        var st := core.IoStat(kind: 0, size: 0, modified: 0)
        kinds := Get64(b, 24) == 1
        for pos < fill {
            rec := raw.Offset(data, pos)
            reclen := int(Get16(rec, 16))
            if reclen < DirSlot || pos + reclen > fill {
                // A record the kernel never writes: DirNext reports it.
                break
            }
            slot := raw.Offset(facts, (pos / DirSlot) * DirSlot)
            nm := raw.Offset(rec, 19)
            t := raw.Read(raw.Offset(rec, 18))
            if Get32(slot, 0) == 0 && kinds && t != 0 {
                // Read for kinds alone: d_type is the kind.
                DtypeInto(t, mut st)
                Put32(slot, 0, st.kind)
            } else if Get32(slot, 0) == 0 && !IsDots(nm, CNameLen(nm, reclen - 19)) {
                _ = TestPoint(13)
                s := StatInto(h, nm, AtSymlinkNoFollow | AtNoAutomount, mut st)
                if s == -4 {
                    return Result(s)
                }
                if s == -2 {
                    Put32(slot, 0, DirSkip)
                } else {
                    if s != 0 {
                        DtypeInto(raw.Read(raw.Offset(rec, 18)), mut st)
                    }
                    Put64(slot, 8, Trunc[u64](st.size))
                    Put64(slot, 16, Trunc[u64](st.modified))
                    Put32(slot, 0, st.kind)
                }
            }
            pos += reclen
        }
        return Ok(1)
    }
}

/// DirNext serves the next entry already in block (DirFill's), with no
/// system call, into name and st. value: the name's length; 0 at the end;
/// -1 when the block holds no ready entry (call DirFill, then DirNext
/// again). . and .. are skipped, and so is an entry DirFill marked gone. A
/// name longer than name is NoSpace with value the length it needs; the
/// entry stays next. name and st are written only for an entry.
/// O(len) of the name; allocates nothing.
export DirNext(block: mut []u8, name: mut []u8, st: mut core.IoStat): OsResult {
    if block.Len() < DirBlockBytes {
        return FailWith(core.SysErr.Other)
    }
    unsafe "block holds DirBlockBytes bytes, laid out by DirFill" {
        b := raw.DataMut(block)
        data := raw.Offset(b, DirHead)
        facts := raw.Offset(b, DirFacts)
        for {
            if Get64(b, 16) == 2 {
                return Ok(0)
            }
            pos := Trunc[int](Get64(b, 0))
            fill := Trunc[int](Get64(b, 8))
            if pos >= fill {
                return Ok(-1)
            }
            rec := raw.Offset(data, pos)
            reclen := int(Get16(rec, 16))
            if reclen < DirSlot || pos + reclen > fill {
                // A record the kernel never writes: drop the rest of the
                // block rather than read past it.
                Put64(b, 0, Trunc[u64](i64(fill)))
                return FailWith(core.SysErr.Other)
            }
            nm := raw.Offset(rec, 19)
            n := CNameLen(nm, reclen - 19)
            slot := raw.Offset(facts, (pos / DirSlot) * DirSlot)
            k := Get32(slot, 0)
            if IsDots(nm, n) || k == DirSkip {
                Put64(b, 0, Trunc[u64](i64(pos + reclen)))
                continue
            }
            if k == 0 {
                // Facts not read yet: a DirFill that EINTR ended.
                return Ok(-1)
            }
            if n > name.Len() {
                return OsResult(value: i64(n), err: core.SysErr.NoSpace, os: 0)
            }
            raw.Copy(raw.DataMut(name), nm, n)
            st.kind = k
            st.size = Trunc[i64](Get64(slot, 8))
            st.modified = Trunc[i64](Get64(slot, 16))
            Put64(b, 0, Trunc[u64](i64(pos + reclen)))
            return Ok(i64(n))
        }
    }
}

// StatNanos is a statx timestamp, sec seconds and ns nanoseconds (0 to
// 999,999,999) since the epoch, as one i64 of nanoseconds saturated at its
// ends: a time past 2262 is the greatest, one before 1677 the least, so no
// time wraps to the other end. O(1).
StatNanos(sec: i64, ns: i64): i64 {
    if sec >= 9_223_372_036 {
        return if sec == 9_223_372_036 && ns <= 854_775_807 { sec * 1_000_000_000 + ns } else { 0x7FFF_FFFF_FFFF_FFFF }
    }
    if sec < -9_223_372_036 {
        return -0x7FFF_FFFF_FFFF_FFFF - 1
    }
    return sec * 1_000_000_000 + ns
}
