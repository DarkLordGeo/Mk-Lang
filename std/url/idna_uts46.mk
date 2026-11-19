// Why: the URL Standard's domain parser turns international domain names
// into ASCII (and back for display) by UTS 46, whose mapping, normalization
// and validity rules must be followed exactly for hosts to compare equal.
//
// What: DomainToAscii and DomainToUnicode, over Uts46ToAscii, Uts46 and
// Uts46Labels, the label checks (IsValidLabel, JoinersAllowed,
// BidiAllowed) and the mapping (IdnaMap). Implements UTS 46, Unicode IDNA
// Compatibility Processing (revision 36, Unicode 18.0.0), as the URL
// Standard runs it in its section 3.3 (whatwg/url 845bafc).
//
// How: nontransitional processing, CheckBidi and CheckJoiners on,
// IgnoreInvalidPunycode off, and CheckHyphens, UseSTD3ASCIIRules and
// VerifyDnsLength all equal to strict. Step numbers are UTS 46's: section
// 4's processing steps (1 Map, 2 Normalize, 3 Break, 4 Convert/Validate),
// section 4.1's validity criteria (1 to 9 in its order), and section 4.2's
// ToASCII steps; the Bidi rules are RFC 5893 section 2's and the joiner
// rules RFC 5892 appendix A.1 and A.2. With idna_tables.mk and
// idna_punycode.mk this is the IDNA unit a build can leave out (idna.mk).
//
// Every pass is linear in its input and none recurses: the map writes at
// most 18 scalars for one (U+FDFA), NFC is std.text's O(n), Punycode is
// O(L log L), and the joiner and Bidi checks read each label at most twice.

import std.text

// IdnaValue's values: the status of a code point in UTS 46 section 5's
// table, with valid split by General_Category=Mark for criterion 6.
const IdnaValid: u16 = 0
const IdnaMark: u16 = 1
const IdnaDeviation: u16 = 2
const IdnaIgnored: u16 = 3
const IdnaDisallowed: u16 = 4
const IdnaMapped: u16 = 5

const Zwnj: u32 = 0x200C
const Zwj: u32 = 0x200D

// The Bidi classes RFC 5893's rules 2 and 5 allow, as bit sets over
// props.mk's numbering: R, AL, AN, EN, ES, CS, ET, ON, BN and NSM for a
// right-to-left label; L, EN, ES, CS, ET, ON, BN and NSM for a
// left-to-right one.
const RtlAllowed: u32 = (1 << 1) | (1 << 2) | (1 << 6) | (1 << 3) | (1 << 4) | (1 << 7) | (1 << 5) | (1 << 13) | (1 << 9) | (1 << 8)
const LtrAllowed: u32 = (1 << 0) | (1 << 3) | (1 << 4) | (1 << 7) | (1 << 5) | (1 << 13) | (1 << 9) | (1 << 8)

/// The ASCII form of a domain name, as the URL Standard's "domain to
/// ASCII" gives it with strict as its beStrict: "bücher.example" becomes
/// "xn--bcher-kva.example".
///
/// With strict false, an ASCII domain is lower-cased and any other goes
/// through UTS 46's ToASCII, as Parse reads the host of a special URL.
/// With strict true, every domain goes through ToASCII with CheckHyphens,
/// UseSTD3ASCIIRules and VerifyDnsLength, so an xn-- label must also be
/// valid Punycode. Errors: BadHost where the parser fails, OutOfMemory.
/// O(n log n) for n bytes (Punycode); allocates O(n) in a.
export DomainToAscii(a: &Arena, domain: str, strict: bool): Error|OutOfMemory!str from a {
    // Steps 1 to 3: with beStrict, ToASCII's result or failure. (Step 1's
    // strict run only reports a validation error otherwise, which std.url
    // does not report.)
    if strict {
        r := try Uts46ToAscii(a, domain, true)
        return r else {
            fail BadHost
        }
    }
    // Step 5: an ASCII domain is lower-cased, whatever ToASCII says (web
    // compatibility). Step 6: anything else through ToASCII.
    var result := ""
    if IsAsciiText(domain) {
        result = try LowerAsciiCopy(a, domain)
    } else {
        r := try Uts46ToAscii(a, domain, false)
        result = r else {
            fail BadHost
        }
    }
    // Step 7.
    if result.Len() == 0 {
        fail BadHost
    }
    // Step 8.
    for c in result.AsBytes() {
        if IsClass(c, ForbiddenDomain) {
            fail BadHost
        }
    }
    return result
}

