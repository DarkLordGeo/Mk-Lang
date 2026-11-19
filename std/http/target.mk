// Why: a request target read differently by a proxy in front and by the
// server behind it lets a request slip past the proxy's rules; reading it
// strictly by RFC 9112 closes that gap.
//
// What: ReqTarget and ParseTarget (RFC 9112 section 3.2's four forms),
// CheckAuthority for the Host field, CheckEscape and the byte classes.
//
// How: the target is read by its form, never as a URL, and nothing is
// decoded, normalised or resolved: Request.path is the bytes received. The
// Host value is a host and an optional port (RFC 9112 3.2, RFC 3986
// 3.2.2).

// A ReqTarget is a request target split by its form: path and query are views
// of the head, authority is the absolute-form's authority (for the Host
// comparison), and absolute says the target had one.
struct ReqTarget {
    path: str
    query: str
    authority: str
    absolute: bool
}

// ParseTarget reads the target text[ts..te] of a request with method:
// origin-form ("/" path, then an optional "?" query), absolute-form for the
// http and https schemes, authority-form for CONNECT only and "*" for
// OPTIONS only (RFC 9112 3.2.1 to 3.2.4). A byte outside RFC 3986's path and
// query sets (pchar, "/" and "?", with "%" HEXDIG HEXDIG), an origin-form
// that does not start with "/", and a form the method may not use are
// BadRequest at the byte's offset (base counts from the request's first
// byte). O(te - ts); allocates nothing.
ParseTarget(text: str, method: str, ts: int, te: int, base: int): Error!ReqTarget from text {
    b := text.AsBytes()
    if method == "CONNECT" {
        // authority-form: uri-host ":" port, the port required.
        colon := try CheckAuthority(b, ts, te, base)
        if colon < 0 || colon + 1 == te {
            fail BadRequest(offset: base + te)
        }
        return ReqTarget(path: text[ts..te], query: "", authority: text[ts..te], absolute: false)
    }
    if b[ts] == 0x2A && te == ts + 1 {
        if method != "OPTIONS" {
            fail BadRequest(offset: base + ts)
        }
        return ReqTarget(path: "*", query: "", authority: "", absolute: false)
    }
    var ps := ts
    var auth := ""
    var absolute := false
    if b[ts] != 0x2F {
        // absolute-form (RFC 9112 3.2.2), http or https only, split by the
        // same strict rules: "scheme://" authority, then path-abempty.
        k := SchemeEnd(b, ts, te)
        if k < 0 || !(EqFold(text[ts..k], "http://") || EqFold(text[ts..k], "https://")) {
            fail BadRequest(offset: base + ts)
        }
        var j := k
        for j < te && b[j] != 0x2F && b[j] != 0x3F {
            j += 1
        }
        if j == k {
            fail BadRequest(offset: base + k)
        }
        _ = try CheckAuthority(b, k, j, base)
        auth = text[k..j]
        absolute = true
        ps = j
    }
    var q := ps
    for q < te && b[q] != 0x3F {
        if !IsPathByte(b[q]) {
            fail BadRequest(offset: base + q)
        }
        if b[q] == 0x25 {
            try CheckEscape(b, q, te, base)
        }
        q += 1
    }
    for x in q + 1..te {
        // query = *( pchar / "/" / "?" ) (RFC 3986 3.4)
        if !IsPathByte(b[x]) && b[x] != 0x3F {
            fail BadRequest(offset: base + x)
        }
        if b[x] == 0x25 {
            try CheckEscape(b, x, te, base)
        }
    }
    // An absolute-form with an empty path asks for "/" (RFC 9112 3.2.2,
    // RFC 9110 4.2.3).
    path := if q == ps { "/" } else { text[ps..q] }
    query := if q < te { text[q + 1..te] } else { "" }
    return ReqTarget(path: path, query: query, authority: auth, absolute: absolute)
}

// CheckEscape refuses a "%" at b[i] not followed by two hex digits before e
// (RFC 3986 2.1).
CheckEscape(b: []u8, i: int, e: int, base: int): Error!void {
    if i + 2 >= e || HexVal(b[i + 1]) < 0 || HexVal(b[i + 2]) < 0 {
        fail BadRequest(offset: base + i)
    }
}

// CheckAuthority checks b[s..e] as uri-host [":" port] (RFC 3986 3.2.2,
// 3.2.3): an IP-literal in brackets, or a reg-name (which holds an IPv4
// address) of unreserved, sub-delims and pct-encoded bytes, then an
// optional ":" and digits. No userinfo: "@" is refused (RFC 9110 4.2.4).
// Returns the index of the port's ":", or -1. O(e - s); allocates nothing.
CheckAuthority(b: []u8, s: int, e: int, base: int): Error!int {
    var i := s
    if i < e && b[i] == 0x5B {
        i += 1
        for i < e && b[i] != 0x5D {
            c := b[i]
            if !IsUnreserved(c) && !IsSubDelim(c) && c != Colon {
                fail BadRequest(offset: base + i)
            }
            i += 1
        }
        if i == e || i == s + 1 {
            fail BadRequest(offset: base + i)
        }
        i += 1
    } else {
        for i < e && b[i] != Colon {
            c := b[i]
            if c == 0x25 {
                try CheckEscape(b, i, e, base)
            } else if !IsUnreserved(c) && !IsSubDelim(c) {
                fail BadRequest(offset: base + i)
            }
            i += 1
        }
    }
    if i == e {
        return -1
    }
    if b[i] != Colon {
        fail BadRequest(offset: base + i)
    }
    colon := i
    for x in colon + 1..e {
        if !IsDigit(b[x]) {
            fail BadRequest(offset: base + x)
        }
    }
    return colon
}

// IsPathByte: c may stand in a path (pchar or "/", RFC 3986 3.3), "%"
// included; its two hex digits are the caller's to check.
IsPathByte(c: u8): bool {
    return IsUnreserved(c) || IsSubDelim(c) || c == Colon || c == 0x40 || c == 0x2F || c == 0x25
}

// IsUnreserved: ALPHA / DIGIT / "-" / "." / "_" / "~" (RFC 3986 2.3).
IsUnreserved(c: u8): bool {
    return IsAlpha(c) || IsDigit(c) || c == 0x2D || c == 0x2E || c == 0x5F || c == 0x7E
}

// IsSubDelim: "!" / "$" / "&" / "'" / "(" / ")" / "*" / "+" / "," / ";" /
// "=" (RFC 3986 2.2).
IsSubDelim(c: u8): bool {
    return c == 0x21 || c == 0x24 || c >= 0x26 && c <= 0x2C || c == 0x3B || c == 0x3D
}
