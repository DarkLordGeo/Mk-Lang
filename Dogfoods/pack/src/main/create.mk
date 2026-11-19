// Why: the commands that write files must never leave a half-written archive
// or key behind.
//
// What: Create (pack create), WriteArchive, Keygen and Fingerprint, and
// FileSink, the archive.Sink over a file.
//
// How: the archive goes to a temporary file beside its path; the header is
// written last and the file renamed into place, so a cut file never opens.

import src.archive
import src.hex
import src.keys
import src.seal
import src.tree
import std.crypto
import std.fs
import std.hash

/// Writes into a file through its buffered writer.
struct FileSink {
    w: Writer
}

impl archive.Sink for FileSink {
    Put(mut self, b: []u8): WriteError!void {
        try self.w.Bytes(b)
    }
}

/// Packs one directory into an archive, signed and sealed as asked, written
/// to `--out` through a `.partial` file renamed into place.
Create(c: &Ctx, o: &CreateOptions): Error!void {
    if o.dirs.Len() != 1 {
        fail c.Usage("create", "give exactly one directory to pack")
    }
    root := o.dirs[0]
    info: fs.Info = fs.Stat(c.io, root) else e {
        fail c.Fail(root, e)
    }
    if info.kind != fs.Kind.Dir {
        fail c.Fail(root, fs.NotDirectory)
    }
    // the signing secret lives in this local, cleared when Create returns
    var secret := keys.Secret(sign: [0; 64], box: [0; 32])
    defer secret.Wipe()
    signs := o.sign.IsSome()
    if path := o.sign {
        secret = keys.LoadSecret(c.arena, c.io, path) else e {
            fail c.Fail(path, e)
        }
    }
    cipher := match o.cipher else { "chacha20-poly1305" } {
        "chacha20-poly1305" { seal.Cipher.ChaCha20Poly1305 }
        "aes-256-gcm" { seal.Cipher.Aes256Gcm }
        _ { fail c.Usage("create", "--cipher takes chacha20-poly1305 or aes-256-gcm") }
    }
    if o.cipher.IsSome() && o.to.IsNone() {
        fail c.Usage("create", "--cipher needs --to")
    }
    var recipient: ?keys.Public = none
    if path := o.to {
        recipient = some(keys.LoadPublic(c.arena, c.io, path) else e {
            fail c.Fail(path, e)
        })
    }
    items := tree.Walk(c.arena, c.io, root) else e {
        fail c.Fail(root, e)
    }
    var data := HeapList[u8].New(c.alloc)
    defer data.Free(c.alloc)
    entries := tree.Gather(c.arena, c.io, c.alloc, root, items[..], mut data) else e {
        fail c.Fail(root, e)
    }
    // the compressor's arena takes its chunks from the budget (Arena.NewIn)
    var scratch := Arena.NewIn(c.alloc) else e {
        fail c.Fail(root, e)
    }
    defer scratch.Free()
    codec := if o.gzip { archive.Codec.Gzip } else { archive.Codec.Deflate }
    packed := archive.Compress(&scratch, codec, data[..]) else e {
        fail c.Fail(root, e)
    }
    m := archive.Manifest(codec: codec, total: i64(data.Len()), packed: i64(packed.Len()), packed_digest: archive.Digest(packed), entries: entries)
    var text := Text.New(&scratch)
    try m.Write(&scratch, mut text)
    manifest := text.Str().AsBytes()
    var block: [96]u8 = [0; 96]
    signature := if signs {
        block = archive.SignatureBlock(&secret.sign, manifest)
        block[..]
    } else {
        block[..0]
    }
    temp := try WriteArchive(c, &scratch, o.out, recipient, cipher, manifest, signature, packed)
    try Place(c, temp, o.out)
    try c.sys.Print("pack: wrote {out}: {f} files, {d} directories, {t} bytes packed into {p}\n", (out: o.out, f: m.Files(), d: m.entries.Len() - m.Files(), t: m.total, p: m.packed))
    if signs {
        p := secret.Public()
        try c.sys.Print("pack: signed by {fp}\n", (fp: hex.Hex(bytes: p.Fingerprint()[..])))
    }
    if r := recipient {
        try c.sys.Print("pack: sealed to {fp} with {cipher}\n", (fp: hex.Hex(bytes: r.Fingerprint()[..]), cipher))
        return
    }
    // a plain archive is a function of the tree and the key, so its digest
    // can be published and compared across machines
    var whole := hash.Sha256State.New()
    whole.Bytes(archive.HeaderFor(manifest.Len(), signature)[..])
    whole.Bytes(manifest)
    whole.Bytes(signature)
    whole.Bytes(packed)
    try c.sys.Print("pack: sha256 {h}\n", (h: hex.Hex(bytes: whole.Sum()[..])))
}

