// Why: the prelude Path works on raw OS bytes, so its rules must hold on
// byte strings no well-formed path would contain.
// What: TestSTDMISS_PathMatchesModel (Base, Parent, Join, Str, Format against
// a Go model) and TestSTDMISS_RustPushModel (the model against Rust).
// How: adversarial strings (both separators, drives, roots, ill-formed
// UTF-8: truncated, overlong, surrogates, bytes above F4) and random ones.

package std

import (
	"fmt"
	"math/rand/v2"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func pmIsSep(c byte) bool { return c == '/' || runtime.GOOS == "windows" && c == '\\' }

func pmDrive(b []byte) int {
	if runtime.GOOS != "windows" || len(b) < 2 || b[1] != ':' {
		return 0
	}
	if c := b[0] | 0x20; c >= 'a' && c <= 'z' {
		return 2
	}
	return 0
}

func pmRoot(b []byte) int {
	r := pmDrive(b)
	for r < len(b) && pmIsSep(b[r]) {
		r++
	}
	return r
}

func pmBase(b []byte) []byte {
	r := pmRoot(b)
	end := len(b)
	for end > r && pmIsSep(b[end-1]) {
		end--
	}
	if end == r {
		if r > 0 && pmIsSep(b[r-1]) {
			return b[r-1 : r]
		}
		return []byte(".")
	}
	s := end
	for s > r && !pmIsSep(b[s-1]) {
		s--
	}
	return b[s:end]
}

func pmParent(b []byte) ([]byte, bool) {
	r := pmRoot(b)
	end := len(b)
	for end > r && pmIsSep(b[end-1]) {
		end--
	}
	if end == r {
		return nil, false
	}
	s := end
	for s > r && !pmIsSep(b[s-1]) {
		s--
	}
	if s == r {
		if r == 0 {
			return nil, false
		}
		return b[:r], true
	}
	e := s
	for e > r && pmIsSep(b[e-1]) {
		e--
	}
	return b[:e], true
}

// rustPush is Rust's PathBuf::push, written from its documentation
// (doc.rust-lang.org/std/path/struct.PathBuf.html#method.push) and the
// separator rule of its source (std/src/path.rs, PathBuf::_push), not from
// mem_chan.mk (a model that copies Path.Join's own rule cannot catch a
// bug in it). The rules:
//
//   - "if path is absolute, it replaces the current path". On Windows a
//     path is absolute when it has a prefix and a root; and _push also
//     truncates when path has any prefix ("if path has a prefix but no
//     root, it replaces self").
//   - "if path has a root but no prefix (e.g., \windows), it replaces
//     everything except for the prefix (if any) of self" (Windows; on Unix
//     a root makes the path absolute, so it replaces self).
//   - otherwise path is appended, after one MAIN_SEPARATOR when self is
//     non-empty and does not end in a separator, except when self is a
//     drive alone ("C:").
//
// Two deliberate differences, which are Mk's documented choices and are
// marked here so the model is still a statement of Rust's rule: Path
// knows only drive prefixes (no UNC or verbatim prefixes), and an empty
// part gives a copy of self where Rust appends a separator.
func rustPush(self []byte, part string) []byte {
	p := []byte(part)
	if len(p) == 0 {
		return append([]byte{}, self...) // Mk: a copy (Rust: self + separator)
	}
	windows := runtime.GOOS == "windows"
	isSep := func(c byte) bool { return c == '/' || windows && c == '\\' }
	prefix := func(b []byte) int { // a drive prefix "X:", Windows only
		if windows && len(b) >= 2 && b[1] == ':' && (b[0]|0x20) >= 'a' && (b[0]|0x20) <= 'z' {
			return 2
		}
		return 0
	}
	if prefix(p) > 0 {
		return p // a prefix, with or without a root: replaces self
	}
	if isSep(p[0]) {
		// a root and no prefix: keep self's prefix (none on Unix)
		return append(append([]byte{}, self[:prefix(self)]...), p...)
	}
	out := append([]byte{}, self...)
	needSep := len(self) > 0 && !isSep(self[len(self)-1])
	if prefix(self) == 2 && len(self) == 2 {
		needSep = false // "in the special case of `C:` on Windows, do *not* add a separator"
	}
	if needSep {
		if windows {
			out = append(out, '\\') // MAIN_SEPARATOR
		} else {
			out = append(out, '/')
		}
	}
	return append(out, p...)
}

// TestSTDMISS_RustPushModel pins the model itself to Rust's documented
// examples and the Windows drive and root inputs, so a transcription error in it shows
// here and not only as a disagreement with Path.Join.
func TestSTDMISS_RustPushModel(t *testing.T) {
	type tc struct{ self, part, want string }
	cases := []tc{
		{"/tmp", "/etc", "/etc"}, // Rust's doc example
		{"", "a", "a"},
		{"a/", "b", "a/b"},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			tc{"/tmp", "file.bk", `/tmp\file.bk`}, // MAIN_SEPARATOR is `\` there
			tc{`C:\x`, `\y`, `C:\y`},              // a rooted part keeps the drive
			tc{`C:\x`, `/y`, `C:/y`},
			tc{`C:`, `x`, `C:x`},
			tc{`C:`, `\y`, `C:\y`},
			tc{`\x`, `\y`, `\y`},
			tc{`C:\x`, `D:y`, `D:y`},
			tc{`C:\x`, `D:\y`, `D:\y`},
			tc{`C:\windows`, `system32`, `C:\windows\system32`},
		)
	} else {
		cases = append(cases, tc{"/a", `\y`, `/a/\y`},
			tc{"/tmp", "file.bk", "/tmp/file.bk"}) // Rust's doc example
	}
	for _, c := range cases {
		if got := string(rustPush([]byte(c.self), c.part)); got != c.want {
			t.Errorf("rustPush(%q, %q) = %q, want %q", c.self, c.part, got, c.want)
		}
	}
}

