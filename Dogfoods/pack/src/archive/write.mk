// Why: building an archive is the same for a plain file and a sealed stream,
// so the writer works on any Sink.
//
// What: the Sink interface, Compress, SignatureBlock (Ed25519 over the
// manifest), HeaderFor and WriteBody.
//
// How: the data is compressed first, the manifest and signature are built
// from it, and WriteBody writes the sections in order; the header comes last.

import std.compress
import std.crypto

/// Where an archive's bytes go, in order: a file, or a sealing stream.
export interface Sink {
    Put(mut self, b: []u8): WriteError!void
}

/// The data compressed with `codec`. Fails only with `OutOfMemory`. O(n);
/// the result lives in `a`, and the compressor's own buffers too.
export Compress(a: &Arena, codec: Codec, raw: []u8): OutOfMemory![]u8 from a {
    return match codec {
        .Deflate { try compress.Deflate(a, raw) }
        .Gzip { try compress.Gzip(a, raw) }
    }
}

/// The signature block for `manifest`: the public half of `secret`, then the
/// Ed25519 signature. O(n) in the manifest; allocates nothing.
export SignatureBlock(secret: &[64]u8, manifest: []u8): [96]u8 {
    sig := crypto.Ed25519.Sign(secret, manifest)
    var b: [96]u8 = [0; 96]
    (mut b[..32]).CopyFrom(secret[32..64])
    (mut b[32..96]).CopyFrom(sig[..])
    return b
}

/// The header for a manifest of `manifest_len` bytes, signed when
/// `signature` is not empty.
export HeaderFor(manifest_len: int, signature: []u8): [12]u8 {
    return EncodeHeader(&Header(signed: signature.Len() > 0, manifest_len: manifest_len))
}

/// Writes everything after the header: the manifest, the signature block
/// when there is one, and the data. Fails with the sink's `WriteError`.
/// O(n); allocates nothing.
export WriteBody[S: Sink](sink: mut S, manifest: []u8, signature: []u8, data: []u8): WriteError!void {
    try sink.Put(manifest)
    try sink.Put(signature)
    try sink.Put(data)
}
