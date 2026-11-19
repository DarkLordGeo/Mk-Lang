// Why: hosts are the most security-sensitive part of a URL, and the URL
// Standard defines exactly how each kind (domain, IPv4, IPv6, opaque) is
// parsed and written back.
//
// What: the URL Standard's host parsing of its section 3.5 (ParseHost, the
// ends-in-a-number checker, the IPv4 and IPv4-number parsers, ParseIpv6 and
// ParseOpaqueHost) and host serializing of section 3.6 (IpText,
// HostBytes), over HostValue.
//
// How: step numbers in comments are the standard's. The parsers are single
// passes over the host's bytes, with no recursion and no allocation but the
// percent-decoded domain and an opaque host's encoding. Numbers saturate at
// 2^32, which every check below already refuses, so no input overflows.

// HostValue is a host as the parser builds it: Host over byte views, and
// an IPv6 address as its eight pieces.
enum HostValue {
    Domain(name: []u8)
    Ipv4(addr: u32)
    Ipv6(pieces: [8]u16)
    Opaque(text: []u8)
    Empty
}

// ParseHost is the host parser on input, a host's bytes as the URL holds
// them; opaque is the parser's isOpaque, true for a non-special URL. Fails
// with BadHost where the standard returns failure, and with IdnaUnsupported
// for a domain IDNA would process in a build without it. O(n); allocates at
// most 3n bytes in a.
ParseHost(a: &Arena, input: []u8, opaque: bool): Error|OutOfMemory!HostValue from a, input {
    n := input.Len()
    // Step 1: an IPv6 address in brackets.
    if n > 0 && input[0] == b'[' {
        if n < 2 || input[n - 1] != b']' {
            fail BadHost
        }
        pieces := ParseIpv6(input[1..n - 1]) else {
            fail BadHost
        }
        return HostValue.Ipv6(pieces: pieces)
    }
    // Step 2: a non-special URL's host is opaque; an empty one is the
    // empty host.
    if opaque && n == 0 {
        return HostValue.Empty
    }
    if opaque {
        return HostValue.Opaque(text: try ParseOpaqueHost(a, input))
    }
    // Steps 4 to 7. A host with no '%', no capital, no byte above 0x7F and
    // no forbidden domain code point is its own percent-decoding and its
    // own domain: one pass over a table decides it, and the passes of the
    // general case below are skipped.
    var ascii: []u8 = input
    if IsPlainDomain(input) {
        if !IdnaAsciiAllowed(input) {
            fail IdnaUnsupported
        }
    } else {
        // Steps 4 and 5: the domain is the percent-decoding of input.
        domain := try PercentDecodeBytes(a, input)
        // Steps 6 and 7: the domain parser with beStrict false.
        ascii = try ParseDomain(a, domain)
    }
    // Step 8: a domain ending in a number is an IPv4 address or nothing.
    if EndsInNumber(ascii) {
        addr := ParseIpv4(ascii) else {
            fail BadHost
        }
        return HostValue.Ipv4(addr: addr)
    }
    // Step 9.
    return HostValue.Domain(name: ascii)
}

// IsPlainDomain is whether input is non-empty and holds no '%', no ASCII
// capital, no byte above 0x7F and no forbidden domain code point, so the
// domain parser with beStrict false returns it as it is. O(n); allocates
// nothing.
IsPlainDomain(input: []u8): bool {
    var bad: u8 = 0
    for c in input {
        bad |= NotPlain[int(c)]
    }
    return bad == 0 && input.Len() > 0
}

// NotPlainTable is 1 for each byte IsPlainDomain refuses.
NotPlainTable(): [256]u8 {
    var t: [256]u8 = [0; 256]
    for i in 0..256 {
        c := Trunc[u8](i)
        if c >= 0x80 || c >= b'A' && c <= b'Z' || IsClass(c, ForbiddenDomain) {
            t[i] = 1
        }
    }
    return t
}

const NotPlain: [256]u8 = NotPlainTable()

