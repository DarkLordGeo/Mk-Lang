// archive, Mk, std: the task of ../README.md. `create` walks the tree with
// fs.Walk, keeps the regular files, sorts them by their relative path with
// `/` in byte order (List.Sort), reads each whole with fs.ReadBytes into an
// arena reset after every file, hashes it with hash.Sha256 and appends it
// to one payload HeapList; the payload is compressed by compress.Deflate
// (std's one level), the manifest is signed by crypto.Ed25519 (FromSeed on
// the fixed seed), and the archive body is sealed by crypto.AesGcm under
// the fixed key and nonce, then written by fs.WriteAll. `open` reverses it:
// it reads the archive with fs.ReadBytes, opens the seal, verifies the
// signature, inflates the payload with compress.Inflate, checks every
// file's SHA-256 against the manifest, and prints the manifest and a
// summary line to stdout.
// Usage: archive create <dir> <out> [<files>] | archive open <archive>.

import std.compress
import std.crypto
import std.encoding
import std.fs
import std.hash

const Magic: str = "MKARCH01"

// The most a manifest's sizes may add up to: far above any archive this
// program can hold in memory, and low enough that the sum never overflows.
const MaxTotal: int = 1 << 62

/// The archive is not one: too short, or the wrong magic.
error NotArchive
/// The manifest's length runs past the archive's end.
error CutArchive
/// The manifest's signature does not verify.
error BadSignature
/// A manifest line is not `<digest> <size> <path>`.
error BadManifest
/// The payload does not inflate to the manifest's total size.
error SizeDiffers
/// A file's SHA-256 is not the manifest's.
error DigestDiffers
/// The command line is not one of the usages.
error Usage

type ArchiveError = fs.Error | OutOfMemory | WriteError | ParseError | Utf8Error | crypto.Error | compress.Error | encoding.Error | NotArchive | CutArchive | BadSignature | BadManifest | SizeDiffers | DigestDiffers | Usage

/// A file of the tree: its path relative to the root, with `/`, and the
/// path the walk gave, to read it by.
struct Item {
    rel: str
    path: str
}

/// A manifest entry, as open reads it back.
struct Entry {
    digest: []u8
    size: int
}

/// Fills `out` with the bytes first, first + 1, and so on: the row's fixed
/// test material (README.md, Format).
Fixed(first: int, out: mut []u8) {
    for i in 0..out.Len() {
        out[i] = Cast[u8](first + i)
    }
}

/// The relative path of `p` under `root`, with `/` between its parts.
Rel(a: &Arena, root: str, p: str): OutOfMemory|Utf8Error!str from a {
    var start := root.Len()
    if !root.EndsWith("/") && !root.EndsWith("\\") {
        start += 1
    }
    tail := p.AsBytes()[start..]
    b := try a.Slice[u8](tail.Len())
    for i in 0..tail.Len() {
        b[i] = if tail[i] == 92 { 47 } else { tail[i] }
    }
    return try str.From(b[..])
}

/// Writes the u64 `x` little-endian into the 8 bytes of `out`.
PutLen(x: int, out: mut []u8) {
    for i in 0..8 {
        out[i] = Cast[u8]((x >> (8 * i)) & 255)
    }
}

/// Reads the little-endian u64 of the first 8 bytes of `b`.
GetLen(b: []u8): int {
    var x := 0
    for i in 0..8 {
        x = x | (int(b[i]) << (8 * i))
    }
    return x
}

