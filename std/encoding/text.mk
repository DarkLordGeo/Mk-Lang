// Why: text from files and the network arrives in many encodings, and a
// browser-grade program must turn it into UTF-8 exactly as the WHATWG
// Encoding Standard does.
//
// What: Encoding (Name, Decode, DecodeKeepBom, DecodeStrict, Decoder,
// Encode), Decoder.Push for streams, Lookup by label, and the encodings
// Utf8, Utf16Le, Utf16Be, Replacement and XUserDefined; the legacy
// single-byte encodings come from single_tables.mk and the labels from
// labels.mk.
//
// How: every decoder is the standard's: malformed input becomes one U+FFFD
// per maximal malformed unit (Unicode 15 section 3.9's "U+FFFD
// substitution of maximal subparts" for UTF-8), a lone surrogate in UTF-16
// is malformed, and a byte that a single-byte index leaves out is
// malformed. A leading byte order
// mark of the decoder's own encoding is removed, as a TextDecoder with
// ignoreBOM false does; a mark of another encoding is text. Decoding sizes
// its result in one pass and writes it in a second, so it makes one
// allocation of exactly the result's length; the second pass is a copy
// when the text is the bytes, and Decode of valid UTF-8 is one
// simd.ValidUtf8 and a copy. The fast paths are utf8.mk, utf16.mk and
// single.mk.

import std.rt
import std.simd

/// Encoding is one encoding of the WHATWG Encoding Standard: Utf8,
/// Utf16Le, Utf16Be, Replacement, XUserDefined, or one of the legacy
/// single-byte encodings (Windows1252 and the rest); or a multi-byte
/// encoding that Encoding.Of makes from a Codec, as std.encoding.cjk's are.
/// A program links only the tables and the decoder and encoder code of the
/// encodings it names; Lookup names all of this module's.
export struct Encoding {
    id: u8
    table: fn(): []u16
    back: fn(): []u32
    // run is one pass of this encoding's decoder: Run's parameters, with
    // the index table() returns first.
    run: fn([]u16, []u8, int, bool, mut []u8, bool, bool): Ran
    // put is one pass of this encoding's encoder over valid UTF-8, with
    // the reversed index back() returns first: without write, the length
    // of the result, or -1 - i when the scalar at i has no bytes here; with
    // write, it writes the result to out, which holds that length.
    put: fn([]u32, []u8, mut []u8, bool): int
    // codec is a multi-byte encoding's (id IdMulti), NoCodec elsewhere.
    codec: Codec
}

const EmptyMap: []u16 = []
const EmptyRev: []u32 = []

NoTable(): []u16 {
    return EmptyMap
}

NoBack(): []u32 {
    return EmptyRev
}

/// Utf8 is the WHATWG UTF-8 encoding.
export const Utf8: Encoding = Encoding(id: 0, table: NoTable, back: NoBack, run: RunUtf8, put: PutUtf8, codec: NoCodec)

/// Utf16Le is the WHATWG UTF-16LE encoding.
export const Utf16Le: Encoding = Encoding(id: 1, table: NoTable, back: NoBack, run: RunUtf16Le, put: PutUtf16Le, codec: NoCodec)

/// Utf16Be is the WHATWG UTF-16BE encoding.
export const Utf16Be: Encoding = Encoding(id: 2, table: NoTable, back: NoBack, run: RunUtf16Be, put: PutUtf16Be, codec: NoCodec)

/// Replacement is the WHATWG replacement encoding, which the labels of
/// ISO-2022-KR, ISO-2022-CN and the HZ encoding name: any non-empty input
/// decodes to one U+FFFD, and nothing encodes.
export const Replacement: Encoding = Encoding(id: 3, table: NoTable, back: NoBack, run: RunNone, put: PutNone, codec: NoCodec)

/// XUserDefined is the WHATWG x-user-defined encoding: bytes 0x80 to 0xFF
/// are U+F780 to U+F7FF.
export const XUserDefined: Encoding = Encoding(id: 4, table: NoTable, back: NoBack, run: RunUserDefined, put: PutUserDefined, codec: NoCodec)

