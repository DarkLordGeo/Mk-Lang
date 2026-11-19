// Why: signing and sealing need key pairs that survive on disk and can be
// shared, with the secret half never confused with the public one.
//
// What: Secret and Public, Generate and FromSeed, Fingerprint, Save,
// LoadSecret and LoadPublic, and the BadKeyFile error.
//
// How: one text file per half, in the format below. Generate draws a pair
// from the OS random source; FromSeed derives both keys from one seed, for
// tests and reproducible setups.

// Key pairs and the files that hold them.
//
// A secret key file:          A public key file:
//   pack-secret-key 2           pack-public-key 2
//   sign <44 base64 digits>     sign <44 base64 digits>
//   box <44 base64 digits>      box <44 base64 digits>
//
// in the standard base64 alphabet with padding (RFC 4648 section 4).
// Version 1 files, written before, hold the same keys in 64 hex digits
// and are still read.
//
// `sign` is an Ed25519 key: the 32-byte seed in a secret file, the public key
// in a public one. `box` is an X25519 key: the secret scalar, or its public
// point. Each archive is signed with the first and sealed to the second.
import src.hex
import std.crypto
import std.encoding
import std.fs
import std.hash

/// Line `line` of a key file is not what the module comment says.
export error BadKeyFile(line: int)

/// The errors of reading a key file.
export type Error = BadKeyFile | fs.Error | OutOfMemory

/// A secret key: an Ed25519 secret (seed, then public key) and an X25519
/// secret.
export struct Secret {
    export sign: [64]u8
    export box: [32]u8
}

/// A public key: an Ed25519 public key and an X25519 public point.
export struct Public {
    export sign: [32]u8
    export box: [32]u8
}

/// A new secret key from the OS random source. Fails with `Canceled` when
/// the task is cancelled. O(1); allocates nothing.
export Generate(io: Io): Canceled!Secret {
    sign := try crypto.Ed25519.Keypair(io)
    var box: [32]u8 = [0; 32]
    try crypto.Random(io, mut box[..])
    return Secret(sign: sign, box: box)
}

/// The secret key that `seed` always gives, for tests and reproducible
/// setups: the Ed25519 key of the seed, and an X25519 scalar from the last
/// half of the seed's SHA-512, so the two keys are independent. O(1).
export FromSeed(seed: &[32]u8): Secret {
    sign := crypto.Ed25519.FromSeed(seed)
    wide := hash.Sha512(seed[..])
    var box: [32]u8 = [0; 32]
    box.CopyFrom(wide[32..64])
    return Secret(sign: sign, box: box)
}

/// The X25519 base point, u = 9 (RFC 7748 section 4.1).
Base(): [32]u8 {
    var b: [32]u8 = [0; 32]
    b[0] = 9
    return b
}

/// The public point of an X25519 secret. O(1).
export BoxPublic(secret: &[32]u8): [32]u8 {
    return crypto.X25519(secret, &Base())
}

/// Clears both secrets.
export Secret.Wipe(mut self) {
    crypto.Wipe(mut self.sign[..])
    crypto.Wipe(mut self.box[..])
}

/// The public half of `s`. O(1).
export Secret.Public(&self): Public {
    var sign: [32]u8 = [0; 32]
    sign.CopyFrom(self.sign[32..64])
    return Public(sign: sign, box: BoxPublic(&self.box))
}

/// The first eight bytes of the SHA-256 of the signing key, which names a
/// key pair to a person. O(1).
export Public.Fingerprint(&self): [8]u8 {
    return Fingerprint(&self.sign)
}

/// The fingerprint of an Ed25519 public key, as `Public.Fingerprint` gives
/// it. O(1).
export Fingerprint(sign: &[32]u8): [8]u8 {
    sum := hash.Sha256(sign[..])
    var fp: [8]u8 = [0; 8]
    fp.CopyFrom(sum[..8])
    return fp
}

/// Writes the secret key file `path` and the public key file `public_path`.
/// Fails with the file system's errors. O(1); allocates the texts in `a`.
export Save(a: &Arena, io: Io, s: &Secret, path: str, public_path: str): fs.Error|OutOfMemory!void {
    p := s.Public()
    // the secret file's text is built in a buffer of its own and cleared
    // once written, so no copy of the secret stays in the arena
    var buf: [128]u8 = [0; 128]
    var n := Put(mut buf[..], 0, "pack-secret-key 2\nsign ")
    n += encoding.Base64EncodeTo(mut buf[n..], s.sign[..32], encoding.Base64)
    n = Put(mut buf[..], n, "\nbox ")
    n += encoding.Base64EncodeTo(mut buf[n..], s.box[..], encoding.Base64)
    n = Put(mut buf[..], n, "\n")
    fs.WriteAll(io, path, buf[..n]) else e {
        crypto.Wipe(mut buf[..])
        fail e
    }
    crypto.Wipe(mut buf[..])
    sign := try encoding.Base64Encode(a, p.sign[..], encoding.Base64)
    box := try encoding.Base64Encode(a, p.box[..], encoding.Base64)
    public_text := try a.Fmt("pack-public-key 2\nsign {sign}\nbox {box}\n", (sign, box))
    try fs.WriteAll(io, public_path, public_text.AsBytes())
}

/// Copies `text` into `buf` at `at`, and gives where it ends.
Put(buf: mut []u8, at: int, text: str): int {
    (mut buf[at..at + text.Len()]).CopyFrom(text.AsBytes())
    return at + text.Len()
}

