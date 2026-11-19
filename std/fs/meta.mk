// Why: programs need a file's kind, size, permissions and modification
// time, and to set the last two, the same way on both targets.
//
// What: Info, Kind, Permissions, Stat, StatLink, StatPath, SetModified and
// SetPermissions.
//
// How: the facts come from std.rt's StatEx: statx(TYPE|MODE|MTIME|SIZE) on
// Linux, with AT_SYMLINK_NOFOLLOW for StatLink; on Windows one
// GetFileAttributesExW for an entry that is no reparse point and no device,
// and a missing one, by a path that is no UNC, device or \??\ path (\?\X:\
// is a disk path), else CreateFileW with FILE_READ_ATTRIBUTES
// (OPEN_REPARSE_POINT for StatLink) and the basic, standard and
// attribute-tag information classes. GetFileAttributesExW needs only the
// right to list the entry's directory, so an entry the caller may list but
// not open (System Volume Information, for a standard user) has its facts,
// where CreateFileW is Denied. Go's os.Stat and os.Lstat do the same, and
// are the oracle.

import std.core
import std.rt
import std.time

/// What a file system entry is. The enum is open: it may gain variants, so
/// a match on it outside std needs a `_` arm.
export open enum Kind {
    /// A regular file.
    File
    /// A directory.
    Dir
    /// A symbolic link; only StatLink and Walk report one.
    Link
    /// A device, a pipe, a socket or anything else.
    Other
}

/// A file's permission bits. mode is the Unix mode bits, at most 0o7777.
/// On Windows it is 0o444 or 0o666, by the read-only attribute, plus 0o111
/// on a directory.
export struct Permissions {
    /// The mode bits.
    export mode: u32
}

/// The facts Stat, StatLink and StatPath report about a file: its size in
/// bytes, the time of its last modification, its kind and its permissions.
/// Stat follows links; StatLink does not.
export struct Info {
    /// The size in bytes.
    export size: i64
    /// The time of the last modification.
    export modified: time.Time
    /// What the file is.
    export kind: Kind
    /// The file's permissions.
    export permissions: Permissions
}

/// The facts about the file at p, following links. NotFound when nothing
/// is at p; any other failure is the OS's, as its fs.Error member. One
/// runtime call; allocates nothing.
///
/// Under a simulated Io it takes two runtime calls, and permissions is 0:
/// a simulated file system has no mode bits.
export Stat(io: Io, p: str): Error!Info {
    return try StatBytes(io, p.AsBytes(), false)
}

/// The facts about p itself, not following a link: a symbolic link is
/// reported as Kind.Link, with its own time and mode, not its target's,
/// and size 0 on both targets, as Entry reports it (Linux's lstat would
/// give the length of the target's path). Errors as Stat. One runtime call
/// (two under a simulated Io), as Stat; allocates nothing.
export StatLink(io: Io, p: str): Error!Info {
    return try StatBytes(io, p.AsBytes(), true)
}

/// Stat for a Path that Walk or Dir.Next listed. It reads the path from its
/// bytes, so a name that is not UTF-8 is found (on Windows a name's
/// WTF-8). Errors as Stat. One runtime call (two under a simulated Io), as
/// Stat; allocates nothing.
export StatPath(io: Io, p: &Path): Error!Info {
    return try StatBytes(io, p.AsBytes(), false)
}

// StatBytes is Stat (nofollow false) or StatLink (true) of p's bytes. A
// table whose call entry has no stat (std.sim's, the direct table's) is
// asked through the stat entry, which follows links and has no mode bits.
StatBytes(io: Io, p: []u8, nofollow: bool): Error!Info {
    var w := core.IoWords(a: 0, b: 0, c: 0, d: 0)
    r := rt.StatEx(io, p, nofollow, mut w)
    if r.err != core.SysErr.Unsupported {
        _ = try Check(r)
        return InfoOf(Trunc[u32](w.a), w.b, w.c, Trunc[u32](w.d))
    }
    s := str.From(p) else {
        fail InvalidPath
    }
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    _ = try Check(core.IoStatPath(io, s, mut st))
    return InfoOf(st.kind, st.size, st.modified, 0)
}

// InfoOf is the Info of a kind (core.IoKind*), a size, a time in
// nanoseconds since the epoch and mode bits.
InfoOf(kind: u32, size: i64, nanos: i64, mode: u32): Info {
    k := KindOf(kind)
    // Windows' 0o111 stands for a directory; a link to one is a link, so
    // it has none (Go's Lstat gives such a link 0o666 too).
    x: u32 = if k == Kind.Link && Target.os == core.Os.Windows { 0o111 } else { 0 }
    return Info(size: size, modified: time.FromUnix(0, nanos), kind: k, permissions: Permissions(mode: mode & 0o7777 & ~x))
}

// KindOf is the Kind of a core.IoKind constant.
KindOf(kind: u32): Kind {
    return if kind == core.IoKindFile {
        Kind.File
    } else if kind == core.IoKindDir {
        Kind.Dir
    } else if kind == core.IoKindLink {
        Kind.Link
    } else {
        Kind.Other
    }
}

/// Sets the last modification time of the file at p to t, following a
/// link; the access time is left as it is. NotFound when nothing is at p;
/// on Windows a time at or before 1601-01-01 is Os(0); any other failure
/// is the OS's, as its fs.Error member. One runtime call (utimensat on
/// Linux, SetFileInformationByHandle on Windows); allocates nothing.
export SetModified(io: Io, p: str, t: time.Time): Error!void {
    _ = try Check(rt.SetModified(io, p.AsBytes(), t.UnixNanos()))
}

/// Sets the mode bits of the file at p to perm.mode & 0o7777, following a
/// link (fchmodat on Linux). On Windows only the owner's write bit counts:
/// clearing it sets the read-only attribute, setting it clears the
/// attribute, and the other bits are ignored. NotFound when nothing is at
/// p; any other failure is the OS's, as its fs.Error member. One runtime
/// call; allocates nothing.
export SetPermissions(io: Io, p: str, perm: Permissions): Error!void {
    _ = try Check(rt.SetMode(io, p.AsBytes(), perm.mode & 0o7777))
}