const IdUtf8: u8 = 0
const IdUtf16Le: u8 = 1
const IdUtf16Be: u8 = 2
const IdReplacement: u8 = 3
const IdUserDefined: u8 = 4
const IdUnsupported: u8 = 255

/// Lookup is the encoding a label names, by the Encoding Standard's get an
/// encoding: ASCII whitespace around the label is removed and ASCII letters
/// match either case. Fails with Unsupported for a label of a multi-byte
/// CJK encoding, which std.encoding.cjk builds and its Lookup returns, and
/// UnknownLabel for any other label the standard does not have. O(log n) in
/// the label table plus the label's length; allocates nothing; names every
/// encoding of this module, so it links every table of this module and none
/// of std.encoding.cjk's.
export Lookup(label: str): UnknownLabel|Unsupported!Encoding {
    b := label.AsBytes()
    var lo := 0
    var hi := b.Len()
    for lo < hi && IsSpace(b[lo]) {
        lo += 1
    }
    for hi > lo && IsSpace(b[hi - 1]) {
        hi -= 1
    }
    if hi - lo > LongestLabel {
        fail UnknownLabel
    }
    var key: [LongestLabel]u8 = [0; LongestLabel]
    for i in lo..hi {
        c := b[i]
        key[i - lo] = if c >= b'A' && c <= b'Z' { c + 32 } else { c }
    }
    k := key[0..hi - lo]
    labels := Labels
    var l := 0
    var r := labels.Len()
    for l < r {
        mid := l + (r - l) / 2
        c := simd.Compare(labels[mid].AsBytes(), k)
        if c < 0 {
            l = mid + 1
        } else if c > 0 {
            r = mid
        } else {
            id := LabelIds[mid]
            if id == IdUnsupported {
                fail Unsupported
            }
            return ById[int(id)]
        }
    }
    fail UnknownLabel
}

/// Name is the encoding's name in the Encoding Standard: "UTF-8",
/// "windows-1252", "ISO-8859-2", "Shift_JIS"; a Codec's name for an
/// encoding Encoding.Of made. O(1); allocates nothing.
export Encoding.Name(self): str {
    if self.id == IdMulti {
        return self.codec.name()
    }
    return Names[int(self.id)]
}

// Bad is the scalar a step gives for a malformed unit: no scalar is so
// large.
const Bad: u32 = 0x110000

// Step is one unit of input: the scalar it decodes (Bad when malformed)
// and its length in bytes. A length of 0 is a unit that the input ends
// inside of, which waits for more input unless the input is the last.
type Step = (c: u32, n: int)

// Utf8Step decodes the unit at b[i], whose lead byte is not ASCII, by the
// standard's UTF-8 decoder: a lead byte opens a sequence whose second byte
// has a narrowed range after E0, ED, F0 and F4; a byte out of range ends
// the malformed unit before it, so it is read again as a lead.
Utf8Step(b: []u8, i: int, last: bool): Step {
    lead := b[i]
    var need := 0
    var lower: u8 = 0x80
    var upper: u8 = 0xBF
    var c: u32 = 0
    if lead >= 0xC2 && lead <= 0xDF {
        need = 1
        c = u32(lead & 0x1F)
    } else if lead >= 0xE0 && lead <= 0xEF {
        need = 2
        c = u32(lead & 0x0F)
        if lead == 0xE0 {
            lower = 0xA0
        } else if lead == 0xED {
            upper = 0x9F
        }
    } else if lead >= 0xF0 && lead <= 0xF4 {
        need = 3
        c = u32(lead & 0x07)
        if lead == 0xF0 {
            lower = 0x90
        } else if lead == 0xF4 {
            upper = 0x8F
        }
    } else {
        return (c: Bad, n: 1)
    }
    n := b.Len()
    var k := 1
    for k <= need {
        if i + k >= n {
            return if last { (c: Bad, n: k) } else { (c: 0, n: 0) }
        }
        x := b[i + k]
        if x < lower || x > upper {
            return (c: Bad, n: k)
        }
        lower = 0x80
        upper = 0xBF
        c = (c << 6) | u32(x & 0x3F)
        k += 1
    }
    return (c, n: need + 1)
}

