// Why: check, mono and lower all read format strings through this package,
// so its scan, match and spec rules must hold exactly.
// What: tests of Scan, Pieces, Match, the spec grammar, fills and the runner
// choice.
// How: table-driven cases, each with the problem text or the encoded spec word
// it must give.

package fmtspec

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// TestScan: the problems in byte order, as check reports them, on the
// cases check's TestE0706_FormatString judges and mono's per-instance
// judgement repeats.
func TestScan(t *testing.T) {
	for _, c := range []struct{ f, want string }{
		{"{i}\n", ""}, {"{{{i:x}}} {x:.2} {i}", ""}, {"{i", `lone "{"`}, {"i}", `lone "}"`},
		{"{1}", "bad hole name"}, {"{i:q}", "unknown spec"}, {"{i:.123}", "unknown spec"}, {"{i:.}", "unknown spec"},
		{"{a}{", `lone "{"`}, {"{a{b}", `lone "{"`},
	} {
		if _, _, p := Scan([]byte(c.f)); p != c.want {
			t.Errorf("Scan(%q) = %q, want %q", c.f, p, c.want)
		}
	}
}

func fields(names ...string) []Field[int] {
	var fs []Field[int]
	for i, n := range names {
		c := Other
		switch n[0] {
		case 'i':
			c = Int
		case 'f':
			c = Float
		}
		fs = append(fs, Field[int]{Name: n, Index: uint32(i), Type: i, Class: c})
	}
	return fs
}

// TestPieces: the literal runs, each hole's field among the fields and
// its spec word (lower's and mono's piece tables).
func TestPieces(t *testing.T) {
	cases := []struct {
		f     string
		names []string
		want  string
	}{
		{"", nil, ""},
		{"fizz\n", nil, `"fizz\n"/-1/0`},
		{"{i}\n", []string{"i"}, `""/0/0 "\n"/-1/0`},
		{"{{{a}}}", []string{"a"}, `"{"/0/0 "}"/-1/0`},
		{"{ib:x}{fa:.12}", []string{"fa", "ib"}, `""/1/1 ""/0/3074`},
		{"{fa:.0}", []string{"fa"}, `""/0/2`},
	}
	for _, c := range cases {
		hs, ps, p := Scan([]byte(c.f))
		if p != "" {
			t.Fatalf("%q: %s", c.f, p)
		}
		rs, p := Match(hs, fields(c.names...), nil, nil)
		if p != "" {
			t.Fatalf("%q: %s", c.f, p)
		}
		var got []string
		for _, q := range ps {
			arg, spec := -1, uint32(0)
			if q.Hole >= 0 {
				arg, spec = rs[q.Hole].Top, Encode(hs[q.Hole].Spec, rs[q.Hole].Field.Class)
			}
			got = append(got, strings.ReplaceAll(`"`+string(q.Lit)+`"`, "\n", `\n`)+"/"+strconv.Itoa(arg)+"/"+strconv.Itoa(int(spec)))
		}
		if g := strings.Join(got, " "); g != c.want {
			t.Fatalf("%q: pieces %s, want %s", c.f, g, c.want)
		}
	}
}

// TestMatch: each hole must name a field and each field be named, with
// the problems in the order check reports them (scan, unknown field, wrong
// spec for the type, unused field).
func TestMatch(t *testing.T) {
	for _, c := range []struct {
		f     string
		names []string
		want  string
	}{
		{"{ia} {fb}", []string{"fb", "ia"}, ""},
		{"{x} {y}", []string{"x"}, "hole names no field: y"},
		{"{x}", []string{"x", "z"}, "field used by no hole: z"},
		{"{fa:x}", []string{"fa"}, "x needs an integer field: fa"},
		{"{ia:.2}", []string{"ia"}, ".N needs a float field: ia"},
		{"{y} {fa:x}", []string{"fa"}, "hole names no field: y"},
		{"{fa:x}", []string{"fa", "z"}, "x needs an integer field: fa"},
	} {
		hs, _, p := Scan([]byte(c.f))
		if p != "" {
			t.Fatalf("%q: %s", c.f, p)
		}
		if _, p := Match(hs, fields(c.names...), nil, nil); p != c.want {
			t.Errorf("Match(%q) = %q, want %q", c.f, p, c.want)
		}
	}
}

