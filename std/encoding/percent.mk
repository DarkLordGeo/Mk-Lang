// Why: URLs and form bodies escape bytes as %XX, with a different set of
// escaped bytes for each part of a URL.
//
// What: PercentSet (the WHATWG URL Standard's percent-encode sets) and
// PercentSet.Has, PercentEncode and PercentDecode.
//
// How: membership is one byte of flags per ASCII byte, a bit per set,
// computed at compile time; every byte above 0x7E is in every set.
// Encoding and decoding each take two passes, one to size the result
// exactly and one to write it, so each makes one allocation.

import std.rt

/// PercentSet is one of the URL Standard's percent-encode sets. Each holds the
/// C0 controls and every byte above 0x7E. Query holds C0Control; Path,
/// Userinfo, Component and FormUrlencoded each hold the set before it in
/// that list, starting from Query; Fragment adds to C0Control alone and
/// SpecialQuery to Query alone.
export enum PercentSet {
    C0Control
    Fragment
    Query
    SpecialQuery
    Path
    Userinfo
    Component
    FormUrlencoded
}

// SetBit is the flag of set in SetFlags.
SetBit(set: PercentSet): u8 {
    return match set {
        .C0Control { 0x01 }
        .Fragment { 0x02 }
        .Query { 0x04 }
        .SpecialQuery { 0x08 }
        .Path { 0x10 }
        .Userinfo { 0x20 }
        .Component { 0x40 }
        .FormUrlencoded { 0x80 }
    }
}

// Mark adds the sets of flags to each byte of chars.
Mark(t: mut [128]u8, chars: str, flags: u8) {
    for c in chars.AsBytes() {
        t[int(c)] = t[int(c)] | flags
    }
}

// FlagTable is the sets each ASCII byte belongs to, as the URL Standard
// builds them: the C0 controls and DEL, then each set's additions, marked
// in that set and in every set built on it.
FlagTable(): [128]u8 {
    var t: [128]u8 = [0; 128]
    for i in 0..0x20 {
        t[i] = 0xFF
    }
    t[0x7F] = 0xFF
    Mark(mut t, " \"<>`", 0x02)
    Mark(mut t, " \"#<>", 0xFC)
    Mark(mut t, "'", 0x08)
    Mark(mut t, "?^`{}", 0xF0)
    Mark(mut t, "/:;=@[\\]|", 0xE0)
    Mark(mut t, "$%&+,", 0xC0)
    Mark(mut t, "!'()~", 0x80)
    return t
}

const SetFlags: [128]u8 = FlagTable()

const UpperHex: str = "0123456789ABCDEF"

// InSet is whether byte c is in the set whose flag is bit.
InSet(c: u8, bit: u8, flags: &[128]u8): bool {
    return c >= 0x80 || flags[int(c)] & bit != 0
}

/// Has is whether PercentEncode with this set writes b as `%` and two hex
/// digits: the URL Standard's "in the percent-encode set". Every byte
/// below 0x20 or above 0x7E is in every set. FormUrlencoded holds the
/// space, which PercentEncode writes as `+`. O(1); allocates nothing.
export PercentSet.Has(self, b: u8): bool {
    return InSet(b, SetBit(self), &SetFlags)
}

/// PercentEncode is s with every byte in set written as `%` and two
/// upper-case hex digits, in a. FormUrlencoded writes a space as `+`, as
/// the URL Standard's application/x-www-form-urlencoded serializer does.
/// O(n); allocates the result's length in a, at most 3 * s.Len() bytes.
export PercentEncode(a: &Arena, s: []u8, set: PercentSet): OutOfMemory!str from a {
    bit := SetBit(set)
    flags := &SetFlags
    plus := bit == 0x80
    var n := s.Len()
    for c in s {
        if InSet(c, bit, flags) && !(plus && c == 0x20) {
            n += 2
        }
    }
    out := try a.Slice[u8](n)
    digits := UpperHex.AsBytes()
    var o := 0
    for c in s {
        if !InSet(c, bit, flags) {
            out[o] = c
            o += 1
        } else if plus && c == 0x20 {
            out[o] = b'+'
            o += 1
        } else {
            var d := mut out[o..o + 3]
            d[0] = b'%'
            d[1] = digits[int(c >> 4)]
            d[2] = digits[int(c & 15)]
            o += 3
        }
    }
    // Must: every byte written is ASCII.
    return rt.StrFrom(out[..]).Must()
}

// Escape is the byte the escape at b[i] spells when b[i] is `%` and two
// hex digits follow it.
Escape(b: []u8, i: int, dec: &[256]u8): ?u8 {
    if b[i] != b'%' || i + 2 >= b.Len() {
        return none
    }
    hi := dec[int(b[i + 1])]
    lo := dec[int(b[i + 2])]
    if (hi | lo) >= 0x80 {
        return none
    }
    return some((hi << 4) | lo)
}

/// PercentDecode is s with every `%` that two hex digits follow replaced by
/// the byte they spell, in a; any other `%` stays as it is, and `+` is not
/// a space (the URL Standard's percent-decode). O(n); allocates the
/// result's length in a.
export PercentDecode(a: &Arena, s: str): OutOfMemory![]u8 from a {
    b := s.AsBytes()
    dec := &HexDec
    var n := b.Len()
    for i in 0..b.Len() {
        if Escape(b, i, dec).IsSome() {
            n -= 2
        }
    }
    out := try a.Slice[u8](n)
    var i := 0
    var o := 0
    for i < b.Len() {
        if x := Escape(b, i, dec) {
            out[o] = x
            i += 3
        } else {
            out[o] = b[i]
            i += 1
        }
        o += 1
    }
    return out[..]
}