// Unit16 is the UTF-16 code unit at b[i], in the byte order be says.
Unit16(b: []u8, i: int, be: bool): u32 {
    p := b[i..i + 2]
    return if be { (u32(p[0]) << 8) | u32(p[1]) } else { (u32(p[1]) << 8) | u32(p[0]) }
}

// Utf16Step decodes the unit at b[i] by the standard's shared UTF-16
// decoder: a lead surrogate and a trail make one scalar; a lead not
// followed by a trail is malformed alone and what follows is read again; a
// lone trail is malformed; at the end of the last input, a lead surrogate
// or an odd byte (or both) is one malformed unit.
Utf16Step(b: []u8, i: int, last: bool, be: bool): Step {
    rem := b.Len() - i
    if rem < 2 {
        return if last { (c: Bad, n: rem) } else { (c: 0, n: 0) }
    }
    u := Unit16(b, i, be)
    if u < 0xD800 || u > 0xDFFF {
        return (c: u, n: 2)
    }
    if u >= 0xDC00 {
        return (c: Bad, n: 2)
    }
    if rem < 4 {
        return if last { (c: Bad, n: rem) } else { (c: 0, n: 0) }
    }
    v := Unit16(b, i + 2, be)
    if v < 0xDC00 || v > 0xDFFF {
        return (c: Bad, n: 2)
    }
    return (c: 0x10000 + ((u - 0xD800) << 10) + (v - 0xDC00), n: 4)
}

// Utf8Len is the length of scalar c in UTF-8; Bad is U+FFFD's three bytes.
Utf8Len(c: u32): int {
    if c < 0x80 {
        return 1
    }
    if c < 0x800 {
        return 2
    }
    if c < 0x10000 || c == Bad {
        return 3
    }
    return 4
}

// Put writes scalar c (U+FFFD for Bad) as UTF-8 at out[o] and returns the
// offset after it.
Put(out: mut []u8, o: int, c: u32): int {
    if c < 0x80 {
        out[o] = Trunc[u8](c)
        return o + 1
    }
    if c < 0x800 {
        var d := mut out[o..o + 2]
        d[0] = Trunc[u8](0xC0 | (c >> 6))
        d[1] = Trunc[u8](0x80 | (c & 0x3F))
        return o + 2
    }
    if c == Bad {
        var d := mut out[o..o + 3]
        d[0] = 0xEF
        d[1] = 0xBF
        d[2] = 0xBD
        return o + 3
    }
    if c < 0x10000 {
        var d := mut out[o..o + 3]
        d[0] = Trunc[u8](0xE0 | (c >> 12))
        d[1] = Trunc[u8](0x80 | ((c >> 6) & 0x3F))
        d[2] = Trunc[u8](0x80 | (c & 0x3F))
        return o + 3
    }
    var d := mut out[o..o + 4]
    d[0] = Trunc[u8](0xF0 | (c >> 18))
    d[1] = Trunc[u8](0x80 | ((c >> 12) & 0x3F))
    d[2] = Trunc[u8](0x80 | ((c >> 6) & 0x3F))
    d[3] = Trunc[u8](0x80 | (c & 0x3F))
    return o + 4
}

// Ran is what one pass of a decoder did: bytes read, bytes of UTF-8 written
// (or, counting, that would be written), and the offset of the first
// malformed unit, -1 when there was none.
type Ran = (read: int, wrote: int, bad: int)

// Run is one pass of e's decoder over the units of b that start before
// stop. With write it writes UTF-8 to out, which holds what a counting pass
// returned; without, it only counts. A unit that b ends inside of stops it
// unread unless last; with strict it stops at the first malformed unit.
// It starts in decoder state state and returns the state after the pass,
// which only a multi-byte encoding's decoder changes. The replacement
// encoding is the caller's. It calls through e's run field or codec, so a
// program links the decoders of the encodings it names only.
Run(e: Encoding, b: []u8, stop: int, last: bool, state: u8, out: mut []u8, write: bool, strict: bool): Pass {
    if e.id == IdMulti {
        return MultiRun(e.codec, b, stop, last, state, out, write, strict)
    }
    r := e.run(e.table(), b, stop, last, out, write, strict)
    return (read: r.read, wrote: r.wrote, bad: r.bad, state)
}