Create(sys: &System, root: str, out: str, limit: int): ArchiveError!void {
    var items := List[Item].New(sys.arena)
    var w := try fs.Walk(sys.heap, sys.io, root)
    onerr w.Close()
    for {
        e := try w.Next(sys.arena, sys.io) else {
            break
        }
        if e.kind != fs.Kind.File {
            continue
        }
        p := e.path.Str() else {
            fail Utf8Error
        }
        try items.Push(Item(rel: try Rel(sys.arena, root, p), path: p))
    }
    w.Close()
    items.Sort(fn(x: &Item, y: &Item): bool { return x.rel < y.rel })
    var n := items.Len()
    if limit >= 0 && limit < n {
        n = limit
    }
    var manifest := Text.New(sys.arena)
    alloc := sys.heap.Alloc()
    var payload := HeapList[u8].New(alloc)
    defer payload.Free(alloc)
    var scratch := try Arena.New(sys.heap)
    defer scratch.Free()
    for i in 0..n {
        it := &items[i]
        data := try fs.ReadBytes(&scratch, sys.io, it.path)
        digest := hash.Sha256(data)
        line := try scratch.Fmt("{h} {n} {rel}\n", (h: try encoding.HexEncode(&scratch, digest[..]), n: data.Len(), rel: it.rel))
        try manifest.Push(line)
        try payload.Extend(alloc, data)
        scratch.Reset()
    }
    var work := try Arena.New(sys.heap)
    defer work.Free()
    packed := try compress.Deflate(&work, payload[..])
    var seed: [32]u8 = [0; 32]
    Fixed(64, mut seed[..])
    secret := crypto.Ed25519.FromSeed(&seed)
    m := manifest.Str().AsBytes()
    sig := crypto.Ed25519.Sign(&secret, m)
    total := 8 + m.Len() + 64 + packed.Len()
    body := try work.Slice[u8](total)
    PutLen(m.Len(), mut body[..8])
    (mut body[8..8 + m.Len()]).CopyFrom(m)
    (mut body[8 + m.Len()..8 + m.Len() + 64]).CopyFrom(sig[..])
    (mut body[8 + m.Len() + 64..]).CopyFrom(packed)
    var key: [32]u8 = [0; 32]
    Fixed(0, mut key[..])
    var nonce: [12]u8 = [0; 12]
    Fixed(160, mut nonce[..])
    gcm := try crypto.AesGcm.New(key[..])
    file := try work.Slice[u8](8 + total + 16)
    (mut file[..8]).CopyFrom(Magic.AsBytes())
    sealed := try gcm.Seal(&nonce, Magic.AsBytes(), body[..], mut file[8..])
    try fs.WriteAll(sys.io, out, file[..8 + sealed])
    try sys.Print("archive: {n} files, {b} bytes\n", (n, b: payload.Len()))
}

Open(sys: &System, path: str): ArchiveError!void {
    var work := try Arena.New(sys.heap)
    defer work.Free()
    file := try fs.ReadBytes(&work, sys.io, path)
    if file.Len() < 8 + 16 || !crypto.Equal(file[..8], Magic.AsBytes()) {
        fail NotArchive
    }
    var key: [32]u8 = [0; 32]
    Fixed(0, mut key[..])
    var nonce: [12]u8 = [0; 12]
    Fixed(160, mut nonce[..])
    gcm := try crypto.AesGcm.New(key[..])
    plain := try work.Slice[u8](file.Len() - 8 - 16)
    _ = try gcm.Open(&nonce, Magic.AsBytes(), file[8..], mut plain[..])
    if plain.Len() < 8 + 64 {
        fail CutArchive
    }
    mlen := GetLen(plain[..8])
    if mlen < 0 || mlen > plain.Len() - 8 - 64 {
        fail CutArchive
    }
    m := plain[8..8 + mlen]
    var sig: [64]u8 = [0; 64]
    sig.CopyFrom(plain[8 + mlen..8 + mlen + 64])
    packed := plain[8 + mlen + 64..]
    var seed: [32]u8 = [0; 32]
    Fixed(64, mut seed[..])
    secret := crypto.Ed25519.FromSeed(&seed)
    var public: [32]u8 = [0; 32]
    public.CopyFrom(secret[32..])
    if !crypto.Ed25519.Verify(&public, m, &sig) {
        fail BadSignature
    }
    text := try str.From(m)
    if text.Len() > 0 && !text.EndsWith("\n") {
        fail BadManifest
    }
    var entries := List[Entry].New(&work)
    var total := 0
    for line in text.Lines() {
        if line.Len() < 67 || line.Byte(64) != 32 {
            fail BadManifest
        }
        rest := line[65..]
        sp := rest.Find(" ") else {
            fail BadManifest
        }
        size := Cast[int](try rest[..sp].ParseInt())
        if size < 0 || size > MaxTotal - total {
            fail BadManifest
        }
        try entries.Push(Entry(digest: try encoding.HexDecode(&work, line[..64]), size: size))
        total += size
    }
    data := try compress.Inflate(&work, packed, total)
    if data.Len() != total {
        fail SizeDiffers
    }
    var at := 0
    for e in entries[..] {
        h := hash.Sha256(data[at..at + e.size])
        if !crypto.Equal(h[..], e.digest) {
            fail DigestDiffers
        }
        at += e.size
    }
    var out := sys.Stdout()
    try out.Bytes(m)
    try Print(mut out, "ok: {n} files, {t} bytes\n", (n: entries.Len(), t: total))
    try out.Flush()
}

export Main(sys: &System): ArchiveError!void {
    args := sys.args
    if args.Len() >= 4 && args.Len() <= 5 && args[1] == "create" {
        var limit := -1
        if args.Len() == 5 {
            limit = Cast[int](try args[4].ParseInt())
        }
        try Create(sys, args[2], args[3], limit)
        return
    }
    if args.Len() == 3 && args[1] == "open" {
        try Open(sys, args[2])
        return
    }
    fail Usage
}
