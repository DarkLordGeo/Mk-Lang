// Why: a simulated run needs a file system that never touches the real
// disk, loses unsynced data at a crash exactly as a seed says, and answers
// alike on every target.
//
// What: the World's node table (Node, PageEnt) and handle table (Hand,
// NewHand), the foreign runtime records a Sim's handles are (SimOps,
// MarkForeign), and the open, read, write, size, sync, close, rename,
// remove, mkdir, stat, opendir and readdir entries behind them.
//
// How: namespace changes are durable at once, file contents only at sync;
// a file is a list of 4 KiB pages shared copy-on-write with its durable
// state, so a sync copies no data and Restart reverts to the durable pages.
// The model in detail follows.
//
// Model. A flat table of nodes keyed by the cleaned path (Go's path.Clean,
// after Pike's "Lexical File Names in Plan 9", with \ read as /). A node is
// a file or a directory; "." and "/" are directories that always exist.
// Namespace changes (create, rename, remove, mkdir) are durable when they
// return, as on a journaling file system in ordered mode; file contents
// become durable only at sync. A file's data is a list of 4 KiB pages, and
// its durable state the page list at the last sync with that size: a write
// to a page the durable list shares copies it first, so a sync costs one
// pointer per page and no data copy (copy on write, as a log-structured
// store's snapshot). Restart reverts every file to its durable pages.
// Lookups scan the node table: O(files) per path operation (a hash index
// can replace the scan once a workload has thousands of files).

import std.core
import std.raw
import std.rt

const PageSize: int = 4096
const MaxPath: int = 4096

// The largest file (ext4's with 4 KiB blocks, 16 TiB): a write at or past
// it fails with EFBIG, one that would cross it is cut short (POSIX).
const MaxFileSize: i64 = 0x1000_0000_0000
const EFBig: i64 = 27

// One page of a file (Pages below): its number, its live and its durable
// block.
struct PageEnt {
    at: i64
    live: *u8
    dur: *u8
}

// Node kinds; 0 is a free slot.
const NFile: u32 = 1
const NDir: u32 = 2

struct Node {
    path: Vec[u8]
    kind: u32
    linked: bool
    size: i64
    pages: Vec[PageEnt]
    dsize: i64
    dirty: Vec[i64]
    alldirty: bool
    opens: int
    modified: i64
}

// Handle kinds; 0 is a free slot.
const HFile: u32 = 1
const HDir: u32 = 2
const HListen: u32 = 3
const HConn: u32 = 4
const HUdp: u32 = 5
// A simulated child's process handle (proc.mk).
const HProc: u32 = 6
// A handle on the simulated terminal (term.mk): obj 0 its input, 1
// its output.
const HTerm: u32 = 7

// A handle of the current run: h is its runtime record, obj the node, end,
// port or terminal side it names, pos a file's stream position or a directory stream's
// next node.
struct Hand {
    h: uint
    epoch: u32
    kind: u32
    obj: int
    pos: i64
    write: bool
    // The whole-file advisory lock this file handle holds (fscall.mk
    // CallLock): 0 none, 1 shared, 2 exclusive. A close drops it with the
    // entry, as closing the descriptor does natively.
    lock: u32
    // Opened with the options form's append flag: each write at the end.
    append: bool
}

// A Sim's handle is a foreign runtime record (rt.KindForeign):
// released, with no OS handle behind it, dir pointing at SimOps, os the
// world's slot (bits 58 up), the run's epoch (26 bits at 32) and the
// entry's index + 1, and pos the run's token. rt.Abandon and
// rt.CloseRuntimeHandle (Stream.Close, Listener.Close, Datagram.Close,
// File.Abandon) hand it to SimForeignClose, so the simulation sees every
// close; the sim's own close clears dir first and frees it directly.
const SimOps = rt.ForeignOps(close: SimForeignClose, signal: SimForeignSignal)

const EpochMask: uint = 0x3FF_FFFF

SimOpsPtr(): *u8 {
    unsafe "SimOps is static: its address is only stored and compared, never written through" {
        return raw.PtrCast[rt.ForeignOps, u8](raw.Of(&SimOps))
    }
}

// MarkForeign makes the fresh record h foreign: dir is ops (null for a
// record no simulation needs to hear about), os and pos as above.
MarkForeign(h: uint, ops: *u8, os: uint, token: u64) {
    unsafe "h is the record NewSocketHandle just made; no OS handle is behind it" {
        r := rt.Rec(h)
        (*r).released = true
        (*r).kind = rt.KindForeign
        (*r).async = false
        (*r).os = os
        (*r).pos = Cast[i64](token)
        (*r).dir = ops
    }
}

