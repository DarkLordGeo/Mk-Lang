// Why: `pack untar` lists or extracts ordinary tar archives, plain or
// compressed with zstd, gzip or zlib, without unpacking them to memory.
//
// What: Untar, Detect (the framing from the first bytes), Walk, WriteEntry,
// Restore and the Counts summary.
//
// How: the compressed file is read as a stream through the decoder's
// Reader and handed to tar.Walker; entries are written as they arrive.

import src.tar
import std.compress
import std.fs
import std.time

/// How a tar archive is compressed, told by its first bytes.
enum Framing {
    Plain
    Zstd
    Gzip
    Zlib
}

impl Format for Framing {
    Format(&self, w: mut Writer): WriteError!void {
        name := match *self {
            .Plain { "tar" }
            .Zstd { "tar.zst" }
            .Gzip { "tar.gz" }
            .Zlib { "tar.zz" }
        }
        try w.Str(name)
    }
}

/// The zstd window a frame may ask for when `--window-limit` is not given,
/// the decoder's default and RFC 8878's recommendation.
const DefaultWindow: int = 8_388_608

/// What the archive held, for the summary line.
struct Counts {
    files: int
    dirs: int
    skipped: int
    bytes: i64
}

/// A directory extracted, whose time and mode are set once everything
/// inside it is written, since writing into it changes its time.
struct Made {
    path: str
    mode: u32
    modified: i64
}

/// The framing that `head`, the first bytes of a file, starts: a zstd frame
/// or a skippable one (RFC 8878 section 3.1), a gzip member (RFC 1952), a
/// zlib header whose check bits hold (RFC 1950), or else plain tar.
Detect(head: []u8): Framing {
    if head.Len() >= 4 && head[1] == 0x2a && head[2] == 0x4d && head[3] == 0x18 && head[0] & 0xf0 == 0x50 {
        return Framing.Zstd
    }
    if head.Len() >= 4 && head[0] == 0x28 && head[1] == 0xb5 && head[2] == 0x2f && head[3] == 0xfd {
        return Framing.Zstd
    }
    if head.Len() >= 2 && head[0] == 0x1f && head[1] == 0x8b {
        return Framing.Gzip
    }
    if head.Len() >= 2 && head[0] & 0x0f == 8 && (int(head[0]) * 256 + int(head[1])) % 31 == 0 {
        return Framing.Zlib
    }
    return Framing.Plain
}

/// Lists a tar archive, or with `--dest` extracts it and restores each
/// entry's modification time and permission bits.
Untar(c: &Ctx, o: &UntarOptions): Error!void {
    path := try One(c, "untar", o.archives)
    limit := o.window_limit else { 0 }
    if limit < 0 {
        fail c.Usage("untar", "--window-limit must be 0 or more")
    }
    if dest := o.dest {
        fs.MakeDirAll(c.io, dest) else e {
            fail c.Fail(dest, e)
        }
    }
    var scratch := try c.arena.Sub()
    defer scratch.Free()
    var f := fs.Open(c.io, path) else e {
        fail c.Fail(path, e)
    }
    onerr f.Abandon()
    var head: [4]u8 = [0; 4]
    got := try ReadAt(c, &f, path, 0, mut head[..])
    framing := Detect(head[..got])
    raw := try scratch.Slice[u8](65536)
    decoded := try scratch.Slice[u8](65536)
    var fr := f.Reader(c.io, raw)
    buf := try scratch.Slice[u8](65536)
    // each arm reads the tar stream through a Reader, then reads what
    // follows the end blocks with the decoder's own Read, to the stream's
    // end, so the decoder still checks a checksum and finds a cut before
    // any directory is given its time
    walked := match framing {
        .Plain {
            w := Walk(c, &scratch, path, mut fr, o.dest) else e {
                fail Decoded(c, path, e, none, limit)
            }
            for {
                n := fr.Read(buf) else {
                    fail c.Fail(path, ReadError)
                }
                if n == 0 {
                    break
                }
            }
            w
        }
        .Zstd {
            var d := compress.ZstdDecoder.New(&scratch, limit)
            var r := d.Reader(mut fr, decoded)
            w := Walk(c, &scratch, path, mut r, o.dest) else e {
                fail Decoded(c, path, e, d.Cause(), limit)
            }
            for {
                n := d.Read(mut fr, buf) else e {
                    fail Ended(c, path, e, limit)
                }
                if n == 0 {
                    break
                }
            }
            w
        }
        .Gzip | .Zlib {
            var d := if framing == Framing.Gzip { compress.Inflater.NewGzip(&scratch) } else { compress.Inflater.NewZlib(&scratch) }
            var r := d.Reader(mut fr, decoded)
            w := Walk(c, &scratch, path, mut r, o.dest) else e {
                fail Decoded(c, path, e, d.Cause(), limit)
            }
            for {
                n := d.Read(mut fr, buf) else e {
                    fail Ended(c, path, e, limit)
                }
                if n == 0 {
                    break
                }
            }
            w
        }
    }
    try Settle(c, &walked.made, o.dest)
    counts := walked.counts
    f.Close(c.io) else e {
        fail c.Fail(path, e)
    }
    verb := if o.dest.IsSome() { "extracted" } else { "holds" }
    try c.sys.Print("pack: {path} ({framing}) {verb} {f} files, {d} directories, {b} bytes", (path, framing, verb, f: counts.files, d: counts.dirs, b: counts.bytes))
    if counts.skipped > 0 {
        try c.sys.Print(", skipped {s} links and special files", (s: counts.skipped))
    }
    try c.sys.Print("\n")
}

