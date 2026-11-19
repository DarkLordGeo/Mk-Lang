// Why: the manifest is the archive's table of contents and the bytes a
// signature covers, so it must parse one way only and refuse unsafe paths.
//
// What: Codec, Entry and Manifest with Write and Files, ParseManifest, and
// SafePath, which rejects any path that could escape the extraction root.
//
// How: line-oriented text in the format below, parsed field by field into an
// arena; sorting by path gives each file's offset in the data for free.

// The manifest: what an archive holds, as text, one line a fact.
//
//   pack-manifest 1
//   codec deflate              or gzip
//   total 5120                 bytes of every file together
//   packed 2048 <blake3>       bytes of the compressed data, and its BLAKE3
//   dir docs
//   file 1200 <blake3> docs/a.txt
//
// Digests are 64 lowercase hex digits. A path is relative, separated by `/`,
// with no empty, `.` or `..` part, no `\` and no `:`; it is the rest of its
// line, so it may hold spaces. Entries are sorted by path, so a directory
// comes before what it holds, and a file's bytes start where the previous
// file's end.
import src.hex
import std.hash

/// How the data is compressed.
export enum Codec: u8 {
    Deflate = 1
    Gzip = 2
}

/// One file or directory of an archive.
export struct Entry {
    export path: str
    export is_dir: bool
    /// Bytes of the file; 0 for a directory.
    export size: i64
    /// Where the file's bytes start in the decompressed data.
    export offset: i64
    /// BLAKE3 of the file's bytes; zero for a directory.
    export digest: [32]u8
}

/// Everything the manifest says.
export struct Manifest {
    export codec: Codec
    export total: i64
    export packed: i64
    export packed_digest: [32]u8
    export entries: List[Entry]
}

/// Appends the manifest's text to `out`. Fails only with `OutOfMemory`.
/// O(n) in the entries; grows `out` by the text's length and allocates each
/// line once more in `out`'s arena.
export Manifest.Write(&self, a: &Arena, out: mut Text): OutOfMemory!void {
    codec := match self.codec {
        .Deflate { "deflate" }
        .Gzip { "gzip" }
    }
    try out.Push(try a.Fmt("pack-manifest 1\ncodec {codec}\ntotal {t}\n", (codec, t: self.total)))
    try out.Push(try a.Fmt("packed {n} {d}\n", (n: self.packed, d: hex.Hex(bytes: self.packed_digest[..]))))
    for e in &self.entries {
        line := if e.is_dir {
            try a.Fmt("dir {e.path}\n", (e))
        } else {
            try a.Fmt("file {e.size} {d} {e.path}\n", (e, d: hex.Hex(bytes: e.digest[..])))
        }
        try out.Push(line)
    }
}

/// The number of entries that are files.
export Manifest.Files(&self): int {
    var n := 0
    for e in &self.entries {
        if !e.is_dir {
            n += 1
        }
    }
    return n
}

/// Parses a manifest. Fails with `BadManifest` at the first line that is not
/// as the module comment says, or that breaks the order or the sizes, and
/// with `UnsafePath` at a path that could leave the extraction directory.
/// O(n); the entries and copies of their paths live in `a`, so the manifest
/// holds nothing of `text`.
export ParseManifest(a: &Arena, text: str): BadManifest|UnsafePath|OutOfMemory!Manifest from a {
    var m := Manifest(codec: Codec.Deflate, total: 0, packed: 0, packed_digest: [0; 32], entries: List[Entry].New(a))
    var n := 0
    var offset: i64 = 0
    var prev := ""
    for line in text.Lines() {
        n += 1
        if n == 1 {
            if line != "pack-manifest 1" {
                fail BadManifest(line: n)
            }
            continue
        }
        if n == 2 {
            m.codec = match line {
                "codec deflate" { Codec.Deflate }
                "codec gzip" { Codec.Gzip }
                _ { fail BadManifest(line: n) }
            }
            continue
        }
        if n == 3 {
            m.total = try Number(Field(line, "total ") else { fail BadManifest(line: n) }, n)
            continue
        }
        if n == 4 {
            rest := Field(line, "packed ") else {
                fail BadManifest(line: n)
            }
            (size, digest, tail) := try SizeAndDigest(rest, n)
            if tail.Len() != 0 {
                fail BadManifest(line: n)
            }
            m.packed = size
            m.packed_digest = digest
            continue
        }
        var e := Entry(path: "", is_dir: false, size: 0, offset: offset, digest: [0; 32])
        if path := Field(line, "dir ") {
            e.is_dir = true
            e.path = try a.Dup(path)
        } else {
            rest := Field(line, "file ") else {
                fail BadManifest(line: n)
            }
            (size, digest, tail) := try SizeAndDigest(rest, n)
            e.size = size
            e.digest = digest
            e.path = try a.Dup(tail)
            offset += size
        }
        if !SafePath(e.path) {
            fail UnsafePath(line: n)
        }
        if m.entries.Len() > 0 && e.path <= prev {
            fail BadManifest(line: n)
        }
        prev = e.path
        try m.entries.Push(e)
    }
    if n < 4 || offset != m.total {
        fail BadManifest(line: n)
    }
    return m
}

