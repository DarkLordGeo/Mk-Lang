// Why: every URL component is percent-encoded against its own set of code
// points, and host parsing refuses forbidden ones; a per-byte table makes
// each test one load.
//
// What: the URL Standard's code point classes over bytes: the percent-encode
// sets of its section 1.3 (C0 control, fragment, query, special-query, path,
// userinfo) and the forbidden host and forbidden domain code points of
// section 3.2, as one byte of flags per ASCII byte (Class), with Encode,
// LowerAscii and the byte helpers.
//
// How: the table is computed at compile time. Every byte above 0x7F is in
// every percent-encode set and in neither forbidden set. Percent-encoding
// of a component is two passes, one to size it and one to write it, and
// none at all when no byte needs encoding: the component is then a view of
// its input.

const InC0: u8 = 0x01
const InFragment: u8 = 0x02
const InQuery: u8 = 0x04
const InSpecialQuery: u8 = 0x08
const InPath: u8 = 0x10
const InUserinfo: u8 = 0x20
const ForbiddenHost: u8 = 0x40
const ForbiddenDomain: u8 = 0x80

// MarkAll adds flags to each byte of chars.
MarkAll(t: mut [128]u8, chars: str, flags: u8) {
    for c in chars.AsBytes() {
        t[int(c)] = t[int(c)] | flags
    }
}

// ClassTable is the flags of each ASCII byte. Each percent-encode set is
// built on the one before it, as the standard defines them: C0 control,
// then fragment; C0 control, then query, then special-query; query, then
// path, then userinfo.
ClassTable(): [128]u8 {
    var t: [128]u8 = [0; 128]
    sets := InC0 | InFragment | InQuery | InSpecialQuery | InPath | InUserinfo
    for i in 0..0x20 {
        t[i] = sets | ForbiddenDomain
    }
    t[0x7F] = sets | ForbiddenDomain
    MarkAll(mut t, " \"<>`", InFragment)
    MarkAll(mut t, " \"#<>", InQuery | InSpecialQuery | InPath | InUserinfo)
    MarkAll(mut t, "'", InSpecialQuery)
    MarkAll(mut t, "?^`{}", InPath | InUserinfo)
    MarkAll(mut t, "/:;=@[\\]|", InUserinfo)
    // NUL, TAB, LF and CR are C0 controls, already forbidden in a domain.
    MarkAll(mut t, "\0\t\n\r #/:<>?@[\\]^|", ForbiddenHost | ForbiddenDomain)
    MarkAll(mut t, "%", ForbiddenDomain)
    return t
}

const Class: [128]u8 = ClassTable()

const UpperHex: str = "0123456789ABCDEF"

// Encodes is whether byte c is in the percent-encode set whose flag is bit.
Encodes(c: u8, bit: u8): bool {
    return c >= 0x80 || Class[int(c)] & bit != 0
}

// IsClass is whether c is an ASCII byte with flag bit.
IsClass(c: u8, bit: u8): bool {
    return c < 0x80 && Class[int(c)] & bit != 0
}

// IsAlpha is whether c is an ASCII letter.
IsAlpha(c: u8): bool {
    return (c | 0x20) >= b'a' && (c | 0x20) <= b'z'
}

// IsDigit is whether c is an ASCII digit.
IsDigit(c: u8): bool {
    return c >= b'0' && c <= b'9'
}

// HexValue is the value of ASCII hex digit c, or 16 for any other byte.
HexValue(c: u8): u8 {
    if IsDigit(c) {
        return c - b'0'
    }
    l := c | 0x20
    if l >= b'a' && l <= b'f' {
        return l - b'a' + 10
    }
    return 16
}

// Lower is c lower-cased when it is an ASCII capital.
Lower(c: u8): u8 {
    if c >= b'A' && c <= b'Z' {
        return c | 0x20
    }
    return c
}

// Encode is s with every byte in the set whose flag is bit written as '%'
// and two upper-case hex digits: s itself when no byte is in the set, else
// a copy in a. O(n); allocates at most 3n bytes in a, nothing when no byte
// is in the set.
Encode(a: &Arena, s: []u8, bit: u8): OutOfMemory![]u8 from a, s {
    var n := 0
    for c in s {
        if Encodes(c, bit) {
            n += 1
        }
    }
    if n == 0 {
        return s
    }
    out := try a.Slice[u8](s.Len() + 2 * n)
    digits := UpperHex.AsBytes()
    var o := 0
    for c in s {
        if Encodes(c, bit) {
            out[o] = b'%'
            out[o + 1] = digits[int(c >> 4)]
            out[o + 2] = digits[int(c & 15)]
            o += 3
        } else {
            out[o] = c
            o += 1
        }
    }
    return out[..]
}

// LowerAscii is s with every ASCII capital lower-cased: s itself when it
// has none, else a copy in a. O(n); allocates n bytes in a or nothing.
LowerAscii(a: &Arena, s: []u8): OutOfMemory![]u8 from a, s {
    var upper := false
    for c in s {
        if c >= b'A' && c <= b'Z' {
            upper = true
        }
    }
    if !upper {
        return s
    }
    out := try a.Slice[u8](s.Len())
    for i, c in s {
        out[i] = Lower(c)
    }
    return out[..]
}

// Same is whether a and b hold the same bytes.
Same(a: []u8, b: []u8): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}

// SameText is whether bytes a spell text b.
SameText(a: []u8, b: str): bool {
    return Same(a, b.AsBytes())
}
