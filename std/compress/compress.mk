// Why: most callers hold a whole compressed input in memory and want the
// whole output back, with one error set shared by every decoder in the module.
//
// What: the std.compress error set (Corrupt, Truncated, TooLarge, Error),
// and Inflate (RFC 1951) and Gunzip (RFC 1952) over a whole input.
//
// How: Drive runs inflate.mk's resumable decoder into a buffer that grows
// up to limit; Gunzip parses each member header (GzHeader), checks its
// CRC-32 and length trailer, and reads members back to back.

import std.hash

/// The input is not a valid stream. offset is the input byte where the
/// first bad element starts; each decoder's doc says which element that
/// is.
export error Corrupt(offset: int)

/// The input ends inside the stream.
export error Truncated

/// The output would pass the caller's limit, or a Zstandard frame's window
/// would pass the decoder's window limit.
export error TooLarge

/// Every way a decode fails: Corrupt, Truncated, TooLarge or OutOfMemory.
/// The set is open, so a match on it outside std keeps a `_` arm.
export open type Error = Corrupt | Truncated | TooLarge | OutOfMemory

// MaxRatio is deflate's largest expansion: a 258-byte match costs at
// least 2 bits (1 bit for each of a litlen and a distance code of a
// dynamic block), so n input bytes give at most 1032 n output bytes.
const MaxRatio = 1032

// Lim is the output bound: a negative limit acts as 0.
Lim(limit: int): int {
    return if limit < 0 { 0 } else { limit }
}

// FirstCap is the first output buffer of an input of n bytes when nothing
// tells the output size: 4 n (text deflates to a quarter to a third),
// at least 4 KiB, at most lim.
FirstCap(n: int, lim: int): int {
    c := if n <= 1024 { 4096 } else if n > lim / 4 { lim } else { n * 4 }
    return if c > lim { lim } else { c }
}

// Le32 is the 4 bytes of b at i as a little-endian word.
Le32(b: []u8, i: int): u32 {
    w := b[i..b.Len()][0..4]
    return u32(w[0]) | (u32(w[1]) << 8) | (u32(w[2]) << 16) | (u32(w[3]) << 24)
}

// StreamEnd is the input byte after the last one the decoder consumed a
// bit of: a stream ends on a byte boundary, its last byte's unused bits
// being padding.
StreamEnd(c: &Cur): int {
    return (BitPos(c) + 7) >> 3
}

// Drive runs d over data into buf until the stream ends, growing the
// output when a symbol does not fit: to twice its size, or to the room
// the symbol needs, at most lim; the old buffer stays in a. Returns the
// buffer the output is in, d.c.op bytes long.
Drive(a: &Arena, d: mut Dec, data: []u8, buf: mut []u8, lim: int): Error!mut []u8 from a, buf {
    var out := buf
    for {
        st := Run(d, data, out)
        if st == SDone {
            return out
        }
        if st == SInput {
            fail Truncated
        }
        if st == SBad {
            fail Corrupt(offset: d.c.eoff)
        }
        op := d.c.op
        need := d.c.need
        if need > lim - op {
            fail TooLarge
        }
        cap := out.Len()
        var c := if cap <= lim / 2 { cap * 2 } else { lim }
        if c < op + need {
            c = op + need
        }
        next := try a.Slice[u8](c)
        (mut next[0..op]).CopyFrom(out[0..op])
        out = next
    }
}

/// Decodes a raw DEFLATE stream (RFC 1951) into at most limit bytes. All
/// of data is the one stream; a negative limit acts as 0.
///
/// Fails with Truncated when data ends inside the stream, and TooLarge
/// when the output would pass limit. Fails with Corrupt at the first bad
/// element, whose offset is the byte holding the first bit that cannot be
/// decoded, or at any byte after the final block. O(data + output);
/// allocates the output in a: a first buffer of 4 times the input (at
/// least 4 KiB, at most limit), doubled as needed, each outgrown buffer
/// left in a.
export Inflate(a: &Arena, data: []u8, limit: int): Error![]u8 from a {
    lim := Lim(limit)
    buf := try a.Slice[u8](FirstCap(data.Len(), lim))
    var d := NewDec(0, 0)
    out := try Drive(a, mut d, data, buf, lim)
    end := StreamEnd(&d.c)
    if end != data.Len() {
        fail Corrupt(offset: end)
    }
    return out[0..d.c.op]
}

