// Why: a sealed archive can be opened only by the holder of one X25519 key,
// in the manner of age, and any change to it is detected.
//
// What: Sealer (New, Flush, Finish), Open and Opened.Decrypt, Keys, Cipher,
// and the errors NotSealed, WrongKey, Cut, BadChunk, WeakKey and Version.
//
// How: an ephemeral X25519 key agrees a secret with the recipient, and the
// data is sealed in fixed-size chunks under the layout below.

// Sealing an archive to a recipient's X25519 key, in the manner of age.
//
//    0  "MKPE"
//    4  version: 2, or 1 for archives written before AES-256-GCM
//    5  version 2: the cipher, 1 ChaCha20-Poly1305 (RFC 8439) or 2
//       AES-256-GCM (NIST SP 800-38D); then zero bytes up to 8
//    8  the sender's ephemeral X25519 public point (32 bytes)
//   40  the plain length, u64
//   48  HMAC-SHA-256 of bytes 0 to 48 under the MAC key (32 bytes)
//   80  the chunks: each ChunkLen plain bytes sealed with the cipher, the
//       last one shorter or as long, each followed by its 16-byte tag
//
// The shared secret is X25519(ephemeral, recipient). Version 2 derives the
// key and the MAC key from it with HKDF-SHA-256 (RFC 5869), the salt both
// public points and the info "pack seal 2 key" or "pack seal 2 mac".
// Version 1 took them from SHA-512 of the secret and both points (the first
// and the last 32 bytes); it is still read. Chunk i's nonce is i as eight
// little-endian bytes, three zero bytes, and 1 for the last chunk or 0
// before it, so chunks cannot be reordered, dropped or cut at a chunk
// boundary. Each chunk's associated data is the header MAC, which binds the
// chunks to the header. Every secret is cleared with crypto.Wipe once used.
import src.archive
import src.keys
import std.crypto
import std.hash

/// Bytes before the first chunk.
export const HeaderLen: int = 80

/// Plain bytes in every chunk but the last.
export const ChunkLen: int = 65536

const TagLen: int = 16
const Magic: str = "MKPE"

/// The input does not start with the sealed-archive magic.
export error NotSealed
/// The header's MAC does not verify: the key is not the recipient's, or the
/// header is damaged.
export error WrongKey
/// The sealed stream is shorter or longer than its header says.
export error Cut
/// Chunk `index` does not authenticate.
export error BadChunk(index: int)
/// The recipient's key is a low-order point, which gives no secret.
export error WeakKey
/// The header names a version, or a cipher, this program does not read.
export error Version(version: int, cipher: int)

/// The errors of opening a sealed archive.
export type Error = NotSealed | WrongKey | Cut | BadChunk | WeakKey | Version

/// The cipher that seals the chunks.
export enum Cipher: u8 {
    ChaCha20Poly1305 = 1
    Aes256Gcm = 2
}

impl Format for Cipher {
    Format(&self, w: mut Writer): WriteError!void {
        name := match *self {
            .ChaCha20Poly1305 { "ChaCha20-Poly1305" }
            .Aes256Gcm { "AES-256-GCM" }
        }
        try w.Str(name)
    }
}

/// Whether `b` starts as a sealed archive does.
export IsSealed(b: []u8): bool {
    if b.Len() < 4 {
        return false
    }
    for i in 0..4 {
        if b[i] != Magic.Byte(i) {
            return false
        }
    }
    return true
}

/// The keys of one sealed stream.
struct Keys {
    key: [32]u8
    mac: [32]u8
}

/// Clears both keys.
Keys.Wipe(mut self) {
    crypto.Wipe(mut self.key[..])
    crypto.Wipe(mut self.mac[..])
}

/// Fails with `WeakKey` when the shared secret is all zeros, which a
/// low-order point gives whatever the scalar.
Strong(shared: &[32]u8): WeakKey!void {
    var zero: [32]u8 = [0; 32]
    if crypto.Equal(shared[..], zero[..]) {
        fail WeakKey
    }
}

