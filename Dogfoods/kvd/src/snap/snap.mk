// Why: recovery and offline reads need the whole store at one point in time,
// readable without loading it.
// What: Write, Check, Load, Show, and View with Find and Entry.
// How: a sorted, indexed file, mapped read-only; the format follows.

// The snapshot index: the whole store at one generation, its keys sorted, with
// an index of entry offsets, so a key is found by binary search in the file's
// bytes. kvd writes a snapshot to a new file and renames it into place, never
// in place, so the bytes of a snapshot file never change once it has its name;
// that is why mapping one is sound.
//
//   header, 40 bytes   "KVDSNAP1", generation u64, written at (Unix ns) i64,
//                      entry count u64, CRC-32 of the first 32 bytes u32,
//                      four zero bytes
//   index              count x u64: each entry's offset in the file
//   entries            key length u16, value length u32, key, value; by key
//   trailer            CRC-32 u32 of every byte between header and trailer
//
// Every integer is little-endian.
import std.fs
import std.hash
import std.time
import src.le
import src.store

/// The size of the header, in bytes.
export const HeaderSize: int = 40

/// The size of an entry's key and value lengths, in bytes.
const EntryHead: int = 6

/// The first eight bytes of every snapshot.
const Magic: str = "KVDSNAP1"

export error NotSnapshot
export error Damaged(offset: i64)

/// What reading or writing a snapshot can fail with. `NotSnapshot` is a file
/// with no snapshot header; `Damaged` a snapshot whose bytes do not hold.
export type Error = NotSnapshot | Damaged | fs.Error | WriteError | OutOfMemory

/// What a snapshot's header says.
export struct Info {
    export generation: u64
    export written: time.Time
    export count: int
    export bytes: i64
}

/// A checked snapshot's bytes, read in place.
export view struct View {
    data: []u8
    count: int
}

/// One entry, as views of the snapshot's bytes.
export struct Entry {
    export key: str
    export value: str
}

/// Checks `data`, a whole snapshot: its header, its trailer's checksum, and
/// that every index offset points inside the entries. O(n); allocates
/// nothing.
export Check(data: []u8): Error!View from data {
    if data.Len() < HeaderSize + 4 {
        fail NotSnapshot
    }
    magic := str.From(data[..8]) else {
        fail NotSnapshot
    }
    if magic != Magic || le.U32(data, 32) != hash.Crc32(data[..32]) {
        fail NotSnapshot
    }
    n := le.U64(data, 24)
    end := data.Len() - 4
    if n > Cast[u64](end - HeaderSize) / 8 {
        fail Damaged(offset: 24)
    }
    count := Cast[int](n)
    if le.U32(data, end) != hash.Crc32(data[HeaderSize..end]) {
        fail Damaged(offset: i64(end))
    }
    v := View(data: data, count: count)
    for i in 0..count {
        at := v.Offset(i)
        if at < HeaderSize + 8 * count || at > end - EntryHead {
            fail Damaged(offset: i64(HeaderSize + 8 * i))
        }
        klen := int(le.U16(data, at))
        vlen := Cast[int](le.U32(data, at + 2))
        if klen > end - at - EntryHead || vlen > end - at - EntryHead - klen {
            fail Damaged(offset: i64(at))
        }
    }
    return v
}

/// What the header says.
export View.Info(&self): Info {
    return Info(
        generation: le.U64(self.data, 8),
        written: time.FromUnix(0, Cast[i64](le.U64(self.data, 16))),
        count: self.count,
        bytes: i64(self.data.Len()),
    )
}

/// The number of entries.
export View.Len(&self): int {
    return self.count
}

/// The offset of entry `i`.
View.Offset(&self, i: int): int {
    return Cast[int](le.U64(self.data, HeaderSize + 8 * i))
}