// RunUtf8, RunUtf16Le, RunUtf16Be, RunSingle, RunUserDefined and RunNone
// are the run fields of the constants.
RunUtf8(t: []u16, b: []u8, stop: int, last: bool, out: mut []u8, write: bool, strict: bool): Ran {
    return Utf8Run(b, stop, last, out, write, strict)
}

RunUtf16Le(t: []u16, b: []u8, stop: int, last: bool, out: mut []u8, write: bool, strict: bool): Ran {
    return Utf16Run(b, stop, last, false, out, write, strict)
}

RunUtf16Be(t: []u16, b: []u8, stop: int, last: bool, out: mut []u8, write: bool, strict: bool): Ran {
    return Utf16Run(b, stop, last, true, out, write, strict)
}

RunSingle(t: []u16, b: []u8, stop: int, last: bool, out: mut []u8, write: bool, strict: bool): Ran {
    return SingleRun(t, b, stop, out, write, strict)
}

// RunUserDefined is x-user-defined: one byte is one unit, 0x80 to 0xFF
// onto U+F780 to U+F7FF.
RunUserDefined(t: []u16, b: []u8, stop: int, last: bool, out: mut []u8, write: bool, strict: bool): Ran {
    var o := 0
    for x in b[0..stop] {
        c := if x >= 0x80 { 0xF700 + u32(x) } else { u32(x) }
        o = if write { Put(out, o, c) } else { o + Utf8Len(c) }
    }
    return (read: stop, wrote: o, bad: -1)
}

// RunNone is the replacement encoding's run field, never called: Push and
// DecodeStrict answer for it before they run a decoder.
RunNone(t: []u16, b: []u8, stop: int, last: bool, out: mut []u8, write: bool, strict: bool): Ran {
    return (read: stop, wrote: 0, bad: -1)
}

// IsUtf16 is whether e is UTF-16LE or UTF-16BE, whose units are not bytes.
IsUtf16(e: Encoding): bool {
    return e.id == IdUtf16Le || e.id == IdUtf16Be
}

// Bytewise is whether a pass of e's decoder that wrote as many bytes as it
// read wrote those bytes: true of UTF-8, x-user-defined and the
// single-byte encodings, where a byte that is not ASCII is a unit of its
// own and becomes two or three bytes, and false of UTF-16 and of a
// multi-byte encoding, whose two bytes can be another scalar's two.
Bytewise(e: Encoding): bool {
    return !IsUtf16(e) && e.id != IdMulti
}

const BomUtf8: []u8 = [0xEF, 0xBB, 0xBF]
const BomLe: []u8 = [0xFF, 0xFE]
const BomBe: []u8 = [0xFE, 0xFF]
const NoBom: []u8 = []

// Bom is the byte order mark a decoder of e removes from the front of its
// input: UTF-8's and UTF-16's own, and none for the other encodings.
Bom(e: Encoding): []u8 {
    return if e.id == IdUtf8 { BomUtf8 } else if e.id == IdUtf16Le { BomLe } else if e.id == IdUtf16Be { BomBe } else { NoBom }
}

/// Decoder decodes one stream in chunks: the concatenation of what Push
/// returns is what Decode returns for the concatenation of the chunks,
/// however they are split. It holds at most three bytes of an unfinished
/// unit between calls, and a multi-byte decoder's state (ISO-2022-JP's
/// mode), and starts a new stream after a Push with last.
export struct Decoder {
    enc: Encoding
    pend: [4]u8
    npend: int
    started: bool
    sent: bool
    state: u8
}

/// Decoder is a decoder of a new stream in this encoding. O(1); allocates
/// nothing.
export Encoding.Decoder(self): Decoder {
    return Decoder(enc: self, pend: [0; 4], npend: 0, started: false, sent: false, state: 0)
}