// ---------------------------------------------------------------------------
// Handles.

// NewHand records a handle of kind for obj; its value is the result, or
// NoSpace when the heap is exhausted.
NewHand(w: mut World, kind: u32, obj: int, write: bool): core.IoResult {
    h := rt.NewSocketHandle(0, 0)
    if h == 0 {
        return Fail(core.SysErr.NoSpace)
    }
    var idx := -1
    for i in 0..w.handles.Len() {
        if w.handles.Get(i).kind == 0 {
            idx = i
            break
        }
    }
    ent := Hand(h: h, epoch: w.epoch, kind: kind, obj: obj, pos: 0, write: write, lock: 0, append: false)
    if idx < 0 {
        if !w.handles.Push(ent) {
            // Foreign with no ops: freed with no OS call and no callback.
            MarkForeign(h, raw.Null[u8](), 0, 0)
            rt.Abandon(h)
            return Fail(core.SysErr.NoSpace)
        }
        idx = w.handles.Len() - 1
    } else {
        w.handles.Set(idx, ent)
    }
    os := (Cast[uint](w.slot) << 58) | ((uint(w.epoch) & EpochMask) << 32) | Cast[uint](idx + 1)
    MarkForeign(h, SimOpsPtr(), os, w.token)
    return Ok(Cast[i64](h))
}

// FindHand is the index of h's entry when h is a handle of kind of this
// world's current run, else -1.
FindHand(w: &World, h: uint, kind: u32): int {
    if h == 0 {
        return -1
    }
    var os: uint = 0
    unsafe "h is a runtime handle: every handle a safe program holds is a live record" {
        r := rt.Rec(h)
        if (*r).kind != rt.KindForeign || !raw.Same((*r).dir, SimOpsPtr()) {
            return -1
        }
        os = (*r).os
    }
    if ((os >> 32) & EpochMask) != (uint(w.epoch) & EpochMask) {
        return -1
    }
    idx := Cast[int](os & 0xFFFF_FFFF) - 1
    if idx < 0 || idx >= w.handles.Len() {
        return -1
    }
    ent := w.handles.Get(idx)
    if ent.h != h || ent.kind != kind || ent.epoch != w.epoch {
        return -1
    }
    return idx
}

// HandKind is the kind of h's entry in this run, or 0.
HandKind(w: &World, h: uint): u32 {
    for k in 1..8 {
        if FindHand(&*w, h, Trunc[u32](k)) >= 0 {
            return Trunc[u32](k)
        }
    }
    return 0
}

// DropHand ends entry i. Its runtime record is freed here (dir cleared
// first, so rt calls no SimOps on the way), except when rt is closing it
// already (w.closing, SimForeignClose): then rt frees it after the call.
DropHand(w: mut World, i: int) {
    h := w.handles.Get(i).h
    w.handles.Set(i, Hand(h: 0, epoch: 0, kind: 0, obj: 0, pos: 0, write: false, lock: 0, append: false))
    if h == w.closing {
        return
    }
    unsafe "h is the live foreign record of entry i" {
        (*rt.Rec(h)).dir = raw.Null[u8]()
    }
    _ = rt.CloseRuntimeHandle(h)
}

// SimForeignClose is SimOps.close: rt closes a Sim's handle with no Io
// (rt.Abandon, Stream.Close and the like). The record names its slot and
// its run's token; the slot's token differs once the run ended (Restart)
// or the Sim was freed, and then there is nothing left to close. Otherwise
// it is the close entry's work, without freeing the record, which rt does
// next. Never called with the slot locked: the sim's own closes clear dir.
SimForeignClose(h: uint) {
    var os: uint = 0
    var tok: u64 = 0
    unsafe "rt calls this with h's live record" {
        r := rt.Rec(h)
        os = (*r).os
        tok = Cast[u64]((*r).pos)
    }
    slot := Cast[int](os >> 58)
    if slot >= SlotCount || tok == 0 {
        return
    }
    LockSlot(slot)
    if SlotToken(slot) == tok {
        var w := raw.Null[World]()
        unsafe "the token matches, so the slot's world word holds this Sim's live World" {
            w = raw.FromAddr[World](uint(SlotWorld(slot)))
        }
        unsafe "the slot's lock is held; the world lives until Free takes it" {
            wm := mut *w
            wm.closing = h
            if !DiskClose(wm, h) && !NetClose(wm, h) && !KidClose(wm, h) {
                _ = TermClose(wm, h)
            }
            wm.closing = 0
            WakeNet(wm)
        }
        var e := Entry(slot: slot, w: w, ok: true)
        Settle(IoFor(slot, tok), mut e)
        if !e.ok {
            return
        }
    }
    UnlockSlot(slot)
}