// GzHeader checks the gzip member header at data[p..] (RFC 1952 section
// 2.3) and returns the offset of its deflate stream.
GzHeader(data: []u8, p: int): Error!int {
    n := data.Len()
    if p >= n {
        fail Truncated
    }
    if data[p] != 0x1F {
        fail Corrupt(offset: p)
    }
    if p + 1 >= n {
        fail Truncated
    }
    if data[p + 1] != 0x8B {
        fail Corrupt(offset: p + 1)
    }
    if p + 2 >= n {
        fail Truncated
    }
    if data[p + 2] != 8 {
        fail Corrupt(offset: p + 2)
    }
    if p + 3 >= n {
        fail Truncated
    }
    flg := data[p + 3]
    if flg & 0xE0 != 0 {
        fail Corrupt(offset: p + 3)
    }
    if n - p < 10 {
        fail Truncated
    }
    var q := p + 10
    if flg & 4 != 0 {
        if n - q < 2 {
            fail Truncated
        }
        xlen := int(data[q]) | (int(data[q + 1]) << 8)
        q += 2
        if n - q < xlen {
            fail Truncated
        }
        q += xlen
    }
    if flg & 8 != 0 {
        q = try ZeroEnd(data, q)
    }
    if flg & 16 != 0 {
        q = try ZeroEnd(data, q)
    }
    if flg & 2 != 0 {
        if n - q < 2 {
            fail Truncated
        }
        want := u32(data[q]) | (u32(data[q + 1]) << 8)
        if hash.Crc32(data[p..q]) & 0xFFFF != want {
            fail Corrupt(offset: q)
        }
        q += 2
    }
    return q
}

// ZeroEnd is the offset after the zero byte that ends the string at q.
ZeroEnd(data: []u8, q: int): Error!int {
    for i in q..data.Len() {
        if data[i] == 0 {
            return i + 1
        }
    }
    fail Truncated
}

/// Decodes one or more gzip members (RFC 1952), back to back, into at most
/// limit bytes; a negative limit acts as 0.
///
/// Fails with Truncated when data ends inside a member, and TooLarge when
/// the output would pass limit. Fails with Corrupt at a bad header field
/// or header CRC, at a trailer whose CRC-32 or length mod 2^32 differs
/// from the member's output, at a bad element of the deflate stream, and
/// at a byte after a member that does not start another one.
/// O(data + output); allocates the output in a, sized by the last
/// member's length field when that is plausible, else as Inflate does.
export Gunzip(a: &Arena, data: []u8, limit: int): Error![]u8 from a {
    lim := Lim(limit)
    n := data.Len()
    // The last member's ISIZE sizes the buffer exactly for one member.
    isize := if n >= 18 { int(Le32(data, n - 4)) } else { -1 }
    cap := if isize >= 0 && isize <= lim && isize / MaxRatio <= n { isize } else { FirstCap(n, lim) }
    var out := try a.Slice[u8](cap)
    var p := 0
    var op := 0
    for {
        q := try GzHeader(data, p)
        var d := NewDec(q, op)
        out = try Drive(a, mut d, data, out, lim)
        end := StreamEnd(&d.c)
        if n - end < 8 {
            fail Truncated
        }
        if Le32(data, end) != hash.Crc32(out[op..d.c.op]) {
            fail Corrupt(offset: end)
        }
        if Le32(data, end + 4) != Trunc[u32](d.c.op - op) {
            fail Corrupt(offset: end + 4)
        }
        op = d.c.op
        p = end + 8
        if p == n {
            return out[0..op]
        }
    }
}