/// Whether the file `path` starts as a secret key file does, so its
/// public half is printed without reading the file as text. Reads at most
/// 16 bytes. Fails with the file system's errors.
export IsSecretFile(io: Io, path: str): fs.Error!bool {
    var f := try fs.Open(io, path)
    onerr f.Abandon()
    var head: [16]u8 = [0; 16]
    var n := 0
    for n < head.Len() {
        got := try f.Read(io, mut head[n..])
        if got == 0 {
            break
        }
        n += got
    }
    try f.Close(io)
    return (str.From(head[..n]) else { "" }) == "pack-secret-key "
}

/// Reads a secret key file. Fails with `BadKeyFile` at the first wrong line,
/// and with the file system's errors. O(1); the file's bytes are read into
/// `a` and cleared there once parsed.
export LoadSecret(a: &Arena, io: Io, path: str): Error!Secret {
    _ = a
    var keys := try Load(io, path, "pack-secret-key")
    s := Secret(sign: crypto.Ed25519.FromSeed(&keys.sign), box: keys.box)
    crypto.Wipe(mut keys.sign[..])
    crypto.Wipe(mut keys.box[..])
    return s
}

/// Reads a public key file. Fails as `LoadSecret` does.
/// A public key file holds nothing secret, so it is read as text into `a`
/// and not cleared.
export LoadPublic(a: &Arena, io: Io, path: str): Error!Public {
    text := try fs.ReadText(a, io, path)
    (sign, box) := try Parse(text.AsBytes(), "pack-public-key")
    return Public(sign: sign, box: box)
}

/// The longest key file read; a version 1 secret file is 156 bytes.
const MaxKeyFile: int = 256

/// The two keys of a key file whose first line is `kind` and a version: 1
/// (hex) or 2 (base64). The file is read into a buffer on the stack, which
/// is cleared once parsed. A file that is not UTF-8 is `BadKeyFile` at line
/// 1, and one of `MaxKeyFile` bytes or more fails at line 4 at the latest,
/// past the three lines a key file has.
Load(io: Io, path: str, kind: str): Error!(sign: [32]u8, box: [32]u8) {
    var f := try fs.Open(io, path)
    onerr f.Abandon()
    var buf: [256]u8 = [0; 256]
    var n := 0
    for n < MaxKeyFile {
        got := f.Read(io, mut buf[n..]) else e {
            crypto.Wipe(mut buf[..])
            fail e
        }
        if got == 0 {
            break
        }
        n += got
    }
    try f.Close(io)
    keys := Parse(buf[..n], kind) else e {
        crypto.Wipe(mut buf[..])
        fail e
    }
    crypto.Wipe(mut buf[..])
    // three lines that parse and fill the buffer leave more unread
    if n == MaxKeyFile {
        fail BadKeyFile(line: 4)
    }
    return keys
}

/// Parses the text of a key file. Fails with `BadKeyFile` at the first
/// line that is not as the module comment says.
Parse(bytes: []u8, kind: str): BadKeyFile!(sign: [32]u8, box: [32]u8) {
    text := str.From(bytes) else {
        fail BadKeyFile(line: 1)
    }
    var sign: [32]u8 = [0; 32]
    var box: [32]u8 = [0; 32]
    var version := 0
    var n := 0
    for line in text.Lines() {
        n += 1
        ok := match n {
            1 {
                version = if line.StartsWith(kind) { Version(line[kind.Len()..]) } else { 0 }
                version != 0
            }
            2 { KeyLine(line, "sign ", version, mut sign) }
            3 { KeyLine(line, "box ", version, mut box) }
            _ { false }
        }
        if !ok {
            fail BadKeyFile(line: n)
        }
    }
    if n != 3 {
        fail BadKeyFile(line: n + 1)
    }
    return (sign, box)
}

/// The version after a key file's kind: 1 or 2, else 0.
Version(rest: str): int {
    return match rest {
        " 1" { 1 }
        " 2" { 2 }
        _ { 0 }
    }
}

/// Reads the key on `line` after `prefix` into `out`: 64 hex digits for
/// version 1, 44 base64 digits for version 2. Whether it held one key.
KeyLine(line: str, prefix: str, version: int, out: mut [32]u8): bool {
    if !line.StartsWith(prefix) {
        return false
    }
    digits := line[prefix.Len()..]
    if version == 1 {
        hex.Decode(digits, mut out[..]) else {
            return false
        }
        return true
    }
    if digits.Len() != encoding.Base64Len(32, encoding.Base64) {
        return false
    }
    n := encoding.Base64DecodeTo(mut out[..], digits, encoding.Base64) else {
        return false
    }
    return n == 32
}

test "a seed gives the same keys every time, and they sign" {
    var seed: [32]u8 = [0; 32]
    seed[0] = 7
    s := FromSeed(&seed)
    t := FromSeed(&seed)
    Assert(crypto.Equal(s.sign[..], t.sign[..]) && crypto.Equal(s.box[..], t.box[..]))
    p := s.Public()
    sig := crypto.Ed25519.Sign(&s.sign, b"msg")
    Assert(crypto.Ed25519.Verify(&p.sign, b"msg", &sig))
}

test "both sides of an X25519 exchange agree" {
    var x: [32]u8 = [0; 32]
    var y: [32]u8 = [0; 32]
    x[0] = 1
    y[0] = 2
    xs := FromSeed(&x)
    ys := FromSeed(&y)
    one := crypto.X25519(&xs.box, &BoxPublic(&ys.box))
    two := crypto.X25519(&ys.box, &BoxPublic(&xs.box))
    Assert(crypto.Equal(one[..], two[..]))
}
