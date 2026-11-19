// Why: named answers that must stay right: deep hovers, implementations, imports.
// What: the probe tests on the repository's std and on a user package.
// How: each probe opens a file, asks one request at a named token and checks the
// target.

package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLSPStdProbes(t *testing.T) {
	std, err := filepath.Abs(filepath.Join("..", "..", "std"))
	must(t, err)
	if _, err := os.Stat(filepath.Join(std, "compress", "inflate.mk")); err != nil {
		t.Skip("no std/compress in this tree")
	}
	c := startWith(t, Options{StdDir: std, CacheDir: testCache, Jobs: 4})
	c.init(true)
	read := func(rel string) (string, string) {
		p := filepath.Join(std, filepath.FromSlash(rel))
		raw, err := os.ReadFile(p)
		must(t, err)
		return pathURI(p), string(raw)
	}
	open := func(rel string) (string, string) {
		uri, text := read(rel)
		start := time.Now()
		c.open(uri, text)
		c.diagnostics(uri)
		t.Logf("%s: open to diagnostics %v (this test's run)", rel, time.Since(start))
		return uri, text
	}
	locs := func(method, uri string, line, col int) []Location {
		var out []Location
		c.result(c.call(method, at(uri, line, col)), &out)
		return out
	}
	hoverAt := func(uri string, line, col int) string {
		var h *hover
		c.result(c.call("textDocument/hover", at(uri, line, col)), &h)
		if h == nil {
			return ""
		}
		return h.Contents.Value
	}
	endsWith := func(ls []Location, file string, line int) bool {
		for _, l := range ls {
			if p, ok := uriPath(l.URI); ok && strings.HasSuffix(filepath.ToSlash(p), file) && (line < 0 || l.Range.Start.Line == line) {
				return true
			}
		}
		return false
	}

	// Go to implementation on Run, a free function, from
	// its call in compress.mk, is its body in inflate.mk, not std.proc's
	// exported Run. The line is found by its text, so a rewrite of
	// inflate.mk does not move the pin.
	_, infSrc := read("compress/inflate.mk")
	runLine, _ := lineOf(t, infSrc, "Run(d: mut Dec")
	deflate, deflateText := read("compress/deflate.mk")
	start := time.Now()
	c.open(deflate, deflateText)
	line, col := lineOf(t, deflateText, "raw.Ctz64")
	first := hoverAt(deflate, line, col+5)
	t.Logf("compress/deflate.mk (%d lines): open to the first hover %v (this test's run)", strings.Count(deflateText, "\n"), time.Since(start))
	if !strings.Contains(first, "Ctz64") {
		t.Errorf("first hover on raw.Ctz64: %q", first)
	}
	cm, cmText := open("compress/compress.mk")
	line, col = lineOf(t, cmText, "Run(d, data, out)")
	for _, m := range []string{"textDocument/implementation", "textDocument/definition"} {
		if got := locs(m, cm, line, col+1); !endsWith(got, "std/compress/inflate.mk", runLine) {
			t.Errorf("%s on Run: %+v, want std/compress/inflate.mk:%d", m, got, runLine+1)
		}
	}
	if h := hoverAt(cm, line, col+1); !strings.Contains(h, "Run(d: mut Dec, src: []u8, out: mut []u8): int") || strings.Contains(h, "std.proc") {
		t.Errorf("hover on Run:\n%s", h)
	}

	// An unexported std struct: checked as std, its layout, kind,
	// fields with their types, and its comment.
	inf, infText := open("compress/inflate.mk")
	line, col = lineOf(t, infText, "struct Dec {")
	h0 := hoverAt(inf, line, col+8)
	for _, w := range []string{"lit: [2342]u32", "stored: int", "module std.compress", "**Layout:** size", "kind plain", "Dec is a decoder"} {
		if !strings.Contains(h0, w) {
			t.Errorf("hover on Dec lacks %q:\n%s", w, h0)
		}
	}
	// References to the unexported Run, from its declaration: both callers.
	line, col = lineOf(t, infText, "Run(d: mut Dec, src: []u8, out: mut []u8): int {")
	var refs []Location
	c.result(c.call("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": inf},
		"position": pos(line, col), "context": map[string]any{"includeDeclaration": true}}), &refs)
	for _, want := range []string{"std/compress/compress.mk", "std/compress/inflater.mk"} {
		if !endsWith(refs, want, -1) {
			t.Errorf("references of Run: %+v, want a use in %s", refs, want)
		}
	}
	// An exported std struct: the checked layout and members.
	ifr, ifrText := open("compress/inflater.mk")
	line, col = lineOf(t, ifrText, "export view struct Inflater")
	h := hoverAt(ifr, line, col+20)
	for _, w := range []string{"**Layout:** size", "align 8", "kind unique", "**Members**", "New(a: &Arena): Inflater", "Read(mut self"} {
		if !strings.Contains(h, w) {
			t.Errorf("hover on Inflater lacks %q:\n%s", w, h)
		}
	}
	// An import binding: hover names the module, definition opens it.
	line, col = lineOf(t, deflateText, "raw.Ctz64")
	if h := hoverAt(deflate, line, col); !strings.Contains(h, "import std.raw") {
		t.Errorf("hover on the binding raw:\n%s", h)
	}
	if got := locs("textDocument/definition", deflate, line, col); !endsWith(got, "std/raw/raw.mk", 0) {
		t.Errorf("definition of the binding raw: %+v", got)
	}

	// An interface member to its impls, and an impl's method back.
	iface, ifaceText := open("core/core_iface.mk")
	line, col = lineOf(t, ifaceText, "    Items(&self): []T")
	if got := locs("textDocument/implementation", iface, line, col+4); !endsWith(got, "std/core/coll_list.mk", -1) {
		t.Errorf("implementation of Slice.Items: %+v, want List's in coll_list.mk", got)
	}
	list, listText := open("core/coll_list.mk")
	line, col = lineOf(t, listText, "    Items(&self): []T {")
	if got := locs("textDocument/implementation", list, line, col+4); !endsWith(got, "std/core/core_iface.mk", -1) {
		t.Errorf("implementation of List's Items: %+v, want Slice's member in core_iface.mk", got)
	}
	c.shutdown()
}

