// Why: The list route takes its filters from the URL query.
//
// What: QueryParam, Decode and the error BadQuery.
//
// How: Decodes `application/x-www-form-urlencoded` values (`+` is a space,
// `%XX` a byte) and refuses a bad escape or text that is not UTF-8.

/// A query parameter with no valid `%XX` escape, or one that decodes to text
/// that is not UTF-8. `index` is the parameter's position in the query.
export error BadQuery(index: int)

/// The value of the first `name=value` pair of a URL query, decoded from
/// `application/x-www-form-urlencoded` (`+` is a space, `%XX` a byte), or none
/// when the query has no such pair. A pair with no `=` has the value "". O(n);
/// allocates the decoded value in `a` when it holds an escape.
export QueryParam(a: &Arena, query: str, name: str): BadQuery|OutOfMemory!?str from a, query {
    var index := 0
    for pair in query.Split("&") {
        if pair.Len() == 0 {
            continue
        }
        eq := pair.Find("=") else { pair.Len() }
        key := try Decode(a, pair[..eq], index)
        if key == name {
            value := if eq < pair.Len() { pair[eq + 1..] } else { "" }
            return some(try Decode(a, value, index))
        }
        index += 1
    }
    return none
}

/// `s` with `+` and `%XX` decoded; `s` itself when it holds neither. Fails with
/// `BadQuery(index)` on a short or non-hex escape or a result that is not UTF-8.
export Decode(a: &Arena, s: str, index: int): BadQuery|OutOfMemory!str from a, s {
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
                fail BadQuery(index: index)
            }
            hi := Hex(s.Byte(i + 1)) else { fail BadQuery(index: index) }
            lo := Hex(s.Byte(i + 2)) else { fail BadQuery(index: index) }
            out[n] = hi * 16 + lo
            i += 3
        } else {
            out[n] = b
            i += 1
        }
        n += 1
    }
    return str.From(out[..n]) else { fail BadQuery(index: index) }
}

// The value of one hex digit.
Hex(b: u8): ?u8 {
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

test "QueryParam decodes plus and percent escapes" (sys: &System) {
    v := try QueryParam(sys.arena, "q=two+words%21&tag=x", "q")
    Assert(v.Must() == "two words!")
}

test "QueryParam gives none for a missing name and empty for a bare key" (sys: &System) {
    missing := try QueryParam(sys.arena, "a=1&b", "c")
    Assert(missing.IsNone())
    bare := try QueryParam(sys.arena, "a=1&b", "b")
    Assert(bare.Must() == "")
}

test "Decode refuses a truncated escape" (sys: &System) {
    _ = Decode(sys.arena, "abc%4", 0) else {
        return
    }
    Assert(false)
}
