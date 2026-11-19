// Why: three bugs found in the vector scans must stay fixed: an overflow at
// a huge read limit, JSON unescaping after long plain runs, a weak prefilter.
// What: ReadHead's clamp at a limit near the int maximum (ReadRequest and
// Fetch), json.Parse's long-run strings, and FindPre's rarest-byte pair.
// How: Mk fixtures in a std copy, checked against Go's encoding/json and
// core.FindFrom, at dev and release.

package std

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// svLimitFixture is http/zz_svlimit.mk in the test's std copy: ReadRequest
// over core.HtMem (htMemFixture) at the limits around the int range's ends.
const svLimitFixture = `// Test fixture (std/std-vector_findings_test.go): SV-1.
import std.core

export SvLimitRun(sys: &System): Error|WriteError!void {
    var out := sys.Stdout()
    texts: [4]str = ["GET / HTTP/1.1\r\nHost: x\r\n\r\n", "POST /p HTTP/1.1\r\nHost: x\r\nContent-Length: 10\r\n\r\n0123456789", "POST /c HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nabcde\r\n0\r\n\r\n", "\r\n\r\nGET /e HTTP/1.1\r\nHost: x\r\n\r\n"]
    limits: [7]int = [9223372036854775807, 9223372036854775806, 9223372036854775000, 4611686018427387904, 0, -1, -9223372036854775807 - 1]
    for t in 0..4 {
        for l in 0..7 {
            for step in 1..4 {
                var ar := try Arena.New(sys.heap)
                defer ar.Free()
                var m := core.HtMemOf(texts[t].AsBytes(), step)
                var rb: [16]u8 = [0; 16]
                var r := core.HtMemReader(sys.io, mut m, mut rb[..])
                limit := limits[l]
                match ReadRequest(&ar, sys.io, mut r, limit) {
                    ok(req) {
                        n := req.body.Len()
                        try Print(mut out, "{t} {l} {step} ok {p} {n}\n", (t, l, step, p: req.path, n))
                    }
                    err(e) {
                        var k := "other"
                        match e {
                            TooLarge {
                                k = "toolarge"
                            }
                            Closed {
                                k = "closed"
                            }
                            _ {}
                        }
                        try Print(mut out, "{t} {l} {step} {k}\n", (t, l, step, k))
                    }
                }
            }
        }
    }
    try out.Flush()
}
`

const svLimitMain = `import std.http

export Main(sys: &System): http.Error|WriteError!void {
    try http.SvLimitRun(sys)
}
`

// TestSTDVECTOR_ReadRequestLimitNearIntMax: ReadRequest with a
// limit at or near the int maximum reads the request (ReadHead's clamp must
// not compute limit + 1), and a limit of 0 or
// below is TooLarge at the first byte, with no overflow either, in pieces
// of 1 to 3 bytes, dev and release.
func TestSTDVECTOR_ReadRequestLimitNearIntMax(t *testing.T) {
	t.Parallel()
	paths := []string{"/", "/p", "/c", "/e"}
	bodies := []int{0, 10, 5, 0}
	var want strings.Builder
	for ti := range 4 {
		for l := range 7 {
			for step := 1; step < 4; step++ {
				if l < 4 {
					fmt.Fprintf(&want, "%d %d %d ok %s %d\n", ti, l, step, paths[ti], bodies[ti])
				} else {
					fmt.Fprintf(&want, "%d %d %d toolarge\n", ti, l, step)
				}
			}
		}
	}
	fx := map[string]string{"core/zz_httpmem.mk": htMemFixture, "http/zz_svlimit.mk": svLimitFixture}
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, fx, svLimitMain, flags...), "")
			if r.code != 0 {
				t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
			}
			diffLines(t, strings.ReplaceAll(r.stdout, "\r\n", "\n"), want.String())
		})
	}
}