// ParseDomain is the URL Standard's domain parser with beStrict false
// (section 3.3) on a percent-decoded host: an ASCII domain lower-cased, as
// web compatibility asks, and anything else through UTS 46's ToASCII
// (idna.mk). Fails with BadHost for bytes that are not UTF-8 (the decode
// gives U+FFFD, which UTS 46 disallows), an empty result, or a forbidden
// domain code point. O(n); allocates at most n bytes in a for an ASCII
// domain.
ParseDomain(a: &Arena, domain: []u8): Error|OutOfMemory![]u8 from a, domain {
    var ascii := true
    for c in domain {
        if c >= 0x80 {
            ascii = false
        }
    }
    var result: []u8 = domain
    if ascii {
        if !IdnaAsciiAllowed(domain) {
            fail IdnaUnsupported
        }
        result = try LowerAscii(a, domain)
    } else {
        text := str.From(domain) else {
            fail BadHost
        }
        result = try IdnaToAscii(a, text)
    }
    if result.Len() == 0 {
        fail BadHost
    }
    for c in result {
        if IsClass(c, ForbiddenDomain) {
            fail BadHost
        }
    }
    return result
}

// PercentDecodeBytes is the URL Standard's percent-decode of s: s itself
// when it holds no '%', else a copy in a. O(n); allocates at most n bytes.
PercentDecodeBytes(a: &Arena, s: []u8): OutOfMemory![]u8 from a, s {
    var escapes := 0
    for i in 0..s.Len() {
        if IsEscape(s, i) {
            escapes += 1
        }
    }
    if escapes == 0 {
        return s
    }
    out := try a.Slice[u8](s.Len() - 2 * escapes)
    var i := 0
    var o := 0
    for i < s.Len() {
        if IsEscape(s, i) {
            out[o] = (HexValue(s[i + 1]) << 4) | HexValue(s[i + 2])
            i += 3
        } else {
            out[o] = s[i]
            i += 1
        }
        o += 1
    }
    return out[..]
}

// IsEscape is whether s[i] starts a percent-encoded byte: '%' and two ASCII
// hex digits.
IsEscape(s: []u8, i: int): bool {
    return s[i] == b'%' && i + 2 < s.Len() && HexValue(s[i + 1]) < 16 && HexValue(s[i + 2]) < 16
}

// ParseOpaqueHost is the opaque-host parser: BadHost for a forbidden host
// code point, else input encoded with the C0 control set. O(n); allocates at
// most 3n bytes in a.
ParseOpaqueHost(a: &Arena, input: []u8): Error|OutOfMemory![]u8 from a, input {
    // Step 1.
    for c in input {
        if IsClass(c, ForbiddenHost) {
            fail BadHost
        }
    }
    // Step 4; steps 2 and 3 are validation errors only.
    return try Encode(a, input, InC0)
}

// EndsInNumber is the ends-in-a-number checker on a non-empty ASCII
// domain: whether its last label (a trailing empty label dropped) is ASCII
// digits, or "0x" or "0X" and hex digits.
EndsInNumber(s: []u8): bool {
    // Steps 2 and 3: drop one trailing empty label.
    var end := s.Len()
    if end > 0 && s[end - 1] == b'.' {
        end -= 1
    }
    // A last label of digits, or "0x" and hex digits, ends in a hex digit
    // or 'x': any other last byte ends no number.
    if end == 0 || HexValue(s[end - 1]) >= 16 && s[end - 1] | 0x20 != b'x' {
        return false
    }
    // Step 4: the last label.
    var start := end
    for start > 0 && s[start - 1] != b'.' {
        start -= 1
    }
    last := s[start..end]
    if last.Len() == 0 {
        return false
    }
    // Step 5.
    var digits := true
    for c in last {
        if !IsDigit(c) {
            digits = false
        }
    }
    if digits {
        return true
    }
    // Step 6: an IPv4 number that is not all digits is "0x" and hex digits.
    if last.Len() < 2 || last[0] != b'0' || last[1] | 0x20 != b'x' {
        return false
    }
    for c in last[2..] {
        if HexValue(c) >= 16 {
            return false
        }
    }
    return true
}

// Ipv4Cap is where an IPv4 number saturates: 2^32, past every value the
// parser accepts.
const Ipv4Cap: u64 = 0x1_0000_0000