/// The Unicode form of a domain name, as the URL Standard's "domain to
/// Unicode" gives it: UTS 46's ToUnicode with CheckHyphens and
/// UseSTD3ASCIIRules false, each Punycode label decoded. When ToUnicode
/// records an error, the result is domain itself, copied. Cannot fail but
/// for OutOfMemory. O(n log n) for n bytes (Punycode); allocates O(n) in
/// a.
export DomainToUnicode(a: &Arena, domain: str): OutOfMemory!str from a {
    // Step 1.
    r := try Uts46(a, domain, false)
    // Step 2.
    if !r.ok {
        return try a.Dup(domain)
    }
    // Step 3.
    return r.text
}

// Uts46ToAscii is section 4.2's ToASCII on domain with the flags above,
// in a, or none when it fails.
Uts46ToAscii(a: &Arena, domain: str, strict: bool): OutOfMemory!?str from a {
    // Step 1: the processing; steps 2 and 3: each label with a non-ASCII
    // code point to "xn--" and its Punycode.
    p := try Uts46Labels(a, domain, strict)
    if !p.ok {
        return none
    }
    var out := Text.New(a)
    for i in 0..p.labels.Len() {
        label := p.labels[i]
        if i > 0 {
            try out.Push(".")
        }
        var n := label.Len()
        if IsAsciiText(label) {
            try out.Push(label)
        } else {
            r := try PunycodeEncode(a, label)
            encoded := r else {
                return none
            }
            try out.Push("xn--")
            try out.Push(encoded)
            n = 4 + encoded.Len()
        }
        // Step 4: VerifyDnsLength, each label 1 to 63 bytes, the root's
        // empty label included.
        if strict && (n == 0 || n > 63) {
            return none
        }
    }
    result := out.Str()
    // Step 4: the name 1 to 253 bytes.
    if strict && (result.Len() == 0 || result.Len() > 253) {
        return none
    }
    return some(try a.Dup(result))
}

// Uts46 is section 4's processing of domain with the flags above, as
// section 4.3's ToUnicode returns it: the domain with each label
// converted, and whether no error was recorded.
Uts46(a: &Arena, domain: str, strict: bool): OutOfMemory!(text: str, ok: bool) from a {
    p := try Uts46Labels(a, domain, strict)
    var out := Text.New(a)
    for i in 0..p.labels.Len() {
        if i > 0 {
            try out.Push(".")
        }
        try out.Push(p.labels[i])
    }
    return (text: try a.Dup(out.Str()), ok: p.ok)
}

