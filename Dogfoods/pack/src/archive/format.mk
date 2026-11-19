// Why: one fixed byte layout lets every pack command, and other tools, find
// an archive's manifest, signature and data without reading it whole.
//
// What: the header constants, Header with EncodeHeader and DecodeHeader,
// IsMagic, the little-endian PutU32/PutU64/U32/U64 helpers, and the Error set
// every reader of an archive returns.
//
// How: a 12-byte header gives the manifest's length; the sections follow in
// the order below, so a reader can stop after the signature.

// The container. Every integer is little-endian.
//
//    0  "MKPK"
//    4  version, 1
//    5  flags: bit 0 is set when a signature block follows the manifest
//    6  two zero bytes
//    8  the manifest's length in bytes, u32
//   12  the manifest, UTF-8 text (manifest.mk)
//       the signature block when signed: the signer's Ed25519 public key
//       (32 bytes), then its signature of the manifest's bytes (64 bytes)
//       the data: one DEFLATE (RFC 1951) or gzip (RFC 1952) stream of every
//       file's bytes, in manifest order, to the end of the archive

/// Bytes before the manifest.
export const HeaderLen: int = 12

/// Bytes of a signature block: a public key, then a signature.
export const SignatureLen: int = 96

/// The longest manifest a reader accepts, so a corrupt length cannot ask for
/// gigabytes.
export const MaxManifest: int = 16_777_216

const Magic: str = "MKPK"
const Version: u8 = 1
const FlagSigned: u8 = 1

/// The part of an archive an error is in.
export enum Section: u8 {
    Header = 1
    Manifest = 2
    Signature = 3
    Data = 4
}

impl Format for Section {
    Format(&self, w: mut Writer): WriteError!void {
        name := match *self {
            .Header { "header" }
            .Manifest { "manifest" }
            .Signature { "signature" }
            .Data { "data" }
        }
        try w.Str(name)
    }
}

/// The input does not start with the archive magic.
export error NotArchive
/// The archive's version is one this program does not read.
export error Unsupported(version: int)
/// The archive ends inside `section`.
export error Cut(section: Section)
/// A reserved header byte is not zero, or the manifest length is out of range.
export error BadHeader
/// Line `line` of the manifest is malformed.
export error BadManifest(line: int)
/// Line `line` of the manifest names a path outside the extraction directory.
export error UnsafePath(line: int)
/// The signature does not verify against the manifest.
export error BadSignature
/// A signer was required and the archive has no signature.
export error Unsigned
/// The archive is signed, by a key other than the one required.
export error OtherSigner
/// The compressed data does not match the manifest's BLAKE3 digest.
export error DataDigest
/// The data holds more or fewer bytes than the manifest lists.
export error DataSize
/// Entry `index` of the manifest does not match its BLAKE3 digest.
export error EntryDigest(index: int)

/// The errors of reading an archive's layout and checking its manifest.
export type Error = NotArchive | Unsupported | Cut | BadHeader | BadManifest | UnsafePath | BadSignature | Unsigned | OtherSigner | DataDigest | DataSize | EntryDigest

/// The fixed fields before the manifest.
export struct Header {
    export signed: bool
    export manifest_len: int
}

/// The 12 header bytes for `h`. O(1).
export EncodeHeader(h: &Header): [12]u8 {
    var b: [12]u8 = [0; 12]
    for i in 0..4 {
        b[i] = Magic.Byte(i)
    }
    b[4] = Version
    if h.signed {
        b[5] = FlagSigned
    }
    PutU32(mut b[8..12], Cast[u32](h.manifest_len))
    return b
}

/// Reads a header from the first bytes of `b`. Fails with `NotArchive` when
/// the magic is wrong, `Cut` when `b` is shorter than a header, `Unsupported`
/// for another version and `BadHeader` for a nonzero reserved byte or a
/// manifest length past `MaxManifest`. O(1).
export DecodeHeader(b: []u8): NotArchive|Unsupported|Cut|BadHeader!Header {
    if b.Len() < 4 || !IsMagic(b) {
        fail NotArchive
    }
    if b.Len() < HeaderLen {
        fail Cut(section: Section.Header)
    }
    if b[4] != Version {
        fail Unsupported(version: int(b[4]))
    }
    if b[5] & ~FlagSigned != 0 || b[6] != 0 || b[7] != 0 {
        fail BadHeader
    }
    n := int(U32(b[8..12]))
    if n == 0 || n > MaxManifest {
        fail BadHeader
    }
    return Header(signed: b[5] & FlagSigned != 0, manifest_len: n)
}

IsMagic(b: []u8): bool {
    for i in 0..4 {
        if b[i] != Magic.Byte(i) {
            return false
        }
    }
    return true
}

/// Writes `x` into the first four bytes of `b`, least significant first.
export PutU32(b: mut []u8, x: u32) {
    for i in 0..4 {
        b[i] = Trunc[u8](x >> Cast[u32](8 * i))
    }
}

/// Writes `x` into the first eight bytes of `b`, least significant first.
export PutU64(b: mut []u8, x: u64) {
    for i in 0..8 {
        b[i] = Trunc[u8](x >> Cast[u64](8 * i))
    }
}

/// The little-endian u32 in the first four bytes of `b`.
export U32(b: []u8): u32 {
    var x: u32 = 0
    for i in 0..4 {
        x = x | u32(b[i]) << Cast[u32](8 * i)
    }
    return x
}

/// The little-endian u64 in the first eight bytes of `b`.
export U64(b: []u8): u64 {
    var x: u64 = 0
    for i in 0..8 {
        x = x | u64(b[i]) << Cast[u64](8 * i)
    }
    return x
}

test "a header survives encoding" {
    b := EncodeHeader(&Header(signed: true, manifest_len: 300))
    h := DecodeHeader(b[..]).Must()
    Assert(h.signed && h.manifest_len == 300)
}

test "a header with a reserved bit set is refused" {
    var b := EncodeHeader(&Header(signed: false, manifest_len: 5))
    b[6] = 1
    var refused := false
    _ = DecodeHeader(b[..]) else {
        refused = true
        _ = Header(signed: false, manifest_len: 0)
    }
    Assert(refused)
}
