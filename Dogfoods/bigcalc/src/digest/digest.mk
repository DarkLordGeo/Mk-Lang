// Why: `bigcalc hash` must digest large files without reading them whole.
//
// What: Algs, Sums, File, Bytes and Empty.
//
// How: SHA-1, SHA-256, SHA-384 and SHA-512 (FIPS 180-4) through the
// streaming states, 64 KiB at a time, one task per file.

import std.fs
import std.hash

/// Which digests to compute.
export struct Algs {
    export sha1: bool
    export sha256: bool
    export sha384: bool
    export sha512: bool
}

/// One file's digests, or the error that stopped it.
export struct Sums {
    export path: str
    export sha1: [20]u8
    export sha256: [32]u8
    export sha384: [48]u8
    export sha512: [64]u8
    export failed: ?fs.Error
}

/// Empty is the result slot for path before it is hashed.
export Empty(path: str): Sums {
    return Sums(path: path, sha1: [0; 20], sha256: [0; 32], sha384: [0; 48], sha512: [0; 64], failed: none)
}

/// File hashes the file at r.path with every digest of algs, reading it once
/// in 64 KiB pieces; a task body, so it records an error in r.failed rather
/// than returning it. O(file); allocates nothing (the buffer is on the
/// task's stack).
export File(io: Io, algs: Algs, r: mut Sums) {
    var f := fs.Open(io, r.path) else e {
        r.failed = some(e)
        return
    }
    var s1 := hash.Sha1State.New()
    var s256 := hash.Sha256State.New()
    var s384 := hash.Sha384State.New()
    var s512 := hash.Sha512State.New()
    var buf: [65536]u8 = [0; 65536]
    for {
        n := f.Read(io, mut buf[..]) else e {
            r.failed = some(e)
            f.Abandon()
            return
        }
        if n == 0 {
            break
        }
        piece := buf[..n]
        if algs.sha1 {
            s1.Bytes(piece)
        }
        if algs.sha256 {
            s256.Bytes(piece)
        }
        if algs.sha384 {
            s384.Bytes(piece)
        }
        if algs.sha512 {
            s512.Bytes(piece)
        }
    }
    f.Close(io) else {
        f.Abandon()
    }
    r.sha1 = s1.Sum()
    r.sha256 = s256.Sum()
    r.sha384 = s384.Sum()
    r.sha512 = s512.Sum()
}

/// Bytes hashes data already in memory with the one-shot functions.
export Bytes(path: str, data: []u8): Sums from path {
    return Sums(
        path: path,
        sha1: hash.Sha1(data),
        sha256: hash.Sha256(data),
        sha384: hash.Sha384(data),
        sha512: hash.Sha512(data),
        failed: none,
    )
}