const p1UserFixture = `/// A point.
struct Point {
    /// The x.
    x: int
    y: f64
    name: str
}

Point.Norm(&self): f64 {
    return self.y
}

interface Shape {
    Area(&self): f64
}

impl Shape for Point {
    Area(&self): f64 {
        return self.y
    }
}

export Main(sys: &System) {
    p := Point(x: 1, y: 2.0, name: "a")
    _ = p.Norm()
    _ = p.Area()
}
`

// TestLSPUserTypeHover: a user struct's hover holds its layout, kinds,
// fields, methods and impls; a construction label goes to its field.
func TestLSPUserTypeHover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.mk")
	must(t, writeFile(path, p1UserFixture))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, p1UserFixture)
	c.diagnostics(uri)
	line, col := lineOf(t, p1UserFixture, "p := Point(")
	var h hover
	c.result(c.call("textDocument/hover", at(uri, line, col+6)), &h)
	for _, w := range []string{"struct Point", "A point.", "Point is plain", "**Layout:** size 32, align 8", "kind plain", "x: int", "name: str", "Norm(&self): f64", "impl Shape"} {
		if !strings.Contains(h.Contents.Value, w) {
			t.Errorf("hover on Point lacks %q:\n%s", w, h.Contents.Value)
		}
	}
	line, col = lineOf(t, p1UserFixture, "name: \"a\"")
	var ls []Location
	c.result(c.call("textDocument/definition", at(uri, line, col)), &ls)
	if dl, _ := lineOf(t, p1UserFixture, "    name: str"); len(ls) != 1 || ls[0].Range.Start.Line != dl {
		t.Errorf("definition of the label name: %+v, want the field on line %d", ls, dl+1)
	}
	line, col = lineOf(t, p1UserFixture, "    Area(&self): f64 {")
	c.result(c.call("textDocument/implementation", at(uri, line, col+4)), &ls)
	if il, _ := lineOf(t, p1UserFixture, "interface Shape {"); len(ls) != 1 || ls[0].Range.Start.Line != il+1 {
		t.Errorf("implementation of the impl's Area: %+v, want the interface member on line %d", ls, il+2)
	}
	c.shutdown()
}
