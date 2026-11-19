// Why: `pack zlib` and `pack unzlib` turn files into zlib streams (RFC 1950)
// and back, in the manner of `pigz -z`.
//
// What: Zlib, Unzlib, their whole-file and streamed paths, CheckAdler and
// AdlerCombine.
//
// How: one small file is done in one call; a larger one, or several in a
// row, streams through the linear encoder or the inflater's Reader, a buffer
// at a time. Both print the Adler-32 of the plain data, and unzlib checks it
// against the stream's trailer.

import std.compress
import std.fs

/// Files up to this size are compressed or decompressed in one call.
const WholeLimit: i64 = 65536

/// The most `unzlib` writes when `--max` is not given: 1 GiB.
const DefaultMax: int = 1_073_741_824

/// Adler-32's modulus, the largest prime below 2^16 (RFC 1950 section 9).
const AdlerBase: u64 = 65521

/// The Adler-32 of two pieces back to back, from each piece's Adler-32 and
/// the second's length, as zlib's adler32_combine computes it: the first
/// sum adds, and the second gains the first sum once per byte of the
/// second piece. O(1).
AdlerCombine(first: u32, second: u32, second_len: i64): u32 {
    rem := Cast[u64](second_len % Cast[i64](AdlerBase))
    var sum1 := u64(first & 0xffff)
    var sum2 := (rem * sum1) % AdlerBase
    sum1 += u64(second & 0xffff) + AdlerBase - 1
    sum2 += u64(first >> 16) + u64(second >> 16) + AdlerBase - rem
    if sum1 >= AdlerBase {
        sum1 -= AdlerBase
    }
    if sum1 >= AdlerBase {
        sum1 -= AdlerBase
    }
    if sum2 >= AdlerBase << 1 {
        sum2 -= AdlerBase << 1
    }
    if sum2 >= AdlerBase {
        sum2 -= AdlerBase
    }
    return Trunc[u32](sum2 << 16 | sum1)
}

/// The size of the file `path`, reported when it cannot be read.
FileSize(c: &Ctx, path: str): Failed!i64 {
    info := fs.Stat(c.io, path) else e {
        fail c.Fail(path, e)
    }
    if info.kind == fs.Kind.Dir {
        fail c.Fail(path, fs.IsDirectory)
    }
    return info.size
}

/// Compresses one file into a zlib stream at `--out`.
Zlib(c: &Ctx, o: &ZlibOptions): Error!void {
    if o.inputs.Len() == 0 {
        fail c.Usage("zlib", "give one or more files to compress")
    }
    level := o.level else { 6 }
    if level < 0 || level > 9 {
        fail c.Usage("zlib", "--level takes 0 to 9")
    }
    every := o.sync_every else { 0 }
    if every < 0 {
        fail c.Usage("zlib", "--sync-every must be 0 or more")
    }
    var scratch := try c.arena.Sub()
    defer scratch.Free()
    whole := o.inputs.Len() == 1 && o.level.IsNone() && every == 0 && try FileSize(c, o.inputs[0]) <= WholeLimit
    (size, adler) := if whole {
        try ZlibWhole(c, &scratch, o.inputs[0], o.out)
    } else {
        try ZlibStream(c, &scratch, o.inputs, o.out, level, every)
    }
    out_size := try FileSize(c, o.out)
    try c.sys.Print("pack: wrote {out}: {size} bytes into {out_size}, adler32 {adler:x}\n", (out: o.out, size, out_size, adler))
}

/// Compresses the small file `path` in one call, and gives its size and
/// Adler-32.
ZlibWhole(c: &Ctx, a: &Arena, path: str, out: str): Error!(size: i64, adler: u32) {
    data := fs.ReadBytes(a, c.io, path) else e {
        fail c.Fail(path, e)
    }
    packed := try compress.Zlib(a, data)
    try WriteWhole(c, a, out, packed)
    return (size: i64(data.Len()), adler: compress.Adler32(data))
}

