// Why: `convert` can show the bytes it produces as text.
// What: Dumper, DumpKind and ParseDump.
// How: hex lines or padded base64 lines of 76 characters.

import std.encoding

/// How `convert` writes the bytes it produces.
export enum DumpKind {
    /// The bytes themselves
    Raw
    /// Lower-case hex, 64 digits a line
    Hex
    /// Padded standard base64 (RFC 4648 section 4), 76 characters a line
    Base64
}

/// The characters a hex dump puts on a line.
const HexLine: int = 64

/// The characters a base64 dump puts on a line, as MIME does (RFC 2045 section 6.8).
const Base64Line: int = 76

/// The dump kind `name` names, or `none`.
export ParseDump(name: str): ?DumpKind {
    return match name {
        "raw" { some(DumpKind.Raw) }
        "hex" { some(DumpKind.Hex) }
        "base64" { some(DumpKind.Base64) }
        _ { none }
    }
}

/// A writer of one output stream as raw bytes, hex or base64, that may be
/// handed the stream in pieces of any size: base64's groups of three bytes
/// and both dumps' lines run on across pieces.
export struct Dumper {
    kind: DumpKind
    // characters on the current output line
    column: int
    // bytes of a base64 group not yet complete, never more than 2
    carry: [2]u8
    held: int
}

/// A dumper of kind `kind` at the start of a stream.
export Dumper.New(kind: DumpKind): Dumper {
    return Dumper(kind: kind, column: 0, carry: [0; 2], held: 0)
}

/// Writes the next piece of the stream. O(n); allocates twice the piece's
/// length in `a` for a dump, nothing for raw bytes.
export Dumper.Write(mut self, a: &Arena, w: mut Writer, bytes: []u8): OutOfMemory|WriteError!void {
    match self.kind {
        .Raw { try w.Bytes(bytes) }
        .Hex { try self.Lines(w, (try encoding.HexEncode(a, bytes)).AsBytes(), HexLine) }
        .Base64 { try self.WriteBase64(a, w, bytes) }
    }
}

/// Writes the complete base64 groups of the carried bytes and `bytes`, and
/// carries the rest to the next piece.
Dumper.WriteBase64(mut self, a: &Arena, w: mut Writer, bytes: []u8): OutOfMemory|WriteError!void {
    total := self.held + bytes.Len()
    whole := total - total % 3
    if whole == 0 {
        for b in bytes {
            self.carry[self.held] = b
            self.held += 1
        }
        return
    }
    group := try a.Slice[u8](whole)
    for i in 0..self.held {
        group[i] = self.carry[i]
    }
    used := whole - self.held
    for i in 0..used {
        group[self.held + i] = bytes[i]
    }
    self.held = 0
    for b in bytes[used..] {
        self.carry[self.held] = b
        self.held += 1
    }
    try self.Base64Of(a, w, group[..])
}

/// Writes `group` in base64, padded, as lines of `Base64Line` characters.
Dumper.Base64Of(mut self, a: &Arena, w: mut Writer, group: []u8): OutOfMemory|WriteError!void {
    dst := try a.Slice[u8](encoding.Base64Len(group.Len(), encoding.Base64))
    n := encoding.Base64EncodeTo(dst, group, encoding.Base64)
    try self.Lines(w, dst[..n], Base64Line)
}

/// Writes `digits`, ASCII, starting a new line each time the current one
/// holds `width` characters.
Dumper.Lines(mut self, w: mut Writer, digits: []u8, width: int): WriteError!void {
    var rest := digits
    for rest.Len() > 0 {
        if self.column == width {
            try w.Str("\n")
            self.column = 0
        }
        take := if rest.Len() < width - self.column { rest.Len() } else { width - self.column }
        try w.Bytes(rest[..take])
        self.column += take
        rest = rest[take..]
    }
}

/// Ends the stream: writes the last base64 group with its padding, and the
/// newline that ends a dump's last line. O(1); allocates at most 4 bytes in `a`.
export Dumper.Finish(mut self, a: &Arena, w: mut Writer): OutOfMemory|WriteError!void {
    if self.kind == DumpKind.Base64 && self.held > 0 {
        n := self.held
        last := self.carry
        self.held = 0
        try self.Base64Of(a, w, last[..n])
    }
    if self.column > 0 {
        try w.Str("\n")
        self.column = 0
    }
}
