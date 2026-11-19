// Why: imgproc writes PNM files.
// What: Encode and EncodeBitmap.
// How: P5 and P6 in binary, or P2 and P3 in ASCII with lines of at most 70
// characters as Netpbm asks, and the bitmap P4.

import std.simd

/// Appends `s` to `out`.
PushStr(out: mut List[u8], s: str): OutOfMemory!void {
    for b in s.AsBytes() {
        try out.Push(b)
    }
}

/// Appends the decimal digits of `n`, which is at least 0.
PushInt(out: mut List[u8], n: int): OutOfMemory!void {
    var digits: [20]u8 = [0; 20]
    var k := 0
    var v := n
    for {
        digits[k] = b'0' + Trunc[u8](v % 10)
        k += 1
        v /= 10
        if v == 0 {
            break
        }
    }
    for k > 0 {
        k -= 1
        try out.Push(digits[k])
    }
}

/// Encodes `img` as a PNM file in `a`: binary unless `ascii` is set. Fails
/// with `OutOfMemory` when `a` cannot hold the file. O(n); allocates the file
/// from `a`.
export Encode(a: &Arena, img: &Image, ascii: bool): OutOfMemory![]u8 from a {
    var out := List[u8].New(a)
    magic := if ascii {
        if img.channels == 1 { "P2" } else { "P3" }
    } else {
        img.Magic()
    }
    try PushStr(mut out, magic)
    try PushStr(mut out, "\n")
    try PushInt(mut out, img.width)
    try PushStr(mut out, " ")
    try PushInt(mut out, img.height)
    try PushStr(mut out, "\n")
    try PushInt(mut out, img.maxval)
    try PushStr(mut out, "\n")
    samples := img.Samples()
    if !ascii {
        for b in samples {
            try out.Push(b)
        }
        done := out.Done()
        return done[..]
    }
    row := img.RowBytes()
    for y in 0..img.height {
        var col := 0
        for x in 0..row {
            v := int(samples[y * row + x])
            width := if v >= 100 { 3 } else if v >= 10 { 2 } else { 1 }
            if col > 0 && col + 1 + width > 70 {
                try out.Push(b'\n')
                col = 0
            }
            if col > 0 {
                try out.Push(b' ')
                col += 1
            }
            try PushInt(mut out, v)
            col += width
        }
        try out.Push(b'\n')
    }
    done := out.Done()
    return done[..]
}

/// Encodes a gray `img` as a binary PBM (P4) in `a`: a sample at most half
/// of maxval is black, a set bit, and each row packs 8 pixels a byte, the
/// first in the high bit, the last byte padded with 0 bits. 16 samples a
/// step: Reverse puts the first sample in the top lane, so the comparison's
/// Mask.Bits holds the step's two bytes in order, past the row end white.
/// O(n); allocates the file from `a`.
export EncodeBitmap(a: &Arena, img: &Image): OutOfMemory![]u8 from a
    requires img.channels == 1
{
    var out := List[u8].New(a)
    try PushStr(mut out, "P4\n")
    try PushInt(mut out, img.width)
    try PushStr(mut out, " ")
    try PushInt(mut out, img.height)
    try PushStr(mut out, "\n")
    half := simd.Vec[u8, 16].Splat(Trunc[u8](img.maxval / 2))
    white := simd.Vec[u8, 16].Splat(255)
    samples := img.Samples()
    for y in 0..img.height {
        row := samples[y * img.width..(y + 1) * img.width]
        var x := 0
        for x < row.Len() {
            bits := (simd.Vec[u8, 16].FromSliceOr(row, x, white).Reverse() <= half).Bits()
            try out.Push(Trunc[u8](bits >> 8))
            if x + 8 < row.Len() {
                try out.Push(Trunc[u8](bits))
            }
            x += 16
        }
    }
    done := out.Done()
    return done[..]
}
