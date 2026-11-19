// Why: fs.RemoveAll and fs.Walk must remove and descend a tree without
// following a link out of it and without a path that another process can
// swap under them between the check and the delete.
//
// What: the Windows primitives: NameAt, DirOpenAt, RemoveAt and
// NotEmptyCode, with AtPath and DirKeep, which build an entry's path.
//
// How: a directory opened inside a held one, and an entry removed by a
// POSIX-semantics delete through a handle opened on the entry itself
// (FileDispositionInfoEx with DELETE, POSIX_SEMANTICS and
// IGNORE_READONLY_ATTRIBUTE: the name goes at once even while another
// handle is open, a read-only file goes as Go's os.RemoveAll removes it,
// and a link or junction goes itself, never its target: the handle is
// opened with OPEN_REPARSE_POINT). kernel32 has no open relative to a
// handle (NtCreateFile is ntdll's), so an entry's path is the held
// directory's path (the \\?\ form) and the entry's name, built in UTF-16.
// A directory DirOpenAt opened keeps its path in its stream's block, read
// once (DirKeep): the path the open used, or for a whole path its
// final path; any other held directory (the open-dir entry's, which pins
// nothing) is asked GetFinalPathNameByHandleW at each call. What keeps a
// kept path from naming another place, measured on NTFS (Windows 11
// 10.0.26300): a directory DirOpenAt holds is opened without
// FILE_SHARE_DELETE, so it cannot be renamed or replaced (sharing
// violation), and NTFS refuses to rename any directory above an open
// handle (access denied), so no ancestor can move while a descendant is
// held. Each call is a few system calls on the pool; every handle opened
// is closed on every path.

import std.core
import std.raw

const DeleteAccess: u32 = 0x0001_0000
const WriteAttributesAccess: u32 = 0x100
const ReadAttributesAt: u32 = 0x80
const ShareNoDelete: u32 = 3
const FlagOpenReparse: u32 = 0x0020_0000
const InfoDisposition: u32 = 4
const InfoAttributeTag: u32 = 9
const InfoDispositionEx: u32 = 21
// FILE_DISPOSITION_FLAG_DELETE | POSIX_SEMANTICS | IGNORE_READONLY_ATTRIBUTE.
const DispositionFlags: u32 = 0x1 | 0x2 | 0x10
const AttributeReadOnlyAt: u32 = 0x1

/// NotEmptyCode is the os code of a directory that is not empty
/// (ERROR_DIR_NOT_EMPTY).
export const NotEmptyCode: i64 = 145

/// NameAt checks name for an *At call: with a held directory (dir: true) it
/// must be one entry's name: not empty, not . or .., and no '/', '\', ':'
/// (a stream) or NUL, since the name is joined to the held directory's
/// path and must not leave it; with none (a whole path) only not empty.
/// false is InvalidPath, refused before any system call. O(len); allocates
/// nothing.
export NameAt(name: []u8, dir: bool): bool {
    if name.Len() == 0 {
        return false
    }
    if !dir {
        return true
    }
    if name.Len() == 1 && name[0] == 46 {
        return false
    }
    if name.Len() == 2 && name[0] == 46 && name[1] == 46 {
        return false
    }
    for x in name {
        if x == 47 || x == 92 || x == 58 || x == 0 {
            return false
        }
    }
    return true
}