/// The keys of a version 1 stream: SHA-512 of the shared secret and both
/// points. The caller has checked the secret with `Strong`.
DeriveSha512(shared: &[32]u8, ephemeral: &[32]u8, recipient: &[32]u8): Keys {
    var input: [96]u8 = [0; 96]
    (mut input[..32]).CopyFrom(shared[..])
    (mut input[32..64]).CopyFrom(ephemeral[..])
    (mut input[64..]).CopyFrom(recipient[..])
    var wide := hash.Sha512(input[..])
    var k := Keys(key: [0; 32], mac: [0; 32])
    k.key.CopyFrom(wide[..32])
    k.mac.CopyFrom(wide[32..])
    crypto.Wipe(mut input[..])
    crypto.Wipe(mut wide[..])
    return k
}

/// The keys of a version 2 stream: HKDF-SHA-256 with both points as the
/// salt. The caller has checked the secret with `Strong`.
DeriveHkdf(shared: &[32]u8, ephemeral: &[32]u8, recipient: &[32]u8): Keys {
    var salt: [64]u8 = [0; 64]
    (mut salt[..32]).CopyFrom(ephemeral[..])
    (mut salt[32..]).CopyFrom(recipient[..])
    var prk := crypto.HkdfExtract(salt[..], shared[..], crypto.HashKind.Sha256)
    var k := Keys(key: [0; 32], mac: [0; 32])
    // 32 bytes from a key not yet wiped: HkdfExpand fails only past 8,160
    // bytes or after Wipe
    crypto.HkdfExpand(&prk, b"pack seal 2 key", mut k.key[..]).Must()
    crypto.HkdfExpand(&prk, b"pack seal 2 mac", mut k.mac[..]).Must()
    prk.Wipe()
    return k
}

Nonce(index: int, last: bool): [12]u8 {
    var n: [12]u8 = [0; 12]
    archive.PutU64(mut n[..8], Cast[u64](index))
    if last {
        n[11] = 1
    }
    return n
}

/// The version 2 header of a stream sealed with `cipher` from `ephemeral`
/// to `recipient`, `plain_len` bytes long, and its keys. Fails with
/// `WeakKey` for a low-order recipient. Clears the shared secret.
Begin(ephemeral: &[32]u8, recipient: &[32]u8, plain_len: i64, cipher: Cipher): WeakKey!(header: [80]u8, keys: Keys) {
    var shared := crypto.X25519(ephemeral, recipient)
    public := keys.BoxPublic(ephemeral)
    // an all-zero secret holds nothing to clear
    try Strong(&shared)
    k := DeriveHkdf(&shared, &public, recipient)
    crypto.Wipe(mut shared[..])
    var h: [80]u8 = [0; 80]
    for i in 0..4 {
        h[i] = Magic.Byte(i)
    }
    h[4] = 2
    h[5] = u8(cipher)
    (mut h[8..40]).CopyFrom(public[..])
    archive.PutU64(mut h[40..48], Cast[u64](plain_len))
    mac := crypto.Hmac256(k.mac[..], h[..48])
    (mut h[48..]).CopyFrom(mac[..])
    return (header: h, keys: k)
}

/// Seals `plain` into `out`, its length plus a tag, with `cipher` under
/// `key`. The callers' buffers always agree in length, so neither cipher
/// can fail. An AES-256-GCM key object is made and wiped for each chunk:
/// its key expansion is small beside 64 KiB of data.
SealChunk(cipher: Cipher, key: &[32]u8, nonce: &[12]u8, aad: []u8, plain: []u8, out: mut []u8) {
    match cipher {
        .ChaCha20Poly1305 { crypto.ChaCha20Poly1305.Seal(key, nonce, aad, plain, out).Must() }
        .Aes256Gcm {
            var g := crypto.AesGcm.New(key[..]).Must()
            _ = g.Seal(nonce, aad, plain, out).Must()
            g.Wipe()
        }
    }
}

/// Opens `sealed` into `out` with `cipher` under `key`. Fails with the
/// cipher's error: `BadTag` for a chunk that does not authenticate,
/// `BadLength` for one too short to hold a tag.
OpenChunk(cipher: Cipher, key: &[32]u8, nonce: &[12]u8, aad: []u8, sealed: []u8, out: mut []u8): crypto.Error!void {
    match cipher {
        .ChaCha20Poly1305 { try crypto.ChaCha20Poly1305.Open(key, nonce, aad, sealed, out) }
        .Aes256Gcm {
            var g := try crypto.AesGcm.New(key[..])
            _ = g.Open(nonce, aad, sealed, out) else e {
                g.Wipe()
                fail e
            }
            g.Wipe()
        }
    }
}

