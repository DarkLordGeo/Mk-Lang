// Why: listing a directory must cost one system call per block of entries,
// not one per name, and look the same on both targets.
//
// What: Dir (Open, Next, Close), Entry and MakeDir.
//
// How: over the IoTable entries opendir, readdir and mkdir: on Linux
// getdents64 into an 8 KiB block plus statx per entry, on Windows
// GetFileInformationByHandleEx(FileFullDirectoryInfo); every path entry
// runs on the blocking pool. The error mapping is fs.Check's (fs.mk).

import std.core
import std.rt

/// An open directory, listed one entry at a time by Next. Dir is linear:
/// end it with Close. Like File, it is Send but not Share.
export linear struct Dir {
    h: uint
    nosh: [0]Cell[u8]
}

/// One entry of a directory listing. name is the entry's own name, without
/// the directory; its bytes come from the OS and may not be UTF-8 (Path.Str
/// says). is_dir and size describe the entry itself, not a link's target:
/// a link to a directory is not a directory, and a directory's or a link's
/// size is 0, on both targets.
export struct Entry {
    /// The entry's own name, without the directory, in the OS's bytes.
    export name: Path
    /// Whether the entry is a directory; a link to a directory is not.
    export is_dir: bool
    /// The size in bytes; 0 for a directory or a link.
    export size: i64
}

// NameBuf is Next's first buffer for a name, on its frame: Linux names are
// at most 255 bytes and Windows names at most 255 UTF-16 units, 765 UTF-8
// bytes, so nearly every name fits and the rest take the arena path.
const NameBuf = 512

/// Opens the directory at p for listing. NotDirectory when p is a file,
/// NotFound when nothing is at p; any other failure is the OS's, as its
/// fs.Error member. O(1); allocates nothing.
export Dir.Open(io: Io, p: str): Error!Dir {
    h := try Check(core.IoOpenDir(io, p))
    return Dir(h: Trunc[uint](h), nosh: [])
}

/// The next entry of the directory, its name copied into a; none after the
/// last. `.` and `..` never appear, and the order is the file system's.
/// OutOfMemory when a cannot hold the name; any other failure is the OS's,
/// as its fs.Error member. After a failure the directory stays open: Close
/// it.
///
/// O(1) runtime calls per entry, two for a name over 512 bytes, and
/// O(name) copying; allocates one block of exactly the name's length in a.
/// A long name is read straight into that block.
export Dir.Next(mut self, a: &Arena, io: Io): Error|OutOfMemory!?Entry from a {
    var buf: [512]u8 = [0; 512]
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    r := core.IoReadDir(io, self.h, mut buf[..], mut st)
    if r.err == core.SysErr.NoSpace && r.value > i64(NameBuf) {
        need := Cast[int](r.value)
        big := try a.Slice[u8](need)
        n := try CheckCount(core.IoReadDir(io, self.h, big, mut st), need)
        if n == 0 {
            return none
        }
        // big is already the name's block in a: wrap it, no second copy.
        return some(Entry(name: core.PathOf(big[0..n]), is_dir: st.kind == core.IoKindDir, size: st.size))
    }
    n := try CheckCount(r, NameBuf)
    if n == 0 {
        return none
    }
    return some(try EntryOf(a, buf[0..n], &st))
}

// EntryOf is the Entry for a name read into a frame buffer, copied into a.
EntryOf(a: &Arena, name: []u8, st: &core.IoStat): OutOfMemory!Entry from a {
    p := try Path.New(a, name)
    return Entry(name: p, is_dir: st.kind == core.IoKindDir, size: st.size)
}

/// Closes the directory. It ignores every error and cannot be cancelled,
/// so it takes no Io and cannot fail. O(1); allocates nothing.
export Dir.Close(self) {
    Dir(h, nosh: _) := self
    rt.Abandon(h)
}

/// Creates the directory p. Its parent must exist: NotFound otherwise.
/// Exists when anything is at p, a directory included. Denied, InvalidPath
/// or Os as the OS answers. One runtime call; allocates nothing.
export MakeDir(io: Io, p: str): Error!void {
    _ = try Check(core.IoMkdir(io, p))
}
