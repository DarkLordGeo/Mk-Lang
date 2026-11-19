// Why: IDNA's private functions need tests, and IDNA can be cut from std.url
// as one unit.
// What: a second Mk driver over url.DomainToAscii, url.DomainToUnicode and the
// unit's private functions, which a fixture exports in the driver's std copy.
// How: cutting IDNA deletes this file with std/url/idna_*.mk; oracle cases
// through url.Parse and Url.Set use the main driver (urlAsk).

package std

import "testing"

// urlIdnaDriver is the Mk side of the IDNA tests. Arguments are hex as in
// urlDriver; strictness is "1" or "0".
const urlIdnaDriver = `import std.url
` + urlDriverHead + `
Case(a: &Arena, line: str): OutOfMemory!str from a {
    var parts := line.Split(" ")
    op := parts.Next() else { "" }
    x := parts.Next() else { "" }
    y := parts.Next() else { "" }
    z := parts.Next() else { "" }
    w4 := parts.Next() else { "" }
    w5 := parts.Next() else { "" }
    match op {
        "big" {
            // fn, then prefix, unit repeated count times, suffix: inputs
            // past the 64 KiB line of standard input. The answer is the
            // result's length and its first 32 bytes, hex.
            count := Cast[int](w4.ParseInt() else { 0 })
            unit := try TextOf(a, z)
            var t := Text.New(a)
            try t.Push(try TextOf(a, y))
            for _ in 0..count {
                try t.Push(unit)
            }
            try t.Push(try TextOf(a, w5))
            r := match x {
                "dta1" { url.DomainToAscii(a, t.Str(), true) else e { return try a.Fmt("err {e}", (e)) } }
                "dta0" { url.DomainToAscii(a, t.Str(), false) else e { return try a.Fmt("err {e}", (e)) } }
                _ { try url.DomainToUnicode(a, t.Str()) }
            }
            b := r.AsBytes()
            end := if b.Len() < 32 { b.Len() } else { 32 }
            head := b[0..end]
            return try a.Fmt("ok {n} {h}", (n: b.Len(), h: try HexOf(a, head)))
        }
        "dta" {
            r := url.DomainToAscii(a, try TextOf(a, x), y == "1") else e {
                return try a.Fmt("err {e}", (e))
            }
            return try a.Fmt("ok {h}", (h: try H(a, r)))
        }
        "dtu" {
            return try a.Fmt("ok {h}", (h: try H(a, try url.DomainToUnicode(a, try TextOf(a, x)))))
        }
        "u46" {
            r := try url.FxUts46(a, try TextOf(a, x), y == "1")
            return try a.Fmt("ok {ok} {h}", (ok: r.ok, h: try H(a, r.text)))
        }
        "u46a" {
            r := try url.FxUts46ToAscii(a, try TextOf(a, x), y == "1")
            return if r == "<none>" { "ok none" } else { try a.Fmt("ok {h}", (h: try H(a, r))) }
        }
        "pye" {
            r := try url.FxPunyEncode(a, try TextOf(a, x))
            return if r == "<none>" { "ok none" } else { try a.Fmt("ok {h}", (h: try H(a, r))) }
        }
        "pyd" {
            r := try url.FxPunyDecode(a, try TextOf(a, x))
            return if r == "<none>" { "ok none" } else { try a.Fmt("ok {h}", (h: try H(a, r))) }
        }
        "idnavalues" {
            lo := Cast[int](x.ParseInt() else { 0 })
            count := Cast[int](y.ParseInt() else { 0 })
            return try a.Fmt("ok {h}", (h: try url.FxIdnaValues(a, lo, count)))
        }
        "idnamaps" {
            count := Cast[int](x.ParseInt() else { 0 })
            return try a.Fmt("ok {h}", (h: try url.FxIdnaMaps(a, count)))
        }
        _ {
            return "bad op"
        }
    }
}

export Main(sys: &System): ReadError|WriteError|OutOfMemory!void {
    var r := sys.Stdin()
    var out := sys.Stdout()
    var arena := try Arena.New(sys.heap)
    defer arena.Free()
    for {
        line := try r.Line(&arena) else {
            break
        }
        answer := try Case(&arena, line)
        try out.Str(answer)
        try out.Str("\n")
        arena.Reset()
    }
    try out.Flush()
}
`

// urlIdnaFixture is written to std/url in the IDNA driver's copy of std:
// it exports the unit's private functions. Nothing here is part of std.
const urlIdnaFixture = `// Fixture of the std-url IDNA tests: private functions, exported.

import std.rt

export FxPunyDecode(a: &Arena, s: str): OutOfMemory!str from a {
    r := try PunycodeDecode(a, s.AsBytes())
    return r else { "<none>" }
}

export FxPunyEncode(a: &Arena, s: str): OutOfMemory!str from a {
    r := try PunycodeEncode(a, s)
    return r else { "<none>" }
}

export FxUts46(a: &Arena, s: str, strict: bool): OutOfMemory!(text: str, ok: bool) from a {
    return try Uts46(a, s, strict)
}

export FxUts46ToAscii(a: &Arena, s: str, strict: bool): OutOfMemory!str from a {
    r := try Uts46ToAscii(a, s, strict)
    return r else { "<none>" }
}

// FxIdnaValues is IdnaValue of each code point from lo, four hex digits
// each; a surrogate is "0000".
export FxIdnaValues(a: &Arena, lo: int, count: int): OutOfMemory!str from a {
    digits := "0123456789abcdef".AsBytes()
    out := try a.Slice[u8](4 * count)
    for k in 0..count {
        cp := lo + k
        var v: u16 = 0
        if cp < 0xD800 || cp > 0xDFFF {
            v = IdnaValue(Cast[u32](cp))
        }
        out[4 * k] = digits[int(v >> 12)]
        out[4 * k + 1] = digits[int((v >> 8) & 15)]
        out[4 * k + 2] = digits[int((v >> 4) & 15)]
        out[4 * k + 3] = digits[int(v & 15)]
    }
    return rt.StrFrom(out[..]).Must()
}

// FxIdnaMaps is the text of mappings 0 to count - 1, each hex, joined by
// ",".
export FxIdnaMaps(a: &Arena, count: int): OutOfMemory!str from a {
    digits := "0123456789abcdef".AsBytes()
    var t := Text.New(a)
    for k in 0..count {
        if k > 0 {
            try t.Push(",")
        }
        m := IdnaMapping(k)
        out := try a.Slice[u8](2 * m.Len())
        for i in 0..m.Len() {
            out[2 * i] = digits[int(m[i] >> 4)]
            out[2 * i + 1] = digits[int(m[i] & 15)]
        }
        try t.Push(rt.StrFrom(out[..]).Must())
    }
    return try a.Dup(t.Str())
}
`

// urlIdnaBuild is the IDNA driver's build.
var urlIdnaBuild = urlBuild{key: "idna", driver: urlIdnaDriver, fixtures: map[string]string{"url/tests_idna_fixture.mk": urlIdnaFixture}}

// urlIdnaAsk sends records to the IDNA driver for mode.
func urlIdnaAsk(t *testing.T, mode string, records []string) []string {
	t.Helper()
	return urlAskOf(t, urlIdnaBuild, mode, records)
}