/// Decode is bytes decoded to text, each malformed unit replaced by
/// U+FFFD, and a leading byte order mark of this encoding removed. O(n);
/// allocates the text's length in a, at most 3 bytes for each byte of input.
export Encoding.Decode(self, a: &Arena, bytes: []u8): OutOfMemory!str from a {
    if self.id == IdUtf8 && simd.ValidUtf8(bytes) {
        // Valid UTF-8: one check and one copy, whatever its length.
        b := if bytes.Len() >= 3 && simd.Equal(bytes[0..3], BomUtf8) { bytes[3..] } else { bytes }
        out := try a.Slice[u8](b.Len())
        out.CopyFrom(b)
        // Must: out is a copy of bytes that simd.ValidUtf8 accepted.
        return rt.StrFrom(out[..]).Must()
    }
    var d := self.Decoder()
    return try d.Push(a, bytes, true)
}

/// DecodeKeepBom is Decode with a leading byte order mark kept as U+FEFF,
/// as a TextDecoder with ignoreBOM true does; for Utf8 it is the Encoding
/// Standard's "UTF-8 decode without BOM", which the URL Standard asks of a
/// form body. O(n); allocates the text's length in a, at most 3 bytes for
/// each byte of input.
export Encoding.DecodeKeepBom(self, a: &Arena, bytes: []u8): OutOfMemory!str from a {
    if self.id == IdUtf8 && simd.ValidUtf8(bytes) {
        out := try a.Slice[u8](bytes.Len())
        out.CopyFrom(bytes)
        // Must: out is a copy of bytes that simd.ValidUtf8 accepted.
        return rt.StrFrom(out[..]).Must()
    }
    var d := self.Decoder()
    // A started decoder reads a leading mark as text.
    d.started = true
    return try d.Push(a, bytes, true)
}

/// DecodeStrict is bytes decoded to text, a leading byte order mark of this
/// encoding removed. Fails with BadInput at the first malformed unit: the
/// start of a UTF-8 sequence, a UTF-16 unit or a byte the encoding leaves
/// out, or of a unit the input ends inside of; any input to Replacement is
/// BadInput at 0. O(n); allocates the text's length in a, nothing on error.
export Encoding.DecodeStrict(self, a: &Arena, bytes: []u8): BadInput|OutOfMemory!str from a {
    if self.id == IdReplacement {
        if bytes.Len() > 0 {
            fail BadInput(offset: 0)
        }
        return ""
    }
    bom := Bom(self)
    skip := if bom.Len() > 0 && bytes.Len() >= bom.Len() && simd.Equal(bytes[0..bom.Len()], bom) { bom.Len() } else { 0 }
    b := bytes[skip..]
    var none0: [1]u8 = [0; 1]
    count := Run(self, b, b.Len(), true, 0, mut none0[0..0], false, true)
    if count.bad >= 0 {
        fail BadInput(offset: skip + count.bad)
    }
    out := try a.Slice[u8](count.wrote)
    if Bytewise(self) && count.wrote == b.Len() {
        // Every unit was one byte or a well-formed UTF-8 sequence written
        // as itself.
        out.CopyFrom(b)
    } else {
        _ = Run(self, b, b.Len(), true, 0, mut out[..], true, true)
    }
    // Must: Run writes only scalars as UTF-8.
    return rt.StrFrom(out[..]).Must()
}