// Uts46Labels is section 4's processing of domain: its labels after steps
// 1 to 4, a Punycode label replaced by its decoding, and whether no error
// was recorded.
Uts46Labels(a: &Arena, domain: str, strict: bool): OutOfMemory!(labels: List[str], ok: bool) from a {
    // Step 1, Map.
    mapped := try IdnaMap(a, domain)
    // Step 2, Normalize.
    normal := try text.Normalize(a, mapped, text.Form.Nfc)
    // Step 3, Break, and step 4's conversion of each "xn--" label.
    var labels := List[str].New(a)
    var decoded := List[bool].New(a)
    var ok := true
    var longest := 0
    b := normal.AsBytes()
    var start := 0
    for k in 0..b.Len() + 1 {
        if k < b.Len() && b[k] != b'.' {
            continue
        }
        var label := normal[start..k]
        start = k + 1
        var punycode := false
        lb := label.AsBytes()
        if lb.Len() >= 4 && lb[0] == b'x' && lb[1] == b'n' && lb[2] == b'-' && lb[3] == b'-' {
            // Step 4.1.1: a non-ASCII code point is an error.
            if !IsAsciiText(label) {
                ok = false
            } else {
                // Step 4.1.2: the rest from Punycode, else an error and
                // the label kept.
                r := try PunycodeDecode(a, lb[4..])
                if d := r {
                    label = d
                    punycode = true
                    // Step 4.1.3.
                    if d.Len() == 0 || IsAsciiText(d) {
                        ok = false
                    }
                } else {
                    ok = false
                }
            }
        }
        if label.Len() > longest {
            longest = label.Len()
        }
        try labels.Push(label)
        try decoded.Push(punycode)
    }
    if !ok {
        // The text is the same whatever the validity criteria say.
        return (labels: labels, ok: false)
    }
    // Steps 4.1.4 and 4.2.1: the validity criteria. CheckBidi applies to
    // every label once one label of the converted domain is right-to-left.
    scalars := try a.Slice[char](longest)
    bidi := IsBidiDomain(&labels)
    for i in 0..labels.Len() {
        label := labels[i]
        if label.Len() == 0 {
            continue
        }
        // Criterion 1: only a decoded label can be out of NFC (UTS 46
        // section 4.5).
        if decoded[i] {
            nfc := try text.Normalize(a, label, text.Form.Nfc)
            if nfc != label {
                return (labels: labels, ok: false)
            }
        }
        if !IsValidLabel(label, strict, bidi, scalars) {
            return (labels: labels, ok: false)
        }
    }
    return (labels: labels, ok: true)
}

// IsValidLabel is section 4.1's criteria 2 to 9 for a non-empty label,
// with nontransitional processing; scalars has room for its scalars.
IsValidLabel(label: str, strict: bool, bidi: bool, scalars: mut []char): bool {
    var n := 0
    for c in label.Scalars() {
        scalars[n] = c
        n += 1
    }
    s := scalars[0..n]
    first := u32(s[0])
    if strict {
        // Criteria 2 and 3: CheckHyphens.
        if n >= 4 && u32(s[2]) == 0x2D && u32(s[3]) == 0x2D {
            return false
        }
        if first == 0x2D || u32(s[n - 1]) == 0x2D {
            return false
        }
    } else if n >= 4 && first == 0x78 && u32(s[1]) == 0x6E && u32(s[2]) == 0x2D && u32(s[3]) == 0x2D {
        // Criterion 4: no "xn--" without CheckHyphens.
        return false
    }
    // Criterion 6: no leading combining mark.
    if IdnaValue(first) == IdnaMark {
        return false
    }
    for c in s {
        v := u32(c)
        // Criterion 5.
        if v == 0x2E {
            return false
        }
        // Criterion 7: valid or deviation, and with UseSTD3ASCIIRules an
        // ASCII code point is a lower-case letter, a digit or '-'.
        if IdnaValue(v) > IdnaDeviation {
            return false
        }
        if strict && v < 0x80 && !(v >= 0x61 && v <= 0x7A || v >= 0x30 && v <= 0x39 || v == 0x2D) {
            return false
        }
    }
    // Criterion 8 (CheckJoiners) and criterion 9 (CheckBidi).
    return JoinersAllowed(s) && (!bidi || BidiAllowed(s))
}

// JoinersAllowed is RFC 5892's CONTEXTJ rules for each joiner of s:
// U+200D only after a Virama (A.2); U+200C after a Virama, or between
// (L|D) T* and T* (R|D) by Joining_Type (A.1). U+200C is Joining_Type U,
// so each scan stops at the next joiner: a run of T is read at most twice.
JoinersAllowed(s: []char): bool {
    n := s.Len()
    for i in 0..n {
        c := u32(s[i])
        if c != Zwnj && c != Zwj {
            continue
        }
        if i > 0 && IsVirama(s[i - 1]) {
            continue
        }
        if c == Zwj {
            return false
        }
        var left := false
        var j := i
        for j > 0 {
            j -= 1
            t := JoiningType(s[j])
            if t != JoinT {
                left = t == JoinL || t == JoinD
                break
            }
        }
        var right := false
        for k in i + 1..n {
            t := JoiningType(s[k])
            if t != JoinT {
                right = t == JoinR || t == JoinD
                break
            }
        }
        if !left || !right {
            return false
        }
    }
    return true
}