// svFetchMaxMain fetches args[1] with limit the int maximum and prints the
// status and the body, or the error's kind.
const svFetchMaxMain = `import std.http

export Main(sys: &System): http.Error|WriteError!void {
    var out := sys.Stdout()
    hs: [1]http.Header = [http.Header(name: "X-A", value: "1")]
    reply := http.Fetch(sys.arena, sys.io, "GET", sys.args[1], hs[..], "".AsBytes(), 9223372036854775807) else e {
        match e {
            http.TooLarge {
                try out.Str("large\n")
            }
            _ {
                try out.Str("other\n")
            }
        }
        try out.Flush()
        return
    }
    s := reply.status
    try Print(mut out, "status {s}\n", (s))
    try out.Str("b ")
    try out.Bytes(reply.body)
    try out.Str("\n")
    try out.Flush()
}
`

// TestSTDVECTOR_FetchLimitIntMax is the same through Fetch: limit the int
// maximum reads an interim 100 and the final reply (ReadHead is called with
// lim - consumed, which is limit itself for the first head), dev and
// release.
func TestSTDVECTOR_FetchLimitIntMax(t *testing.T) {
	t.Parallel()
	interim := htRawServer(t, "HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	plain := htRawServer(t, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n0\r\n\r\n")
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			exe := buildNative(t, nil, svFetchMaxMain, flags...)
			for _, c := range []struct{ addr, want string }{{interim, "status 200\nb ok\n"}, {plain, "status 200\nb abc\n"}} {
				r := runNative(t, exe, "", "http://"+c.addr+"/")
				if r.code != 0 {
					t.Fatalf("%s: exit %d\nstdout:\n%s\nstderr:\n%s", c.addr, r.code, r.stdout, r.stderr)
				}
				if got := strings.ReplaceAll(r.stdout, "\r\n", "\n"); got != c.want {
					t.Errorf("%s: got %q, want %q", c.addr, got, c.want)
				}
			}
		})
	}
}

// svJsonEscMain parses each framed document of stdin (a 4-byte
// little-endian length, then the bytes) with json.Parse and prints the
// decoded string in hex, or the error's offset.
const svJsonEscMain = `import std.json

const Hex: [16]u8 = [0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66]

export Main(sys: &System): OutOfMemory|ParseError|ReadError|WriteError|Utf8Error!void {
    size := Cast[int](try sys.args[1].ParseInt())
    var inp := sys.Stdin()
    var out := sys.Stdout()
    buf := try sys.arena.Slice[u8](size)
    got := try inp.ReadAll(buf)
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var i := 0
    for i + 4 <= got {
        n := Cast[int](buf[i]) | (Cast[int](buf[i + 1]) << 8) | (Cast[int](buf[i + 2]) << 16) | (Cast[int](buf[i + 3]) << 24)
        doc := try str.From(buf[i + 4..i + 4 + n])
        i += 4 + n
        match json.Parse(&a, doc) {
            ok(v) {
                if s := v.Str() {
                    b := s.AsBytes()
                    h := try a.Slice[u8](2 * b.Len() + 1)
                    for k in 0..b.Len() {
                        h[2 * k] = Hex[Cast[int](b[k] >> 4)]
                        h[2 * k + 1] = Hex[Cast[int](b[k] & 15)]
                    }
                    h[2 * b.Len()] = 0x0A
                    try out.Str("ok ")
                    try out.Bytes(h[..])
                } else {
                    try out.Str("notstr\n")
                }
            }
            err(e) {
                match e {
                    json.Syntax(offset) {
                        try Print(mut out, "syntax {offset}\n", (offset))
                    }
                    _ {
                        try out.Str("other\n")
                    }
                }
            }
        }
        a.Reset()
    }
    try out.Flush()
}
`

// svJsonEscCase is one JSON escape document: its text, and the decoded string or
// the offset of its first error.
type svJsonEscCase struct {
	doc  string
	want string // "ok <hex>" or "syntax <offset>"
}