/// Entry `i`, in key order; `none` when its bytes are not UTF-8.
export View.Entry(&self, i: int): ?Entry from self.data {
    at := self.Offset(i)
    klen := int(le.U16(self.data, at))
    vlen := Cast[int](le.U32(self.data, at + 2))
    k := at + EntryHead
    key := str.From(self.data[k..k + klen]) else {
        return none
    }
    value := str.From(self.data[k + klen..k + klen + vlen]) else {
        return none
    }
    return some(Entry(key: key, value: value))
}

/// The value of `key`, by binary search over the index. O(log n · key);
/// allocates nothing.
export View.Find(&self, key: str): ?str from self.data {
    var lo := 0
    var hi := self.count
    for lo < hi {
        mid := lo + (hi - lo) / 2
        e := self.Entry(mid) else {
            return none
        }
        if e.key == key {
            return some(e.value)
        }
        if e.key < key {
            lo = mid + 1
        } else {
            hi = mid
        }
    }
    return none
}

/// Writes every entry of `s` as the snapshot of generation `generation` at
/// `path`, replacing any file there, and syncs it; holds an exclusive lock
/// on the file while it writes. The keys are sorted in `arena`. O(n log n);
/// allocates a list of the keys.
export Write(arena: &Arena, io: Io, path: str, generation: u64, s: &store.Store): Error!Info {
    var keys := try s.Keys(arena, "")
    keys.Sort(fn(x: &str, y: &str): bool { return *x < *y })
    var o := fs.ReadWrite
    o.create = true
    o.truncate = true
    var f := try fs.OpenWith(io, path, o)
    onerr f.Abandon()
    try f.Lock(io, fs.LockMode.Exclusive)
    written := time.Wall(io)
    var head: [40]u8 = [0; 40]
    for i, c in Magic.AsBytes() {
        head[i] = c
    }
    le.PutU64(mut head[..], 8, generation)
    le.PutU64(mut head[..], 16, Cast[u64](written.UnixNanos()))
    le.PutU64(mut head[..], 24, Cast[u64](keys.Len()))
    le.PutU32(mut head[..], 32, hash.Crc32(head[..32]))
    var buf: [8192]u8 = [0; 8192]
    var w := f.Writer(io, mut buf[..])
    try w.Bytes(head[..])
    var sum := hash.Crc32State.New()
    var at := HeaderSize + 8 * keys.Len()
    var slot: [8]u8 = [0; 8]
    for k in keys {
        le.PutU64(mut slot[..], 0, Cast[u64](at))
        try w.Bytes(slot[..])
        sum.Bytes(slot[..])
        at += EntryHead + k.Len() + (s.Get(k) else { "" }).Len()
    }
    var eh: [6]u8 = [0; 6]
    for k in keys {
        v := s.Get(k) else { "" }
        le.PutU16(mut eh[..], 0, Cast[u16](k.Len()))
        le.PutU32(mut eh[..], 2, Cast[u32](v.Len()))
        try w.Bytes(eh[..])
        try w.Str(k)
        try w.Str(v)
        sum.Bytes(eh[..])
        sum.Bytes(k.AsBytes())
        sum.Bytes(v.AsBytes())
    }
    var tail: [4]u8 = [0; 4]
    le.PutU32(mut tail[..], 0, sum.Sum())
    try w.Bytes(tail[..])
    try w.Flush()
    try f.Sync(io)
    try f.Unlock(io)
    try f.Close(io)
    return Info(generation: generation, written: written, count: keys.Len(), bytes: i64(at + 4))
}

/// Maps the whole of `f`, `size` bytes, read-only.
MapOwn(io: Io, f: &fs.File, size: int): fs.Error!fs.Mapping {
    unsafe "a kvd snapshot is never written in place: a new one is renamed over it, so the mapped bytes cannot change; the caller holds the file's shared lock" {
        return try fs.Map(io, f, 0, size)
    }
}

/// Closes `f`, abandoning it if the close fails: it was only read.
Release(f: fs.File, io: Io) {
    f.Close(io) else {
        f.Abandon()
    }
}

