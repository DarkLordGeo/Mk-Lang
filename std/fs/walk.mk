// Why: walking a tree must neither follow links nor be carried out of the
// tree by a rename above it, and must not allocate a path per entry.
//
// What: Walk, Walker (Next, SkipDir, Close) and WalkEntry.
//
// How: a depth-first walk in the OS's order, as Go's filepath.WalkDir walks
// but unsorted, and as Rust's walkdir holds its directories. One directory
// stream is open per level of the current path, each opened inside its
// parent's (std.rt's DirOpenAt: openat with O_NOFOLLOW on Linux, a held
// parent on Windows). The path of the entry last returned lives in one byte
// buffer on the walker's heap, grown to the deepest path seen and never
// shrunk until Close.

import std.core
import std.rt

/// One entry of a walk. path is the root's path, then each name below it
/// after a separator, in the OS's bytes (on Windows a name's WTF-8). kind
/// is what the entry is: Kind.Link for a link, which the walk does not
/// follow. depth is 0 for the root, 1 for its entries, and so on.
export struct WalkEntry {
    /// The root's path, then each name below it after a separator.
    export path: Path
    /// What the entry is; Kind.Link for a link, which the walk does not follow.
    export kind: Kind
    /// 0 for the root, 1 for its entries, and so on.
    export depth: int
}

// WalkLevel is one open directory of a walk: its runtime handle, and the
// length of its path in the walker's buffer.
struct WalkLevel {
    h: uint
    plen: int
}

/// A depth-first walk of a directory tree, made by Walk. Walker is linear:
/// end it with Close. It holds one open directory per level of the entry
/// last returned, and its buffers on the heap it was made with.
export linear view struct Walker {
    heap: &Heap
    levels: HeapList[WalkLevel]
    buf: HeapList[u8]
    // The path of the entry last returned is buf[0..plen].
    plen: int
    // RootNext: the root is not yet returned; DirNext: the entry last
    // returned is a directory not yet entered; Pending: the entry at
    // buf[0..plen], of kind pending, was read but not returned (its path
    // could not be allocated); Listing otherwise.
    state: int
    root_kind: Kind
    pending: Kind
    nosh: [0]Cell[u8]
}

const RootNext = 0
const DirNext = 1
const Listing = 2
const Pending = 3

// NameRoom is the room a name may take: 255 bytes on Linux, 255 UTF-16
// units on Windows, at most 765 bytes of WTF-8. A longer one, which no
// target names, is read after the buffer grows to the length readdir
// reports.
const NameRoom = 1024

/// Starts a walk of the tree at root. The walk returns root first, at
/// depth 0, and then, when root is a directory, everything under it, depth
/// first, each directory's entries in the order the OS lists them. No link
/// is followed, root included: a link is returned as Kind.Link. Each
/// directory is opened inside its parent, so a rename above it cannot
/// carry the walk out of the tree.
///
/// NotFound when root is missing; any other failure is the OS's, as its
/// fs.Error member; OutOfMemory when h cannot hold root's path. One runtime
/// call (a stat that does not follow a link); allocates root's path on h.
///
/// A name is joined to its directory with the target's separator, and
/// with `/` on every target under a simulated Io (a Sim's or a Tape's), so
/// a seed gives one run.
export Walk(h: &Heap, io: Io, root: str): Error|OutOfMemory!Walker from h {
    info := try StatLink(io, root)
    var buf := HeapList[u8].New(h.Alloc())
    for c in root.AsBytes() {
        buf.Push(h.Alloc(), c) else {
            buf.Free(h.Alloc())
            fail OutOfMemory
        }
    }
    return Walker(heap: h, levels: HeapList[WalkLevel].New(h.Alloc()), buf: buf, plen: root.Len(), state: RootNext, root_kind: info.kind, pending: info.kind, nosh: [])
}

/// The next entry of the walk, its path copied into a; none after the
/// last. A directory is entered by the next call, unless SkipDir comes
/// first.
///
/// Canceled when the scope is cancelled; the walk stays where it was, so a
/// later call tries the same step again. A directory that cannot be opened
/// or read fails once with the OS's error (Denied, ...), and the walk goes
/// on after it. OutOfMemory when a cannot hold the path or the walker's
/// heap cannot grow its buffers; the walk stays where it was, and the next
/// call returns the same entry.
///
/// O(1) runtime calls and O(path) copying per entry; allocates the path in
/// a, and grows the walker's buffers on its heap to the deepest path.
export Walker.Next(mut self, a: &Arena, io: Io): Error|OutOfMemory!?WalkEntry from a {
    if self.state == RootNext {
        path := try Path.New(a, self.buf[0..self.plen])
        self.state = if self.root_kind == Kind.Dir { DirNext } else { Listing }
        return some(WalkEntry(path: path, kind: self.root_kind, depth: 0))
    }
    if self.state == Pending {
        return some(try self.Deliver(a))
    }
    if self.state == DirNext {
        try self.Enter(io)
    }
    for self.levels.Len() > 0 {
        top := self.levels[self.levels.Len() - 1]
        start := try self.NameStart(top.plen, PathWin(io))
        var st := core.IoStat(kind: 0, size: 0, modified: 0)
        r := core.IoReadDir(io, top.h, mut self.buf[start..start + NameRoom], mut st)
        long := r.err == core.SysErr.NoSpace && r.value > i64(NameRoom)
        if !long && r.err != core.SysErr.None && r.err != core.SysErr.Canceled {
            // A directory that cannot be read: the walk goes on after it.
            self.Leave()
        }
        n := if long {
            try self.ReadLong(io, top.h, start, Cast[int](r.value), mut st)
        } else {
            try CheckCount(r, NameRoom)
        }
        if n == 0 {
            self.Leave()
            continue
        }
        self.plen = start + n
        self.pending = KindOf(st.kind)
        self.state = Pending
        return some(try self.Deliver(a))
    }
    return none
}