/// The errors of reading a tar stream, its source's `ReadError` not yet
/// reported, since only the caller knows the decoder that can say why.
type WalkError = Error | ReadError

/// Reports a zstd frame whose window is over the limit.
WindowTooLarge(c: &Ctx, path: str, limit: int): Failed {
    window := if limit > 0 { limit } else { DefaultWindow }
    c.Say("pack: {path}: a zstd frame needs a window over {window} bytes; raise --window-limit to read it\n", (path, window))
    return Failed(code: ExitBad)
}

/// Reports a failure of a decoder's own Read after the end blocks.
Ended(c: &Ctx, path: str, e: compress.Error | ReadError, limit: int): Failed | OutOfMemory {
    return match e {
        compress.TooLarge { WindowTooLarge(c, path, limit) }
        OutOfMemory { OutOfMemory }
        compress.Corrupt(offset) { c.Fail(path, compress.Corrupt(offset: offset)) }
        compress.Truncated { c.Fail(path, compress.Truncated) }
        ReadError { c.Fail(path, ReadError) }
        _ {
            Panic("compress.Error gained a member this match does not handle")
        }
    }
}

/// Reports a failure of the stream: the decoder's own error when it has
/// one, since its Reader can only say `ReadError`, else `e`.
Decoded(c: &Ctx, path: str, e: WalkError, cause: ?compress.Error, limit: int): Error {
    if why := cause {
        return match why {
            compress.TooLarge { WindowTooLarge(c, path, limit) }
            _ { c.Fail(path, why) }
        }
    }
    return match e {
        ReadError { c.Fail(path, ReadError) }
        Failed(code) { Failed(code: code) }
        OutOfMemory { OutOfMemory }
        WriteError { WriteError }
    }
}

/// What a walk read: the counts, and the directories it made.
struct Walked {
    counts: Counts
    made: List[Made]
}

/// Reads every entry of the tar stream `r` up to its end blocks: prints
/// it, or writes it under `dest` with its file's time and mode restored,
/// and gives the counts and the directories made, whose times and modes
/// `Settle` sets.
Walk(c: &Ctx, a: &Arena, path: str, r: mut Reader, dest: ?str): WalkError!Walked from a {
    var w := tar.Walker.New()
    var counts := Counts(files: 0, dirs: 0, skipped: 0, bytes: 0)
    var made := List[Made].New(a)
    buf := try a.Slice[u8](65536)
    for {
        e: tar.Entry = try Next(c, a, path, mut w, r) else {
            break
        }
        match e.kind {
            .File {
                counts.files += 1
                counts.bytes += e.size
            }
            .Dir { counts.dirs += 1 }
            .Link | .Other {
                counts.skipped += 1
                try c.sys.Print("- skipped    {e.path}\n", (e))
                continue
            }
        }
        to := dest else {
            try Show(c, &e)
            continue
        }
        full := try a.Fmt("{to}/{p}", (to, p: e.path))
        if e.kind == tar.Kind.Dir {
            fs.MakeDirAll(c.io, full) else err {
                fail c.Fail(full, err)
            }
            try made.Push(Made(path: full, mode: e.mode, modified: e.modified))
            continue
        }
        try WriteEntry(c, path, full, &e, mut w, r, buf)
        try Restore(c, full, e.mode, e.modified)
        try Shown(c, full, e.path)
    }
    return Walked(counts: counts, made: made)
}

