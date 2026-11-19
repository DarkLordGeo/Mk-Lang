// Why: std.fs needs directories made, listed and stat'ed on Windows, with
// names that survive the round trip even when they are not valid UTF-16.
//
// What: the bindings behind fs.Dir, fs.MakeDir and fs.Stat: MakeDir
// (CreateDirectoryW), StatPath, DirOpen, DirFill, DirNext, DirBlockBytes and
// DirFillOnOpen.
//
// How: a stat over a handle (Go's os.Stat: CreateFileW with
// BACKUP_SEMANTICS, then FileBasicInfo and FileStandardInfo), and a
// directory stream over GetFileInformationByHandleEx(FileFullDirectoryInfo)
// (Go's os.ReadDir), so a stream is an ordinary handle that CloseHandle
// closes. Names reach std as WTF-8: UTF-8, with an unpaired surrogate as its
// own three bytes, which every path this module takes decodes back (Wide),
// so a listed name reopens; Go's os.ReadDir gives the same bytes.

import std.core
import std.raw

/// DirBlockBytes is the size of the block a directory stream reads into: a
/// 32-byte head (the next record's offset at 0, the state at 8: 0 fresh, 1
/// records left, 2 the end, 3 the block used up; at 16 the units of the
/// held directory's path kept in the block, 0 for none, and at 20 where
/// that path starts, 0 for none; at 24 1 when the OS said the listing ends
/// after the block's records), then FILE_FULL_DIR_INFO records, which carry
/// every fact IoStat holds, so DirNext makes no system call, up to the kept
/// path or the block's end. A stream removeat.mk's DirOpenAt opened keeps
/// at the block's end the path it was opened by, with a separator, when it
/// has at most DirPathMax units. Same value on both targets.
export const DirBlockBytes: int = 16384

/// DirFillOnOpen: std.rt's DirOpenAt makes a stream's first DirFill in the
/// open's own pool job, one blocking-pool hop fewer per directory listed.
/// A failed first fill leaves the block as
/// it was, so the stream's first read fills again and reports the error.
export const DirFillOnOpen: bool = true

const DirHead: int = 32
// DirPathMax is the most units of a held directory's path a block keeps
// (removeat.mk DirKeep), so the records keep at least 12,248 bytes; a
// longer path is read from the OS at each call, as without a kept one.
const DirPathMax: int = 2048
// DirRecMax is the size of the largest FILE_FULL_DIR_INFO record, 68
// bytes and a name of 255 units (every Windows file system's limit),
// rounded to 8: DirMore asks the OS again only into a tail that holds one,
// since a call whose buffer cannot hold the next entry can lose it (in a
// probe by the Mk authors on NTFS, an 80-byte call returned "." and the
// next full call no "..").
const DirRecMax: int = 584
const FileListDirectory: u32 = 1
const FileReadAttributes: u32 = 0x80
const FileFullDirectoryInfo: u32 = 14
const AttributeReparse: u32 = 0x400
const TagSymlink: u32 = 0xA000_000C
const TagMountPoint: u32 = 0xA000_0003
// SYMLINK_FLAG_RELATIVE in a symbolic link's reparse data.
const SymlinkRelative: u32 = 1
// FILETIME of the Unix epoch, in 100 ns units since 1601.
const UnixEpoch: i64 = 116_444_736_000_000_000

/// MakeDir makes the directory p. Anything already at p is Exists
/// (ERROR_ALREADY_EXISTS), as Go's os.Mkdir; a missing parent is NotFound
/// (ERROR_PATH_NOT_FOUND).
/// O(1) system calls; the path converts to UTF-16 in a stack buffer, or,
/// from 248 units on, in pages mapped for the call and given back.
export MakeDir(p: str): OsResult {
    var small: [1024]u16 = [0; 1024]
    w := WidePath(p, mut small[..])
    if w.err != core.SysErr.None {
        return FailWith(w.err)
    }
    var r := Ok(0)
    unsafe "kernel32; w.p is NUL-terminated" {
        if CreateDirectoryW(w.p, raw.Null[u8]()) == 0 {
            r = LastError()
        }
    }
    FreePath(w)
    return r
}

