// Why: a multi-byte encoding needs a decoder that keeps state between
// chunks, but its index tables should live in a module above std.encoding;
// Codec is the seam through which such a module (std.encoding.cjk) builds
// an Encoding.
//
// What: Codec, a decoder handler that reads one unit of bytes in a state
// and an encoder handler that writes one scalar in a state, and
// Encoding.Of, which makes an Encoding of it; MultiRun and MultiPut drive
// it for Decode, DecodeStrict, Push and Encode.
//
// How: the driver runs two passes (count, then write), keeps the pending
// bytes and the state between chunks, and handles strictness and U+FFFD.
// It trusts a Codec for nothing that memory or UTF-8 depends on: a scalar
// it does not accept is a malformed unit, and a length out of range is cut
// to the input.

/// Codec is the decoder and encoder of a multi-byte encoding, which
/// Encoding.Of turns into an Encoding. std.encoding.cjk's encodings are
/// made this way; a program needs its own Codec only for an encoding the
/// Encoding Standard does not have.
///
/// Each function must give the same result for the same arguments:
/// std.encoding calls it once to size the result and again to write it. A
/// Codec that breaks this rule can make Decode, DecodeStrict, Push and
/// Encode panic on a bounds check, or return text padded with NUL bytes;
/// it cannot make them write outside their result or return a str that is
/// not UTF-8.
///
/// Provisional: the limits (one u8 of state, units of at most four bytes
/// and two scalars, eight bytes for a scalar) may change in a minor
/// release.
export struct Codec {
    /// The encoding's name, which Encoding.Name returns.
    export name: fn(): str
    /// One unit of input: decode(b, i, last, state) reads the unit at b[i]
    /// in decoder state state (0 at a stream's start) and returns its
    /// length n, its scalar c, a second scalar c2 that some units decode
    /// to (Big5's four pairs), and the state after it. c and c2 are
    /// 0xFFFFFFFF for no scalar (an escape sequence decodes to none, and
    /// most units have no c2); any other value that is not a scalar makes
    /// the unit malformed, one U+FFFD. An n of 0 says that b ends inside
    /// the unit and more input may follow; it is read as one malformed
    /// unit when last is true or four bytes or more are left, so a unit is
    /// at most four bytes. n is at least 1 otherwise.
    export decode: fn([]u8, int, bool, u8): (n: int, c: u32, c2: u32, state: u8)
    /// One scalar of output: encode(c, state, out) writes the bytes of c in
    /// encoder state state (0 at the start) to out, which holds 8, and
    /// returns how many and the state after; n of -1 says that the encoding
    /// has no bytes for c. After the last scalar it is called once with c
    /// 0xFFFFFFFF, to write what returns the stream to state 0
    /// (ISO-2022-JP's escape to ASCII).
    export encode: fn(u32, u8, mut []u8): (n: int, state: u8)
}

// IdMulti is the id of an encoding Encoding.Of made.
const IdMulti: u8 = 254

// NoScalar is the scalar a Codec gives for none, and the end of input it
// is given.
const NoScalar: u32 = 0xFFFFFFFF

NoName(): str {
    return ""
}

NoDecode(b: []u8, i: int, last: bool, state: u8): (n: int, c: u32, c2: u32, state: u8) {
    return (n: 1, c: Bad, c2: NoScalar, state)
}

NoEncode(c: u32, state: u8, out: mut []u8): (n: int, state: u8) {
    return (n: -1, state)
}

// NoCodec is the codec field of every encoding not made by Encoding.Of,
// never called.
const NoCodec: Codec = Codec(name: NoName, decode: NoDecode, encode: NoEncode)

/// Of is the encoding that codec decodes and encodes. Decode,
/// DecodeStrict, Decoder, Encode and Name work on it as on the other
/// encodings, except that no byte order mark is removed. O(1); allocates
/// nothing. A const may hold one, as std.encoding.cjk's do.
///
/// Decode's bound of three bytes of text for each byte of input holds when
/// no unit's scalars take more than three bytes of UTF-8 for each of its
/// bytes, as in every encoding of the standard. Encode calls codec.encode
/// twice for each scalar. Provisional, as Codec is.
export Encoding.Of(codec: Codec): Encoding {
    return Encoding(id: IdMulti, table: NoTable, back: NoBack, run: RunNone, put: PutNone, codec: codec)
}

// Pass is one pass of a decoder from a state: what Ran says, and the
// decoder state after the units it read.
type Pass = (read: int, wrote: int, bad: int, state: u8)

// IsScalar is whether c is a Unicode scalar value.
IsScalar(c: u32): bool {
    return c < 0xD800 || (c > 0xDFFF && c <= 0x10FFFF)
}

// MultiRun is Run for an encoding Encoding.Of made: codec's decoder over
// the units of b that start before stop, from state.
MultiRun(codec: Codec, b: []u8, stop: int, last: bool, state: u8, out: mut []u8, write: bool, strict: bool): Pass {
    var i := 0
    var o := 0
    var s := state
    var bad := -1
    for i < stop {
        left := b.Len() - i
        u := codec.decode(b, i, last, s)
        var n := u.n
        var c := u.c
        var c2 := u.c2
        if n <= 0 {
            if !last && left < 4 {
                // The unit is unfinished: it is read again with more input.
                break
            }
            n = left
            c = Bad
            c2 = NoScalar
        }
        if n > left {
            n = left
        }
        if c != NoScalar && !IsScalar(c) {
            c = Bad
        }
        if c2 != NoScalar && !IsScalar(c2) {
            c2 = Bad
        }
        if c == Bad || c2 == Bad {
            if bad < 0 {
                bad = i
            }
            if strict {
                break
            }
        }
        if c != NoScalar {
            o = if write { Put(out, o, c) } else { o + Utf8Len(c) }
        }
        if c2 != NoScalar {
            o = if write { Put(out, o, c2) } else { o + Utf8Len(c2) }
        }
        s = u.state
        i += n
    }
    return (read: i, wrote: o, bad, state: s)
}

// MultiPut is Encoding.put for an encoding Encoding.Of made, over valid
// UTF-8 b: without write, the length of the result, or -1 - i when the
// scalar at i has no bytes in it; with write, it writes the result to out,
// which holds that length.
MultiPut(codec: Codec, b: []u8, out: mut []u8, write: bool): int {
    var buf: [8]u8 = [0; 8]
    var s: u8 = 0
    var o := 0
    var i := 0
    n := b.Len()
    for i <= n {
        var c := NoScalar
        var step := 0
        if i < n {
            sc := ScalarAt(b, i)
            c = sc.c
            step = sc.n
        }
        r := codec.encode(c, s, mut buf[..])
        if r.n < 0 || r.n > 8 {
            if i == n {
                // The end has no scalar to blame: the last one is.
                return -1 - LastScalar(b)
            }
            return -1 - i
        }
        if write {
            (mut out[o..o + r.n]).CopyFrom(buf[0..r.n])
        }
        o += r.n
        s = r.state
        if i == n {
            break
        }
        i += step
    }
    return o
}

// LastScalar is the offset of the last scalar of valid UTF-8 b, 0 when b
// is empty.
LastScalar(b: []u8): int {
    var i := b.Len()
    for i > 0 {
        i -= 1
        if b[i] & 0xC0 != 0x80 {
            return i
        }
    }
    return 0
}