// ---------------------------------------------------------------------------
// Paths.

// FindNode is the linked node at the cleaned path p, or -1.
FindNode(w: &World, p: []u8): int {
    for i in 0..w.nodes.Len() {
        n := w.nodes.Get(i)
        if n.kind != 0 && n.linked && BytesEq(n.path.View(), p) {
            return i
        }
    }
    return -1
}

// DirOf is the kind of the directory a cleaned path's parent is: NDir when
// it exists as a directory, NFile when it is a file, 0 when missing.
ParentKind(w: &World, p: []u8): u32 {
    pl := ParentLen(p)
    if pl == 0 {
        return NDir
    }
    parent := p[0..pl]
    if IsRoot(parent) {
        return NDir
    }
    i := FindNode(&*w, parent)
    if i < 0 {
        return 0
    }
    return w.nodes.Get(i).kind
}

// ParentErr is the error of a create under p's parent, or None.
ParentErr(w: &World, p: []u8): core.SysErr {
    k := ParentKind(&*w, p)
    if k == NDir {
        return core.SysErr.None
    }
    if k == NFile {
        return core.SysErr.NotDirectory
    }
    return core.SysErr.NotFound
}

// IsChild: c's parent is the cleaned path d.
IsChild(c: []u8, d: []u8): bool {
    pl := ParentLen(c)
    if pl == 0 {
        return d.Len() == 1 && d[0] == b'.'
    }
    if IsRoot(c[0..pl]) && c[0] == b'/' {
        return d.Len() == 1 && d[0] == b'/' && c.Len() > 1
    }
    return BytesEq(c[0..pl], d)
}

// NewNode adds a linked node; its index, or -1 when out of memory.
NewNode(w: mut World, p: []u8, kind: u32): int {
    var path := Vec[u8].Empty()
    if !AppendBytes(mut path, p) {
        return -1
    }
    node := Node(path: path, kind: kind, linked: true, size: 0, pages: Vec[PageEnt].Empty(), dsize: 0,
        dirty: Vec[i64].Empty(), alldirty: false, opens: 0, modified: w.clock)
    for i in 0..w.nodes.Len() {
        if w.nodes.Get(i).kind == 0 {
            w.nodes.Set(i, node)
            return i
        }
    }
    if !w.nodes.Push(node) {
        path.Free()
        return -1
    }
    return w.nodes.Len() - 1
}

// ---------------------------------------------------------------------------
// Pages.
//
// A file's pages are a sparse index: entries sorted by page number, each
// with its live block and its durable block (either may be null, a hole;
// both the same block while the page is unchanged since the last sync).
// Holes take no entry, so a write at 1 TiB is one entry (Linux's page
// cache keys pages by index in an xarray, a radix tree; a sorted array
// with binary search does the same job here, with O(1) appends, the
// write-ahead-log case, and O(entries) inserts in the middle). dirty holds
// the page numbers whose live block differs from the durable one, so a
// sync or a Restart touches only those pages: an append-and-sync log costs
// O(pages written) per sync, not O(pages of the file). alldirty stands in
// for a dirty list that could not grow, and after a truncation: every
// entry is then looked at once.

NewPage(): *u8 {
    p := rt.HeapAlloc(PageSize, 16)
    if !raw.IsNull(p) {
        unsafe "p is a fresh page" {
            raw.Fill(p, 0, PageSize)
        }
    }
    return p
}

FreePage(p: *u8) {
    if !raw.IsNull(p) {
        rt.HeapFree(p, PageSize, 16)
    }
}

// FindPage is the index of n's first entry whose page number is at least
// pi: binary search, after a check of the last entry (appends).
FindPage(n: &Node, pi: i64): int {
    k := n.pages.Len()
    if k == 0 || n.pages.Get(k - 1).at < pi {
        return k
    }
    var lo := 0
    var hi := k
    for lo < hi {
        mid := lo + (hi - lo) / 2
        if n.pages.Get(mid).at < pi {
            lo = mid + 1
        } else {
            hi = mid
        }
    }
    return lo
}

// MarkDirty records that page pi's live block now differs from its durable
// one; a list that cannot grow turns into alldirty, so this never fails.
MarkDirty(n: mut Node, pi: i64) {
    if !n.alldirty && !n.dirty.Push(pi) {
        n.alldirty = true
        n.dirty.Truncate(0)
    }
}

// CompactPages drops the entries with neither block (after a sync or a
// revert of a truncated or new page); O(entries), run only when one exists.
CompactPages(n: mut Node) {
    var k := 0
    for j in 0..n.pages.Len() {
        e := n.pages.Get(j)
        if !raw.IsNull(e.live) || !raw.IsNull(e.dur) {
            n.pages.Set(k, e)
            k += 1
        }
    }
    n.pages.Truncate(k)
}

