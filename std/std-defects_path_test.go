// Why: `{p}` of a Path whose bytes are not UTF-8 must print one U+FFFD per maximal
// subpart (Unicode 3.9, table 3-8's practice), so a surrogate's WTF-8, ED A0 80, prints three.
// What: TestStdDefects_PathFormatSubparts.
// How: the outputs are pinned bytes, not a model, so a change of the rule shows.

package std

import (
	"strings"
	"testing"
)

func TestStdDefects_PathFormatSubparts(t *testing.T) {
	cases := []struct {
		in   string // the Path's bytes, as Mk array elements
		want string // the bytes {p} prints, each followed by a space
	}{
		// A surrogate's WTF-8: ED's second byte must be 80 to 9F, so each of
		// the three bytes is its own maximal subpart.
		{"0xED, 0xA0, 0x80", "239 191 189 239 191 189 239 191 189 "},
		{"0x61, 0xED, 0xBF, 0xBF, 0x62", "97 239 191 189 239 191 189 239 191 189 98 "},
		// A truncated four-byte sequence is one maximal subpart.
		{"0xF0, 0x9F, 0x98", "239 191 189 "},
		// A lone continuation byte and an overlong lead: one each.
		{"0x80, 0xC0, 0x41", "239 191 189 239 191 189 65 "},
		// Well-formed UTF-8 prints as it is.
		{"0xC3, 0xA9", "195 169 "},
	}
	var lits []string
	for _, c := range cases {
		lits = append(lits, "["+c.in+"]")
	}
	prog := `const Cases: [][]u8 = [` + strings.Join(lits, ", ") + `]

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    a := sys.arena
    for c in Cases {
        p := Path.New(a, c) else {
            return
        }
        var t := Text.New(a)
        var w := t.Writer()
        try Print(mut w, "{p}", (p: &p))
        for b in t.Str().AsBytes() {
            try Print(mut out, "{b} ", (b: b))
        }
        try out.Str("\n")
    }
}
`
	exe := missBuild(t, prog)
	got := strings.Split(strings.TrimSuffix(missRun(t, exe, t.TempDir()), "\n"), "\n")
	if len(got) != len(cases) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(cases), strings.Join(got, "\n"))
	}
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("{p} of [%s] = %q, want %q", c.in, got[i], c.want)
		}
	}
}