// AtPath is the UTF-16 path of name inside the held directory dir (0: name
// is a whole path, WidePath's form), in small or in pages FreePath gives
// back. The directory's part is the path held keeps (dir's stream block,
// DirKeep's) when it keeps one; else it comes from
// GetFinalPathNameByHandleW. Either is UTF-16, never turned into bytes, so
// a directory whose own name has an unpaired surrogate still works. err
// InvalidPath for a name NameAt refuses or that is not WTF-8; Other when
// pages cannot be had.
AtPath(dir: uint, held: []u8, name: []u8, small: mut []u16): WPath {
    var r := WPath(p: raw.Null[u16](), map: raw.Null[u8](), bytes: 0, err: core.SysErr.InvalidPath)
    if !NameAt(name, dir != 0) {
        return r
    }
    if dir == 0 {
        return WidePath(PathOf(name), small)
    }
    if held.Len() >= DirBlockBytes {
        unsafe "held is dir's DirBlockBytes block" {
            b := raw.Data(held)
            n := int(Get32(b, 16))
            if n > 0 {
                return AtKept(raw.PtrCast[u8, u16](raw.Offset(b, int(Get32(b, 20)))), n, name, small)
            }
        }
    }
    unsafe "kernel32; small or the pages hold cap units" {
        var buf := raw.DataMut(small)
        var cap := small.Len()
        // The directory's units, the separator, a unit per name byte, NUL.
        _ = TestPoint(11)
        var n := Trunc[int](GetFinalPathNameByHandleW(dir, buf, Trunc[u32](cap), 0))
        if n == 0 {
            return FailPath(GetLastError())
        }
        if n + 2 + name.Len() > cap {
            // n is the size it needs, its NUL included; one more try in
            // pages (the directory cannot be renamed while held, but its
            // final path is read again anyway).
            if n > MaxUnits {
                return r
            }
            bytes := PageRound((n + 2 + name.Len()) * 2)
            map := Reserve(bytes, 4096)
            if raw.IsNull(map) || !Commit(map, bytes) {
                FreeMap(map, bytes)
                r.err = core.SysErr.Other
                return r
            }
            buf = raw.PtrCast[u8, u16](map)
            cap = bytes / 2
            r.map = map
            r.bytes = bytes
            _ = TestPoint(11)
            n = Trunc[int](GetFinalPathNameByHandleW(dir, buf, Trunc[u32](cap), 0))
            if n == 0 || n + 2 + name.Len() > cap {
                e := GetLastError()
                FreeMap(map, bytes)
                return if n == 0 { FailPath(e) } else { WPath(p: raw.Null[u16](), map: raw.Null[u8](), bytes: 0, err: core.SysErr.Other) }
            }
        }
        // A volume root's final path ends with its separator.
        if raw.Read(raw.Offset(buf, n - 1)) != 92 {
            raw.Write(raw.Offset(buf, n), 92)
            n += 1
        }
        k := Wide(PathOf(name), raw.SliceMut(raw.Offset(buf, n), cap - n))
        if k <= 0 {
            FreeMap(r.map, r.bytes)
            return WPath(p: raw.Null[u16](), map: raw.Null[u8](), bytes: 0, err: core.SysErr.InvalidPath)
        }
        r.p = buf
        r.err = core.SysErr.None
        return r
    }
}

// AtKept is AtPath's path of name after the n units at kept, a held
// directory's path with its separator (DirKeep's), in small or in pages.
AtKept(kept: *u16, n: int, name: []u8, small: mut []u16): WPath {
    var r := WPath(p: raw.Null[u16](), map: raw.Null[u8](), bytes: 0, err: core.SysErr.InvalidPath)
    unsafe "kept holds n units; small or the pages hold cap units" {
        var buf := raw.DataMut(small)
        var cap := small.Len()
        // The directory's units, a unit per name byte, NUL.
        if n + name.Len() + 1 > cap {
            bytes := PageRound((n + name.Len() + 1) * 2)
            map := Reserve(bytes, 4096)
            if raw.IsNull(map) || !Commit(map, bytes) {
                FreeMap(map, bytes)
                r.err = core.SysErr.Other
                return r
            }
            buf = raw.PtrCast[u8, u16](map)
            cap = bytes / 2
            r.map = map
            r.bytes = bytes
        }
        for i in 0..n {
            raw.Write(raw.Offset(buf, i), raw.Read(raw.Offset(kept, i)))
        }
        k := Wide(PathOf(name), raw.SliceMut(raw.Offset(buf, n), cap - n))
        if k <= 0 {
            FreeMap(r.map, r.bytes)
            return WPath(p: raw.Null[u16](), map: raw.Null[u8](), bytes: 0, err: core.SysErr.InvalidPath)
        }
        r.p = buf
        r.err = core.SysErr.None
        return r
    }
}