// DropLive empties the live state (a truncation to 0): every live block
// the durable state does not share is freed. O(entries).
DropLive(n: mut Node) {
    for j in 0..n.pages.Len() {
        e := n.pages.Mut(j)
        if !raw.Same(e.live, e.dur) {
            FreePage(e.live)
        }
        e.live = raw.Null[u8]()
    }
    CompactPages(n)
    n.dirty.Truncate(0)
    n.alldirty = n.pages.Len() > 0
}

FreeNode(n: mut Node) {
    for j in 0..n.pages.Len() {
        e := n.pages.Get(j)
        if !raw.Same(e.live, e.dur) {
            FreePage(e.live)
        }
        FreePage(e.dur)
    }
    n.pages.Free()
    n.dirty.Free()
    n.alldirty = false
    n.path.Free()
    n.kind = 0
    n.linked = false
}

// ReadNode copies up to buf.Len() bytes of n from pos; the count. One
// search, then a walk: O(log entries + bytes).
ReadNode(n: &Node, pos: i64, buf: mut []u8): int {
    if pos >= n.size {
        return 0
    }
    want := if i64(buf.Len()) > n.size - pos { int(n.size - pos) } else { buf.Len() }
    var j := FindPage(&*n, pos / i64(PageSize))
    var done := 0
    for done < want {
        at := pos + i64(done)
        pi := at / i64(PageSize)
        off := int(at % i64(PageSize))
        k := if PageSize - off > want - done { want - done } else { PageSize - off }
        for j < n.pages.Len() && n.pages.Get(j).at < pi {
            j += 1
        }
        p := if j < n.pages.Len() && n.pages.Get(j).at == pi { n.pages.Get(j).live } else { raw.Null[u8]() }
        unsafe "buf[done..done + k] is in bounds; p, when not null, is a page of PageSize bytes" {
            dst := raw.DataMut(mut buf[done..done + k])
            if raw.IsNull(p) {
                raw.Fill(dst, 0, k)
            } else {
                raw.Copy(dst, raw.Offset(p, off), k)
            }
        }
        done += k
    }
    return done
}

// WriteNode stores data at pos (the caller bounds pos + data.Len() by
// MaxFileSize); the count, which is short only when the heap ran out (0
// then means nothing was written: NoSpace). A page unchanged since the
// last sync is copied first (copy on write) and becomes dirty. O(log
// entries + bytes) for writes at the end or over existing pages, plus
// O(entries) per new page inserted in the middle.
WriteNode(n: mut Node, pos: i64, data: []u8): int {
    var j := FindPage(&*n, pos / i64(PageSize))
    var done := 0
    for done < data.Len() {
        at := pos + i64(done)
        pi := at / i64(PageSize)
        off := int(at % i64(PageSize))
        k := if PageSize - off > data.Len() - done { data.Len() - done } else { PageSize - off }
        for j < n.pages.Len() && n.pages.Get(j).at < pi {
            j += 1
        }
        if j == n.pages.Len() || n.pages.Get(j).at != pi {
            q := NewPage()
            if raw.IsNull(q) {
                return done
            }
            if !n.pages.InsertAt(j, PageEnt(at: pi, live: q, dur: raw.Null[u8]())) {
                FreePage(q)
                return done
            }
            MarkDirty(n, pi)
        } else {
            e := n.pages.Get(j)
            if raw.Same(e.live, e.dur) || raw.IsNull(e.live) {
                q := NewPage()
                if raw.IsNull(q) {
                    return done
                }
                if !raw.IsNull(e.live) {
                    unsafe "both are pages of PageSize bytes" {
                        raw.Copy(q, e.live, PageSize)
                    }
                    // Unchanged since the sync until now; a null live block
                    // (truncated) is dirty already.
                    MarkDirty(n, pi)
                }
                n.pages.Mut(j).live = q
            }
        }
        p := n.pages.Get(j).live
        unsafe "p is a private page; data[done..done + k] is in bounds" {
            raw.Copy(raw.Offset(p, off), raw.Data(data[done..done + k]), k)
        }
        done += k
        end := at + i64(k)
        if end > n.size {
            n.size = end
        }
    }
    return done
}

// SyncEnt makes entry j's live block durable; true when the entry is left
// with neither block.
SyncEnt(n: mut Node, j: int): bool {
    e := n.pages.Mut(j)
    if !raw.Same(e.live, e.dur) {
        FreePage(e.dur)
        e.dur = e.live
    }
    return raw.IsNull(e.dur)
}