/// The bytes a stream of `plain_len` plain bytes seals to, header included.
export SealedLen(plain_len: i64): i64 {
    chunks := if plain_len == 0 { i64(1) } else { (plain_len + i64(ChunkLen) - 1) / i64(ChunkLen) }
    return i64(HeaderLen) + plain_len + chunks * i64(TagLen)
}

/// A stream that seals what it is given, chunk by chunk, into `out`, for a
/// recipient. Its header is ready at once, for the caller to write first or
/// last; `Finish` seals the last chunk.
export view struct Sealer {
    out: Writer
    cipher: Cipher
    keys: Keys
    export header: [80]u8
    plain: mut []u8
    sealed: mut []u8
    used: int
    index: int
}

/// A sealer with `cipher` to `recipient`'s X25519 point for `plain_len`
/// bytes, with an ephemeral key from the OS random source, cleared once
/// the header is made. Fails with `Canceled` from the random source,
/// `WeakKey` for a low-order point, and `OutOfMemory` for its two chunk
/// buffers (about 128 KiB), which live in `a`.
export Sealer.New(a: &Arena, io: Io, out: Writer, recipient: &[32]u8, plain_len: i64, cipher: Cipher): Canceled|WeakKey|OutOfMemory!Sealer from a, out {
    plain := try a.Slice[u8](ChunkLen)
    sealed := try a.Slice[u8](ChunkLen + TagLen)
    var ephemeral: [32]u8 = [0; 32]
    try crypto.Random(io, mut ephemeral[..])
    (header, keys: k) := Begin(&ephemeral, recipient, plain_len, cipher) else e {
        crypto.Wipe(mut ephemeral[..])
        fail e
    }
    crypto.Wipe(mut ephemeral[..])
    return Sealer(out: out, cipher: cipher, keys: k, header: header, plain: plain, sealed: sealed, used: 0, index: 0)
}

Sealer.Flush(mut self, last: bool): WriteError!void {
    nonce := Nonce(self.index, last)
    n := self.used
    SealChunk(self.cipher, &self.keys.key, &nonce, self.header[48..], self.plain[..n], mut self.sealed[..n + TagLen])
    try self.out.Bytes(self.sealed[..n + TagLen])
    self.index += 1
    self.used = 0
}

impl archive.Sink for Sealer {
    Put(mut self, b: []u8): WriteError!void {
        var rest := b
        for rest.Len() > 0 {
            if self.used == ChunkLen {
                try self.Flush(false)
            }
            room := ChunkLen - self.used
            n := if rest.Len() < room { rest.Len() } else { room }
            (mut self.plain[self.used..self.used + n]).CopyFrom(rest[..n])
            self.used += n
            rest = rest[n..]
        }
    }
}

/// Seals the last chunk, clears the keys and the plain buffer, and flushes
/// the writer. Fails with the writer's `WriteError`, the keys cleared
/// either way.
export Sealer.Finish(mut self): WriteError!void {
    self.Flush(true) else e {
        self.Wipe()
        fail e
    }
    self.Wipe()
    try self.out.Flush()
}

/// Clears the keys and the plain buffer, for a stream given up.
export Sealer.Wipe(mut self) {
    self.keys.Wipe()
    crypto.Wipe(mut self.plain[..])
}

/// What a sealed archive's header says, once its MAC has verified.
export struct Opened {
    export cipher: Cipher
    keys: Keys
    export plain_len: i64
    mac: [32]u8
}