/// Push decodes the next chunk of the stream and returns the text it
/// completes, as Decode does; with last, a unit the stream ends inside of
/// is U+FFFD, and the decoder then starts a new stream. O(n) in the chunk;
/// allocates the returned text's length in a.
export Decoder.Push(mut self, a: &Arena, chunk: []u8, last: bool): OutOfMemory!str from a {
    e := self.enc
    if e.id == IdReplacement {
        // The standard's replacement decoder: one U+FFFD for a stream that
        // holds any byte, at its first byte.
        first := !self.sent && chunk.Len() > 0
        self.sent = if last { false } else { self.sent || first }
        return if first { "\u{FFFD}" } else { "" }
    }
    var c := chunk
    if !self.started {
        bom := Bom(e)
        have := self.npend + c.Len()
        var matches := true
        var j := 0
        for j < bom.Len() && j < have {
            x := if j < self.npend { self.pend[j] } else { c[j - self.npend] }
            if x != bom[j] {
                matches = false
            }
            j += 1
        }
        if bom.Len() > 0 && matches && have < bom.Len() && !last {
            // Too few bytes yet to tell a mark from text: keep them.
            for x in c {
                self.pend[self.npend] = x
                self.npend += 1
            }
            return ""
        }
        self.started = true
        if bom.Len() > 0 && matches && have >= bom.Len() {
            // The pending bytes are all of the mark's front.
            c = c[bom.Len() - self.npend..]
            self.npend = 0
        }
    }
    // The units that start in the pending bytes are read from the pending
    // bytes and the chunk's first bytes; a unit is at most four bytes, so
    // eight hold them all unless the chunk ends sooner.
    k := self.npend
    var head: [8]u8 = [0; 8]
    for i in 0..k {
        head[i] = self.pend[i]
    }
    take := if c.Len() < 8 - k { c.Len() } else { 8 - k }
    for i in 0..take {
        head[k + i] = c[i]
    }
    whole := take == c.Len()
    h := head[0..k + take]
    var none0: [1]u8 = [0; 1]
    // Each part is counted and then written from the same decoder state;
    // the state after the chunk is kept for the next.
    s0 := self.state
    var h1 := (read: 0, wrote: 0, bad: -1, state: s0)
    if k > 0 {
        h1 = Run(e, h, k, last && whole, s0, mut none0[0..0], false, false)
    }
    var rest := c[0..0]
    var r1 := (read: 0, wrote: 0, bad: -1, state: h1.state)
    if h1.read >= k {
        rest = c[h1.read - k..]
        r1 = Run(e, rest, rest.Len(), last, h1.state, mut none0[0..0], false, false)
    }
    out := try a.Slice[u8](h1.wrote + r1.wrote)
    if k > 0 {
        _ = Run(e, h, k, last && whole, s0, mut out[0..h1.wrote], true, false)
    }
    if h1.read >= k {
        if Bytewise(e) && r1.bad < 0 && r1.wrote == r1.read {
            // Every unit was one byte or a well-formed UTF-8 sequence
            // written as itself: the text is the bytes.
            (mut out[h1.wrote..]).CopyFrom(rest[0..r1.read])
        } else {
            _ = Run(e, rest, rest.Len(), last, h1.state, mut out[h1.wrote..], true, false)
        }
    }
    self.state = if last { 0 } else { r1.state }
    // What is left unread is an unfinished unit of at most three bytes.
    left := if h1.read < k { h[h1.read..] } else { rest[r1.read..] }
    self.npend = 0
    for x in left {
        self.pend[self.npend] = x
        self.npend += 1
    }
    if last {
        self.started = false
        self.sent = false
    }
    // Must: Run writes only scalars as UTF-8.
    return rt.StrFrom(out[..]).Must()
}

// ScalarAt is the scalar of valid UTF-8 b at i and its length.
ScalarAt(b: []u8, i: int): Step {
    lead := u32(b[i])
    if lead < 0x80 {
        return (c: lead, n: 1)
    }
    if lead < 0xE0 {
        return (c: ((lead & 0x1F) << 6) | (u32(b[i + 1]) & 0x3F), n: 2)
    }
    if lead < 0xF0 {
        return (c: ((lead & 0x0F) << 12) | ((u32(b[i + 1]) & 0x3F) << 6) | (u32(b[i + 2]) & 0x3F), n: 3)
    }
    return (c: ((lead & 0x07) << 18) | ((u32(b[i + 1]) & 0x3F) << 12) | ((u32(b[i + 2]) & 0x3F) << 6) | (u32(b[i + 3]) & 0x3F), n: 4)
}

// ByteIn is the byte a single-byte encoding writes for scalar c (U+0080
// or above), by binary search of its reversed index rev (code point << 8 |
// byte, sorted); none when the encoding has no byte for c.
ByteIn(rev: []u32, c: u32): ?u8 {
    var l := 0
    var r := rev.Len()
    for l < r {
        mid := l + (r - l) / 2
        k := rev[mid] >> 8
        if k < c {
            l = mid + 1
        } else if k > c {
            r = mid
        } else {
            return some(Trunc[u8](rev[mid]))
        }
    }
    return none
}