// DirKeep keeps in fresh, the zeroed DirBlockBytes block of the stream h
// that DirOpenAt just opened, the path of h with a separator, at the
// block's end (dir.mk's layout), so every *At call inside h builds its
// path from it with no system call. The path is the one the open
// used (w) when the held parent dir keeps a path itself (held, its block,
// keeps one: a stream this function kept, so dir is pinned and so is
// every directory above it): its kept path and one name, which names no
// link (the open checked it is a directory, not a reparse point).
// Otherwise it is h's own final path (GetFinalPathNameByHandleW, once):
// for a whole path (dir 0), which may name links above it or be relative
// to a working directory that can change, and for a parent that keeps no
// path (the open-dir entry's handle, which shares delete and so pins
// nothing, or one past DirPathMax), whose final path, read before the
// open, may name another place by the time the open ran (RemoveAll's
// root). The header's pin keeps either naming h while it is
// held: h cannot be renamed, nor any directory above it. A path past
// DirPathMax units is not kept. Written before the stream is filled or
// returned, and never again.
DirKeep(fresh: mut []u8, h: uint, dir: uint, held: []u8, w: WPath) {
    if fresh.Len() < DirBlockBytes {
        return
    }
    unsafe "fresh is a zeroed DirBlockBytes block no one else uses yet; w.p is NUL-terminated" {
        b := raw.DataMut(fresh)
        // The path is read into the records' area first, which DirPathMax
        // + 1 units leave apart from the block's end.
        var src := w.p
        var n := 0
        // The parent keeps a path (its block's word 16), so w was built
        // from it under the pin.
        var pinned := false
        if dir != 0 && held.Len() >= DirBlockBytes {
            pinned = Get32(raw.Data(held), 16) != 0
        }
        if !pinned {
            src = raw.PtrCast[u8, u16](raw.Offset(b, DirHead))
            _ = TestPoint(11)
            n = Trunc[int](GetFinalPathNameByHandleW(h, src, Trunc[u32](DirPathMax), 0))
            if n == 0 || n >= DirPathMax {
                return
            }
        } else {
            for n <= DirPathMax && raw.Read(raw.Offset(src, n)) != 0 {
                n += 1
            }
        }
        if n == 0 || n >= DirPathMax {
            return
        }
        sep := raw.Read(raw.Offset(src, n - 1)) != 92
        units := if sep { n + 1 } else { n }
        start := DirBlockBytes - ((units * 2 + 7) & ~7)
        dst := raw.PtrCast[u8, u16](raw.Offset(b, start))
        for i in 0..n {
            raw.Write(raw.Offset(dst, i), raw.Read(raw.Offset(src, i)))
        }
        if sep {
            raw.Write(raw.Offset(dst, n), 92)
        }
        Put32(b, 16, Trunc[u32](units))
        Put32(b, 20, Trunc[u32](start))
    }
}

// TagOf reads h's attributes and reparse tag (FILE_ATTRIBUTE_TAG_INFO):
// the kind it is as a directory entry, IoKindLink for a symbolic link or
// junction (dir.mk LinkTag); 0 when the read fails.
TagOf(h: uint): u32 {
    var info: [1]u64 = [0; 1]
    unsafe "kernel32; info holds the 8-byte record" {
        b := raw.PtrCast[u64, u8](raw.DataMut(mut info[..]))
        if GetFileInformationByHandleEx(h, InfoAttributeTag, b, 8) == 0 {
            return 0
        }
        attr := Get32(b, 0)
        if LinkTag(attr, Get32(b, 4)) {
            return core.IoKindLink
        }
        return if (attr & AttributeDirectory) != 0 { core.IoKindDir } else { core.IoKindFile }
    }
}

/// DirOpenAt opens the directory name inside the held directory dir (0:
/// name is a whole path) for listing: CreateFileW with FILE_LIST_DIRECTORY,
/// BACKUP_SEMANTICS and OPEN_REPARSE_POINT, shared for reading and writing
/// but not deleting (the header's pin); value is the handle. A symbolic
/// link or junction, and a file, are NotDirectory (the link is never
/// followed); a directory another process holds open for deleting is
/// Denied (ERROR_SHARING_VIOLATION); InvalidPath as AtPath; else
/// MapError's. held is dir's stream block when the caller has it (empty
/// otherwise), whose kept path AtPath uses; fresh is the new stream's
/// zeroed block (empty when there is none), in which DirKeep keeps the
/// path of the directory opened. O(1) system calls (no
/// GetFinalPathNameByHandleW inside a dir with a kept path; else one for
/// the new handle's own path, and one more for a dir without a kept path,
/// whose path the open is built from); the path is built in a stack buffer, or
/// in pages mapped for the call and given back.
export DirOpenAt(dir: uint, held: []u8, name: []u8, fresh: mut []u8): OsResult {
    var small: [1024]u16 = [0; 1024]
    w := AtPath(dir, held, name, mut small[..])
    if w.err != core.SysErr.None {
        return FailWith(w.err)
    }
    var r := Ok(0)
    unsafe "kernel32; w.p is NUL-terminated" {
        h := CreateFileW(w.p, FileListDirectory | ReadAttributesAt, ShareNoDelete, raw.Null[u8](), OpenExisting,
            FlagBackupSemantics | FlagOpenReparse, 0)
        if h == InvalidHandle {
            r = LastError()
        } else {
            k := TagOf(h)
            if k == core.IoKindDir {
                DirKeep(fresh, h, dir, held, w)
                r = Ok(Trunc[i64](h))
            } else {
                _ = CloseHandle(h)
                r = if k == 0 { FailWith(core.SysErr.Other) } else { OsResult(value: 0, err: core.SysErr.NotDirectory, os: 267) }
            }
        }
    }
    FreePath(w)
    return r
}