/// Writes `data` to `out` through a temporary file renamed into place.
WriteWhole(c: &Ctx, a: &Arena, out: str, data: []u8): Error!void {
    (file, temp) := try Stage(c, a, out)
    var f := file
    onerr Discard(c.io, f, temp)
    f.WriteAll(c.io, data) else e {
        fail c.Fail(out, e)
    }
    f.Sync(c.io) else e {
        fail c.Fail(out, e)
    }
    f.Close(c.io) else e {
        fail c.Fail(out, e)
    }
    try Place(c, temp, out)
}

/// What a stream has taken in so far: its Adler-32, its length, and the
/// bytes since the last sync flush.
struct Taken {
    adler: u32
    bytes: i64
    since: int
}

/// Compresses the files `paths`, one after another, into one zlib stream
/// at `out` a buffer at a time through the encoder, ending a block with a
/// sync flush every `every` input bytes when `every` is above 0. Gives the
/// count of input bytes and their Adler-32, combined piece by piece. A
/// file that cannot be read abandons the encoder, and no output is left.
ZlibStream(c: &Ctx, a: &Arena, paths: []str, out: str, level: int, every: int): Error!(size: i64, adler: u32) {
    (file, temp) := try Stage(c, a, out)
    var f := file
    onerr Discard(c.io, f, temp)
    buf := try a.Slice[u8](65536)
    wbuf := try a.Slice[u8](65536)
    var w := f.Writer(c.io, wbuf)
    var enc := try compress.Encoder.New(a, compress.Framing.Zlib, level)
    onerr enc.Abandon()
    var taken := Taken(adler: 1, bytes: 0, since: 0)
    for path in paths {
        try Feed(c, path, out, mut enc, mut w, buf, every, mut taken)
    }
    enc.Finish(mut w) else {
        fail c.Fail(out, WriteError)
    }
    w.Flush() else {
        fail c.Fail(out, WriteError)
    }
    f.Sync(c.io) else e {
        fail c.Fail(out, e)
    }
    f.Close(c.io) else e {
        fail c.Fail(out, e)
    }
    try Place(c, temp, out)
    return (size: taken.bytes, adler: taken.adler)
}

/// Reads the file `path` into the encoder, a buffer at a time.
Feed(c: &Ctx, path: str, out: str, enc: mut compress.Encoder, w: mut Writer, buf: mut []u8, every: int, taken: mut Taken): Failed!void {
    var source := fs.Open(c.io, path) else e {
        fail c.Fail(path, e)
    }
    onerr source.Abandon()
    for {
        n := source.Read(c.io, buf) else e {
            fail c.Fail(path, e)
        }
        if n == 0 {
            break
        }
        taken.adler = AdlerCombine(taken.adler, compress.Adler32(buf[..n]), i64(n))
        taken.bytes += i64(n)
        enc.Write(w, buf[..n]) else {
            fail c.Fail(out, WriteError)
        }
        taken.since += n
        if every > 0 && taken.since >= every {
            enc.Flush(w) else {
                fail c.Fail(out, WriteError)
            }
            taken.since = 0
        }
    }
    source.Close(c.io) else e {
        fail c.Fail(path, e)
    }
}

/// Decompresses one zlib stream into `--out`.
Unzlib(c: &Ctx, o: &UnzlibOptions): Error!void {
    path := try One(c, "unzlib", o.inputs)
    max := i64(o.max else { DefaultMax })
    if max < 0 {
        fail c.Usage("unzlib", "--max must be 0 or more")
    }
    size := try FileSize(c, path)
    var scratch := try c.arena.Sub()
    defer scratch.Free()
    // the inflater checks the trailer too; this check reads it from the
    // file, so a stream followed by other bytes is refused as well, and
    // before the output is renamed into place
    trailer := try Trailer(c, path, size)
    (written, adler) := if size <= WholeLimit {
        try UnzlibWhole(c, &scratch, path, o.out, max, trailer)
    } else {
        try UnzlibStream(c, &scratch, path, o.out, max, trailer)
    }
    try c.sys.Print("pack: wrote {out}: {written} bytes from {size}, adler32 {adler:x} checked\n", (out: o.out, written, size, adler))
}

