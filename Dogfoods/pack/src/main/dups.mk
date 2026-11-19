// Why: `pack list --dups` shows which files of an archive repeat another's
// contents, from the manifest alone.
//
// What: Content, Group and Dups.
//
// How: the entries are grouped by their BLAKE3 digests, already in the
// manifest, so no data is read or decompressed.

import src.archive
import src.hex
import std.crypto

/// A file's BLAKE3 digest as a table key. A byte array has neither `Eq`
/// nor `Hash`, so the key compares and hashes its bytes itself.
struct Content {
    digest: [32]u8
}

impl Eq for Content {
    Eq(&self, other: &Content): bool {
        return crypto.Equal(self.digest[..], other.digest[..])
    }
}

impl Hash for Content {
    Hash(&self, h: mut Hasher) {
        h.Bytes(self.digest[..])
    }
}

/// The files of one content: the first and last of them in manifest
/// order, chained from first to last through the `next` of `Dups`.
struct Group {
    first: int
    last: int
    count: int
}

/// Prints each set of files with the same contents, in manifest order: a
/// line with the count, the size and the digest's first 8 bytes, then one
/// line a file, and last what the copies cost. The digests come from the
/// archive, which anyone may have written, so the table is keyed with a
/// random key. O(n) for n entries; allocates the table and n ints in `a`.
Dups(c: &Ctx, a: &Arena, path: str, m: &archive.Manifest): Error!void {
    key := HashKey.New(c.io) else e {
        fail c.Fail(path, e)
    }
    var groups := Map[Content, Group].NewKeyed(a, key)
    entries := m.entries[..]
    // next[i] is the file after entry i with the same contents
    next := try a.Slice[int](entries.Len())
    for i in 0..entries.Len() {
        if entries[i].is_dir {
            continue
        }
        g := try groups.Ensure(Content(digest: entries[i].digest), Group(first: i, last: i, count: 0))
        if g.count > 0 {
            next[g.last] = i
        }
        g.last = i
        g.count += 1
    }
    var sets := 0
    var copies := 0
    var spare: i64 = 0
    for i in 0..entries.Len() {
        e := &entries[i]
        if e.is_dir {
            continue
        }
        g := groups.Get(&Content(digest: e.digest)) else {
            continue
        }
        if g.first != i || g.count < 2 {
            continue
        }
        sets += 1
        copies += g.count - 1
        spare += e.size * i64(g.count - 1)
        try c.sys.Print("= {n} files of {e.size} bytes, blake3 {d}\n", (n: g.count, e, d: hex.Hex(bytes: e.digest[..8])))
        var j := i
        for _ in 0..g.count {
            f := &entries[j]
            try c.sys.Print("  {f.path}\n", (f))
            j = next[j]
        }
    }
    if sets == 0 {
        try c.sys.Print("no two files have the same contents\n")
        return
    }
    try c.sys.Print("{sets} sets of files with the same contents; the {copies} copies hold {spare} bytes\n", (sets, copies, spare))
}