/// RemoveAt removes the entry name inside the held directory dir (0: name
/// is a whole path): opened with DELETE and OPEN_REPARSE_POINT (a link or
/// junction is removed itself), then the POSIX-semantics delete of the
/// header; on a file system without it (ERROR_INVALID_PARAMETER,
/// ERROR_INVALID_FUNCTION, ERROR_NOT_SUPPORTED: FAT, before Windows 10
/// 1709) the classic delete, after clearing a read-only attribute.
/// is_dir must match: a directory with is_dir false is IsDirectory, and
/// anything else with is_dir true NotDirectory (Linux's EISDIR and ENOTDIR;
/// a link counts as not a directory). A directory that is not empty is
/// Other with os ERROR_DIR_NOT_EMPTY 145 (NotEmptyCode); a directory held
/// by DirOpenAt cannot be removed until its handle is closed (Denied,
/// ERROR_SHARING_VIOLATION); InvalidPath as AtPath; else MapError's.
/// held as DirOpenAt's. O(1) system calls; the path as DirOpenAt's.
export RemoveAt(dir: uint, held: []u8, name: []u8, is_dir: bool): OsResult {
    var small: [1024]u16 = [0; 1024]
    w := AtPath(dir, held, name, mut small[..])
    if w.err != core.SysErr.None {
        return FailWith(w.err)
    }
    var r := Ok(0)
    unsafe "kernel32; w.p is NUL-terminated" {
        h := CreateFileW(w.p, DeleteAccess | ReadAttributesAt, ShareAll, raw.Null[u8](), OpenExisting,
            FlagBackupSemantics | FlagOpenReparse, 0)
        if h == InvalidHandle {
            r = LastError()
        } else {
            r = DeleteHandle(h, w.p, is_dir)
            _ = CloseHandle(h)
        }
    }
    FreePath(w)
    return r
}

// DeleteHandle deletes the entry open as h at p (RemoveAt's body).
DeleteHandle(h: uint, p: *u16, is_dir: bool): OsResult {
    k := TagOf(h)
    if k == 0 {
        return FailWith(core.SysErr.Other)
    }
    if (k == core.IoKindDir) != is_dir {
        return FailWith(if is_dir { core.SysErr.NotDirectory } else { core.SysErr.IsDirectory })
    }
    var flags: u32 = DispositionFlags
    unsafe "kernel32; flags is the 4-byte FILE_DISPOSITION_INFO_EX" {
        if SetFileInformationByHandle(h, InfoDispositionEx, raw.PtrCast[u32, u8](raw.OfMut(mut flags)), 4) != 0 {
            return Ok(0)
        }
        e := GetLastError()
        if e != 87 && e != 1 && e != 50 {
            return Fail(e)
        }
        var del: u8 = 1
        if SetFileInformationByHandle(h, InfoDisposition, raw.OfMut(mut del), 1) != 0 {
            return Ok(0)
        }
        e2 := GetLastError()
        if e2 != 5 || !ClearReadOnly(p) {
            return Fail(e2)
        }
        if SetFileInformationByHandle(h, InfoDisposition, raw.OfMut(mut del), 1) != 0 {
            return Ok(0)
        }
        return LastError()
    }
}

// ClearReadOnly clears the read-only attribute of the entry at p (itself,
// not a link's target): true when it was set and is now clear.
ClearReadOnly(p: *u16): bool {
    unsafe "kernel32; p is NUL-terminated" {
        h := CreateFileW(p, WriteAttributesAccess | ReadAttributesAt, ShareAll, raw.Null[u8](), OpenExisting,
            FlagBackupSemantics | FlagOpenReparse, 0)
        if h == InvalidHandle {
            return false
        }
        // FILE_BASIC_INFO: four times at 0, 8, 16, 24 (0: unchanged), the
        // attributes at 32.
        var basic: [5]u64 = [0; 5]
        b := raw.PtrCast[u64, u8](raw.DataMut(mut basic[..]))
        var ok := false
        if GetFileInformationByHandleEx(h, FileBasicInfoClass, b, 40) != 0 {
            attr := Get32(b, 32)
            if (attr & AttributeReadOnlyAt) != 0 {
                Put64(b, 0, 0)
                Put64(b, 8, 0)
                Put64(b, 16, 0)
                Put64(b, 24, 0)
                left := attr & ~AttributeReadOnlyAt
                // 0 would mean "unchanged": NORMAL (0x80) stands for none.
                Put32(b, 32, if left == 0 { 0x80 } else { left })
                ok = SetFileInformationByHandle(h, FileBasicInfoClass, b, 40) != 0
            }
        }
        _ = CloseHandle(h)
        return ok
    }
}