/// Reads and authenticates the header of `sealed` with `secret`, the
/// recipient's X25519 scalar. Fails with `NotSealed`, `Version` for a
/// version or cipher it does not know, `Cut` when the header or the stream
/// is shorter than the header says, `WeakKey`, and `WrongKey` when the MAC
/// does not verify. O(1). The caller clears the keys with `Wipe`.
export Open(sealed: []u8, secret: &[32]u8): Error!Opened {
    if !IsSealed(sealed) {
        fail NotSealed
    }
    if sealed.Len() < HeaderLen {
        fail Cut
    }
    version := sealed[4]
    cipher := match version {
        1 { Cipher.ChaCha20Poly1305 }
        2 {
            match sealed[5] {
                1 { Cipher.ChaCha20Poly1305 }
                2 { Cipher.Aes256Gcm }
                _ { fail Version(version: 2, cipher: int(sealed[5])) }
            }
        }
        _ { fail Version(version: int(version), cipher: 0) }
    }
    var ephemeral: [32]u8 = [0; 32]
    ephemeral.CopyFrom(sealed[8..40])
    var shared := crypto.X25519(secret, &ephemeral)
    recipient := keys.BoxPublic(secret)
    try Strong(&shared)
    var k := if version == 1 { DeriveSha512(&shared, &ephemeral, &recipient) } else { DeriveHkdf(&shared, &ephemeral, &recipient) }
    crypto.Wipe(mut shared[..])
    mac := crypto.Hmac256(k.mac[..], sealed[..48])
    if !crypto.Equal(mac[..], sealed[48..80]) {
        k.Wipe()
        fail WrongKey
    }
    plain_len := Cast[i64](archive.U64(sealed[40..48]))
    if plain_len < 0 || i64(sealed.Len()) < SealedLen(plain_len) {
        k.Wipe()
        fail Cut
    }
    var o := Opened(cipher: cipher, keys: k, plain_len: plain_len, mac: [0; 32])
    o.mac.CopyFrom(sealed[48..80])
    return o
}

/// Clears the keys.
export Opened.Wipe(mut self) {
    self.keys.Wipe()
}

// Fails with the sealed archive's error for chunk `index`, which the cipher
// refused with `e`: a tag that does not verify is a damaged or forged chunk,
// and a length the cipher refuses is a stream cut short.
ChunkFailure(e: crypto.Error, index: int): Cut|BadChunk!void {
    match e {
        crypto.BadTag { fail BadChunk(index: index) }
        crypto.BadLength { fail Cut }
        // the cipher's other errors are about keys and encodings, which a
        // chunk cannot cause; crypto.Error may gain members
        _ { fail BadChunk(index: index) }
    }
}

/// Decrypts the chunks of `sealed` into `out`, exactly `o.plain_len` bytes.
/// Fails with `BadChunk` at the first chunk that does not authenticate, and
/// with `Cut` when bytes follow the last chunk. O(n); allocates nothing.
export Opened.Decrypt(&self, sealed: []u8, out: mut []u8): Cut|BadChunk!void {
    var pos := HeaderLen
    var done := 0
    var index := 0
    total := Trunc[int](self.plain_len)
    for {
        n := if total - done < ChunkLen { total - done } else { ChunkLen }
        last := done + n == total
        nonce := Nonce(index, last)
        OpenChunk(self.cipher, &self.keys.key, &nonce, self.mac[..], sealed[pos..pos + n + TagLen], mut out[done..done + n]) else e {
            try ChunkFailure(e, index)
        }
        pos += n + TagLen
        done += n
        index += 1
        if last {
            break
        }
    }
    if pos != sealed.Len() {
        fail Cut
    }
}

test "a sealed header opens with the recipient's key and not another's" {
    var seed: [32]u8 = [0; 32]
    seed[0] = 3
    recipient := keys.FromSeed(&seed)
    seed[0] = 4
    other := keys.FromSeed(&seed)
    var e: [32]u8 = [0; 32]
    e[0] = 5
    (header, keys: k) := try Begin(&e, &keys.BoxPublic(&recipient.box), 3, Cipher.Aes256Gcm)
    _ = k
    var stream: [99]u8 = [0; 99]
    (mut stream[..80]).CopyFrom(header[..])
    o := try Open(stream[..], &recipient.box)
    Assert(o.plain_len == 3 && o.cipher == Cipher.Aes256Gcm)
    var wrong := false
    _ = Open(stream[..], &other.box) else {
        wrong = true
    }
    Assert(wrong)
}

test "both ciphers seal a chunk that opens, and refuse a changed one" {
    var key: [32]u8 = [7; 32]
    nonce := Nonce(0, true)
    plain := b"sealed chunk"
    for cipher in [Cipher.ChaCha20Poly1305, Cipher.Aes256Gcm] {
        var sealed: [28]u8 = [0; 28]
        SealChunk(cipher, &key, &nonce, b"aad", plain, mut sealed[..])
        var out: [12]u8 = [0; 12]
        try OpenChunk(cipher, &key, &nonce, b"aad", sealed[..], mut out[..])
        Assert(str.From(out[..]).Must() == "sealed chunk")
        sealed[3] ^= 1
        var refused := false
        OpenChunk(cipher, &key, &nonce, b"aad", sealed[..], mut out[..]) else {
            refused = true
        }
        Assert(refused)
    }
}