/// Opens the snapshot at `path` and takes a shared lock on it: waits for
/// the lock, or with `try_lock` gives up when another holder has it.
/// `none` when there is no snapshot, or it is locked and `try_lock`.
OpenLocked(io: Io, path: str, try_lock: bool): fs.Error!?fs.File {
    var f := fs.OpenWith(io, path, fs.ReadOnly) else e {
        match e {
            fs.NotFound {
                return none
            }
            _ {
                fail e
            }
        }
    }
    onerr f.Abandon()
    if !try_lock {
        try f.Lock(io, fs.LockMode.Shared)
        return some(f)
    }
    if !try f.TryLock(io, fs.LockMode.Shared) {
        Release(f, io)
        return none
    }
    return some(f)
}

/// Reads all `size` bytes of `f` into `arena`, for a system that maps no
/// files (std.sim). O(size).
ReadCopy(arena: &Arena, io: Io, f: &fs.File, size: int): Error![]u8 from arena {
    data := try arena.Slice[u8](size)
    var got := 0
    for got < size {
        n := try f.ReadAt(io, i64(got), mut data[got..])
        if n == 0 {
            fail Damaged(offset: i64(got))
        }
        got += n
    }
    return data[..]
}

/// Puts every entry of `v` into `s`.
Fill(a: &Alloc into s, v: &View, s: mut store.Store): Error!Info {
    for i in 0..v.Len() {
        e := v.Entry(i) else {
            fail Damaged(offset: i64(v.Offset(i)))
        }
        try s.Apply(a, store.Op.Set, e.key, e.value)
    }
    return v.Info()
}

/// Loads the snapshot at `path` into `s`, which should be empty, and says
/// what it held; `none` when there is none. Maps the file read-only under a
/// shared lock, or reads it into `arena` where the system maps no files.
/// O(n); allocates what the store holds.
export Load(a: &Alloc into s, arena: &Arena, io: Io, path: str, s: mut store.Store): Error!?Info {
    f := try OpenLocked(io, path, false) else {
        return none
    }
    defer Release(f, io)
    size := Cast[int](try f.Size(io))
    m := MapOwn(io, &f, size) else e {
        match e {
            fs.Unsupported {
                data := try ReadCopy(arena, io, &f, size)
                return some(try Fill(a, &try Check(data[..]), s))
            }
            _ {
                fail e
            }
        }
    }
    v := Check(m.Bytes()) else e {
        m.Unmap()
        fail e
    }
    info := Fill(a, &v, s) else e {
        m.Unmap()
        fail e
    }
    m.Unmap()
    return some(info)
}

/// Writes what the snapshot at `path` holds to `out`: its header and every
/// entry, or with `key` only that key's value, found by binary search.
/// Takes the shared lock only if it is free; returns false when the file is
/// missing or locked. O(n), or O(log n) for a key.
export Show(arena: &Arena, io: Io, path: str, key: ?str, out: mut Writer): Error!bool {
    f := try OpenLocked(io, path, true) else {
        return false
    }
    defer Release(f, io)
    size := Cast[int](try f.Size(io))
    m := MapOwn(io, &f, size) else e {
        match e {
            fs.Unsupported {
                data := try ReadCopy(arena, io, &f, size)
                try Write1(&try Check(data[..]), key, out)
                return true
            }
            _ {
                fail e
            }
        }
    }
    v := Check(m.Bytes()) else e {
        m.Unmap()
        fail e
    }
    Write1(&v, key, out) else e {
        m.Unmap()
        fail e
    }
    m.Unmap()
    return true
}

/// The body of `Show` over checked bytes.
Write1(v: &View, key: ?str, out: mut Writer): Error!void {
    if k := key {
        if value := v.Find(k) {
            try Print(out, "{value}\n", (value))
        } else {
            try Print(out, "(none)\n")
        }
        return
    }
    info := v.Info()
    try Print(out, "generation {info.generation}, {info.count} keys, {info.bytes} bytes, written {info.written}\n", (info))
    for i in 0..v.Len() {
        e := v.Entry(i) else {
            fail Damaged(offset: i64(v.Offset(i)))
        }
        try Print(out, "{e.key} = {e.value}\n", (e))
    }
}
