// Why: reading a large file through a mapping avoids copying it into an
// arena.
//
// What: Map and Mapping (Bytes, Unmap): a read-only private mapping of a
// file's range.
//
// How: mmap(PROT_READ, MAP_PRIVATE) on Linux and a PAGE_READONLY section
// viewed with FILE_MAP_READ on Windows (std.rt's FileMap, which rounds the
// offset down to the page or the 64 KiB allocation granularity and returns
// the address of the byte asked for). Calling fs.Map is E1101 outside
// unsafe: the caller answers that no process changes the mapped bytes while
// the Mapping lives, since another process's change would be undefined
// behaviour. A range another process shortens faults, and the runtime turns
// the fault into a panic report.

import std.core
import std.rt

/// A read-only view of a range of a file in memory, made by Map. Mapping
/// is linear: end it with Unmap. It owns the mapping, not the file, which
/// may be closed while the Mapping lives.
export linear struct Mapping {
    // addr is the address of the first byte asked for, len their count;
    // base and blen are the mapping itself, which Unmap releases. All 0 for
    // an empty mapping.
    addr: i64
    len: int
    base: i64
    blen: i64
}

/// Maps len bytes of f, from offset on, into memory, read-only and
/// private. Map can be called only in an unsafe block (E1101 elsewhere):
/// the caller answers that no process changes the mapped bytes while the
/// Mapping lives. len 0 gives an empty mapping and makes no system call.
///
/// Os(0) for a negative offset or len, or a range past the end of the
/// file, so no mapped page lies past the end on either target. Denied when
/// f was not opened for reading. Any other failure is the OS's, as its
/// fs.Error member.
///
/// O(1): a size query and a mapping call (on Windows a section, made and
/// closed, and a view); allocates nothing but the mapping's address space,
/// whose pages the OS reads from the file on first touch.
export Map(io: Io, f: &File, offset: i64, len: int): Error!Mapping {
    if offset < 0 || len < 0 {
        fail Os(code: 0)
    }
    if len == 0 {
        return Mapping(addr: 0, len: 0, base: 0, blen: 0)
    }
    size := try Check(core.IoSize(io, f.cur.h))
    if offset > size || i64(len) > size - offset {
        fail Os(code: 0)
    }
    var w := core.IoWords(a: 0, b: 0, c: 0, d: 0)
    addr := try Check(rt.FileMap(io, f.cur.h, offset, len, mut w))
    return Mapping(addr: addr, len: len, base: w.c, blen: w.d)
}

/// The mapped bytes, readable while the Mapping lives. Reading them may
/// fault pages in from the file. Reading a page that another process cut
/// from the file panics; it is not undefined behaviour. O(1); allocates
/// nothing.
export Mapping.Bytes(&self): []u8 from self {
    return rt.MapBytes(self.addr, self.len)
}

/// Releases the mapping; the file is not touched. It cannot be cancelled
/// and cannot fail: unmapping a range the OS mapped fails only on a broken
/// handle, and that error is ignored, as Abandon ignores a close's error.
/// O(1): one system call, none for an empty mapping; allocates nothing.
export Mapping.Unmap(self) {
    Mapping(addr: _, len, base, blen) := self
    if len > 0 {
        _ = rt.FileUnmap(rt.NativeIo(), base, blen)
    }
}