// RevertEnt drops entry j's live block for its durable one (Restart).
RevertEnt(n: mut Node, j: int): bool {
    e := n.pages.Mut(j)
    if !raw.Same(e.live, e.dur) {
        FreePage(e.live)
        e.live = e.dur
    }
    return raw.IsNull(e.live)
}

// SyncNode makes n's live state durable: O(dirty pages x log entries), no
// allocation, so it cannot fail (true; the bool is kept for the caller).
SyncNode(n: mut Node): bool {
    var empty := false
    if n.alldirty {
        for j in 0..n.pages.Len() {
            if SyncEnt(n, j) {
                empty = true
            }
        }
    } else {
        for d in 0..n.dirty.Len() {
            pi := n.dirty.Get(d)
            j := FindPage(&*n, pi)
            if j < n.pages.Len() && n.pages.Get(j).at == pi && SyncEnt(n, j) {
                empty = true
            }
        }
    }
    if empty {
        CompactPages(n)
    }
    n.dirty.Truncate(0)
    n.alldirty = false
    n.dsize = n.size
    return true
}

// RevertNode drops n's live state for its durable one (Restart), by the
// same walk as SyncNode.
RevertNode(n: mut Node) {
    var empty := false
    if n.alldirty {
        for j in 0..n.pages.Len() {
            if RevertEnt(n, j) {
                empty = true
            }
        }
    } else {
        for d in 0..n.dirty.Len() {
            pi := n.dirty.Get(d)
            j := FindPage(&*n, pi)
            if j < n.pages.Len() && n.pages.Get(j).at == pi && RevertEnt(n, j) {
                empty = true
            }
        }
    }
    if empty {
        CompactPages(n)
    }
    n.dirty.Truncate(0)
    n.alldirty = false
    n.size = n.dsize
}

// ---------------------------------------------------------------------------
// The entries' bodies, on a world whose slot is held and whose gate passed.

// DiskOpen opens p with the open entry's mode: the legacy forms (0 read,
// 1 create or truncate and write, 2 write the file as it is) or the
// options form (core.IoOpenOptions with its read, write, append, create,
// truncate and exclusive flags; the permission bits are not kept, the
// Sim has no modes). Linux's answers: a directory is IsDirectory, a
// missing file without create NotFound, an existing one with create and
// exclusive Exists, no access asked EINVAL.
DiskOpen(w: mut World, p: str, mode: u32): core.IoResult {
    var buf: [4096]u8 = [0; 4096]
    pl := CleanPath(p, mut buf[..])
    if pl < 0 {
        return Fail(core.SysErr.InvalidPath)
    }
    cp := buf[0..pl]
    var write := false
    var app := false
    var create := false
    var trunc := false
    var excl := false
    if (mode & core.IoOpenOptions) != 0 {
        write = (mode & core.IoOpenWrite) != 0
        app = (mode & core.IoOpenAppend) != 0
        create = (mode & core.IoOpenCreate) != 0
        trunc = (mode & core.IoOpenTruncate) != 0
        excl = (mode & core.IoOpenExclusive) != 0
        if (mode & core.IoOpenRead) == 0 && !write && !app {
            return FailOs(22)
        }
    } else if mode == 1 {
        write = true
        create = true
        trunc = true
    } else if mode == 2 {
        write = true
    } else if mode != 0 {
        return FailOs(22)
    }
    if IsRoot(cp) {
        return Fail(core.SysErr.IsDirectory)
    }
    var i := FindNode(&*w, cp)
    if i >= 0 && w.nodes.Get(i).kind == NDir {
        return Fail(core.SysErr.IsDirectory)
    }
    if i >= 0 && create && excl {
        return Fail(core.SysErr.Exists)
    }
    if i < 0 {
        if !create {
            return Fail(core.SysErr.NotFound)
        }
        e := ParentErr(&*w, cp)
        if e != core.SysErr.None {
            return Fail(e)
        }
        i = NewNode(w, cp, NFile)
        if i < 0 {
            return Fail(core.SysErr.NoSpace)
        }
    }
    r := NewHand(w, HFile, i, write || app)
    if r.err != core.SysErr.None {
        return r
    }
    hi := FindHand(&*w, Trunc[uint](r.value), HFile)
    w.handles.Mut(hi).append = app
    n := w.nodes.Mut(i)
    n.opens += 1
    if trunc && (write || app) {
        DropLive(n)
        n.size = 0
        n.modified = w.clock
    }
    return r
}