/// Writes the archive for `path` into a temporary file beside it, and gives
/// the temporary file's path, for the caller to rename into place. Its
/// first bytes, the header, are written last, so a file cut short by a
/// crash never reads as an archive. On any failure the file is removed.
WriteArchive(c: &Ctx, scratch: &Arena, path: str, recipient: ?keys.Public, cipher: seal.Cipher, manifest: []u8, signature: []u8, data: []u8): Error!str from scratch {
    (file, temp) := try Stage(c, scratch, path)
    var f := file
    onerr Discard(c.io, f, temp)
    buf := try scratch.Slice[u8](65536)
    header := archive.HeaderFor(manifest.Len(), signature)
    if r := recipient {
        plain_len := i64(header.Len() + manifest.Len() + signature.Len() + data.Len())
        try Zeros(c, mut f, path, seal.HeaderLen)
        var s := seal.Sealer.New(scratch, c.io, f.Writer(c.io, buf), &r.box, plain_len, cipher) else e {
            fail c.Fail(path, e)
        }
        lead := s.header
        // on a failure before Finish, which clears them, the keys are
        // cleared here
        s.Put(header[..]) else {
            s.Wipe()
            fail c.Fail(path, WriteError)
        }
        archive.WriteBody(mut s, manifest, signature, data) else {
            s.Wipe()
            fail c.Fail(path, WriteError)
        }
        s.Finish() else {
            fail c.Fail(path, WriteError)
        }
        try WriteLead(c, &f, path, lead[..])
    } else {
        try Zeros(c, mut f, path, archive.HeaderLen)
        var sink := FileSink(w: f.Writer(c.io, buf))
        archive.WriteBody(mut sink, manifest, signature, data) else {
            fail c.Fail(path, WriteError)
        }
        sink.w.Flush() else {
            fail c.Fail(path, WriteError)
        }
        try WriteLead(c, &f, path, header[..])
    }
    f.Sync(c.io) else e {
        fail c.Fail(path, e)
    }
    f.Close(c.io) else e {
        fail c.Fail(path, e)
    }
    return temp
}

/// Reserves the first `n` bytes of `f` with zeros, for the header written
/// last.
Zeros(c: &Ctx, f: mut fs.File, path: str, n: int): Failed!void {
    var zero: [80]u8 = [0; 80]
    var done := 0
    for done < n {
        done += f.Write(c.io, zero[..n - done]) else e {
            fail c.Fail(path, e)
        }
    }
}

/// Writes the header over the zeros `Zeros` left.
WriteLead(c: &Ctx, f: &fs.File, path: str, lead: []u8): Failed!void {
    var done := 0
    for done < lead.Len() {
        done += f.WriteAt(c.io, i64(done), lead[done..]) else e {
            fail c.Fail(path, e)
        }
    }
}

/// Closes a half-written file without reporting, and removes it.
Discard(io: Io, f: fs.File, path: str) {
    f.Abandon()
    fs.Remove(io, path) else {}
}

/// Makes a key pair and writes NAME.key and NAME.pub.
Keygen(c: &Ctx, o: &KeygenOptions): Error!void {
    if o.names.Len() != 1 {
        fail c.Usage("keygen", "give exactly one name")
    }
    name := o.names[0]
    var secret := if text := o.seed {
        var seed: [32]u8 = [0; 32]
        hex.Decode(text, mut seed[..]) else {
            fail c.Usage("keygen", "--seed takes 64 lowercase hex digits")
        }
        s := keys.FromSeed(&seed)
        crypto.Wipe(mut seed[..])
        s
    } else {
        keys.Generate(c.io) else {
            fail c.Fail(name, Canceled)
        }
    }
    defer secret.Wipe()
    secret_path := try c.arena.Fmt("{name}.key", (name))
    public_path := try c.arena.Fmt("{name}.pub", (name))
    keys.Save(c.arena, c.io, &secret, secret_path, public_path) else e {
        fail c.Fail(secret_path, e)
    }
    try c.sys.Print("pack: wrote {secret_path} and {public_path}\n", (secret_path, public_path))
}

/// Prints the fingerprint of a public or secret key file.
Fingerprint(c: &Ctx, o: &FingerprintOptions): Error!void {
    if o.keys.Len() != 1 {
        fail c.Usage("fingerprint", "give exactly one key file")
    }
    path := o.keys[0]
    secret_file := keys.IsSecretFile(c.io, path) else e {
        fail c.Fail(path, e)
    }
    public := if secret_file {
        var s := keys.LoadSecret(c.arena, c.io, path) else e {
            fail c.Fail(path, e)
        }
        p := s.Public()
        s.Wipe()
        p
    } else {
        keys.LoadPublic(c.arena, c.io, path) else e {
            fail c.Fail(path, e)
        }
    }
    try c.sys.Print("{fp}  {path}\n", (fp: hex.Hex(bytes: public.Fingerprint()[..]), path))
}