// UnixNanos turns a FILETIME (100 ns units since 1601) into nanoseconds
// since the Unix epoch, saturated at i64's ends: a time before 1677 (a
// FILETIME of 0 included) is the least, one past 2262 (or a FILETIME with
// its top bit set) the greatest, so no time wraps to the other end.
// O(1).
UnixNanos(ft: i64): i64 {
    if ft < 0 {
        return 0x7FFF_FFFF_FFFF_FFFF
    }
    d := ft - UnixEpoch
    if d > 92_233_720_368_547_758 {
        return 0x7FFF_FFFF_FFFF_FFFF
    }
    if d < -92_233_720_368_547_758 {
        return -0x7FFF_FFFF_FFFF_FFFF - 1
    }
    return d * 100
}

// LinkTag: a reparse point with this tag is a symbolic link or a junction
// (Go's rule); other reparse points (cloud files, dedup) are what they hold.
LinkTag(attr: u32, tag: u32): bool {
    return (attr & AttributeReparse) != 0 && (tag == TagSymlink || tag == TagMountPoint)
}

/// StatPath fills st for p, following symbolic links: the handle is opened
/// on the target, so kind is never IoKindLink here. First, for a path that
/// AttributesStep (call.mk) allows, StatEx's first step, StatExAttributes:
/// one GetFileAttributesExW, which opens no handle (Go's os.Stat,
/// golang.org/issue/19922), answering for an entry that is no reparse point
/// and for a missing file or path (2, 3), the error CreateFileW gives; a
/// UNC, device or \??\ path (a named pipe, which that call would open and
/// take an instance of, a mailslot), a link or junction, which must be
/// followed, a device such as NUL, a zero last-write time (a named pipe
/// reached through a link) and a file the system holds, as pagefile.sys,
/// take the handle path below.
/// O(1) system calls; the path converts to UTF-16 in a stack buffer, or,
/// from 248 units on, in pages mapped for the call and given back.
export StatPath(p: str, st: mut core.IoStat): OsResult {
    var small: [1024]u16 = [0; 1024]
    w := WidePath(p, mut small[..])
    if w.err != core.SysErr.None {
        return FailWith(w.err)
    }
    var r := Ok(0)
    if AttributesStep(p.AsBytes()) {
        var words := core.IoWords(a: 0, b: 0, c: 0, d: 0)
        if a := StatExAttributes(w.p, mut words) {
            if a.err == core.SysErr.None {
                st.kind = Trunc[u32](words.a)
                st.size = words.b
                st.modified = words.c
            }
            FreePath(w)
            return a
        }
    }
    unsafe "kernel32; w.p is NUL-terminated" {
        h := CreateFileW(w.p, FileReadAttributes, ShareAll, raw.Null[u8](), OpenExisting, FlagBackupSemantics, 0)
        if h == InvalidHandle {
            r = LastError()
        } else {
            r = StatHandle(h, st)
            _ = CloseHandle(h)
        }
    }
    FreePath(w)
    return r
}

// StatHandle fills st from FILE_BASIC_INFO (LastWriteTime at 16,
// FileAttributes at 32) and FILE_STANDARD_INFO (EndOfFile at 8).
StatHandle(h: uint, st: mut core.IoStat): OsResult {
    var basic: [5]u64 = [0; 5]
    var stdi: [3]u64 = [0; 3]
    unsafe "kernel32; basic holds 40 bytes, std 24" {
        b := raw.PtrCast[u64, u8](raw.DataMut(mut basic[..]))
        s := raw.PtrCast[u64, u8](raw.DataMut(mut stdi[..]))
        if GetFileInformationByHandleEx(h, 0, b, 40) == 0 {
            return LastError()
        }
        if GetFileInformationByHandleEx(h, 1, s, 24) == 0 {
            return LastError()
        }
        attr := Get32(b, 32)
        st.kind = core.IoKindFile
        st.size = Trunc[i64](Get64(s, 8))
        if (attr & AttributeDirectory) != 0 {
            st.kind = core.IoKindDir
            st.size = 0
        } else if GetFileType(h) != 1 {
            st.kind = core.IoKindOther
        }
        st.modified = UnixNanos(Trunc[i64](Get64(b, 16)))
    }
    return Ok(0)
}

/// DirOpen opens p as a directory stream: the handle. A file is
/// NotDirectory (ERROR_DIRECTORY, 267), as ENOTDIR on Linux.
/// O(1) system calls; the path converts to UTF-16 in a stack buffer, or,
/// from 248 units on, in pages mapped for the call and given back; rt
/// allocates the stream's block.
export DirOpen(p: str): OsResult {
    var small: [1024]u16 = [0; 1024]
    w := WidePath(p, mut small[..])
    if w.err != core.SysErr.None {
        return FailWith(w.err)
    }
    var r := Ok(0)
    unsafe "kernel32; w.p is NUL-terminated" {
        h := CreateFileW(w.p, FileListDirectory, ShareAll, raw.Null[u8](), OpenExisting, FlagBackupSemantics, 0)
        if h == InvalidHandle {
            r = LastError()
        } else if !IsDirHandle(h) {
            _ = CloseHandle(h)
            r = Fail(267)
        } else {
            r = Ok(Trunc[i64](h))
        }
    }
    FreePath(w)
    return r
}