// svJsonEscCases builds the documents: a top-level string whose first
// escape sits at every offset 0 to 40, followed by plain runs of 0 to 300
// bytes (around every vector and word size) and more escapes, so Unescape's
// two passes see long plain runs after the first backslash; and the same
// strings broken after the escape by a control byte, a bad escape, a bad
// or truncated \u, a lone surrogate or a missing closing quote, each at a
// known offset.
func svJsonEscCases() []svJsonEscCase {
	rng := uint64(0x5EED5EED5EED5EED)
	next := func(k int) int {
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return int(rng % uint64(k))
	}
	plainRunes := []string{"a", "b", "z", " ", "~", "{", "<", "&", "/", "\u00e9", "\u4e2d", "\U0001F600", "\x7f"}
	plain := func(n int) string {
		var b strings.Builder
		for b.Len() < n {
			r := plainRunes[next(len(plainRunes))]
			if b.Len()+len(r) > n {
				r = "q"
			}
			b.WriteString(r)
		}
		return b.String()
	}
	escapes := []string{`\n`, `\"`, "\\\\", `\/`, `\b`, `\f`, `\r`, `\t`, `\u00e9`, `\u0041`, `\u4E2D`, `\ud83d\ude00`, `\u0000`}
	lens := []int{0, 1, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 100, 255, 300}
	var cs []svJsonEscCase
	for off := 0; off <= 40; off++ {
		for _, ln := range lens {
			body := plain(off) + escapes[next(len(escapes))] + plain(ln)
			if next(2) == 0 {
				body += escapes[next(len(escapes))] + plain(next(301))
			}
			doc := `"` + body + `"`
			if next(4) == 0 {
				doc += " \n"
			}
			var s string
			if err := json.Unmarshal([]byte(doc), &s); err != nil {
				panic(fmt.Sprintf("SV-4 case %q: %v", doc, err))
			}
			cs = append(cs, svJsonEscCase{doc, "ok " + hex.EncodeToString([]byte(s))})
			// The same string broken at its end, after the escapes and the
			// plain runs: the error's offset is where the break starts.
			at := 1 + len(body)
			breaks := []string{string(rune(next(0x20))), `\x`, `\u12G4`, `\udc00`, `\ud800\u0041`, `\ud800x`, `\u12`, `\`, ``}
			k := next(len(breaks))
			bad := `"` + body + breaks[k]
			want := at
			switch k {
			case 0, 1, 2, 3, 4, 5:
				bad += `abc"`
			default:
				// A truncated \u, a lone backslash or no closing quote: the
				// text ends inside the string, Syntax at its length.
				want = len(bad)
			}
			cs = append(cs, svJsonEscCase{bad, fmt.Sprintf("syntax %d", want)})
		}
	}
	return cs
}

// TestSTDVECTOR_JsonUnescapeLongRuns: json.Parse decodes
// strings whose first escape comes early and whose plain runs after it are
// long (Unescape's first pass steps by NextSpecial, its copy by
// simd.IndexByte) exactly as Go's encoding/json does, and reports the first
// error of a broken one at the offset a byte-at-a-time pass would, dev and
// release.
func TestSTDVECTOR_JsonUnescapeLongRuns(t *testing.T) {
	t.Parallel()
	cs := svJsonEscCases()
	var in strings.Builder
	var want strings.Builder
	for _, c := range cs {
		n := len(c.doc)
		in.Write([]byte{byte(n), byte(n >> 8), byte(n >> 16), byte(n >> 24)})
		in.WriteString(c.doc)
		want.WriteString(c.want + "\n")
	}
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, nil, svJsonEscMain, flags...), in.String(), strconv.Itoa(in.Len()))
			if r.code != 0 {
				t.Fatalf("exit %d\nstderr:\n%s", r.code, r.stderr)
			}
			got := strings.Split(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n")
			ws := strings.Split(want.String(), "\n")
			if len(got) != len(ws) {
				t.Fatalf("%d lines, want %d", len(got), len(ws))
			}
			bad := 0
			for i := range ws {
				if got[i] != ws[i] {
					if bad < 10 {
						t.Errorf("case %d %q:\n got %.200s\nwant %.200s", i/1, cs[min(i, len(cs)-1)].doc, got[i], ws[i])
					}
					bad++
				}
			}
			t.Logf("%d documents, %d differ", len(cs), bad)
		})
	}
}

