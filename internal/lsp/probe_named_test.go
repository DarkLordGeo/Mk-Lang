// Why: two dense lines of std/compress/compress.mk exercise every kind of name.
// What: the named probes: what each token's hover says and where its definition
// lands.
// How: opens the file under --std and asks every token.

package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPCompressProbes(t *testing.T) {
	std, err := filepath.Abs(filepath.Join("..", "..", "std"))
	must(t, err)
	path := filepath.Join(std, "compress", "compress.mk")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no std/compress in this tree")
	}
	text := string(raw)
	uri := pathURI(path)
	c := startWith(t, Options{StdDir: std, CacheDir: testCache, Jobs: 4})
	c.init(true)
	c.open(uri, text)
	c.diagnostics(uri)
	// The library chapter's own line for Slice[T].Len, as the reference
	// has it now.
	lenLine := surfaceDocs["Slice.Len"].Lines
	if len(lenLine) == 0 {
		t.Fatal("the library surface lists no Slice[T].Len")
	}
	type probe struct {
		line, at string
		off      int
		hover    []string
		def      string // file suffix and the line it must hold
	}
	for _, p := range []probe{
		// compress.mk:70 `fail Corrupt(offset: d.c.eoff)`.
		{"fail Corrupt(offset: d.c.eoff)", "fail", 0, []string{"`fail e`: Return the error `e`", "In `Drive`, whose result is `Error!mut []u8`", "Error = Corrupt | Truncated | TooLarge | OutOfMemory"}, ""},
		{"fail Corrupt(offset: d.c.eoff)", "Corrupt", 0, []string{"error Corrupt", "offset: int", "**In the error sets**", "Error = Corrupt | Truncated | TooLarge | OutOfMemory"}, "compress/compress.mk|export error Corrupt(offset: int)"},
		{"fail Corrupt(offset: d.c.eoff)", "offset", 0, []string{"(field) offset: int", "Payload of `error Corrupt`"}, "compress/compress.mk|export error Corrupt(offset: int)"},
		{"fail Corrupt(offset: d.c.eoff)", "d.c", 0, []string{"(param) d: mut Dec", "A `mut` loan the caller passes", "**Dec**", "c: Cur"}, "compress/compress.mk|Drive(a: &Arena, d: mut Dec"},
		{"fail Corrupt(offset: d.c.eoff)", "c.eoff", 0, []string{"(field) c: Cur", "In `struct Dec`", "**Cur**", "eoff: int"}, "compress/inflate.mk|    c: Cur"},
		{"fail Corrupt(offset: d.c.eoff)", "eoff", 0, []string{"(field) eoff: int", "In `struct Cur`"}, "compress/inflate.mk|    eoff: int"},
		// compress.mk:77 `cap := out.Len()`.
		{"cap := out.Len()", "cap", 0, []string{"(local) cap: int"}, "compress/compress.mk|cap := out.Len()"},
		{"cap := out.Len()", "out", 0, []string{"(local) out: mut []u8", "Holds a loan from `buf`"}, "compress/compress.mk|var out := buf"},
		{"cap := out.Len()", "Len", 0, []string{"Len(&self): int", "Here on `out: mut []u8`", "module std.core", lenLine[0]}, "core/core_slice.mk|export Slice[T].Len(&self): int"},
	} {
		line, col := lineOf(t, text, p.line)
		col += strings.Index(p.line, p.at) + p.off
		var h *hover
		c.result(c.call("textDocument/hover", at(uri, line, col)), &h)
		got := ""
		if h != nil {
			got = h.Contents.Value
		}
		for _, w := range p.hover {
			if !strings.Contains(got, w) {
				t.Errorf("hover on %q in %q lacks %q:\n%s", p.at, p.line, w, got)
			}
		}
		if p.def == "" {
			continue
		}
		file, want, _ := strings.Cut(p.def, "|")
		var ls []Location
		c.result(c.call("textDocument/definition", at(uri, line, col)), &ls)
		ok := false
		for _, l := range ls {
			lp, _ := uriPath(l.URI)
			if !strings.HasSuffix(filepath.ToSlash(lp), "std/"+file) {
				continue
			}
			lr, _ := os.ReadFile(lp)
			if strings.Contains(newLines(string(lr)).line(l.Range.Start.Line), want) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("definition of %q in %q: %+v, want std/%s at %q", p.at, p.line, ls, file, want)
		}
	}
	// The inferred type of cap as an inlay hint.
	line, col := lineOf(t, text, "cap := out.Len()")
	var hints []struct {
		Position Position
		Label    string
	}
	c.result(c.call("textDocument/inlayHint", map[string]any{"textDocument": map[string]any{"uri": uri},
		"range": map[string]any{"start": pos(line, 0), "end": pos(line+1, 0)}}), &hints)
	found := false
	for _, h := range hints {
		if h.Position.Line == line && h.Position.Character == col+3 && strings.Contains(h.Label, "int") {
			found = true
		}
	}
	if !found {
		t.Errorf("no `: int` inlay hint after cap: %+v", hints)
	}
	c.shutdown()
}