/// Decompresses the small file `path` in one call, and gives the count of
/// bytes written and their Adler-32.
UnzlibWhole(c: &Ctx, a: &Arena, path: str, out: str, max: i64, trailer: u32): Error!(written: i64, adler: u32) {
    data := fs.ReadBytes(a, c.io, path) else e {
        fail c.Fail(path, e)
    }
    plain := compress.Unzlib(a, data, Trunc[int](max)) else e {
        failure := match e {
            compress.TooLarge { OverMax(c, path, max) }
            _ { c.Fail(path, e) }
        }
        fail failure
    }
    adler := compress.Adler32(plain)
    try CheckAdler(c, path, adler, trailer)
    try WriteWhole(c, a, out, plain)
    return (written: i64(plain.Len()), adler)
}

/// Decompresses `path` a buffer at a time through the inflater's Reader.
UnzlibStream(c: &Ctx, a: &Arena, path: str, out: str, max: i64, trailer: u32): Error!(written: i64, adler: u32) {
    var source := fs.Open(c.io, path) else e {
        fail c.Fail(path, e)
    }
    onerr source.Abandon()
    (file, temp) := try Stage(c, a, out)
    var f := file
    onerr Discard(c.io, f, temp)
    rbuf := try a.Slice[u8](65536)
    dbuf := try a.Slice[u8](65536)
    buf := try a.Slice[u8](65536)
    var fr := source.Reader(c.io, rbuf)
    var inflater := compress.Inflater.NewZlib(a)
    var r := inflater.Reader(mut fr, dbuf)
    var adler: u32 = 1
    var written: i64 = 0
    for {
        n := r.Read(buf) else {
            why := inflater.Cause() else {
                fail c.Fail(path, ReadError)
            }
            fail c.Fail(path, why)
        }
        if n == 0 {
            break
        }
        if written + i64(n) > max {
            fail OverMax(c, path, max)
        }
        adler = AdlerCombine(adler, compress.Adler32(buf[..n]), i64(n))
        f.WriteAll(c.io, buf[..n]) else e {
            fail c.Fail(out, e)
        }
        written += i64(n)
    }
    source.Close(c.io) else e {
        fail c.Fail(path, e)
    }
    try CheckAdler(c, path, adler, trailer)
    f.Sync(c.io) else e {
        fail c.Fail(out, e)
    }
    f.Close(c.io) else e {
        fail c.Fail(out, e)
    }
    try Place(c, temp, out)
    return (written, adler)
}

/// Fails when the data's Adler-32 is not the one the stream's file ends
/// with.
CheckAdler(c: &Ctx, path: str, adler: u32, trailer: u32): Failed!void {
    if adler != trailer {
        c.Say("pack: {path}: the data's Adler-32 {adler:x} is not the trailer's {trailer:x}\n", (path, adler, trailer))
        fail Failed(code: ExitBad)
    }
}

/// Reports a stream that decompresses to more than `--max` bytes.
OverMax(c: &Ctx, path: str, max: i64): Failed {
    c.Say("pack: {path}: the data is over the limit of {max} bytes; raise --max to keep it\n", (path, max))
    return Failed(code: ExitBad)
}

/// The Adler-32 in the last four bytes of the zlib file `path`, most
/// significant byte first (RFC 1950 section 2.2).
Trailer(c: &Ctx, path: str, size: i64): Failed!u32 {
    if size < 6 {
        fail c.Fail(path, compress.Truncated)
    }
    var f := fs.Open(c.io, path) else e {
        fail c.Fail(path, e)
    }
    onerr f.Abandon()
    var b: [4]u8 = [0; 4]
    n := try ReadAt(c, &f, path, size - 4, mut b[..])
    f.Close(c.io) else e {
        fail c.Fail(path, e)
    }
    if n != 4 {
        fail c.Fail(path, compress.Truncated)
    }
    return u32(b[0]) << 24 | u32(b[1]) << 16 | u32(b[2]) << 8 | u32(b[3])
}

test "AdlerCombine gives the Adler-32 of the pieces back to back" {
    whole := compress.Adler32(b"the quick brown fox jumps over the lazy dog")
    first := compress.Adler32(b"the quick brown fox ")
    second := compress.Adler32(b"jumps over the lazy dog")
    Assert(AdlerCombine(first, second, 23) == whole)
    Assert(AdlerCombine(1, whole, 43) == whole)
    Assert(AdlerCombine(whole, 1, 0) == whole)
}