// NameLen is the UTF-8 length of units UTF-16 units at p, and Narrows
// writes them to out (which holds at least that many bytes), as WTF-8. A
// high surrogate is paired only with a unit inside the name.
NameLen(p: *u16, units: int): int {
    var n := 0
    var i := 0
    for i < units {
        (c, w) := NameScalar(p, units, i)
        if c < 0x80 {
            n += 1
        } else if c < 0x800 {
            n += 2
        } else if c < 0x10000 {
            n += 3
        } else {
            n += 4
        }
        i += w
    }
    return n
}

NameScalar(p: *u16, units: int, i: int): (c: u32, w: int) {
    unsafe "i < units; p holds units units" {
        u := u32(raw.Read(raw.Offset(p, i)))
        if u >= 0xD800 && u < 0xDC00 && i + 1 < units {
            v := u32(raw.Read(raw.Offset(p, i + 1)))
            if v >= 0xDC00 && v < 0xE000 {
                return (c: 0x10000 + ((u - 0xD800) << 10) + (v - 0xDC00), w: 2)
            }
        }
        // An unpaired surrogate is itself: PutScalar writes it as three
        // bytes (WTF-8), which Wide turns back into the same unit.
        return (c: u, w: 1)
    }
}

Narrows(p: *u16, units: int, out: mut []u8) {
    var n := 0
    var i := 0
    for i < units {
        (c, w) := NameScalar(p, units, i)
        n = PutScalar(out, n, c)
        i += w
    }
}

// IsDots: the name is . or ..
IsDots(p: *u16, units: int): bool {
    unsafe "p holds units units" {
        if units == 1 {
            return raw.Read(p) == 46
        }
        return units == 2 && raw.Read(p) == 46 && raw.Read(raw.Offset(p, 1)) == 46
    }
}

/// DirFill makes the next entries of the stream h ready in block, the
/// stream's DirBlockBytes block (zero when the stream opened, but for a
/// path DirOpenAt kept): the only part of a listing that makes system
/// calls, so std.task runs it on the blocking pool, once per block rather
/// than once per entry.
/// When the block is used up, one GetFileInformationByHandleEx
/// (FileFullDirectoryInfo) refills it; while the records leave room for
/// another whole record, the same job asks again into that room (DirMore),
/// so the listing's end, ERROR_NO_MORE_FILES, is found by the fill that
/// read its last entries, and the stream ends there without another fill.
/// value: 1 when entries are ready, 0 at the end. The contract is
/// Linux's.
/// One system call per refill, and one more when it leaves room for a
/// record (the end, on every file system that fills the buffer it is
/// given); allocates nothing (block is the stream's).
export DirFill(h: uint, block: mut []u8): OsResult {
    if block.Len() < DirBlockBytes {
        return FailWith(core.SysErr.Other)
    }
    unsafe "block holds DirBlockBytes bytes: the head, then the records Windows wrote" {
        b := raw.DataMut(block)
        data := raw.Offset(b, DirHead)
        state := Get64(b, 8)
        if state == 2 {
            return Ok(0)
        }
        if state == 1 {
            return Ok(1)
        }
        cap := RecEnd(b) - DirHead
        _ = TestPoint(12)
        // Test point 14 makes the first ask short, so DirMore chains.
        first := if TestPoint(14) { 1024 } else { cap }
        if GetFileInformationByHandleEx(h, FileFullDirectoryInfo, data, Trunc[u32](first)) == 0 {
            e := GetLastError()
            // ERROR_NO_MORE_FILES ends a listing; on the first call
            // ERROR_FILE_NOT_FOUND does too: an empty volume root has no .
            // or .. entry (MS-FSA 2.1.5.6.3, STATUS_NO_SUCH_FILE; Go's
            // os/dir_windows.go).
            if e == 18 || (e == 2 && state == 0) {
                Put64(b, 8, 2)
                return Ok(0)
            }
            return Fail(e)
        }
        Put64(b, 0, 0)
        Put64(b, 8, 1)
        DirMore(h, b, cap)
        return Ok(1)
    }
}

