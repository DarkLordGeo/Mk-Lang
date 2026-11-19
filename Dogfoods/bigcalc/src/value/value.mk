// Why: every command takes byte values and files in the same forms.
//
// What: Spec (hex:, b64:, text:, file:), Input, Blocks, Pem, Text and Save.
//
// How: a spec is decoded into an arena; PEM or DER files are split into
// their blocks; Save writes through a temporary file and a rename.

import std.encoding
import std.fs

/// A spec that does not start with hex:, b64:, text: or file:.
export error BadSpec

/// Error is every way reading a value can fail.
export type Error = BadSpec | encoding.Error | fs.Error | ReadError | OutOfMemory

/// Spec is the bytes a spec names: hex:HEX, b64:BASE64 (RFC 4648, padded),
/// text:TEXT (its UTF-8 bytes) or file:PATH (the file's contents). Fails
/// with BadSpec for another prefix, and with the decoder's or the file's
/// error. Allocates the bytes in a, except for text:, a view of spec.
export Spec(a: &Arena, io: Io, spec: str): Error![]u8 from a, spec {
    if spec.StartsWith("hex:") {
        return try encoding.HexDecode(a, spec[4..])
    }
    if spec.StartsWith("b64:") {
        return try encoding.Base64Decode(a, spec[4..], encoding.Base64)
    }
    if spec.StartsWith("text:") {
        return spec[5..].AsBytes()
    }
    if spec.StartsWith("file:") {
        return try fs.ReadBytes(a, io, spec[5..])
    }
    fail BadSpec
}

/// Input is the whole of path, or of standard input for `-`. O(n);
/// allocates the bytes, and for standard input its growth, in a.
export Input(a: &Arena, io: Io, stdin: mut Reader, path: str): Error![]u8 from a {
    if path != "-" {
        return try fs.ReadBytes(a, io, path)
    }
    var data := List[u8].New(a)
    var buf: [4096]u8 = [0; 4096]
    for {
        n := try stdin.Read(mut buf[..])
        if n == 0 {
            break
        }
        for b in buf[..n] {
            try data.Push(b)
        }
    }
    bytes := data.Done()
    return bytes[..]
}

/// A block of a key or certificate file: its PEM label, or "" for a file of
/// DER, and its DER bytes.
export struct Block {
    export label: str
    export der: []u8
}

/// Blocks reads a PEM or DER file: every PEM block of a file that starts
/// with `-----BEGIN` after any leading whitespace, else the whole file as
/// one DER block. Fails with the file's error, or encoding's for bad PEM.
/// Allocates the file and the decoded blocks in a.
export Blocks(a: &Arena into a, io: Io, path: str): Error!List[Block] from a {
    data := try fs.ReadBytes(a, io, path)
    var out := List[Block].New(a)
    text := Pem(data) else {
        try out.Push(Block(label: "", der: data))
        return out
    }
    blocks: List[encoding.PemBlock] = try encoding.ParsePem(a, text)
    for block in blocks {
        try out.Push(Block(label: block.label, der: block.der))
    }
    return out
}

/// Save writes data to path through a temporary file beside it, synced and
/// then renamed into place, so a crash leaves the old file or the new one,
/// never a part of either. Fails with the file system's error, leaving the
/// temporary file behind. O(data); allocates the temporary name in a.
export Save(a: &Arena, io: Io, path: str, data: []u8): fs.Error|OutOfMemory!void {
    tmp := try a.Fmt("{path}.tmp", (path))
    var f := try fs.Create(io, tmp)
    onerr f.Abandon()
    try f.WriteAll(io, data)
    try f.Sync(io)
    try f.Close(io)
    try fs.Rename(io, tmp, path)
}

/// Pem is data as text when it is PEM: UTF-8 whose first non-blank line
/// starts with `-----BEGIN`.
export Pem(data: []u8): ?str from data {
    text := str.From(data) else {
        return none
    }
    if !text.Trim().StartsWith("-----BEGIN") {
        return none
    }
    return some(text)
}

/// Text is data in the output encoding named: "hex" (the default) or
/// "base64". Allocates the text in a.
export Text(a: &Arena, data: []u8, encoding_name: str): OutOfMemory!str from a {
    if encoding_name == "base64" {
        return try encoding.Base64Encode(a, data, encoding.Base64)
    }
    return try encoding.HexEncode(a, data)
}
