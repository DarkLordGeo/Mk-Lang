// Why: std.fs's RemoveAll must delete a tree without following a link that
// someone swaps in mid-walk, so it opens and removes entries relative to a
// directory it holds, never by a path it rebuilds.
//
// What: DirOpenAt and RemoveAt (std-internal), with their pool jobs,
// HeldBlock, and RemoveNotEmpty with NotEmptyOs.
//
// How: a directory is opened inside a held one, with no link followed, as a
// directory stream the readdir entry reads, and an entry is removed inside
// a held directory (sys/<target>/removeat.mk: Linux openat and unlinkat
// relative to the held descriptor; Windows the held directory's path, read
// once and kept with its stream, pinned while held, and a POSIX-semantics
// delete). The calls go through the Io's call entry on the pool, so a
// program links them only when it uses them, and std.sim and a Recorder's
// Tape answer Unsupported. The ring is not used: an UNLINKAT of a 9p path
// on io_uring is unchecked.
//
// RemoveAll's walk over these: open the root with DirOpenAt(io, 0, path);
// for each entry of a held directory d, RemoveAt(io, d, name, false) for
// anything but a directory, and for a directory DirOpenAt(io, d, name),
// its walk, its handle closed, then RemoveAt(io, d, name, true). A
// directory DirOpenAt holds cannot be removed until it is closed (Windows).

import std.core
import std.raw
import std.sys

/// DirOpenAt opens the directory name inside the directory stream dir (a
/// runtime handle from DirOpenAt or the opendir entry; 0: name is a whole
/// path) and returns it as a directory stream: value its runtime handle,
/// read with core.IoReadDir, closed with core.IoClose or Abandon. A link is
/// never followed: a link, or anything but a directory, is NotDirectory.
/// With a dir, name must be one entry's name (no separator, not . or ..):
/// else InvalidPath, before any call. Bytes that are not WTF-8 are
/// InvalidPath on Windows. Pool, cancellable before the call
/// starts; a handle the call made is always wrapped or closed. The stream
/// is read for its entries' kinds (fs.Walk and RemoveAll read nothing
/// else): on Linux each kind comes from d_type, with size and time 0, and a
/// statx only where d_type is unknown. On Windows the stream keeps
/// its directory's path, so a DirOpenAt or RemoveAt inside it reads no
/// final path from the OS; and where sys.DirFillOnOpen (Windows),
/// the job also makes the stream's first fill, so listing a directory
/// costs one pool hop fewer, and a listing that fits one fill ends with no
/// further hop. O(1) system calls; allocates one HandleRec and the
/// stream's block on the runtime heap.
export DirOpenAt(io: Io, dir: uint, name: []u8): core.IoResult {
    var a := CallOf(core.IoOpDirOpenAt, core.IoRoutePool, dir, DirOpenAtJob)
    a.p = raw.Data(name)
    a.pn = name.Len()
    native := core.IsNative(io)
    var blk := raw.Null[u8]()
    if native {
        // Made before the job, which marks it (sys.DirOpenAt) and, where
        // sys.DirFillOnOpen, fills it; on the heap's exhaustion the job
        // does neither and the wrap below fails as NewDirHandle's.
        blk = NewDirBlock()
        a.q = blk
        a.qn = if raw.IsNull(blk) { 0 } else { sys.DirBlockBytes }
        a.out = HeldBlock(dir)
        a.outn = if raw.IsNull(a.out) { 0 } else { sys.DirBlockBytes }
    }
    r := Run(io, mut a)
    if r.err != core.SysErr.None || !native {
        // An Io that is not native (std.sim) answers with its own runtime
        // handle, which needs no wrapping.
        if !raw.IsNull(blk) {
            HeapFree(blk, sys.DirBlockBytes, 16)
        }
        return r
    }
    os := Trunc[uint](r.value)
    h := if raw.IsNull(blk) { NewDirHandle(os) } else { DirHandleWith(os, blk) }
    if h == 0 {
        _ = sys.Close(os)
        return core.IoResult(value: 0, err: core.SysErr.Other, os: 0)
    }
    return core.IoResult(value: Trunc[i64](h), err: core.SysErr.None, os: 0)
}

// HeldBlock is the stream block of dir, a runtime handle of a native Io
// (0: none), when it is a directory stream; null otherwise. The block is
// the handle's, alive while its caller holds it, so for the whole call;
// sys reads only what DirOpenAt wrote there before the handle was
// returned (the kept path), never what a fill writes.
HeldBlock(dir: uint): *u8 {
    if dir == 0 {
        return raw.Null[u8]()
    }
    r := Rec(dir)
    unsafe "dir is a live runtime handle the caller holds" {
        if (*r).kind != KindDir {
            return raw.Null[u8]()
        }
        return (*r).dir
    }
}

DirOpenAtJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record; q, when not null, the new stream's zeroed block of qn bytes, which no one else uses until the call returns; out, when not null, dir's block of outn bytes" {
        fresh := raw.SliceMut((*a).q, (*a).qn)
        r := sys.DirOpenAt((*a).os, raw.Slice((*a).out, (*a).outn), In(a), fresh)
        Set(a, r)
        if r.err == core.SysErr.None && sys.DirFillOnOpen && (*a).qn == sys.DirBlockBytes {
            // The first fill's own error is left for the first read: the
            // block stays unfilled, so that read fills again and reports it.
            _ = sys.DirFill(Trunc[uint](r.value), raw.SliceMut((*a).q, (*a).qn))
        }
    }
}

/// RemoveAt removes the entry name inside the directory stream dir (0:
/// name is a whole path); a link is removed itself, never its target.
/// is_dir says what the caller listed: a directory with is_dir false is
/// IsDirectory, anything else with is_dir true NotDirectory, so an entry
/// replaced since it was listed is reported, not removed the wrong way. A
/// directory that is not empty is Other (RemoveNotEmpty says so on every
/// target); InvalidPath as DirOpenAt; else the OS's (sys.MapError). Pool,
/// cancellable before the call starts; one entry, removed or not.
/// Inside a stream DirOpenAt opened, Windows builds the entry's path from
/// the stream's kept path. O(1) system calls; allocates nothing.
export RemoveAt(io: Io, dir: uint, name: []u8, is_dir: bool): core.IoResult {
    var a := CallOf(core.IoOpRemoveAt, core.IoRoutePool, dir, RemoveAtJob)
    a.p = raw.Data(name)
    a.pn = name.Len()
    a.w.a = if is_dir { 1 } else { 0 }
    if core.IsNative(io) {
        a.q = HeldBlock(dir)
        a.qn = if raw.IsNull(a.q) { 0 } else { sys.DirBlockBytes }
    }
    return Run(io, mut a)
}

RemoveAtJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record; q, when not null, dir's block of qn bytes" {
        Set(a, sys.RemoveAt((*a).os, raw.Slice((*a).q, (*a).qn), In(a), (*a).w.a == 1))
    }
}

/// NotEmptyOs is the os code RemoveAt's answer carries for a directory
/// that is not empty on this target (sys.NotEmptyCode), for an Io that
/// simulates the call (std.sim) so RemoveNotEmpty recognises its answer.
export const NotEmptyOs: i64 = sys.NotEmptyCode

/// RemoveNotEmpty: r is RemoveAt's answer for a directory that is not
/// empty (Linux ENOTEMPTY, Windows ERROR_DIR_NOT_EMPTY), which RemoveAll
/// meets when an entry was added during its walk. O(1); allocates nothing.
export RemoveNotEmpty(r: core.IoResult): bool {
    return r.err == core.SysErr.Other && r.os == sys.NotEmptyCode
}