/// The rest of `line` after `prefix`, or none when it does not start so.
Field(line: str, prefix: str): ?str from line {
    if !line.StartsWith(prefix) {
        return none
    }
    return some(line[prefix.Len()..])
}

/// A size, a space, a digest, then the rest after one more space, if any.
SizeAndDigest(rest: str, n: int): BadManifest!(size: i64, digest: [32]u8, tail: str) {
    space := rest.Find(" ") else {
        fail BadManifest(line: n)
    }
    size := try Number(rest[..space], n)
    after := rest[space + 1..]
    if after.Len() < 64 {
        fail BadManifest(line: n)
    }
    var digest: [32]u8 = [0; 32]
    hex.Decode(after[..64], mut digest[..]) else {
        fail BadManifest(line: n)
    }
    tail := if after.Len() == 64 {
        ""
    } else if after.Byte(64) == b' ' {
        after[65..]
    } else {
        fail BadManifest(line: n)
    }
    return (size, digest, tail)
}

/// A decimal count with no sign, as the manifest writes it.
Number(text: str, n: int): BadManifest!i64 {
    if text.Len() == 0 || text.Len() > 18 {
        fail BadManifest(line: n)
    }
    for i in 0..text.Len() {
        c := text.Byte(i)
        if c < b'0' || c > b'9' {
            fail BadManifest(line: n)
        }
    }
    return text.ParseInt() else {
        fail BadManifest(line: n)
    }
}

/// Whether `path` stays inside the directory it is extracted to.
export SafePath(path: str): bool {
    if path.Len() == 0 || path.Has("\\") || path.Has(":") {
        return false
    }
    for part in path.Split("/") {
        if part.Len() == 0 || part == "." || part == ".." {
            return false
        }
    }
    return true
}

/// BLAKE3 of `data`, as the manifest records it.
export Digest(data: []u8): [32]u8 {
    return hash.Blake3(data)
}

test "a manifest parses back to what was written" (sys: &System) {
    a := sys.arena
    var m := Manifest(codec: Codec.Gzip, total: 3, packed: 9, packed_digest: Digest(b"zzz"), entries: List[Entry].New(a))
    try m.entries.Push(Entry(path: "d", is_dir: true, size: 0, offset: 0, digest: [0; 32]))
    try m.entries.Push(Entry(path: "d/a b", is_dir: false, size: 3, offset: 0, digest: Digest(b"abc")))
    var text := Text.New(a)
    try m.Write(a, mut text)
    back := try ParseManifest(a, text.Str())
    Assert(back.codec == Codec.Gzip && back.total == 3 && back.packed == 9)
    Assert(back.entries.Len() == 2 && back.entries[1].path == "d/a b")
    Assert(back.entries[1].digest[0] == m.entries[1].digest[0])
}

test "SafePath refuses paths that leave the directory" {
    Assert(SafePath("a/b.txt"))
    Assert(!SafePath("../a"))
    Assert(!SafePath("a//b"))
    Assert(!SafePath("/etc/passwd"))
    Assert(!SafePath("c:/x"))
    Assert(!SafePath("a\\..\\b"))
}
