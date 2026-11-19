// Why: std.fs's File.Lock, Walker.Next, fs.Copy, fs.MakeDirAll and
// fs.RemoveAll use file operations that go through the Io's call entry, so
// a Sim must answer them itself for its tests to interrupt those calls and
// make them again.
//
// What: FsCall, which routes the call entry's file operations: the
// whole-file advisory lock (CallLock, CallUnlock), truncation
// (CallTruncate), stat by a path's bytes (CallStat), a directory opened
// inside a held one (IoOpDirOpenAt) and an entry removed inside a held one
// (IoOpRemoveAt).
//
// How: each answers as the native entry does on Linux (an OFD lock, per
// open handle; openat with O_NOFOLLOW | O_DIRECTORY; unlinkat), with the
// simulated disk's latency and its cancel before the call for the pool
// routes (DiskWait), and none for the lock's inline try. The simulated file
// system has no links, so nofollow changes nothing. Bytes that are not
// UTF-8 name nothing a Sim holds: Unsupported, as rt.OpenBytes answers.

import std.core
import std.raw
import std.rt

// FsCall answers the call a when its op is one of this file's, else none.
FsCall(io: Io, a: *core.IoCallArgs): ?core.IoResult {
    unsafe "a is the caller's record, live until the call entry returns" {
        op := (*a).op
        if op == core.IoOpLock {
            // The checked route (a waiting Lock's first try) is a
            // cancellation point, as natively (task.Stop): Canceled with
            // nothing taken.
            if (*a).route == core.IoRouteChecked && Halted(io) {
                return some(core.IoResult(value: 0, err: core.SysErr.Canceled, os: 0))
            }
            return some(CallLock(io, (*a).h, (*a).w.a == 2))
        }
        if op == core.IoOpUnlock {
            return some(CallUnlock(io, (*a).h))
        }
        // The pool routes check before they start, as natively (a
        // cancelled scope on the main task, too).
        if (op == core.IoOpStat || op == core.IoOpDirOpenAt || op == core.IoOpRemoveAt || op == core.IoOpTruncate) && Halted(io) {
            return some(core.IoResult(value: 0, err: core.SysErr.Canceled, os: 0))
        }
        if op == core.IoOpTruncate {
            return some(CallTruncate(io, (*a).h, (*a).w.a))
        }
        if op == core.IoOpStat {
            return some(CallStat(io, raw.Slice((*a).p, (*a).pn), mut (*a).w))
        }
        if op == core.IoOpDirOpenAt {
            return some(CallDirOpenAt(io, (*a).h, raw.Slice((*a).p, (*a).pn)))
        }
        if op == core.IoOpRemoveAt {
            return some(CallRemoveAt(io, (*a).h, raw.Slice((*a).p, (*a).pn), (*a).w.a == 1))
        }
    }
    return none
}

// CallLock is one try of h's whole-file lock: value 1 taken (or converted:
// a handle holds one lock, shared or exclusive), 0 another handle of the
// file holds a lock that excludes it. Closed when h is not an open file
// of this run. O(handles); allocates nothing.
CallLock(io: Io, h: uint, exclusive: bool): core.IoResult {
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    w := WorldOf(&e)
    i := FindHand(&*w, h, HFile)
    if i < 0 {
        Leave(&e)
        return Fail(core.SysErr.Closed)
    }
    node := w.handles.Get(i).obj
    for j in 0..w.handles.Len() {
        o := w.handles.Get(j)
        if j != i && o.kind == HFile && o.obj == node && o.lock != 0 && (exclusive || o.lock == 2) {
            Leave(&e)
            return Ok(0)
        }
    }
    w.handles.Mut(i).lock = if exclusive { 2 } else { 1 }
    Leave(&e)
    return Ok(1)
}

