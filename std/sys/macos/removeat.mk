// Why: fs.RemoveAll must not follow a symlink or a rename out of the tree
// it deletes, so it cannot name entries by path.
//
// What: NameAt, DirOpenAt, RemoveAt and NotEmptyCode, the primitives of
// RemoveAll.
//
// How: openat with O_DIRECTORY | O_NOFOLLOW and unlinkat, both relative to
// a directory descriptor the caller holds, so each entry is named by its
// parent's descriptor (as Go's removeall_at.go does on Darwin). Each call
// is one system call on the pool, two when Darwin's unlinkat of a
// directory answers EPERM and an fstatat tells IsDirectory from a refusal.
// A removal inside a held stream lowers the count of entries its next
// refill skips (dir.mk reopens the directory at each refill).

import std.core
import std.raw

// FsAtRemoveDir is Darwin's AT_REMOVEDIR.
const FsAtRemoveDir: i32 = 0x80

/// NameAt checks name for an *At call: with a held directory (dir: true) it
/// must be one entry's name: not empty, not . or .., no '/' and no NUL;
/// with none (a whole path) only not empty. false is InvalidPath, refused
/// before any system call. O(len); allocates nothing.
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
        if x == 47 || x == 0 {
            return false
        }
    }
    return true
}

/// DirOpenAt opens the directory name inside the held directory dir (0:
/// name is a whole path from the working directory) for listing: openat
/// with O_DIRECTORY, O_NOFOLLOW and close-on-exec; value is the
/// descriptor. A link (ELOOP, or ENOTDIR from O_DIRECTORY on a link) and a
/// file are NotDirectory, so a link is never followed; InvalidPath for a
/// name NameAt refuses or one longer than PATH_MAX (1,024); else
/// MapError's. held (dir's stream block) is not used here; fresh, the new
/// stream's zeroed block when there is one, is marked to be read for kinds
/// alone (DirKindOnly): only fs.Walk and RemoveAll list such a stream.
/// O(1) system calls; allocates nothing (the name is copied into a stack
/// buffer).
export DirOpenAt(dir: uint, held: []u8, name: []u8, fresh: mut []u8): OsResult {
    _ = held
    var c: [1024]u8 = [0; 1024]
    if !NameAt(name, dir != 0) || !FsCBytes(name, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    base := if dir == 0 { FsAtFdcwd } else { Trunc[i32](dir) }
    var fd: i32 = -1
    unsafe "c is NUL-terminated" {
        fd = openat(base, raw.Data(c[..]), ORdOnly | ODirectory | ONoFollow | OCloExec)
    }
    if fd < 0 {
        e := Errno()
        if e == 62 || e == 20 {
            return OsResult(value: 0, err: core.SysErr.NotDirectory, os: e)
        }
        return Fail(e)
    }
    DirKindOnly(fresh)
    return Ok(i64(fd))
}

/// RemoveAt removes the entry name inside the held directory dir (0: name
/// is a whole path): unlinkat, with AT_REMOVEDIR when is_dir. A link is
/// removed itself, never its target. Errors: IsDirectory (os 21) when name
/// is a directory and is_dir is false (Darwin answers EPERM, and an
/// fstatat without following a link tells it from a refusal); NotDirectory
/// when is_dir and it is not one (ENOTDIR); a directory that is not empty
/// is Other with os ENOTEMPTY 66 (RemoveNotEmpty in std.rt says so on every
/// target); InvalidPath as DirOpenAt; else MapError's. held is dir's
/// stream block when dir is a stream: a removal inside it lowers the count
/// of entries the stream's next refill skips (DirFill reopens the
/// directory and skips the entries it used), so RemoveAll's removals while
/// it lists make the listing miss no entry. O(1) system calls; allocates
/// nothing.
export RemoveAt(dir: uint, held: []u8, name: []u8, is_dir: bool): OsResult {
    var c: [1024]u8 = [0; 1024]
    if !NameAt(name, dir != 0) || !FsCBytes(name, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    base := if dir == 0 { FsAtFdcwd } else { Trunc[i32](dir) }
    flags := if is_dir { FsAtRemoveDir } else { 0 }
    var r: i32 = 0
    unsafe "c is NUL-terminated" {
        r = unlinkat(base, raw.Data(c[..]), flags)
    }
    if r == 0 {
        if dir != 0 && held.Len() >= DirBlockBytes {
            // The entry was listed from dir's stream, so its next refill
            // skips one entry fewer (dir.mk's head word at 32).
            unsafe "held is dir's stream block, used by this call alone" {
                b := raw.Data(held)
                used := FsGet(b, 32)
                if used > 0 {
                    FsPut(b, 32, used - 1)
                }
            }
        }
        return Ok(0)
    }
    e := Errno()
    if e == 1 && !is_dir {
        var sb: [18]u64 = [0; 18]
        var s: i32 = -1
        unsafe "c is NUL-terminated" {
            s = FsStatAt(base, raw.Data(c[..]), FsAtSymlinkNoFollow, mut sb)
        }
        if s == 0 && (StatMode(&sb) & 0o170000) == 0o040000 {
            return OsResult(value: 0, err: core.SysErr.IsDirectory, os: 21)
        }
    }
    return Fail(e)
}

/// NotEmptyCode is the os code of a directory that is not empty (Darwin's
/// ENOTEMPTY).
export const NotEmptyCode: i64 = 66