DiskRead(w: mut World, h: uint, buf: mut []u8, pos: i64): core.IoResult {
    i := FindHand(&*w, h, HFile)
    if i < 0 {
        if FindHand(&*w, h, HDir) >= 0 {
            return Fail(core.SysErr.IsDirectory)
        }
        return Fail(core.SysErr.Closed)
    }
    hd := w.handles.Get(i)
    at := if pos < 0 { hd.pos } else { pos }
    c := ReadNode(&w.nodes.Get(hd.obj), at, buf)
    if pos < 0 {
        w.handles.Mut(i).pos = at + i64(c)
    }
    return Ok(i64(c))
}

DiskWrite(w: mut World, h: uint, data: []u8, pos: i64): core.IoResult {
    i := FindHand(&*w, h, HFile)
    if i < 0 {
        return Fail(core.SysErr.Closed)
    }
    hd := w.handles.Get(i)
    if !hd.write {
        return Fail(core.SysErr.Denied)
    }
    // O_APPEND: every write at the end, whatever position it names.
    at := if hd.append { w.nodes.Get(hd.obj).size } else if pos < 0 { hd.pos } else { pos }
    // No bytes is a write of 0 at any position, with no other effect, as
    // natively (POSIX write of count 0 to a regular file); so the cut
    // below never meets a position past the largest file.
    if data.Len() == 0 {
        return Ok(0)
    }
    // past the largest file, EFBIG; across it, a short write.
    if at >= MaxFileSize {
        return FailOs(EFBig)
    }
    body := if i64(data.Len()) > MaxFileSize - at { data[0..int(MaxFileSize - at)] } else { data }
    clock := w.clock
    n := w.nodes.Mut(hd.obj)
    c := WriteNode(n, at, body)
    n.modified = clock
    if c == 0 && body.Len() > 0 {
        return Fail(core.SysErr.NoSpace)
    }
    if pos < 0 || hd.append {
        w.handles.Mut(i).pos = at + i64(c)
    }
    return Ok(i64(c))
}

// DiskTruncate is CallTruncate's work on the world (fscall.mk).
DiskTruncate(w: mut World, h: uint, size: i64): core.IoResult {
    i := FindHand(&*w, h, HFile)
    if i < 0 {
        return Fail(core.SysErr.Closed)
    }
    hd := w.handles.Get(i)
    if !hd.write {
        return Fail(core.SysErr.Denied)
    }
    if size > MaxFileSize {
        return FailOs(EFBig)
    }
    clock := w.clock
    n := w.nodes.Mut(hd.obj)
    if !TruncNode(n, size) {
        return Fail(core.SysErr.NoSpace)
    }
    n.modified = clock
    return Ok(0)
}

// TruncNode sets n's size: growing reads zeros past the old end (no page
// is made); shrinking zeroes the rest of the new last page (copied on
// write, so the durable block stays) and drops the live blocks of the
// pages wholly past it. False when the heap cannot give that page's copy.
TruncNode(n: mut Node, size: i64): bool {
    if size >= n.size {
        n.size = size
        return true
    }
    if size == 0 {
        DropLive(n)
        n.size = 0
        return true
    }
    off := int(size % i64(PageSize))
    if off != 0 {
        zero: [4096]u8 = [0; 4096]
        tail := PageSize - off
        if WriteNode(n, size, zero[0..tail]) < tail {
            return false
        }
    }
    first := (size + i64(PageSize) - 1) / i64(PageSize)
    var dropped := false
    for j in 0..n.pages.Len() {
        e := n.pages.Get(j)
        if e.at >= first && !raw.IsNull(e.live) {
            if !raw.Same(e.live, e.dur) {
                FreePage(e.live)
            }
            n.pages.Mut(j).live = raw.Null[u8]()
            MarkDirty(n, e.at)
            dropped = true
        }
    }
    if dropped {
        CompactPages(n)
    }
    n.size = size
    return true
}

DiskSize(w: mut World, h: uint): core.IoResult {
    i := FindHand(&*w, h, HFile)
    if i < 0 {
        return Fail(core.SysErr.Closed)
    }
    return Ok(w.nodes.Get(w.handles.Get(i).obj).size)
}

DiskSync(w: mut World, h: uint): core.IoResult {
    i := FindHand(&*w, h, HFile)
    if i < 0 {
        return Fail(core.SysErr.Closed)
    }
    if !SyncNode(w.nodes.Mut(w.handles.Get(i).obj)) {
        return Fail(core.SysErr.NoSpace)
    }
    return Ok(0)
}

