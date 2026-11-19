// Why: positions and hashes must not depend on line endings or file order.
// What: tests of Normalize, SourceMap text, line and column, slices, path
// checks, file order and concurrent adds.
// How: CRLF and lone CR inputs, bad paths and parallel Add calls.

package source

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, raw, want string }{
		{"plain", "a\nb", "a\nb"},
		{"crlf", "a\r\nb\r\n", "a\nb\n"},
		{"bom", "\xEF\xBB\xBFfn", "fn"},
		{"bom-not-at-start", "x\xEF\xBB\xBF", "x\xEF\xBB\xBF"},
		{"lone-cr-kept", "a\rb", "a\rb"},
		{"cr-cr-lf-once", "a\r\r\nb", "a\r\nb"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Normalize([]byte(c.raw)); got != c.want {
				t.Fatalf("Normalize(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

func TestSourceMapTextIsLFOnly(t *testing.T) {
	t.Parallel()
	m := NewSourceMap()
	f := m.Add("a.mk", []byte("x := 1\r\ny := 2\r\n"))
	if strings.Contains(f.Text, "\r") {
		t.Fatalf("CR left in %q", f.Text)
	}
	if f.Hash != HashString("mk.file.v1", "x := 1\ny := 2\n") {
		t.Fatal("hash is not over the normalized text")
	}
	g := m.Add("b.mk", []byte("x := 1\ny := 2\n"))
	if f.Hash != g.Hash {
		t.Fatal("CRLF and LF files must hash equal")
	}
}

func TestLineColIsOneBasedBytes(t *testing.T) {
	t.Parallel()
	m := NewSourceMap()
	// "é" is 2 bytes, "→" is 3. The file ends in '\n', so line 3 is empty.
	f := m.Add("u.mk", []byte("é→x\nab\n"))
	cases := []struct {
		off  uint32
		want LineCol
	}{
		{0, LineCol{1, 1}},
		{2, LineCol{1, 3}}, // the arrow starts at byte 2
		{5, LineCol{1, 6}}, // x
		{6, LineCol{1, 7}}, // the newline
		{7, LineCol{2, 1}},
		{9, LineCol{2, 3}},
		{10, LineCol{3, 1}}, // end of file, on the empty last line
	}
	for _, c := range cases {
		if got := f.LineCol(c.off); got != c.want {
			t.Errorf("LineCol(%d) = %v, want %v", c.off, got, c.want)
		}
		if off, ok := f.Offset(c.want); !ok || off != c.off {
			t.Errorf("Offset(%v) = %d, %v; want %d", c.want, off, ok, c.off)
		}
	}
	if f.LineCount() != 3 || f.Line(1) != "é→x" || f.Line(2) != "ab" || f.Line(3) != "" {
		t.Fatalf("lines: count %d, %q %q %q", f.LineCount(), f.Line(1), f.Line(2), f.Line(3))
	}
	if _, ok := f.Offset(LineCol{2, 4}); ok {
		t.Error("column past the newline must not resolve")
	}
	if _, ok := f.Offset(LineCol{4, 1}); ok {
		t.Error("line past the end must not resolve")
	}
	if ie := Guard("t", Span{}, func() { f.LineCol(11) }); ie == nil {
		t.Error("offset past end must ICE")
	}
	if got := m.Position(Pos{File: f.ID, Off: 7}).String(); got != "u.mk:2:1" {
		t.Errorf("Position = %q", got)
	}
}

func TestLoneCRIsNotALineBreak(t *testing.T) {
	t.Parallel()
	f := NewSourceMap().Add("cr.mk", []byte("a\rb\nc"))
	if f.LineCount() != 2 || f.LineCol(2) != (LineCol{1, 3}) {
		t.Fatalf("count %d, LineCol(2) %v", f.LineCount(), f.LineCol(2))
	}
}

func TestSourceMapSliceAndText(t *testing.T) {
	t.Parallel()
	m := NewSourceMap()
	f := m.Add("s.mk", []byte("fn Main() {}"))
	if got := m.Text(NewSpan(f.ID, 3, 7)); got != "Main" {
		t.Fatalf("Text = %q", got)
	}
	if m.Text(Span{}) != "" || m.File(NoFile) != nil || m.LineCol(Pos{}) != (LineCol{}) {
		t.Fatal("zero values must map to zero results")
	}
	if ie := Guard("t", Span{}, func() { m.Text(NewSpan(f.ID, 3, 99)) }); ie == nil {
		t.Fatal("span past end must ICE")
	}
	if ie := Guard("t", Span{}, func() { m.File(99) }); ie == nil {
		t.Fatal("unknown FileID must ICE")
	}
}

func TestSourceMapRejectsBadPaths(t *testing.T) {
	t.Parallel()
	m := NewSourceMap()
	m.Add("a.mk", nil)
	for _, p := range []string{"", `a\b.mk`, "a.mk"} {
		if ie := Guard("t", Span{}, func() { m.Add(p, nil) }); ie == nil || ie.Runtime {
			t.Errorf("Add(%q): want ICE, got %+v", p, ie)
		}
	}
}

func TestSourceMapFilesSortedByPath(t *testing.T) {
	t.Parallel()
	m := NewSourceMap()
	for _, p := range []string{"z.mk", "a/b.mk", "B.mk", "a.mk"} {
		m.Add(p, nil)
	}
	var got []string
	for _, f := range m.Files() {
		got = append(got, f.Path)
	}
	if strings.Join(got, " ") != "B.mk a.mk a/b.mk z.mk" || m.Len() != 4 {
		t.Fatalf("Files = %v, Len %d", got, m.Len())
	}
	if f, ok := m.ByPath("a/b.mk"); !ok || m.File(f.ID) != f {
		t.Fatal("ByPath")
	}
}

func TestSourceMapConcurrentAdd(t *testing.T) {
	t.Parallel()
	m := NewSourceMap()
	const n = 3000 // crosses several chunks
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := w; i < n; i += 8 {
				f := m.Add(fmt.Sprintf("f%05d.mk", i), []byte(fmt.Sprint(i)))
				if m.File(f.ID) != f {
					t.Errorf("File(%d) mismatch", f.ID)
				}
			}
		}()
	}
	wg.Wait()
	if m.Len() != n {
		t.Fatalf("Len = %d", m.Len())
	}
	for i, f := range m.Files() {
		if f.Path != fmt.Sprintf("f%05d.mk", i) || f.Text != fmt.Sprint(i) {
			t.Fatalf("file %d = %q %q", i, f.Path, f.Text)
		}
	}
}