// RecEnd is where the records of block b end: at the path DirOpenAt kept
// there (head word 20), or at the block's end.
RecEnd(b: *u8): int {
    s := int(Get32(b, 20))
    return if s == 0 { DirBlockBytes } else { s }
}

// DirMore asks the OS for more records, in DirFill's job, while the free
// tail of the cap bytes of records b holds DirRecMax: the records that
// come back are chained after the last (its NextEntryOffset), and
// ERROR_NO_MORE_FILES marks the block the listing's last (head word 24),
// so DirStep ends the stream after its last record. Any other answer, or a
// chain DirNext would refuse, marks nothing: the next fill asks again and
// reports an error (an ERROR_MORE_DATA keeps the entry for the next call,
// the probe above).
DirMore(h: uint, b: *u8, cap: int) {
    unsafe "b is a DirBlockBytes block whose first cap bytes after the head hold the records DirFill read" {
        data := raw.Offset(b, DirHead)
        var last := 0
        for {
            for {
                next := int(Get32(raw.Offset(data, last), 0))
                if next == 0 {
                    break
                }
                if last + next + 68 > cap {
                    return
                }
                last += next
            }
            end := last + 68 + int(Get32(raw.Offset(data, last), 60))
            off := (end + 7) & ~7
            if end > cap || cap - off < DirRecMax {
                return
            }
            if GetFileInformationByHandleEx(h, FileFullDirectoryInfo, raw.Offset(data, off), Trunc[u32](cap - off)) == 0 {
                if GetLastError() == 18 {
                    Put64(b, 24, 1)
                }
                return
            }
            Put32(raw.Offset(data, last), 0, Trunc[u32](off - last))
            last = off
        }
    }
}

/// DirNext serves the next entry already in block (DirFill's), with no
/// system call, into name and st: value the name's length, 0 at the end, -1
/// when the block holds no ready entry (call DirFill, then DirNext again);
/// . and .. skipped; NoSpace with the length needed, the entry staying next;
/// st the entry's own facts (a symbolic link or junction is IoKindLink).
/// name and st are written only for an entry.
/// O(len) of the name; allocates nothing.
export DirNext(block: mut []u8, name: mut []u8, st: mut core.IoStat): OsResult {
    if block.Len() < DirBlockBytes {
        return FailWith(core.SysErr.Other)
    }
    unsafe "block holds DirBlockBytes bytes, laid out by DirFill" {
        b := raw.DataMut(block)
        data := raw.Offset(b, DirHead)
        cap := RecEnd(b) - DirHead
        for {
            state := Get64(b, 8)
            if state == 2 {
                return Ok(0)
            }
            if state != 1 {
                return Ok(-1)
            }
            pos := Trunc[int](Get64(b, 0))
            rec := raw.Offset(data, pos)
            next := int(Get32(rec, 0))
            units := int(Get32(rec, 60)) / 2
            if pos + 68 + units * 2 > cap || (next != 0 && pos + next >= cap) {
                Put64(b, 8, 2)
                return FailWith(core.SysErr.Other)
            }
            np := raw.PtrCast[u8, u16](raw.Offset(rec, 68))
            if IsDots(np, units) {
                DirStep(b, pos, next)
                continue
            }
            n := NameLen(np, units)
            if n > name.Len() {
                return OsResult(value: i64(n), err: core.SysErr.NoSpace, os: 0)
            }
            Narrows(np, units, name)
            attr := Get32(rec, 56)
            st.size = Trunc[i64](Get64(rec, 40))
            st.modified = UnixNanos(Trunc[i64](Get64(rec, 24)))
            if LinkTag(attr, Get32(rec, 64)) {
                st.kind = core.IoKindLink
                st.size = 0
            } else if (attr & AttributeDirectory) != 0 {
                st.kind = core.IoKindDir
                st.size = 0
            } else {
                st.kind = core.IoKindFile
            }
            DirStep(b, pos, next)
            return Ok(i64(n))
        }
    }
}

// DirStep moves the cursor past the record at pos: to the next one, or,
// when it was the last, to the listing's end if DirMore found it (head
// word 24), else to an empty block.
DirStep(b: *u8, pos: int, next: int) {
    if next == 0 {
        Put64(b, 8, if Get64(b, 24) == 1 { 2 } else { 3 })
    } else {
        Put64(b, 0, Trunc[u64](pos + next))
    }
}