// DiskClose closes a file or directory handle; false when h is neither.
DiskClose(w: mut World, h: uint): bool {
    var i := FindHand(&*w, h, HFile)
    if i >= 0 {
        ni := w.handles.Get(i).obj
        DropHand(w, i)
        n := w.nodes.Mut(ni)
        n.opens -= 1
        if !n.linked && n.opens == 0 {
            FreeNode(n)
        }
        return true
    }
    i = FindHand(&*w, h, HDir)
    if i >= 0 {
        di := w.handles.Get(i).obj
        DropHand(w, i)
        if di >= 0 {
            d := w.nodes.Mut(di)
            d.opens -= 1
            if !d.linked && d.opens == 0 {
                FreeNode(d)
            }
        }
        return true
    }
    return false
}

// Unlink takes node i out of the namespace; it is freed now, or by its
// last close.
Unlink(w: mut World, i: int) {
    n := w.nodes.Mut(i)
    n.linked = false
    if n.opens == 0 {
        FreeNode(n)
    }
}

// HasChild: some linked node sits directly under the cleaned path d.
HasChild(w: &World, d: []u8): bool {
    for i in 0..w.nodes.Len() {
        n := w.nodes.Get(i)
        if n.kind != 0 && n.linked && IsChild(n.path.View(), d) {
            return true
        }
    }
    return false
}

DiskRename(w: mut World, src: str, dst: str): core.IoResult {
    var sb: [4096]u8 = [0; 4096]
    var db: [4096]u8 = [0; 4096]
    sl := CleanPath(src, mut sb[..])
    dl := CleanPath(dst, mut db[..])
    if sl < 0 || dl < 0 {
        return Fail(core.SysErr.InvalidPath)
    }
    sp := sb[0..sl]
    dp := db[0..dl]
    si := FindNode(&*w, sp)
    if si < 0 {
        return Fail(core.SysErr.NotFound)
    }
    e := ParentErr(&*w, dp)
    if e != core.SysErr.None {
        return Fail(e)
    }
    if BytesEq(sp, dp) {
        return Ok(0)
    }
    skind := w.nodes.Get(si).kind
    di := FindNode(&*w, dp)
    if di >= 0 {
        dkind := w.nodes.Get(di).kind
        if dkind == NDir && skind == NFile {
            return Fail(core.SysErr.IsDirectory)
        }
        if dkind == NFile && skind == NDir {
            return Fail(core.SysErr.NotDirectory)
        }
        if dkind == NDir && HasChild(&*w, dp) {
            return FailOs(39)
        }
    }
    if skind == NDir && dp.Len() > sp.Len() && BytesEq(dp[0..sp.Len()], sp) && dp[sp.Len()] == b'/' {
        return FailOs(22)
    }
    // Build every new path before changing anything, so out of memory
    // leaves the namespace as it was.
    var moved := Vec[int].Empty()
    var fresh := Vec[Vec[u8]].Empty()
    for i in 0..w.nodes.Len() {
        n := w.nodes.Get(i)
        if n.kind == 0 || !n.linked {
            continue
        }
        np := n.path.View()
        var tail := -1
        if i == si {
            tail = np.Len()
        } else if skind == NDir && np.Len() > sp.Len() && BytesEq(np[0..sp.Len()], sp) && np[sp.Len()] == b'/' {
            tail = sp.Len()
        }
        if tail < 0 {
            continue
        }
        var q := Vec[u8].Empty()
        ok := AppendBytes(mut q, dp) && AppendBytes(mut q, np[tail..])
        if !ok || !moved.Push(i) || !fresh.Push(q) {
            q.Free()
            for k in 0..fresh.Len() {
                fresh.Mut(k).Free()
            }
            fresh.Free()
            moved.Free()
            return Fail(core.SysErr.NoSpace)
        }
    }
    if di >= 0 {
        Unlink(w, di)
    }
    for k in 0..moved.Len() {
        n := w.nodes.Mut(moved.Get(k))
        n.path.Free()
        n.path = fresh.Get(k)
    }
    fresh.Free()
    moved.Free()
    return Ok(0)
}

DiskRemove(w: mut World, p: str): core.IoResult {
    var buf: [4096]u8 = [0; 4096]
    pl := CleanPath(p, mut buf[..])
    if pl < 0 {
        return Fail(core.SysErr.InvalidPath)
    }
    cp := buf[0..pl]
    i := FindNode(&*w, cp)
    if i < 0 {
        if IsRoot(cp) {
            return Fail(core.SysErr.Denied)
        }
        return Fail(core.SysErr.NotFound)
    }
    if w.nodes.Get(i).kind == NDir && HasChild(&*w, cp) {
        return FailOs(39)
    }
    Unlink(w, i)
    return Ok(0)
}

