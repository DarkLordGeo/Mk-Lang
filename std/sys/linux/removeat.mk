// Why: fs.RemoveAll must not follow a symlink or a rename out of the tree
// it deletes, so it cannot name entries by path.
//
// What: NameAt, DirOpenAt, RemoveAt and NotEmptyCode, the primitives of
// RemoveAll.
//
// How: openat with O_DIRECTORY | O_NOFOLLOW and unlinkat, both relative to a
// directory descriptor the caller holds, so each entry is named by its
// parent's descriptor (as Go's removeall_at.go and Rust's remove_dir_all on
// Linux do). Each call is one system call on the pool.

import std.core
import std.raw

const ONoFollowAt: uint = 0o400000

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
/// name NameAt refuses or one longer than PATH_MAX; else MapError's. held
/// (dir's stream block) is not used here; fresh, the new stream's zeroed
/// block when there is one, is marked to be read for kinds alone
/// (DirKindOnly): only fs.Walk and RemoveAll list such a stream.
/// O(1) system calls; allocates nothing (the name is copied into a stack
/// buffer).
export DirOpenAt(dir: uint, held: []u8, name: []u8, fresh: mut []u8): OsResult {
    _ = held
    var c: [4096]u8 = [0; 4096]
    if !NameAt(name, dir != 0) || !CBytes(name, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    base := if dir == 0 { AtFdcwd } else { dir }
    unsafe "system call; c is NUL-terminated" {
        r := Syscall(Nrs().openat, base, raw.Addr(raw.Data(c[..])), ORdOnly | ODirectory | ONoFollowAt | OCloExec, 0, 0, 0)
        if r == -40 || r == -20 {
            return OsResult(value: 0, err: core.SysErr.NotDirectory, os: i64(-r))
        }
        if !IsErr(r) {
            DirKindOnly(fresh)
        }
        return Result(r)
    }
}

/// RemoveAt removes the entry name inside the held directory dir (0: name
/// is a whole path): unlinkat, with AT_REMOVEDIR when is_dir. A link is
/// removed itself, never its target. Errors: IsDirectory when name is a
/// directory and is_dir is false (EISDIR); NotDirectory when is_dir and it
/// is not one (ENOTDIR); a directory that is not empty is Other with os
/// ENOTEMPTY 39 (RemoveNotEmpty in std.rt says so on both targets);
/// InvalidPath as DirOpenAt; else MapError's. O(1) system calls; allocates
/// nothing.
export RemoveAt(dir: uint, held: []u8, name: []u8, is_dir: bool): OsResult {
    _ = held
    var c: [4096]u8 = [0; 4096]
    if !NameAt(name, dir != 0) || !CBytes(name, mut c[..]) {
        return FailWith(core.SysErr.InvalidPath)
    }
    base := if dir == 0 { AtFdcwd } else { dir }
    flags := if is_dir { AtRemoveDir } else { 0 }
    unsafe "system call; c is NUL-terminated" {
        return Result(Syscall(Nrs().unlinkat, base, raw.Addr(raw.Data(c[..])), flags, 0, 0, 0))
    }
}

/// NotEmptyCode is the os code of a directory that is not empty (ENOTEMPTY).
export const NotEmptyCode: i64 = 39
