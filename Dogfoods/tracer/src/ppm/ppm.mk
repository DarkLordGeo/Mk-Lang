// Why: The tracer reads textures and writes images in the simplest colour
// format there is.
//
// What: Netpbm colour images (the PPM format,
// netpbm.sourceforge.net/doc/ppm.html): P3 with ASCII samples and P6 with
// binary ones, with Decode, Encode, and Checksum (64-bit FNV-1a).
//
// How: Samples are 8-bit, or 16-bit big-endian past a maxval of 255.

import std.math

/// The file does not start with `P3` or `P6`.
export error BadMagic
/// A header field or an ASCII sample at this byte offset is missing or out of
/// range.
export error BadHeader(offset: int)
/// The file ends before its last sample.
export error Truncated

/// What decoding can fail with.
export type Error = BadMagic | BadHeader | Truncated | OutOfMemory

/// How samples are written.
export enum Encoding {
    /// P6, one byte a sample
    Binary8
    /// P6, two big-endian bytes a sample
    Binary16
    /// P3, decimal text
    Ascii
}

/// The size of a decoded image.
export struct Size {
    export width: int
    export height: int
}

struct Cursor {
    data: []u8
    pos: int
}

IsSpace(b: u8): bool {
    return b == b' ' || b == b'\t' || b == b'\n' || b == b'\r'
}

// Skips whitespace and `#` comments, which run to the end of their line.
Cursor.Skip(mut self) {
    for self.pos < self.data.Len() {
        b := self.data[self.pos]
        if b == b'#' {
            for self.pos < self.data.Len() && self.data[self.pos] != b'\n' {
                self.pos += 1
            }
        } else if IsSpace(b) {
            self.pos += 1
        } else {
            return
        }
    }
}

// Reads a decimal number from 0 to `max`.
Cursor.Number(mut self, max: int): Error!int {
    self.Skip()
    start := self.pos
    var n := 0
    for self.pos < self.data.Len() {
        b := self.data[self.pos]
        if b < b'0' || b > b'9' {
            break
        }
        n = n * 10 + Cast[int](b - b'0')
        if n > max {
            fail BadHeader(offset: start)
        }
        self.pos += 1
    }
    if self.pos == start {
        if self.pos == self.data.Len() {
            fail Truncated
        }
        fail BadHeader(offset: start)
    }
    return n
}

/// Decodes a P3 or P6 image, appending its samples to `out` as three values
/// in [0, 1] per pixel, row by row from the top. Fails with `BadMagic`,
/// `BadHeader` or `Truncated` on a malformed file, and `OutOfMemory` when `a`
/// runs out. O(n) in the file; appends 12 bytes per pixel to `out`.
export Decode(a: &Alloc into out, data: []u8, out: mut HeapList[f32]): Error!Size {
    if data.Len() < 2 || data[0] != b'P' || (data[1] != b'3' && data[1] != b'6') {
        fail BadMagic
    }
    ascii := data[1] == b'3'
    var c := Cursor(data: data, pos: 2)
    width := try c.Number(1 << 16)
    height := try c.Number(1 << 16)
    maxval := try c.Number(65535)
    if width == 0 || height == 0 || maxval == 0 {
        fail BadHeader(offset: c.pos)
    }
    scale := 1.0 / Cast[f32](maxval)
    count := width * height * 3
    if ascii {
        for _ in 0..count {
            try out.Push(a, Cast[f32](try c.Number(maxval)) * scale)
        }
        return Size(width: width, height: height)
    }
    // One whitespace byte separates the header from the binary samples.
    pos := c.pos + 1
    wide := maxval > 255
    need := if wide { count * 2 } else { count }
    if data.Len() - pos < need {
        fail Truncated
    }
    for i in 0..count {
        v := if wide { Cast[int](data[pos + 2 * i]) << 8 | Cast[int](data[pos + 2 * i + 1]) } else { Cast[int](data[pos + i]) }
        if v > maxval {
            fail BadHeader(offset: pos + i)
        }
        try out.Push(a, Cast[f32](v) * scale)
    }
    return Size(width: width, height: height)
}

// Maps a linear intensity to a sample: clamped to [0, 1], gamma-encoded with
// exponent 1/2.2, rounded half to even. NaN, which no correct render makes,
// becomes 0 rather than a panic in Cast.
Quantize(c: f64, maxval: int): int {
    if !(c > 0.0) {
        return 0
    }
    if c >= 1.0 {
        return maxval
    }
    return Cast[int](math.RoundEven(math.Pow(c, 1.0 / 2.2) * Cast[f64](maxval)))
}

PushText(out: mut List[u8], s: str): OutOfMemory!void {
    for b in s.AsBytes() {
        try out.Push(b)
    }
}

/// Encodes `width` by `height` pixels of linear RGB in `pixels` (three values
/// a pixel) as a PPM file. Fails only with `OutOfMemory`. O(n); allocates the
/// file in `a`.
export Encode(a: &Arena, width: int, height: int, pixels: []f64, enc: Encoding): OutOfMemory![]u8 from a {
    var out := List[u8].New(a)
    var scratch := try a.Sub()
    defer scratch.Free()
    maxval := if enc == Encoding.Binary16 { 65535 } else { 255 }
    magic := if enc == Encoding.Ascii { "P3" } else { "P6" }
    try PushText(mut out, try scratch.Fmt("{magic}\n{width} {height}\n{maxval}\n", (magic, width, height, maxval)))
    for i, c in pixels {
        q := Quantize(c, maxval)
        match enc {
            .Binary8 {
                try out.Push(Trunc[u8](q))
            }
            .Binary16 {
                // PPM's 16-bit samples are big-endian: swap the bytes, then
                // write the low byte of the swapped value first.
                be := math.ReverseBytes16(Trunc[u16](q))
                try out.Push(Trunc[u8](be))
                try out.Push(Trunc[u8](be >> 8))
            }
            .Ascii {
                sep := if i % (width * 3) == width * 3 - 1 { "\n" } else { " " }
                try PushText(mut out, try scratch.Fmt("{q}{sep}", (q, sep)))
            }
        }
    }
    return out.Done()[..]
}

/// The 64-bit FNV-1a hash of `data` (Fowler, Noll and Vo), which the tracer
/// prints so two renders compare by one line. O(n); allocates nothing.
export Checksum(data: []u8): u64 {
    var h: u64 = 0xCBF29CE484222325
    for b in data {
        h = (h ^ Cast[u64](b)) *% 0x100000001B3
    }
    return h
}

test "an encoded image decodes to the same samples" (sys: &System) {
    pixels: [6]f64 = [0.0, 1.0, 0.5, 1.0, 0.0, 0.25]
    a := sys.heap.Alloc()
    for enc in [Encoding.Binary8, Encoding.Binary16, Encoding.Ascii] {
        file := try Encode(sys.arena, 2, 1, pixels[..], enc)
        var back := HeapList[f32].New(a)
        defer back.Free(a)
        size := try Decode(a, file, mut back)
        Assert(size.width == 2 && size.height == 1 && back.Len() == 6)
        Assert(back[0] == 0.0 && back[1] == 1.0)
    }
}

test "a P6 file cut short is Truncated" (sys: &System) {
    a := sys.heap.Alloc()
    var out := HeapList[f32].New(a)
    defer out.Free(a)
    _ = Decode(a, b"P6 2 2 255\n\x01\x02", mut out) else e {
        match e {
            Truncated {
                return
            }
            _ {
                Assert(false)
            }
        }
    }
    Assert(false)
}