/// Sets the time and mode of each directory made, the deepest first, so
/// setting a directory's time is the last change made inside its parent,
/// then prints each as the file system now has it.
Settle(c: &Ctx, made: &List[Made], dest: ?str): Error!void {
    to := dest else {
        return
    }
    for i in 0..made.Len() {
        d := &made[made.Len() - 1 - i]
        try Restore(c, d.path, d.mode, d.modified)
    }
    for d in made {
        try Shown(c, d.path, d.path[to.Len() + 1..])
    }
}

/// The next entry of the archive, or none at its end.
Next(c: &Ctx, a: &Arena, path: str, w: mut tar.Walker, r: mut Reader): WalkError!?tar.Entry from a {
    return w.Next(a, r) else e {
        failure: WalkError = match e {
            ReadError { ReadError }
            OutOfMemory { OutOfMemory }
            other { c.Fail(path, other) }
        }
        fail failure
    }
}

/// Prints an entry as `list` would.
Show(c: &Ctx, e: &tar.Entry): WriteError!void {
    letters := tar.Letters(e.mode)
    perm := str.From(letters[..]) else { "?????????" }
    t := time.FromUnix(e.modified, 0)
    if e.kind == tar.Kind.Dir {
        try c.sys.Print("d {perm} {s:>10}  {t}  {e.path}/\n", (perm, s: "-", t, e))
    } else {
        try c.sys.Print("f {perm} {e.size:>10}  {t}  {e.path}\n", (perm, e, t))
    }
}

/// Writes the data of file entry `e` to `full`, made new or truncated.
WriteEntry(c: &Ctx, path: str, full: str, e: &tar.Entry, w: mut tar.Walker, r: mut Reader, buf: mut []u8): WalkError!void {
    if parent := Parent(full) {
        fs.MakeDirAll(c.io, parent) else err {
            fail c.Fail(parent, err)
        }
    }
    var f := fs.Create(c.io, full) else err {
        fail c.Fail(full, err)
    }
    onerr Discard(c.io, f, full)
    var left := e.size
    for left > 0 {
        n := w.Read(r, buf) else err {
            failure: WalkError = match err {
                ReadError { ReadError }
                tar.Cut(offset) { c.Fail(path, tar.Cut(offset: offset)) }
            }
            fail failure
        }
        f.WriteAll(c.io, buf[..n]) else err {
            fail c.Fail(full, err)
        }
        left -= i64(n)
    }
    f.Close(c.io) else err {
        fail c.Fail(full, err)
    }
}

/// The directory part of `p`, when it has one.
Parent(p: str): ?str from p {
    var i := p.Len()
    for i > 0 {
        i -= 1
        if p.Byte(i) == b'/' {
            return some(p[..i])
        }
    }
    return none
}

/// Sets `full`'s permission bits, those of owner, group and others, and
/// its modification time to the archive's.
Restore(c: &Ctx, full: str, mode: u32, modified: i64): Failed!void {
    // the set-user-id, set-group-id and sticky bits are not restored: an
    // archive from elsewhere must not make a program that runs as its owner
    fs.SetPermissions(c.io, full, fs.Permissions(mode: mode & 0o777)) else e {
        fail c.Fail(full, e)
    }
    fs.SetModified(c.io, full, time.FromUnix(modified, 0)) else e {
        fail c.Fail(full, e)
    }
}

/// Prints what the file system now says of an extracted entry: its kind,
/// permission bits and modification time, read back with `fs.Stat`. On
/// Windows only the owner's write bit is kept, so the bits differ there.
Shown(c: &Ctx, full: str, rel: str): Error!void {
    info := fs.Stat(c.io, full) else e {
        fail c.Fail(full, e)
    }
    letters := tar.Letters(info.permissions.mode & 0o777)
    perm := str.From(letters[..]) else { "?????????" }
    kind := if info.kind == fs.Kind.Dir { "d" } else { "f" }
    size := if info.kind == fs.Kind.Dir { 0 } else { info.size }
    try c.sys.Print("x {kind} {perm} {size:>10}  {t}  {rel}\n", (kind, perm, size, t: info.modified, rel))
}

test "Detect tells the framings apart by their first bytes" {
    Assert(Detect(b"\x28\xb5\x2f\xfd") == Framing.Zstd)
    Assert(Detect(b"\x50\x2a\x4d\x18") == Framing.Zstd)
    Assert(Detect(b"\x1f\x8b\x08\x00") == Framing.Gzip)
    Assert(Detect(b"\x78\x9c\x00\x00") == Framing.Zlib)
    Assert(Detect(b"docs") == Framing.Plain)
}