// ParseIpv4Number is the IPv4 number parser's number, saturated at
// Ipv4Cap, or none for failure (the validation flag is not kept).
ParseIpv4Number(part: []u8): ?u64 {
    // Step 1.
    if part.Len() == 0 {
        return none
    }
    // Steps 3 to 5: the radix from the prefix.
    var digits := part
    var radix: u64 = 10
    if part.Len() >= 2 && part[0] == b'0' && part[1] | 0x20 == b'x' {
        digits = part[2..]
        radix = 16
    } else if part.Len() >= 2 && part[0] == b'0' {
        digits = part[1..]
        radix = 8
    }
    // Steps 6 to 8.
    var v: u64 = 0
    for c in digits {
        d := u64(HexValue(c))
        if d >= radix {
            return none
        }
        if v < Ipv4Cap {
            v = v * radix + d
        }
        if v > Ipv4Cap {
            v = Ipv4Cap
        }
    }
    return some(v)
}

// ParseIpv4 is the IPv4 parser on an ASCII domain that ends in a number:
// the address, or none for failure.
ParseIpv4(s: []u8): ?u32 {
    // Steps 1 and 2: parts split on '.', one trailing empty part dropped.
    var end := s.Len()
    if end > 0 && s[end - 1] == b'.' {
        end -= 1
    }
    text := s[0..end]
    var count := 1
    for c in text {
        if c == b'.' {
            count += 1
        }
    }
    // Step 4.
    if count > 4 {
        return none
    }
    // Steps 5 to 7: every part an IPv4 number, all but the last below 256.
    var ipv4: u64 = 0
    var index := 0
    var start := 0
    for k in 0..text.Len() + 1 {
        if k < text.Len() && text[k] != b'.' {
            continue
        }
        n := ParseIpv4Number(text[start..k]) else {
            return none
        }
        start = k + 1
        index += 1
        if index < count {
            if n > 255 {
                return none
            }
            // Step 10: n times 256^(3 - counter).
            ipv4 += n << Cast[u64](8 * (4 - index))
            continue
        }
        // Step 8: the last part below 256^(5 - count).
        if n >= u64(1) << Cast[u64](8 * (5 - count)) {
            return none
        }
        ipv4 += n
    }
    return some(Trunc[u32](ipv4))
}

// ParseIpv6 is the IPv6 parser on the text between the brackets: the
// eight pieces, or none for failure.
ParseIpv6(s: []u8): ?[8]u16 {
    var address: [8]u16 = [0; 8]
    var piece := 0
    var compress := -1
    var p := 0
    n := s.Len()
    // Step 5: a leading "::".
    if p < n && s[p] == b':' {
        if p + 1 >= n || s[p + 1] != b':' {
            return none
        }
        p += 2
        piece += 1
        compress = piece
    }
    // Step 6.
    for p < n {
        // Step 6.1.
        if piece == 8 {
            return none
        }
        // Step 6.2: a "::" in the middle.
        if s[p] == b':' {
            if compress >= 0 {
                return none
            }
            p += 1
            piece += 1
            compress = piece
            continue
        }
        // Steps 6.3 and 6.4: up to four hex digits.
        var value := 0
        var length := 0
        for length < 4 && p < n && HexValue(s[p]) < 16 {
            value = value * 16 + int(HexValue(s[p]))
            p += 1
            length += 1
        }
        // Step 6.5: an IPv4 address in the last two pieces.
        if p < n && s[p] == b'.' {
            if length == 0 {
                return none
            }
            p -= length
            if piece > 6 {
                return none
            }
            var seen := 0
            for p < n {
                if seen > 0 {
                    if s[p] == b'.' && seen < 4 {
                        p += 1
                    } else {
                        return none
                    }
                }
                if p >= n || !IsDigit(s[p]) {
                    return none
                }
                var v4 := -1
                for p < n && IsDigit(s[p]) {
                    d := int(s[p] - b'0')
                    if v4 < 0 {
                        v4 = d
                    } else if v4 == 0 {
                        return none
                    } else {
                        v4 = v4 * 10 + d
                    }
                    if v4 > 255 {
                        return none
                    }
                    p += 1
                }
                address[piece] = Trunc[u16](int(address[piece]) * 0x100 + v4)
                seen += 1
                if seen == 2 || seen == 4 {
                    piece += 1
                }
            }
            if seen != 4 {
                return none
            }
            break
        }
        // Step 6.6: a ':' must be followed by more.
        if p < n && s[p] == b':' {
            p += 1
            if p >= n {
                return none
            }
        } else if p < n {
            // Step 6.7.
            return none
        }
        // Steps 6.9 and 6.10.
        address[piece] = Trunc[u16](value)
        piece += 1
    }
    // Step 7: move the pieces after the compression to the end.
    if compress >= 0 {
        var swaps := piece - compress
        piece = 7
        for piece != 0 && swaps > 0 {
            t := address[piece]
            address[piece] = address[compress + swaps - 1]
            address[compress + swaps - 1] = t
            piece -= 1
            swaps -= 1
        }
        return some(address)
    }
    // Step 8.
    if piece != 8 {
        return none
    }
    return some(address)
}

