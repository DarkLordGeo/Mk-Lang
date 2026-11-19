// Why: str.Replace and Text.PushReplaced follow Go's strings.ReplaceAll
// rules, and their inlined release forms need an oracle beyond Mk's own tests.
// What: TestCOREREPL_ReplaceMatchesGo and FuzzCOREREPL_Replace.
// How: one release driver answers many cases; strings.ReplaceAll judges.

// The differential test and fuzz target of str.Replace and
// Text.PushReplaced, built --release (the inlined forms; the dev forms are
// tests_text.mk's Replace blocks against a naive oracle), against Go's
// strings.ReplaceAll, whose rules they take: the leftmost match first, no
// overlap, the replacement never searched, and an empty old between
// scalars. go test runs the cases and the seeds; go test -fuzz
// FuzzCOREREPL_Replace -parallel 2 explores (at most 5 minutes a run).

package std

import (
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// coreReplDriver reads cases of three lines, the text, old and new, each in
// hex after an x, and writes one line per case: the result of str.Replace
// in hex after an x, then t=1 when Text.PushReplaced, appending to a text
// that holds "<" and was cleared and reused, gave "<" and the same bytes.
const coreReplDriver = `Nib(c: u8): u8 {
    if c >= 97 {
        return c - 87
    }
    return c - 48
}

Hex(a: &Arena, line: str): Utf8Error|OutOfMemory!str from a {
    b := line.AsBytes()
    z := try a.Slice[u8]((b.Len() - 1) / 2)
    for k in 0..z.Len() {
        z[k] = (Nib(b[1 + 2 * k]) << 4) | Nib(b[2 + 2 * k])
    }
    return try str.From(z[..])
}

const Digits = "0123456789abcdef"

Case(a: &Arena, t: mut Text, out: mut Writer, lines: mut Lines, first: str): WriteError|Utf8Error|OutOfMemory!void {
    s := try Hex(a, first)
    old := try Hex(a, lines.Next().Must())
    new := try Hex(a, lines.Next().Must())
    r := try s.Replace(a, old, new)
    try out.Str("x")
    for c in r.AsBytes() {
        var d: [2]u8 = [Digits.Byte(Cast[int](c >> 4)), Digits.Byte(Cast[int](c & 15))]
        try out.Bytes(d[..])
    }
    t.Clear()
    try t.Push("<")
    try t.PushReplaced(s, old, new)
    same := t.Str().Len() == r.Len() + 1 && t.Str()[1..] == r
    if same {
        try out.Str(" t=1\n")
    } else {
        try out.Str(" t=0\n")
    }
}

export Main(sys: &System): ReadError|WriteError|Utf8Error|OutOfMemory!void {
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 26)
    n := try inp.ReadAll(buf)
    text := try str.From(buf[0..n])
    var out := sys.Stdout()
    var lines := text.Lines()
    var w := try sys.arena.Sub()
    defer w.Free()
    var t := Text.New(sys.arena)
    for first := lines.Next() {
        try Case(&w, mut t, mut out, mut lines, first)
        w.Reset()
    }
    try out.Flush()
}
`

// coreReplCase is one case's input lines and the model's output line.
func coreReplCase(s, old, new string) (in, want string) {
	in = "x" + hex.EncodeToString([]byte(s)) + "\nx" + hex.EncodeToString([]byte(old)) + "\nx" + hex.EncodeToString([]byte(new)) + "\n"
	return in, "x" + hex.EncodeToString([]byte(strings.ReplaceAll(s, old, new))) + " t=1"
}

// coreReplBuild builds the driver --release over a copy of the live std in
// dir and returns it, or skips when the toolchain is missing.
func coreReplBuild(t testing.TB, dir string) string {
	t.Helper()
	stdDir := copyStd(t, dir, nil)
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(coreReplDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	out, status := mkCmd(t, dir, stdDir, "build", "--release", "-o", exe, "main.mk")
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// coreReplPieces are the scalars of random texts: few distinct ASCII
// letters, so matches are frequent, periodic and overlapping, and scalars
// of 2 to 4 bytes, two of them sharing the continuation byte A9.
var coreReplPieces = []string{"a", "b", "a", "c", "é", "©", "€", "\U0001F600", "a", "b"}

func coreReplText(r *rand.Rand, pieces int) string {
	var b strings.Builder
	for i := 0; i < pieces; i++ {
		b.WriteString(coreReplPieces[r.Intn(len(coreReplPieces))])
	}
	return b.String()
}

// coreReplRun runs the cases through exe and reports each line that differs.
func coreReplRun(t *testing.T, exe string, cases [][3]string) {
	t.Helper()
	var in, want strings.Builder
	for _, c := range cases {
		ci, cw := coreReplCase(c[0], c[1], c[2])
		in.WriteString(ci)
		want.WriteString(cw + "\n")
	}
	got := runNative(t, exe, in.String())
	if got.code != 0 {
		t.Fatalf("driver exit %d\n%s", got.code, got.stderr)
	}
	gl := strings.Split(strings.ReplaceAll(got.stdout, "\r\n", "\n"), "\n")
	wl := strings.Split(want.String(), "\n")
	if len(gl) != len(wl) {
		t.Fatalf("driver wrote %d lines, want %d", len(gl), len(wl))
	}
	bad := 0
	for i := range wl {
		if gl[i] != wl[i] {
			if bad < 5 {
				c := cases[i]
				t.Errorf("case %d (Replace(%q, %q, %q)): got %.200q, want %.200q", i, c[0], c[1], c[2], gl[i], wl[i])
			}
			bad++
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d cases differ", bad, len(wl)-1)
	}
}

// TestCOREREPL_ReplaceMatchesGo runs 4,000 random cases, the edge cases of
// those rules, and four inputs of 1.25 to 4 MiB, in one run of the release
// driver.
func TestCOREREPL_ReplaceMatchesGo(t *testing.T) {
	t.Parallel()
	exe := coreReplBuild(t, t.TempDir())
	cases := [][3]string{
		{"", "", ""}, {"", "", "x"}, {"", "a", "x"}, {"abc", "", "-"},
		{"hé€\U0001F600", "", "|"}, {"aaa", "aa", "b"}, {"aaaa", "aa", "b"},
		{"abababa", "aba", "X"}, {"aaa", "a", "aa"}, {"abab", "ab", "aab"}, {"aab", "ab", "a"},
		{"abc", "abc", ""}, {"ab", "abc", "X"}, {"éé", "©", "X"},
		{"é©é", "©", "X"}, {"a-b--c", "-", ""},
	}
	r := rand.New(rand.NewSource(833))
	for i := 0; i < 4000; i++ {
		s := coreReplText(r, r.Intn(80))
		var old string
		switch i % 4 {
		case 0: // a piece of the text, so it matches at least once
			if p := []rune(s); len(p) > 0 {
				a := r.Intn(len(p))
				old = string(p[a : a+r.Intn(min(len(p)-a, 12))+1])
			}
		case 1: // a periodic needle, as Two-Way's short-period case
			old = strings.Repeat(coreReplText(r, 1+r.Intn(2)), 1+r.Intn(6))
		default:
			old = coreReplText(r, r.Intn(5))
		}
		new := coreReplText(r, r.Intn(6))
		cases = append(cases, [3]string{s, old, new})
	}
	line := "the cat sat on the mat, café €5 \U0001F600\n"
	big := strings.Repeat(line, 1<<15)
	cases = append(cases,
		[3]string{big, "cat", "elephant"},
		[3]string{big, "\n", ""},
		[3]string{strings.Repeat("a", 1<<22) + "b", "aaaaaaab", "X"},
		[3]string{strings.Repeat("ab", 1<<20), "abab", "c"},
	)
	coreReplRun(t, exe, cases)
}

func FuzzCOREREPL_Replace(f *testing.F) {
	f.Add("aaa", "aa", "b")
	f.Add("abc", "", "-")
	f.Add("hé€\U0001F600", "€", "©")
	f.Add(strings.Repeat("ab", 40)+"c", "ababc", "")
	f.Add("é©", "©", "xx")
	dir, err := os.MkdirTemp("", "mk-corerepl-fuzz")
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { os.RemoveAll(dir) })
	exe := coreReplBuild(f, dir)
	f.Fuzz(func(t *testing.T, s, old, new string) {
		if !utf8.ValidString(s) || !utf8.ValidString(old) || !utf8.ValidString(new) {
			return
		}
		if len(s) > 1<<14 {
			s = s[:0]
		}
		if len(old) > 1<<8 || len(new) > 1<<8 {
			return
		}
		coreReplRun(t, exe, [][3]string{{s, old, new}})
	})
}
