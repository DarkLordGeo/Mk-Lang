// Why: imgproc reads the Netpbm PGM and PPM formats.
// What: Decode, Cursor and the errors BadMagic, BadHeader, BadSample,
// Truncated, TooLarge and WideSamples.
// How: magic, then width, height and maxval as ASCII decimals with `#`
// comments; P2 and P3 hold ASCII samples, P5 and P6 one raw byte each after
// exactly one whitespace byte.

/// The file does not start with P2, P3, P5 or P6.
export error BadMagic
/// A header field is missing, not a decimal, or zero; `offset` is its byte.
export error BadHeader(offset: int)
/// A sample is not a decimal, or exceeds maxval; `offset` is its byte.
export error BadSample(offset: int)
/// The file ends before its last sample.
export error Truncated(expected: int, got: int)
/// The maxval is above 255: 16-bit samples are not supported.
export error WideSamples(maxval: int)
/// The width or height exceeds `MaxSide`, or the image `MaxSamples`.
export error TooLarge(width: int, height: int)

export type Error = BadMagic | BadHeader | BadSample | Truncated | WideSamples | TooLarge | OutOfMemory

/// A cursor over the bytes of a PNM file.
struct Cursor {
    data: []u8
    pos: int
}

IsSpace(b: u8): bool {
    return b == b' ' || b == b'\t' || b == b'\n' || b == b'\r' || b == 0x0B || b == 0x0C
}

IsDigit(b: u8): bool {
    return b >= b'0' && b <= b'9'
}

/// Skips whitespace and comments.
Cursor.SkipSpace(mut self) {
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

/// Reads a decimal of at most 9 digits; `none` when no digit is next.
Cursor.Decimal(mut self): ?int {
    start := self.pos
    var n := 0
    for self.pos < self.data.Len() && IsDigit(self.data[self.pos]) {
        if self.pos - start == 9 {
            return none
        }
        n = n * 10 + int(self.data[self.pos] - b'0')
        self.pos += 1
    }
    if self.pos == start {
        return none
    }
    return some(n)
}

/// Reads one positive header field.
Cursor.Field(mut self): BadHeader!int {
    self.SkipSpace()
    at := self.pos
    n := self.Decimal() else {
        fail BadHeader(offset: at)
    }
    if n == 0 {
        fail BadHeader(offset: at)
    }
    return n
}

/// Decodes a P2, P3, P5 or P6 file into an image whose samples live in `a`.
/// Fails with the error naming the first fault found. O(n) in the file size;
/// allocates the samples from `a`.
export Decode(a: &Alloc, data: []u8): Error!Image from a {
    if data.Len() < 2 || data[0] != b'P' {
        fail BadMagic
    }
    kind := data[1]
    if kind != b'2' && kind != b'3' && kind != b'5' && kind != b'6' {
        fail BadMagic
    }
    channels := if kind == b'3' || kind == b'6' { 3 } else { 1 }
    var c := Cursor(data: data, pos: 2)
    width := try c.Field()
    height := try c.Field()
    maxval := try c.Field()
    if maxval > 255 {
        fail WideSamples(maxval: maxval)
    }
    if width > MaxSide || height > MaxSide || width * height * channels > MaxSamples {
        fail TooLarge(width: width, height: height)
    }
    var img := try Image.New(a, width, height, channels, maxval)
    onerr img.Free(a)
    if kind == b'5' || kind == b'6' {
        try ReadRaw(&data, c.pos, mut img)
    } else {
        try ReadAscii(mut c, mut img)
    }
    return img
}

/// Copies raw samples, which start after the one whitespace byte at `pos`.
ReadRaw(data: &[]u8, pos: int, img: mut Image): BadHeader|BadSample|Truncated!void {
    if pos >= data.Len() || !IsSpace(data[pos]) {
        fail BadHeader(offset: pos)
    }
    start := pos + 1
    maxval := img.maxval
    dst := img.SamplesMut()
    have := data.Len() - start
    if have < dst.Len() {
        fail Truncated(expected: dst.Len(), got: have)
    }
    dst.CopyFrom(data[start..start + dst.Len()])
    for i, b in dst {
        if int(b) > maxval {
            fail BadSample(offset: start + i)
        }
    }
}

/// Parses ASCII decimal samples.
ReadAscii(c: mut Cursor, img: mut Image): BadSample|Truncated!void {
    maxval := img.maxval
    dst := img.SamplesMut()
    for i in 0..dst.Len() {
        c.SkipSpace()
        if c.pos >= c.data.Len() {
            fail Truncated(expected: dst.Len(), got: i)
        }
        at := c.pos
        n := c.Decimal() else {
            fail BadSample(offset: at)
        }
        if n > maxval {
            fail BadSample(offset: at)
        }
        dst[i] = Trunc[u8](n)
    }
}