// PutUtf8, PutUtf16Le, PutUtf16Be, PutSingle, PutUserDefined and PutNone
// are the put fields of the constants (Encoding's put says what one does).
PutUtf8(rev: []u32, b: []u8, out: mut []u8, write: bool): int {
    if write {
        out.CopyFrom(b)
    }
    return b.Len()
}

PutUtf16Le(rev: []u32, b: []u8, out: mut []u8, write: bool): int {
    if !write {
        return 2 * Utf16Units(b)
    }
    Utf16Write(b, out, false)
    return out.Len()
}

PutUtf16Be(rev: []u32, b: []u8, out: mut []u8, write: bool): int {
    if !write {
        return 2 * Utf16Units(b)
    }
    Utf16Write(b, out, true)
    return out.Len()
}

// PutSingle is a legacy single-byte encoding's: one byte a scalar, ASCII
// as itself and the rest through rev.
PutSingle(rev: []u32, b: []u8, out: mut []u8, write: bool): int {
    n := b.Len()
    var i := 0
    var o := 0
    for i < n {
        sc := ScalarAt(b, i)
        var x := Trunc[u8](sc.c)
        if sc.c >= 0x80 {
            x = ByteIn(rev, sc.c) else {
                return -1 - i
            }
        }
        if write {
            out[o] = x
        }
        o += 1
        i += sc.n
    }
    return o
}

// PutUserDefined is x-user-defined's: ASCII as itself, U+F780 to U+F7FF
// onto 0x80 to 0xFF.
PutUserDefined(rev: []u32, b: []u8, out: mut []u8, write: bool): int {
    n := b.Len()
    var i := 0
    var o := 0
    for i < n {
        sc := ScalarAt(b, i)
        if sc.c >= 0x80 && (sc.c < 0xF780 || sc.c > 0xF7FF) {
            return -1 - i
        }
        if write {
            out[o] = Trunc[u8](if sc.c >= 0x80 { sc.c - 0xF700 } else { sc.c })
        }
        o += 1
        i += sc.n
    }
    return o
}

// PutNone is the replacement encoding's put field, never called: Encode
// fails with Unsupported for it first.
PutNone(rev: []u32, b: []u8, out: mut []u8, write: bool): int {
    return 0
}

/// Encode is s in this encoding, with no byte order mark. Fails with
/// BadInput at the first scalar this encoding has no bytes for (the
/// legacy single-byte and multi-byte encodings and x-user-defined), and
/// with Unsupported for Replacement, which encodes nothing. O(n), and
/// O(n log 128) for a single-byte encoding and O(n log m) for one whose
/// index has m entries (std.encoding.cjk's); allocates the result's length
/// in a, nothing on error.
export Encoding.Encode(self, a: &Arena, s: str): BadInput|Unsupported|OutOfMemory![]u8 from a {
    if self.id == IdReplacement {
        fail Unsupported
    }
    b := s.AsBytes()
    var none0: [1]u8 = [0; 1]
    if self.id == IdMulti {
        m := MultiPut(self.codec, b, mut none0[0..0], false)
        if m < 0 {
            fail BadInput(offset: -1 - m)
        }
        mout := try a.Slice[u8](m)
        _ = MultiPut(self.codec, b, mut mout[..], true)
        return mout[..]
    }
    rev := self.back()
    n := self.put(rev, b, mut none0[0..0], false)
    if n < 0 {
        fail BadInput(offset: -1 - n)
    }
    out := try a.Slice[u8](n)
    _ = self.put(rev, b, mut out[..], true)
    return out[..]
}

// PutUnit writes UTF-16 code unit u at out[o] in the byte order be says.
PutUnit(out: mut []u8, o: int, u: u32, be: bool) {
    var d := mut out[o..o + 2]
    if be {
        d[0] = Trunc[u8](u >> 8)
        d[1] = Trunc[u8](u)
    } else {
        d[0] = Trunc[u8](u)
        d[1] = Trunc[u8](u >> 8)
    }
}