// TestSpecGrammar: [[fill]align][+][0][width][.N][type], each rule
// accepted and refused, the class checks of Match, and the encoded word.
func TestSpecGrammar(t *testing.T) {
	for _, c := range []struct {
		f    string
		name string // the one field
		want string // the problem, or the word in hex
	}{
		{"{ia:5}", "ia", "0x20050040"}, {"{s:5}", "s", "0x20050000"}, {"{s:>5}", "s", "0x20050040"},
		{"{ia:<5}", "ia", "0x20050000"}, {"{s:^5}", "s", "0x20050080"}, {"{s:*^7}", "s", "0x2a070080"},
		{"{s::<3}", "s", "0x3a030000"}, {"{s: >255}", "s", "0x20ff0040"}, {"{ia:+}", "ia", "0x10"},
		{"{ia:08}", "ia", "0x20080060"}, {"{ia:+08}", "ia", "0x20080070"}, {"{fa:+010.3}", "fa", "0x200a0372"},
		{"{ia:x}", "ia", "0x1"}, {"{ia:X}", "ia", "0x3"}, {"{ia:b}", "ia", "0x4"}, {"{ia:o}", "ia", "0x5"},
		{"{ia:#>16b}", "ia", "0x23100044"}, {"{fa:.0}", "fa", "0x2"}, {"{fa:.99}", "fa", "0x6302"},
		{"{ia:0}", "ia", "0 needs a width"}, {"{ia:00}", "ia", "width is 1 to 255"},
		{"{ia:256}", "ia", "width is 1 to 255"}, {"{ia:9999}", "ia", "width is 1 to 255"},
		{"{ia:<}", "ia", "align needs a width"}, {"{ia:*<}", "ia", "align needs a width"},
		{"{ia:<08}", "ia", "0 takes no align"}, {"{ia:+x}", "ia", "+ takes no radix"},
		{"{ia:*5}", "ia", "unknown spec"}, {"{ia:5+}", "ia", "unknown spec"}, {"{ia:?}", "ia", "unknown spec"},
		{"{ia:#x}", "ia", "unknown spec"}, {"{fa:e}", "fa", "unknown spec"}, {"{fa:.100}", "fa", "unknown spec"},
		{"{ia:xx}", "ia", "unknown spec"}, {"{ia:\x01<5}", "ia", "unknown spec"}, {"{ia:\x7f<5}", "ia", "unknown spec"},
		{"{fa:x}", "fa", "x needs an integer field: fa"}, {"{s:b}", "s", "b needs an integer field: s"},
		{"{ia:.2}", "ia", ".N needs a float field: ia"}, {"{s:+}", "s", "+ needs a number field: s"},
		{"{s:05}", "s", "0 needs a number field: s"}, {"{fa:+}", "fa", "0x10"},
	} {
		f := c.f
		hs, _, p := Scan([]byte(f))
		got := p
		if p == "" {
			var rs []Resolved[int]
			rs, p = Match(hs, fields(c.name), nil, nil)
			got = p
			if p == "" {
				got = "0x" + strconv.FormatUint(uint64(Encode(hs[0].Spec, rs[0].Field.Class)), 16)
			}
		}
		if got != c.want {
			t.Errorf("%q: %s, want %s", f, got, c.want)
		}
	}
}

// TestBraceIsNoFill: a fill is any printable ASCII byte
// but a brace, since Scan ends the hole at the first "{" or "}" before the
// spec is read; "~", the last printable byte, still is one.
func TestBraceIsNoFill(t *testing.T) {
	for f, want := range map[string]string{`{s:{>5}`: `lone "{"`, `{s:}>5}`: `lone "}"`, `{s:{<5}x`: `lone "{"`} {
		if _, _, p := Scan([]byte(f)); p != want {
			t.Errorf("%q: %q, want %q", f, p, want)
		}
	}
	if s, p := parseSpec("{>5"); p == "" {
		t.Errorf("parseSpec read %q as a fill", s.Fill)
	}
	hs, _, p := Scan([]byte("{s:~>5}"))
	if p != "" {
		t.Fatal(p)
	}
	rs, p := Match(hs, fields("s"), nil, nil)
	if p != "" || Encode(hs[0].Spec, rs[0].Field.Class) != 0x7e050040 {
		t.Errorf("{s:~>5}: %q %#x", p, Encode(hs[0].Spec, rs[0].Field.Class))
	}
}

// TestRunnerChoice: the padding runner only for a call with a width,
// and the spec-aware value function only for the entries a valued spec
// reads ({n} and {n:x} share one entry).
func TestRunnerChoice(t *testing.T) {
	for _, c := range []struct {
		f      string
		names  []string
		padded bool
		spec   string
	}{
		{"{ia} {s}", []string{"ia", "s"}, false, "[false false]"},
		{"{ia:x} {s}", []string{"ia", "s"}, false, "[true false]"},
		{"{ia} {ia:x}", []string{"ia"}, false, "[true]"},
		{"{ia} {s:<8}", []string{"ia", "s"}, true, "[false false]"},
		{"{ia:08} {s}", []string{"ia", "s"}, true, "[true false]"},
		{"{fa:.2}", []string{"fa"}, false, "[true]"},
	} {
		hs, _, p := Scan([]byte(c.f))
		if p != "" {
			t.Fatalf("%q: %s", c.f, p)
		}
		rs, p := Match(hs, fields(c.names...), nil, nil)
		if p != "" {
			t.Fatalf("%q: %s", c.f, p)
		}
		targets, argOf := Args(hs, rs, len(c.names))
		if g := Padded(hs); g != c.padded {
			t.Errorf("%q: Padded %v, want %v", c.f, g, c.padded)
		}
		if g := fmt.Sprint(SpecTargets(hs, argOf, len(targets))); g != c.spec {
			t.Errorf("%q: SpecTargets %s, want %s", c.f, g, c.spec)
		}
	}
}