// IsBidiDomain is whether a label holds a code point of Bidi_Class R, AL
// or AN (UTS 46 section 2, RFC 5893 section 1.4).
IsBidiDomain(labels: &List[str]): bool {
    for i in 0..labels.Len() {
        for c in labels[i].Scalars() {
            b := BidiClass(c)
            if b == BidiR || b == BidiAL || b == BidiAN {
                return true
            }
        }
    }
    return false
}

// BidiAllowed is RFC 5893 section 2's six rules for the non-empty label s.
BidiAllowed(s: []char): bool {
    // Rule 1: the first is L, R or AL, which makes the label LTR or RTL.
    first := BidiClass(s[0])
    rtl := first == BidiR || first == BidiAL
    if !rtl && first != BidiL {
        return false
    }
    allowed := if rtl { RtlAllowed } else { LtrAllowed }
    var en := false
    var an := false
    var last := first
    for c in s {
        b := BidiClass(c)
        // Rules 2 and 5.
        if (u32(1) << u32(b)) & allowed == 0 {
            return false
        }
        en = en || b == BidiEN
        an = an || b == BidiAN
        if b != BidiNSM {
            last = b
        }
    }
    if rtl {
        // Rules 3 and 4.
        return (last == BidiR || last == BidiAL || last == BidiEN || last == BidiAN) && !(en && an)
    }
    // Rule 6.
    return last == BidiL || last == BidiEN
}

// IdnaValue is c's value in the IDNA mapping table (idna_tables.mk).
IdnaValue(c: u32): u16 {
    b2 := u32(IdnaIndex1[int(c >> (IdnaShift2 + IdnaShift3))])
    b3 := u32(IdnaIndex2[int((b2 << IdnaShift2) | ((c >> IdnaShift3) & ((u32(1) << IdnaShift2) - 1)))])
    return IdnaData[int((b3 << IdnaShift3) | (c & ((u32(1) << IdnaShift3) - 1)))]
}

// IdnaMapping is mapping k's text, UTF-8.
IdnaMapping(k: int): []u8 {
    return IdnaMapBytes[int(IdnaMapEnd[k])..int(IdnaMapEnd[k + 1])]
}

// IdnaMap is section 4's step 1 on s: an ignored code point removed, a
// mapped one replaced, any other kept (a disallowed one fails the
// validity criteria later). Two passes, so the result is one exact
// allocation in a, at most 11 times the bytes of s (U+FDFA's mapping).
IdnaMap(a: &Arena, s: str): OutOfMemory!str from a {
    var bytes := 0
    for c in s.Scalars() {
        v := IdnaValue(u32(c))
        if v >= IdnaMapped {
            bytes += IdnaMapping(int(v - IdnaMapped)).Len()
        } else if v != IdnaIgnored {
            bytes += Utf8Len(u32(c))
        }
    }
    out := try a.Slice[u8](bytes)
    var o := 0
    for c in s.Scalars() {
        v := IdnaValue(u32(c))
        if v >= IdnaMapped {
            m := IdnaMapping(int(v - IdnaMapped))
            (mut out[o..o + m.Len()]).CopyFrom(m)
            o += m.Len()
        } else if v != IdnaIgnored {
            o = PutUtf8(out, o, u32(c))
        }
    }
    // Must: out holds whole scalars of s and mappings, which the generator
    // wrote as UTF-8.
    return str.From(out[..]).Must()
}

// IsAsciiText is whether s is ASCII.
IsAsciiText(s: str): bool {
    for c in s.AsBytes() {
        if c >= 0x80 {
            return false
        }
    }
    return true
}

// LowerAsciiCopy is s with every ASCII capital lower-cased, in a.
LowerAsciiCopy(a: &Arena, s: str): OutOfMemory!str from a {
    b := s.AsBytes()
    out := try a.Slice[u8](b.Len())
    for i, c in b {
        out[i] = if c >= b'A' && c <= b'Z' { c | 0x20 } else { c }
    }
    // Must: only ASCII bytes changed, to ASCII bytes.
    return str.From(out[..]).Must()
}