DiskMkdir(w: mut World, p: str): core.IoResult {
    var buf: [4096]u8 = [0; 4096]
    pl := CleanPath(p, mut buf[..])
    if pl < 0 {
        return Fail(core.SysErr.InvalidPath)
    }
    cp := buf[0..pl]
    if IsRoot(cp) || FindNode(&*w, cp) >= 0 {
        return Fail(core.SysErr.Exists)
    }
    e := ParentErr(&*w, cp)
    if e != core.SysErr.None {
        return Fail(e)
    }
    if NewNode(w, cp, NDir) < 0 {
        return Fail(core.SysErr.NoSpace)
    }
    return Ok(0)
}

StatOf(n: &Node): core.IoStat {
    var kind := core.IoKindFile
    var size := n.size
    if n.kind == NDir {
        kind = core.IoKindDir
        size = 0
    }
    return core.IoStat(kind: kind, size: size, modified: n.modified)
}

DiskStat(w: mut World, p: str, st: mut core.IoStat): core.IoResult {
    var buf: [4096]u8 = [0; 4096]
    pl := CleanPath(p, mut buf[..])
    if pl < 0 {
        return Fail(core.SysErr.InvalidPath)
    }
    cp := buf[0..pl]
    if IsRoot(cp) {
        *st = core.IoStat(kind: core.IoKindDir, size: 0, modified: ClockStart)
        return Ok(0)
    }
    i := FindNode(&*w, cp)
    if i < 0 {
        return Fail(core.SysErr.NotFound)
    }
    *st = StatOf(&w.nodes.Get(i))
    return Ok(0)
}

// A directory stream names its directory by node index, or -1 for "." and
// -2 for "/"; pos is the next node index to look at.
DiskOpenDir(w: mut World, p: str): core.IoResult {
    var buf: [4096]u8 = [0; 4096]
    pl := CleanPath(p, mut buf[..])
    if pl < 0 {
        return Fail(core.SysErr.InvalidPath)
    }
    cp := buf[0..pl]
    var obj := -1
    if IsRoot(cp) {
        if cp[0] == b'/' {
            obj = -2
        }
    } else {
        i := FindNode(&*w, cp)
        if i < 0 {
            return Fail(core.SysErr.NotFound)
        }
        if w.nodes.Get(i).kind != NDir {
            return Fail(core.SysErr.NotDirectory)
        }
        obj = i
    }
    r := NewHand(w, HDir, obj, false)
    // a stream holds its directory open, as a file handle holds
    // its file, so a removed directory's slot is not reused under it (a
    // removed directory lists nothing, as POSIX readdir does).
    if r.err == core.SysErr.None && obj >= 0 {
        w.nodes.Mut(obj).opens += 1
    }
    return r
}

DiskReadDir(w: mut World, h: uint, name: mut []u8, st: mut core.IoStat): core.IoResult {
    i := FindHand(&*w, h, HDir)
    if i < 0 {
        return Fail(core.SysErr.Closed)
    }
    hd := w.handles.Get(i)
    var dot: [1]u8 = [b'.']
    if hd.obj == -2 {
        dot[0] = b'/'
    }
    var j := int(hd.pos)
    for j < w.nodes.Len() {
        n := w.nodes.Get(j)
        var under := false
        if n.kind != 0 && n.linked {
            if hd.obj < 0 {
                under = IsChild(n.path.View(), dot[..])
            } else {
                d := w.nodes.Get(hd.obj)
                under = d.kind == NDir && d.linked && IsChild(n.path.View(), d.path.View())
            }
        }
        if under {
            np := n.path.View()
            base := np[ParentLen(np)..]
            var b := base
            if b.Len() > 0 && b[0] == b'/' {
                b = b[1..]
            }
            if b.Len() > name.Len() {
                return core.IoResult(value: i64(b.Len()), err: core.SysErr.NoSpace, os: 0)
            }
            for k in 0..b.Len() {
                name[k] = b[k]
            }
            *st = StatOf(&n)
            w.handles.Mut(i).pos = i64(j + 1)
            return Ok(i64(b.Len()))
        }
        j += 1
    }
    w.handles.Mut(i).pos = i64(j)
    return Ok(0)
}

// RevertDisk is Restart's half for the disk: unlinked nodes go, the rest
// keep their durable state.
RevertDisk(w: mut World) {
    for i in 0..w.nodes.Len() {
        n := w.nodes.Mut(i)
        if n.kind == 0 {
            continue
        }
        if !n.linked {
            FreeNode(n)
            continue
        }
        n.opens = 0
        if n.kind == NFile {
            RevertNode(n)
        }
    }
}

FreeDisk(w: mut World) {
    for i in 0..w.nodes.Len() {
        n := w.nodes.Mut(i)
        if n.kind != 0 {
            FreeNode(n)
        }
    }
    w.nodes.Free()
}
