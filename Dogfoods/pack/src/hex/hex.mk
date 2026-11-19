// Why: manifests and key files write digests and keys as lowercase hex, and
// must read back exactly what they wrote.
//
// What: Hex, a Format value that prints bytes as hex through `{h}`, and
// Decode with its BadHex error.
//
// How: Decode takes exactly two lowercase digits a byte and reports the
// position of the first bad one; nothing is allocated either way.

/// A byte that is not a lowercase hex digit, or a text of the wrong length,
/// at byte `pos` of the text.
export error BadHex(pos: int)

/// The errors of this module.
export type Error = BadHex

const Digits: str = "0123456789abcdef"

/// Bytes that print as lowercase hex, two digits a byte, through a hole:
/// `{h}` with `h: Hex(bytes: b)`.
export struct Hex {
    export bytes: []u8
}

impl Format for Hex {
    Format(&self, w: mut Writer): WriteError!void {
        var buf: [64]u8 = [0; 64]
        var n := 0
        for x in self.bytes {
            buf[n] = Digits.Byte(int(x >> 4))
            buf[n + 1] = Digits.Byte(int(x & 15))
            n += 2
            if n == 64 {
                try w.Bytes(buf[..n])
                n = 0
            }
        }
        try w.Bytes(buf[..n])
    }
}

/// Decodes `text`, exactly two lowercase digits for each byte of `out`, into
/// `out`. Fails with `BadHex` at the first bad digit, or at the end of the
/// shorter of the two when the lengths disagree. O(n); allocates nothing.
export Decode(text: str, out: mut []u8): BadHex!void {
    if text.Len() != out.Len() * 2 {
        fail BadHex(pos: if text.Len() < out.Len() * 2 { text.Len() } else { out.Len() * 2 })
    }
    for i in 0..out.Len() {
        hi := try Digit(text, 2 * i)
        lo := try Digit(text, 2 * i + 1)
        out[i] = hi << 4 | lo
    }
}

Digit(text: str, pos: int): BadHex!u8 {
    c := text.Byte(pos)
    if c >= b'0' && c <= b'9' {
        return c - b'0'
    }
    if c >= b'a' && c <= b'f' {
        return c - b'a' + 10
    }
    fail BadHex(pos: pos)
}

test "Decode reads what Hex writes" (sys: &System) {
    var out: [3]u8 = [0; 3]
    try Decode("00ff7a", mut out[..])
    Assert(out[0] == 0 && out[1] == 255 && out[2] == 122)
    text := try sys.arena.Fmt("{h}", (h: Hex(bytes: out[..])))
    Assert(text == "00ff7a")
}

test "Decode refuses an upper-case digit and a short text" {
    var out: [2]u8 = [0; 2]
    var upper := false
    Decode("0A00", mut out[..]) else {
        upper = true
    }
    var short := false
    Decode("00", mut out[..]) else {
        short = true
    }
    Assert(upper && short)
}