// PutDecimal writes n in decimal at out[o..] and returns the offset after
// it.
PutDecimal(out: mut []u8, o: int, n: u32): int {
    var digits: [10]u8 = [0; 10]
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
    for i in 0..k {
        out[o + i] = digits[k - 1 - i]
    }
    return o + k
}

const LowerHex: str = "0123456789abcdef"

// PutHex writes n in the shortest lower-case hex at out[o..] and returns
// the offset after it.
PutHex(out: mut []u8, o: int, n: u16): int {
    hex := LowerHex.AsBytes()
    var shift := 12
    for shift > 0 && (n >> Cast[u16](shift)) == 0 {
        shift -= 4
    }
    var at := o
    for shift >= 0 {
        out[at] = hex[int((n >> Cast[u16](shift)) & 15)]
        at += 1
        shift -= 4
    }
    return at
}

// CompressedPiece is the index the IPv6 serializer replaces by "::": the
// first of the longest run of two or more zero pieces, or -1.
CompressedPiece(pieces: &[8]u16): int {
    var longest := -1
    var longest_size := 1
    var found := -1
    var found_size := 0
    for i in 0..8 {
        if pieces[i] != 0 {
            if found_size > longest_size {
                longest = found
                longest_size = found_size
            }
            found = -1
            found_size = 0
        } else {
            if found < 0 {
                found = i
            }
            found_size += 1
        }
    }
    if found_size > longest_size {
        return found
    }
    return longest
}

// IpText writes an IPv4 or IPv6 host serialized, the IPv6 address in
// brackets, at out[0..] (at least 41 bytes), and returns its length; 0 for
// any other host.
IpText(h: &HostValue, out: mut []u8): int {
    match *h {
        .Ipv4(addr) {
            var o := 0
            for i in 0..4 {
                o = PutDecimal(out, o, (addr >> Cast[u32](24 - 8 * i)) & 0xFF)
                if i < 3 {
                    out[o] = b'.'
                    o += 1
                }
            }
            return o
        }
        .Ipv6(pieces) {
            compress := CompressedPiece(&pieces)
            out[0] = b'['
            var o := 1
            var ignore0 := false
            for i in 0..8 {
                if ignore0 && pieces[i] == 0 {
                    continue
                }
                ignore0 = false
                if compress == i {
                    if i == 0 {
                        out[o] = b':'
                        o += 1
                    }
                    out[o] = b':'
                    o += 1
                    ignore0 = true
                    continue
                }
                o = PutHex(out, o, pieces[i])
                if i != 7 {
                    out[o] = b':'
                    o += 1
                }
            }
            out[o] = b']'
            return o + 1
        }
        _ {
            return 0
        }
    }
}

// HostBytes is the bytes of a domain or opaque host, empty for any other.
HostBytes(h: &HostValue): []u8 from h {
    return match *h {
        .Domain(name) { name }
        .Opaque(text) { text }
        _ { "".AsBytes() }
    }
}