// svPairFixture is regex/zz_svpair.mk in the test's std copy: RarePair on
// fixed needles, and FindPre against core.FindFrom on text
// whose letters follow English frequencies, with needles whose end bytes
// are common there.
const svPairFixture = `// Test fixture (std/std-vector_findings_test.go): SV-10.
import std.core
` + svRngMk + `
const SvEng: str = "eeeeeeeeeeeettttttttaaaaaaaaaooooooooiiiiiiinnnnnnnssssssshhhhhhrrrrrrddddllllcccuuummwwffggyyppbbvkjxqz                    059"

export SvPairRun(sys: &System): OutOfMemory|Utf8Error|WriteError!void {
    var out := sys.Stdout()
    ps: [9]str = ["the", "tion", "needle", "ab", "aaaaaaab", "Zz", "theseus", "e e", "status=5"]
    for k in 0..9 {
        pr := RarePair(ps[k].AsBytes())
        lo := pr.lo
        hi := pr.hi
        try Print(mut out, "pair {lo} {hi}\n", (lo, hi))
    }
    var rng := SvRng(x: 0x2545F4914F6CDD1D)
    eng := SvEng.AsBytes()
    var tb := try sys.arena.Slice[u8](20000)
    needles: [8]str = ["the", "tion", "needle", "theseus", "and the", "ing ", "en", "eee"]
    var cases := 0
    var found := 0
    var bad := 0
    for c in 0..400 {
        n := 100 + SvR(mut rng, 19900)
        for i in 0..n {
            tb[i] = eng[SvR(mut rng, eng.Len())]
        }
        s := try str.From(tb[0..n])
        p := needles[c % 8]
        var from := 0
        for from <= n {
            cases += 1
            var x := -1
            var y := -1
            if k := FindPre(s, p, from) {
                x = k
            }
            if k := core.FindFrom(s, p, from) {
                y = k
            }
            if x != y {
                bad += 1
            }
            if y >= 0 {
                found += 1
            }
            from += 1 + SvR(mut rng, 300)
        }
    }
    try Print(mut out, "findpre {cases} found {found} bad {bad}\n", (cases, found, bad))
    try out.Flush()
}
`

const svPairMain = `import std.regex

export Main(sys: &System): OutOfMemory|Utf8Error|WriteError!void {
    try regex.SvPairRun(sys)
}
`

// TestSTDVECTOR_RarePairFindPre: FindPre's pair filter loads
// the needle's rarest byte by ByteRank and the rarest at least half its
// length away (the earlier on a tie), not its first and last, and still finds what core.FindFrom finds on text
// whose letters follow English frequencies, dev and release. The gen side
// (RxPre with RarePair's places as constants) is TestSTDVECTOR_RegexGenPrefilter.
func TestSTDVECTOR_RarePairFindPre(t *testing.T) {
	t.Parallel()
	// The rarest byte, then the rarest at least half the length (and 2)
	// away (any other when none is). the: h, then t. tion: n, then i.
	// needle: l, then n (d is next to l). ab: both. aaaaaaab: b, then the
	// first a. Zz: Z (half of z's rank), then z. theseus: u, then h. "e e":
	// the two e's, the space ranking first. status=5: 5, then s at 0 (u
	// and = are closer than 4).
	want := "pair 0 1\npair 1 3\npair 0 4\npair 0 1\npair 0 7\npair 0 1\npair 1 5\npair 0 2\npair 0 7\n"
	fx := map[string]string{"regex/zz_svpair.mk": svPairFixture}
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, fx, svPairMain, flags...), "")
			if r.code != 0 {
				t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
			}
			out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			if !strings.HasPrefix(out, want) {
				t.Fatalf("pairs:\n%s\nwant\n%s", out, want)
			}
			var cases, found, bad int
			if _, err := fmt.Sscanf(out[len(want):], "findpre %d found %d bad %d\n", &cases, &found, &bad); err != nil {
				t.Fatalf("output: %v\n%s", err, out)
			}
			t.Logf("findpre %d cases, %d found", cases, found)
			if bad != 0 || cases < 10000 || found < 3000 {
				t.Fatalf("findpre: %d cases, %d found, %d differ", cases, found, bad)
			}
		})
	}
}