// CallTruncate sets the size of the file h to size, as ftruncate: a
// longer file reads zeros past its old end, a shorter one loses its tail,
// and the change is durable only after a sync, as a write's. Denied for a
// handle opened without write; Other for a negative size; Closed when h is
// not an open file of this run. The disk's latency and its cancel before
// the call, as the other pool routes. O(pages dropped);
// allocates at most one page (the copy of the new last page).
CallTruncate(io: Io, h: uint, size: i64): core.IoResult {
    if size < 0 {
        return Fail(core.SysErr.Other)
    }
    var e := NoEntry()
    g := Begin(io, FWrite, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    if !DiskWait(io, mut e) {
        return Dead()
    }
    r := DiskTruncate(WorldOf(&e), h, size)
    Leave(&e)
    return r
}

// CallUnlock drops h's lock; none held succeeds. O(1); allocates nothing.
CallUnlock(io: Io, h: uint): core.IoResult {
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    w := WorldOf(&e)
    i := FindHand(&*w, h, HFile)
    if i < 0 {
        Leave(&e)
        return Fail(core.SysErr.Closed)
    }
    w.handles.Mut(i).lock = 0
    Leave(&e)
    return Ok(0)
}

// Mode bits a Sim reports: a file 0o644, a directory 0o755 (a umask of
// 022 over open(2)'s and mkdir(2)'s defaults; the Sim keeps no modes).
const FileMode: i64 = 0o644
const DirMode: i64 = 0o755

// CallStat fills w as rt.StatEx documents (a the kind, b the size, c the
// modification time, d the mode bits) for the path p. O(files) (the node
// table's scan); allocates nothing.
CallStat(io: Io, p: []u8, w: mut core.IoWords): core.IoResult {
    s := str.From(p) else {
        return Fail(core.SysErr.Unsupported)
    }
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    if !DiskWait(io, mut e) {
        return Dead()
    }
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    r := DiskStat(WorldOf(&e), s, mut st)
    Leave(&e)
    if r.err == core.SysErr.None {
        *w = core.IoWords(a: i64(st.kind), b: st.size, c: st.modified, d: if st.kind == core.IoKindDir { DirMode } else { FileMode })
    }
    return r
}

// AtName: name is one entry's name, as a held directory's at-call takes
// it (not empty, no separator, no NUL, not . or ..).
AtName(name: []u8): bool {
    if name.Len() == 0 {
        return false
    }
    for x in name {
        if x == 0 || x == b'/' || x == b'\\' {
            return false
        }
    }
    if name.Len() == 1 && name[0] == b'.' {
        return false
    }
    return !(name.Len() == 2 && name[0] == b'.' && name[1] == b'.')
}

// AtPath writes into out the whole path of name inside the directory
// stream dir (0: name is the path): its length, or -1 when name is not an
// entry's name or a NUL is in it (InvalidPath), -2 when dir is not a
// directory stream of this run (Closed), -3 when the path does not fit.
AtPath(w: &World, dir: uint, name: []u8, out: mut []u8): int {
    if dir == 0 {
        if name.Len() == 0 || name.Len() > out.Len() {
            return if name.Len() == 0 { -1 } else { -3 }
        }
        for i in 0..name.Len() {
            if name[i] == 0 {
                return -1
            }
            out[i] = name[i]
        }
        return name.Len()
    }
    if !AtName(name) {
        return -1
    }
    i := FindHand(&*w, dir, HDir)
    if i < 0 {
        return -2
    }
    obj := w.handles.Get(i).obj
    var n := 1
    if obj == -2 {
        out[0] = b'/'
    } else if obj == -1 {
        out[0] = b'.'
    } else {
        dp := w.nodes.Get(obj).path.View()
        if dp.Len() + 1 + name.Len() > out.Len() {
            return -3
        }
        for k in 0..dp.Len() {
            out[k] = dp[k]
        }
        n = dp.Len()
    }
    if n + 1 + name.Len() > out.Len() {
        return -3
    }
    if out[n - 1] != b'/' {
        out[n] = b'/'
        n += 1
    }
    for k in 0..name.Len() {
        out[n + k] = name[k]
    }
    return n + name.Len()
}

// AtFail is AtPath's failure as a result.
AtFail(k: int): core.IoResult {
    if k == -2 {
        return Fail(core.SysErr.Closed)
    }
    return Fail(core.SysErr.InvalidPath)
}

// CallDirOpenAt opens name inside the directory stream dir (0: name is a
// whole path) as a directory stream: value the stream's runtime handle,
// which rt.DirOpenAt returns as it is on a Sim. A file is NotDirectory.
// O(files); allocates the handle's entry.
CallDirOpenAt(io: Io, dir: uint, name: []u8): core.IoResult {
    var e := NoEntry()
    g := Begin(io, FOpen, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    if !DiskWait(io, mut e) {
        return Dead()
    }
    w := WorldOf(&e)
    var buf: [4096]u8 = [0; 4096]
    k := AtPath(&*w, dir, name, mut buf[..])
    if k < 0 {
        Leave(&e)
        return AtFail(k)
    }
    s := str.From(buf[0..k]) else {
        Leave(&e)
        return Fail(core.SysErr.Unsupported)
    }
    r := DiskOpenDir(w, s)
    Leave(&e)
    return r
}

// CallRemoveAt removes name inside the directory stream dir (0: name is a
// whole path). is_dir is what the caller listed: a directory with is_dir
// false is IsDirectory, a file with is_dir true NotDirectory; a directory
// with entries is Other with rt.NotEmptyOs, which rt.RemoveNotEmpty
// recognises on every target. O(files); allocates nothing.
CallRemoveAt(io: Io, dir: uint, name: []u8, is_dir: bool): core.IoResult {
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    if !DiskWait(io, mut e) {
        return Dead()
    }
    w := WorldOf(&e)
    var buf: [4096]u8 = [0; 4096]
    k := AtPath(&*w, dir, name, mut buf[..])
    if k < 0 {
        Leave(&e)
        return AtFail(k)
    }
    s := str.From(buf[0..k]) else {
        Leave(&e)
        return Fail(core.SysErr.Unsupported)
    }
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    q := DiskStat(w, s, mut st)
    if q.err != core.SysErr.None {
        Leave(&e)
        return q
    }
    if (st.kind == core.IoKindDir) != is_dir {
        Leave(&e)
        return Fail(if is_dir { core.SysErr.NotDirectory } else { core.SysErr.IsDirectory })
    }
    var r := DiskRemove(w, s)
    if r.err == core.SysErr.Other && r.os == 39 {
        r = core.IoResult(value: 0, err: core.SysErr.Other, os: rt.NotEmptyOs)
    }
    Leave(&e)
    return r
}