// pmFormat replaces each maximal subpart (Unicode §3.9, "U+FFFD
// Substitution of Maximal Subparts") by U+FFFD, by table 3-7 written out
// again here. Go's utf8.DecodeRune is not this: it gives one RuneError per
// byte of a truncated sequence ("â" is two), where the Unicode
// practice gives one.
func pmFormat(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			out = append(out, c)
			i++
			continue
		}
		need, lo, hi := 0, byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c == 0xE0:
			need, lo = 2, 0xA0
		case c >= 0xE1 && c <= 0xEC, c == 0xEE, c == 0xEF:
			need = 2
		case c == 0xED:
			need, hi = 2, 0x9F
		case c == 0xF0:
			need, lo = 3, 0x90
		case c >= 0xF1 && c <= 0xF3:
			need = 3
		case c == 0xF4:
			need, hi = 3, 0x8F
		}
		k := 1
		for ; k <= need && i+k < len(b); k++ {
			d := b[i+k]
			if k == 1 && (d < lo || d > hi) || k > 1 && (d < 0x80 || d > 0xBF) {
				break
			}
		}
		if need > 0 && k == need+1 {
			out = append(out, b[i:i+k]...)
		} else {
			out = append(out, "�"...)
		}
		i += k
	}
	return out
}

func pmBytes(b []byte) string {
	var parts []string
	for _, c := range b {
		parts = append(parts, fmt.Sprint(c))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

var pmParts = []string{"x", "", "/y", "D:z", `\w`, "a/b", `D:\v`, "C:", `\\u`}

func TestSTDMISS_PathMatchesModel(t *testing.T) {
	// Unicode's Table 3-8: 61 F1 80 80 E1 80 C2 62 80 63 80 BF 64 is
	// a FFFD FFFD FFFD b FFFD c FFFD FFFD d.
	if got, want := string(pmFormat([]byte("a\xf1\x80\x80\xe1\x80\xc2b\x80c\x80\xbfd"))), "a���b�c��d"; got != want {
		t.Fatalf("the model gives %q for Table 3-8, want %q", got, want)
	}
	cases := []string{"a\xf1\x80\x80\xe1\x80\xc2b\x80c\x80\xbfd", "", ".", "a", "/", "//", "///", "a/", "a//", "/a", "//a", "a/b", "a//b/", "/a/b/c",
		`\`, `a\b`, `\\server\share\x`, `C:`, `C:\`, `C:/`, `C:x`, `c:\x\y\`, `1:\x`, `C:\\`, `:`, `ab:`,
		"\xff", "a\xc0\x80b", "\xf0\x9f\x98", "\xed\xa0\x80/x", "\xf4\x90\x80\x80", "\xe2\x82", "é/ü", "\xf0\x9f\x98\x80/z"}
	src := rand.New(rand.NewPCG(7, 8))
	alphabet := []byte{'a', '/', '\\', ':', 'C', 0xC3, 0xA9, 0xFF, 0xE2, 0x82, 0xAC, 0xF0, 0x80}
	for range 300 {
		n := src.IntN(9)
		var b []byte
		for range n {
			b = append(b, alphabet[src.IntN(len(alphabet))])
		}
		cases = append(cases, string(b))
	}
	var lits []string
	for _, c := range cases {
		lits = append(lits, pmBytes([]byte(c)))
	}
	var partLits []string
	for _, p := range pmParts {
		partLits = append(partLits, fmt.Sprintf("%q", p))
	}
	prog := `const Cases: [][]u8 = [` + strings.Join(lits, ", ") + `]
const Parts: []str = [` + strings.Join(partLits, ", ") + `]

PutBytes(out: mut Writer, tag: str, b: []u8): WriteError!void {
    try out.Str(tag)
    try out.Str(" [")
    var first := true
    for c in b {
        if !first {
            try out.Str(",")
        }
        first = false
        try Print(out, "{c}", (c: c))
    }
    try out.Str("]\n")
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    a := sys.arena
    for c in Cases {
        p := Path.New(a, c) else {
            return
        }
        b := p.Base()
        try PutBytes(mut out, "b", b.AsBytes())
        q := p.Parent() else {
            try out.Str("p none\n")
            continue
        }
        try PutBytes(mut out, "p", q.AsBytes())
    }
    for c in Cases {
        p := Path.New(a, c) else {
            return
        }
        for part in Parts {
            j := p.Join(a, part) else {
                return
            }
            try PutBytes(mut out, "j", j.AsBytes())
        }
        if p.Str().IsSome() {
            try out.Str("s 1\n")
        } else {
            try out.Str("s 0\n")
        }
        var t := Text.New(a)
        var w := t.Writer()
        try Print(mut w, "{p}", (p: &p))
        try PutBytes(mut out, "f", t.Str().AsBytes())
    }
}
`
	exe := missBuild(t, prog)
	got := strings.Split(strings.TrimSuffix(missRun(t, exe, t.TempDir()), "\n"), "\n")
	var want []string
	for _, c := range cases {
		b := []byte(c)
		want = append(want, "b "+pmBytes(pmBase(b)))
		if q, ok := pmParent(b); ok {
			want = append(want, "p "+pmBytes(q))
		} else {
			want = append(want, "p none")
		}
	}
	for _, c := range cases {
		b := []byte(c)
		for _, part := range pmParts {
			want = append(want, "j "+pmBytes(rustPush(b, part)))
		}
		if utf8.Valid(b) {
			want = append(want, "s 1")
		} else {
			want = append(want, "s 0")
		}
		want = append(want, "f "+pmBytes(pmFormat(b)))
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	bad := 0
	for i := range want {
		if got[i] != want[i] {
			bad++
			if bad <= 20 {
				t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
			}
		}
	}
}
