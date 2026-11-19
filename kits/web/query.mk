// Why: handlers read query parameters by name, decoded the way browsers
// encode a form, and one bad key elsewhere in the query must not fail the
// request.
//
// What: Query, the decoded value of a named pair of Request.query, QueryInt,
// the same read as a decimal i64, and Unescape, the decoder of one name or
// value.
//
// How: `application/x-www-form-urlencoded` as browsers send a form (WHATWG
// URL 5.1): pairs split at `&`, a pair's name and value at its first `=`,
// `+` a space and `%XX` a byte. A malformed escape, or a result that is not
// UTF-8, in the pair asked for fails with Status(400); another pair's
// malformed name only makes that pair no match (WHATWG's parser keeps such
// bytes as they are; Query stays strict on what it returns).

import std.http

/// The value of the first pair of req's query whose decoded name is name,
/// decoded, or none when no pair has the name. A pair with no `=` has the
/// value "". A pair whose name does not decode is no match; the value
/// asked for fails with Status(400) when it does not decode. O(query
/// length); allocates in `a` only the decoded texts that hold an escape.
export Query(a: &Arena, req: &http.Request, name: str): http.Error!?str from a, req {
    for pair in req.query.Split("&") {
        if pair.Len() == 0 {
            continue
        }
        eq := pair.Find("=") else {
            pair.Len()
        }
        key := Unescape(a, pair[..eq]) else e {
            match e {
                OutOfMemory {
                    fail OutOfMemory
                }
                _ {
                    continue
                }
            }
        }
        if key == name {
            value := if eq < pair.Len() { pair[eq + 1..] } else { "" }
            return some(try Unescape(a, value))
        }
    }
    return none
}

/// Query's value as a decimal i64, or none when absent. Fails with
/// Status(400) when the value is present but not a number.
export QueryInt(a: &Arena, req: &http.Request, name: str): http.Error!?i64 {
    v := try Query(a, req, name) else {
        return none
    }
    n := v.ParseInt() else {
        fail http.Status(code: 400)
    }
    return some(n)
}

/// s with `+` decoded to a space and each `%XX` to its byte; s itself when
/// it holds neither. Fails with Status(400) on an escape that is short or
/// not hex, or a result that is not UTF-8.
export Unescape(a: &Arena, s: str): http.Error!str from a, s {
    if !s.Has("%") && !s.Has("+") {
        return s
    }
    var out := try a.Slice[u8](s.Len())
    var n := 0
    var i := 0
    for i < s.Len() {
        b := s.Byte(i)
        if b == b'+' {
            out[n] = b' '
            i += 1
        } else if b == b'%' {
            if i + 2 >= s.Len() {
                fail http.Status(code: 400)
            }
            hi := HexDigit(s.Byte(i + 1)) else {
                fail http.Status(code: 400)
            }
            lo := HexDigit(s.Byte(i + 2)) else {
                fail http.Status(code: 400)
            }
            out[n] = hi * 16 + lo
            i += 3
        } else {
            out[n] = b
            i += 1
        }
        n += 1
    }
    v := str.From(out[..n]) else {
        fail http.Status(code: 400)
    }
    return v
}

// HexDigit is the value of one hex digit, or none.
HexDigit(b: u8): ?u8 {
    if b >= b'0' && b <= b'9' {
        return some(b - b'0')
    }
    if b >= b'a' && b <= b'f' {
        return some(b - b'a' + 10)
    }
    if b >= b'A' && b <= b'F' {
        return some(b - b'A' + 10)
    }
    return none
}