// Deliver returns the entry read last, at buf[0..plen], of kind pending,
// its path copied into a. The walker moves past it only once the copy is
// made, so an OutOfMemory leaves it Pending for the next call.
Walker.Deliver(mut self, a: &Arena): OutOfMemory!WalkEntry from a {
    k := self.pending
    path := try Path.New(a, self.buf[0..self.plen])
    self.state = if k == Kind.Dir { DirNext } else { Listing }
    return WalkEntry(path: path, kind: k, depth: self.levels.Len())
}

// Enter opens the directory last returned and pushes it. On Canceled or
// OutOfMemory the walker is unchanged, so the call can be made again; on
// any other failure the directory is skipped and its error returned.
Walker.Enter(mut self, io: Io): Error|OutOfMemory!void {
    r := if self.levels.Len() == 0 {
        rt.DirOpenAt(io, 0, self.buf[0..self.plen])
    } else {
        top := self.levels[self.levels.Len() - 1]
        rt.DirOpenAt(io, top.h, self.buf[self.NameFrom(top.plen, PathWin(io))..self.plen])
    }
    if r.err == core.SysErr.Canceled {
        fail Canceled
    }
    if r.err != core.SysErr.None {
        self.state = Listing
        _ = try Check(r)
    }
    d := Trunc[uint](r.value)
    self.levels.Push(self.heap.Alloc(), WalkLevel(h: d, plen: self.plen)) else {
        // Still DirNext: the next call opens it again.
        rt.Abandon(d)
        fail OutOfMemory
    }
    self.state = Listing
}

// NameFrom is where a name starts after the directory path buf[0..plen]:
// past a separator, which a path that already ends with one (a root such
// as "/" or "dir/") does not get a second time, and a bare drive `C:`
// (that drive's working directory) gets none: `C:name`, as Go's
// filepath.Join writes it, names the entry listed, where a separator
// after the drive would name an entry of the drive's root.
// win is the Windows form (PathWin): under a simulated Io the separator
// is `/` and there is no drive on every target.
Walker.NameFrom(&self, plen: int, win: bool): int {
    drive := win && plen == 2 && self.buf[1] == 0x3A
    sep := plen > 0 && !IsSep(self.buf[plen - 1], win) && !drive
    return if sep { plen + 1 } else { plen }
}

// NameStart makes room for a separator and a name after the directory
// path buf[0..plen], writes the separator unless the path ends with one,
// and returns where the name starts, in the form win says (NameFrom).
Walker.NameStart(mut self, plen: int, win: bool): OutOfMemory!int {
    start := self.NameFrom(plen, win)
    sep := start > plen
    try self.Room(start + NameRoom)
    if sep {
        self.buf[plen] = if win { 0x5C } else { 0x2F }
    }
    return start
}

// Room grows buf to at least n bytes.
Walker.Room(mut self, n: int): OutOfMemory!void {
    for self.buf.Len() < n {
        try self.buf.Push(self.heap.Alloc(), 0)
    }
}

// ReadLong reads a name longer than NameRoom, need bytes, after the buffer
// grows to hold it.
Walker.ReadLong(mut self, io: Io, h: uint, start: int, need: int, st: mut core.IoStat): Error|OutOfMemory!int {
    try self.Room(start + need)
    return try CheckCount(core.IoReadDir(io, h, mut self.buf[start..start + need], st), need)
}

// Leave closes the deepest open directory and pops it.
Walker.Leave(mut self) {
    l := self.levels.Pop() else {
        return
    }
    rt.Abandon(l.h)
}

/// Skips the directory last returned: the walk does not enter it. After an
/// entry that is not a directory it does nothing. O(1); allocates nothing.
export Walker.SkipDir(mut self) {
    if self.state == DirNext {
        self.state = Listing
    }
}

/// Ends the walk: closes every directory it holds and frees its buffers.
/// It cannot be cancelled and cannot fail. O(depth); allocates nothing.
export Walker.Close(self) {
    Walker(heap, levels, buf, plen: _, state: _, root_kind: _, pending: _, nosh: _) := self
    var ls := levels
    for {
        l := ls.Pop() else {
            break
        }
        rt.Abandon(l.h)
    }
    ls.Free(heap.Alloc())
    buf.Free(heap.Alloc())
}
