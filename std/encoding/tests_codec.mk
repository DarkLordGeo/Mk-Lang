// Why: Encoding.Of must drive any Codec correctly and survive one that
// breaks Codec's rules.
// What: test blocks of Codec and Encoding.Of over a toy codec (TxToy).
// How: TxToyDecode and TxToyEncode exercise the driver in codec.mk, which
// std.encoding.cjk's encodings use, and TxToyBad its guards.

import std.simd

TxToyName(): str {
    return "toy"
}

// TxToyDecode: ASCII, upper-cased in state 1; 0x81 and a byte y as
// U+0400 + y; 0x82 toggles the state and decodes to nothing; 0x83 is two
// scalars; 0x84 gives a surrogate; 0x85 never ends; 0x86 claims 100 bytes.
TxToyDecode(b: []u8, i: int, last: bool, s: u8): (n: int, c: u32, c2: u32, state: u8) {
    x := b[i]
    if x < 0x80 {
        up := s == 1 && x >= b'a' && x <= b'z'
        return (n: 1, c: u32(if up { x - 32 } else { x }), c2: NoScalar, state: s)
    }
    if x == 0x81 {
        if b.Len() - i < 2 {
            return (n: 0, c: 0, c2: NoScalar, state: s)
        }
        return (n: 2, c: 0x400 + u32(b[i + 1]), c2: NoScalar, state: s)
    }
    if x == 0x82 {
        return (n: 1, c: NoScalar, c2: NoScalar, state: 1 - s)
    }
    if x == 0x83 {
        return (n: 1, c: u32(b'x'), c2: u32(b'y'), state: s)
    }
    if x == 0x84 {
        return (n: 1, c: 0xD800, c2: NoScalar, state: s)
    }
    if x == 0x85 {
        return (n: 0, c: 0, c2: NoScalar, state: s)
    }
    return (n: 100, c: u32(b'z'), c2: NoScalar, state: s)
}

// TxToyEncode: ASCII as itself, U+0400 to U+04FF after 0x81 (state 1),
// 0x82 at the end after one of those; U+1F600 claims nine bytes.
TxToyEncode(c: u32, s: u8, out: mut []u8): (n: int, state: u8) {
    if c == NoScalar {
        if s == 1 {
            out[0] = 0x82
            return (n: 1, state: 0)
        }
        return (n: 0, state: s)
    }
    if c < 0x80 {
        out[0] = Trunc[u8](c)
        return (n: 1, state: s)
    }
    if c >= 0x400 && c <= 0x4FF {
        out[0] = 0x81
        out[1] = Trunc[u8](c - 0x400)
        return (n: 2, state: 1)
    }
    if c == 0x1F600 {
        return (n: 9, state: s)
    }
    return (n: -1, state: s)
}

const TxToy: Encoding = Encoding.Of(Codec(name: TxToyName, decode: TxToyDecode, encode: TxToyEncode))

// TxToyBad is the offset of the BadInput DecodeStrict of b fails with, -1
// when it succeeds.
TxToyBad(a: &Arena, b: []u8): int {
    _ = TxToy.DecodeStrict(a, b) else e {
        return match e {
            BadInput(offset) { offset }
            OutOfMemory { -2 }
        }
    }
    return -1
}

test "an Encoding.Of a Codec decodes through it, a two-byte unit that is two other bytes of UTF-8 included" (sys: &System) {
    a := sys.arena
    Assert(TxToy.Name() == "toy")
    Assert(TxToy.Decode(a, b"a\x81\x15b").Must() == "a\u{415}b")
    Assert(TxToy.DecodeStrict(a, b"\x81\x15\x81\x16").Must() == "\u{415}\u{416}")
    Assert(TxToy.Decode(a, b"\x83").Must() == "xy")
}

test "a Codec's state carries from unit to unit and, in a Decoder, from chunk to chunk" (sys: &System) {
    a := sys.arena
    Assert(TxToy.Decode(a, b"a\x82bc\x82d").Must() == "aBCd")
    var d := TxToy.Decoder()
    var t := Text.New(a)
    t.Push(d.Push(a, b"a\x82", false).Must()).Must()
    t.Push(d.Push(a, b"bc\x81", false).Must()).Must()
    t.Push(d.Push(a, b"\x15\x82d", true).Must()).Must()
    Assert(t.Str() == "aBC\u{415}d")
    // A Push with last starts the next stream in state 0.
    Assert(d.Push(a, b"e", true).Must() == "e")
}

test "the driver makes a codec's non-scalar U+FFFD, a unit that never ends one U+FFFD, and cuts a unit to the input" (sys: &System) {
    a := sys.arena
    Assert(TxToy.Decode(a, b"\x84q").Must() == "\u{FFFD}q")
    Assert(TxToyBad(a, b"q\x84") == 1)
    Assert(TxToy.Decode(a, b"\x85abcd").Must() == "\u{FFFD}")
    Assert(TxToy.Decode(a, b"a\x85").Must() == "a\u{FFFD}")
    Assert(TxToy.Decode(a, b"\x86abc").Must() == "z")
    var d := TxToy.Decoder()
    Assert(d.Push(a, b"a\x85", false).Must() == "a")
    Assert(d.Push(a, b"", true).Must() == "\u{FFFD}")
}

test "Encode drives a Codec's encoder, ends with its call for the end, and fails at a scalar it has no bytes for" (sys: &System) {
    a := sys.arena
    Assert(simd.Equal(TxToy.Encode(a, "a\u{415}").Must(), b"a\x81\x15\x82"))
    Assert(simd.Equal(TxToy.Encode(a, "ab").Must(), b"ab"))
    Assert(TxEncodeWhy(a, TxToy, "\u{1F600}") == "BadInput 0")
    Assert(TxEncodeWhy(a, TxToy, "x\u{E000}") == "BadInput")
}
