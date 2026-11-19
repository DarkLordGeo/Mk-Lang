// Why: opening a file has many independent choices (read, write, append,
// create, truncate), and adding a choice later must break no program.
//
// What: Options, DefaultOptions and the presets ReadOnly, ReadWrite, Append
// and CreateNew, OpenWith and OpenPath.
//
// How: an options type: plain, built from DefaultOptions or a preset with
// fields assigned, its one unexported field keeping a literal of it out of
// programs, so a field added later breaks none. The flags reach the open
// entry's options form (core.IoOpenOptions), which std.sys maps to
// open(2)'s flags and to CreateFileW's access and disposition alike on both
// targets.

import std.core
import std.rt

/// How OpenWith opens a file: the access wanted, whether to create or
/// truncate it, and the mode bits of a created file. Start from
/// DefaultOptions or a preset and assign the fields to change. Options has
/// a private field, so a literal of it outside std.fs is E0204; a field
/// added later breaks no program.
export struct Options {
    /// Open for reading.
    export read: bool
    /// Open for writing at the file's position.
    export write: bool
    /// Open for writing, every write at the end of the file; implies write.
    export append: bool
    /// Create the file when it is missing.
    export create: bool
    /// Cut the file to 0 bytes when it opens.
    export truncate: bool
    /// Create the file, failing with Exists when it is already there;
    /// implies create.
    export exclusive: bool
    /// The mode bits a created file gets, less the umask on Linux. On
    /// Windows only the owner's write bit counts: clear, the file is
    /// created read-only. Ignored when the file exists.
    export permissions: Permissions
    sealed: u8
}

/// The defaults: read an existing file, create nothing. permissions is
/// 0o666, the mode a created file gets once create is set.
export const DefaultOptions: Options = Options(read: true, write: false, append: false, create: false, truncate: false, exclusive: false, permissions: Permissions(mode: 0o666), sealed: 0)

/// Read an existing file; the same as DefaultOptions.
export const ReadOnly: Options = DefaultOptions

/// Read and write an existing file, without truncating it.
export const ReadWrite: Options = Options(read: true, write: true, append: false, create: false, truncate: false, exclusive: false, permissions: Permissions(mode: 0o666), sealed: 0)

/// Create a new file for reading and writing; Exists when a file is
/// already there.
export const CreateNew: Options = Options(read: true, write: true, append: false, create: true, truncate: false, exclusive: true, permissions: Permissions(mode: 0o666), sealed: 0)

/// Write a log: write only, every write at the end of the file, and the
/// file created when it is missing.
export const Append: Options = Options(read: false, write: true, append: true, create: true, truncate: false, exclusive: false, permissions: Permissions(mode: 0o666), sealed: 0)

/// Opens the file at p as o says. The position starts at 0, except under
/// append, where every write lands at the end of the file and Read, Write
/// and Seek use the OS's own position. One runtime call; allocates nothing.
///
/// Unsupported, before any call, for a set that the targets do not open
/// alike: none of read, write and append; create, truncate or exclusive
/// without write or append; truncate with append. Exists under exclusive
/// when p exists. NotFound when p or its parent is missing and create is
/// not set. Any other failure is the OS's, as its fs.Error member.
///
/// On Windows an append handle has FILE_APPEND_DATA without
/// FILE_WRITE_DATA, so File.Truncate on it is Denied.
export OpenWith(io: Io, p: str, o: Options): Error!File {
    mode := try ModeOf(&o)
    h := try OpenHandle(io, p, mode)
    return if o.append { AppendFile(h) } else { NewFile(h) }
}

/// OpenWith for a Path that Walk or Dir.Next listed. It opens the path
/// from its bytes, so every listed name opens, even one that is not UTF-8:
/// on Linux any bytes, on Windows a name's WTF-8, an unpaired surrogate
/// included. O(n) in the path's length for the checks, then one runtime
/// call; allocates nothing.
///
/// InvalidPath for a NUL byte, or on Windows for bytes that are not WTF-8.
/// Unsupported for bytes that are not UTF-8 under an Io other than the
/// native one (std.sim, a Recorder). Any other failure as OpenWith.
export OpenPath(io: Io, p: &Path, o: Options): Error!File {
    mode := try ModeOf(&o)
    h := Trunc[uint](try Check(rt.OpenBytes(io, p.AsBytes(), mode)))
    return if o.append { AppendFile(h) } else { NewFile(h) }
}

// ModeOf is the open entry's options form for o, or Unsupported for a set
// that std.sys would open differently on the two targets (OpenWith's doc).
ModeOf(o: &Options): Error!u32 {
    writes := o.write || o.append
    if !o.read && !writes {
        fail Unsupported
    }
    if (o.create || o.truncate || o.exclusive) && !writes {
        fail Unsupported
    }
    if o.truncate && o.append {
        fail Unsupported
    }
    var m := core.IoOpenOptions
    if o.read {
        m = m | core.IoOpenRead
    }
    if o.write {
        m = m | core.IoOpenWrite
    }
    if o.append {
        m = m | core.IoOpenAppend
    }
    // exclusive implies create, as Rust's create_new and O_EXCL's meaning.
    if o.create || o.exclusive {
        m = m | core.IoOpenCreate
    }
    if o.exclusive {
        m = m | core.IoOpenExclusive
    }
    if o.truncate {
        m = m | core.IoOpenTruncate
    }
    return m | ((o.permissions.mode & 0o7777) << core.IoOpenPermShift)
}
