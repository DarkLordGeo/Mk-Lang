// Why: past regressions of the server must stay fixed: scope, stale kinds,
// closing, range formatting, ordering, dependants and pull diagnostics.
// What: TestLSPLocalByScope through TestLSPPullReusesCheck, and
// TestLineHunks.
// How: one scripted session per regression.

package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/format"
)

const scopeFixture = `struct Pt {
    x: int
}

G(p: Pt): int {
    x := p.x
    q := Pt(x: x)
    return x + q.x
}

H(n: int): int {
    t := 0
    for i in 0..n {
        t += i
    }
    for i in 0..n {
        t += i
    }
    return t
}

export Main(sys: &System) {
    _ = G(Pt(x: 1))
    _ = H(3)
}
`

// openText writes text as a single-file root and opens it.
func openText(t *testing.T, c *client, name, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	must(t, writeFile(path, text))
	uri := pathURI(path)
	c.open(uri, text)
	c.diagnostics(uri)
	return uri
}

// TestLSPLocalByScope: a local's rename, references and highlights
// take its own tokens only, by scope: not the field of `p.x` or the label
// of `Pt(x: x)` that spell its name, and not the local of a sibling `for
// i` loop.
func TestLSPLocalByScope(t *testing.T) {
	c := start(t)
	c.init(true)
	uri := openText(t, c, "scope.mk", scopeFixture)
	line, col := lineOf(t, scopeFixture, "x := p.x")
	p := at(uri, line, col)
	p["newName"] = "y"
	var we workspaceEdit
	c.result(c.call("textDocument/rename", p), &we)
	var got []Position
	for _, es := range we.Changes {
		for _, e := range es {
			got = append(got, e.Range.Start)
			if e.NewText != "y" {
				t.Errorf("edit %+v", e)
			}
		}
	}
	l2, c2 := lineOf(t, scopeFixture, "Pt(x: x)")
	l3, c3 := lineOf(t, scopeFixture, "return x + q.x")
	want := map[Position]bool{{line, col}: true, {l2, c2 + 6}: true, {l3, c3 + 7}: true}
	if len(got) != len(want) {
		t.Errorf("rename x to y: edits at %v, want %v", got, want)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("rename x to y edits %v, which is not the local x (want %v)", g, want)
		}
	}
	// The first loop's i: its declaration and its one use.
	li, ci := lineOf(t, scopeFixture, "for i in")
	refs := func(method string) []Position {
		t.Helper()
		var out []Position
		if method == "textDocument/references" {
			rp := at(uri, li, ci+4)
			rp["context"] = map[string]any{"includeDeclaration": true}
			var ls []Location
			c.result(c.call(method, rp), &ls)
			for _, l := range ls {
				out = append(out, l.Range.Start)
			}
			return out
		}
		var hs []documentHighlight
		c.result(c.call(method, at(uri, li, ci+4)), &hs)
		for _, h := range hs {
			out = append(out, h.Range.Start)
		}
		return out
	}
	for _, m := range []string{"textDocument/references", "textDocument/documentHighlight"} {
		got := refs(m)
		if len(got) != 2 || got[0] != (Position{li, ci + 4}) || got[1] != (Position{li + 1, 13}) {
			t.Errorf("%s on the first loop's i: %v, want its declaration and its use only", m, got)
		}
	}
	// The second loop's i renames alone.
	p = at(uri, li+3, ci+4)
	p["newName"] = "j"
	we = workspaceEdit{}
	c.result(c.call("textDocument/rename", p), &we)
	n := 0
	for _, es := range we.Changes {
		for _, e := range es {
			n++
			if e.Range.Start.Line < li+3 {
				t.Errorf("rename of the second loop's i edits line %d", e.Range.Start.Line)
			}
		}
	}
	if n != 2 {
		t.Errorf("rename of the second loop's i: %d edits, want 2", n)
	}
	c.shutdown()
}

// semMods are the modifiers of the token at sub+off in text's semantic
// tokens.
func semMods(t *testing.T, c *client, uri, text, sub string, off int) int {
	t.Helper()
	var st semanticTokens
	c.result(c.call("textDocument/semanticTokens/full", doc(uri)), &st)
	line, col := lineOf(t, text, sub)
	got, ok := decodeTokens(st.Data)[[2]int{line, col + off}]
	if !ok {
		t.Fatalf("no semantic token at %q", sub)
	}
	return got[2]
}

// TestLSPStaleKinds: a kind answered from the check before an edit
// that does not parse is not kept: once the buffer checks again, `struct
// Pt` with a Mutex field is unique, as a fresh server says.
func TestLSPStaleKinds(t *testing.T) {
	c, uri := openInferFixture(t)
	withM := strings.Replace(inferFixture, "struct Pt {\n    x: int\n", "struct Pt {\n    m: Mutex[int]\n    x: int\n", 1)
	if withM == inferFixture {
		t.Fatal("fixture changed: no struct Pt")
	}
	_ = semMods(t, c, uri, inferFixture, "struct Pt", 7)
	c.change(uri, 2, withM+"\nBroken( {\n")
	_ = semMods(t, c, uri, withM, "struct Pt", 7)
	c.change(uri, 3, withM)
	c.diagnostics(uri)
	got := semMods(t, c, uri, withM, "struct Pt", 7)
	c.shutdown()

	fresh := start(t)
	fresh.init(true)
	furi := openText(t, fresh, "main.mk", withM)
	want := semMods(t, fresh, furi, withM, "struct Pt", 7)
	fresh.shutdown()
	if want&modUnique == 0 {
		t.Fatalf("fresh server: struct Pt modifiers %b, want unique (the fixture's premise)", want)
	}
	if got != want {
		t.Errorf("struct Pt modifiers after the parse error is fixed: %b, want %b as a fresh server gives", got, want)
	}
}

// lastPublish is the last publishDiagnostics for uri among the
// notifications kept so far, and whether there is one; it drops them.
func (c *client) lastPublish(uri string) (publishDiagnosticsParams, bool) {
	var last publishDiagnosticsParams
	found := false
	var rest []message
	for _, m := range c.notes {
		if m.Method != "textDocument/publishDiagnostics" {
			rest = append(rest, m)
			continue
		}
		var p publishDiagnosticsParams
		if json.Unmarshal(m.Params, &p) == nil && sameURI(p.URI, uri) {
			last, found = p, true
			continue
		}
		rest = append(rest, m)
	}
	c.notes = rest
	return last, found
}

// TestLSPCloseRechecks: closing a buffer with unsaved edits rechecks
// its root from the disk text, so another open file loses the diagnostics
// the discarded text caused.
func TestLSPCloseRechecks(t *testing.T) {
	_, mainURI, utilURI := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	c.diagnostics(mainURI)
	c.open(utilURI, fixtureUtil)
	c.diagnostics(utilURI)
	c.change(utilURI, 2, strings.Replace(fixtureUtil, "export Twice", "export Thrice", 1))
	if d := c.diagnostics(mainURI); len(d.Diagnostics) == 0 {
		t.Fatal("no diagnostic in main.mk for the unsaved rename in util")
	}
	c.notify("textDocument/didClose", doc(utilURI))
	c.call("textDocument/documentSymbol", doc(mainURI))
	d, ok := c.lastPublish(mainURI)
	if !ok || len(d.Diagnostics) != 0 {
		t.Errorf("after closing util: publish %v %+v, want main.mk's diagnostics cleared", ok, d)
	}
	c.shutdown()
}

// TestLSPRangeFormatOtherHunk: a range is formatted although the
// formatter also changes the line count elsewhere in the file.
func TestLSPRangeFormatOtherHunk(t *testing.T) {
	src := "import util\n\n\n\nA() {\n}\n\nB() {\n}\n\nC(): int {\n    return    3\n}\n"
	out, err := format.Source("r4.mk", []byte(src))
	must(t, err)
	if strings.Count(string(out), "\n") == strings.Count(src, "\n") || !strings.Contains(string(out), "    return 3\n") {
		t.Fatalf("premise: the formatter should drop blank lines and fix line 11; got\n%s", out)
	}
	c := start(t)
	c.init(true)
	path := filepath.Join(t.TempDir(), "r4.mk")
	must(t, writeFile(path, src))
	uri := pathURI(path)
	c.open(uri, src)
	var edits []textEdit
	c.result(c.call("textDocument/rangeFormatting", map[string]any{"textDocument": map[string]any{"uri": uri},
		"range": Range{Start: Position{11, 0}, End: Position{12, 0}}, "options": map[string]any{"tabSize": 4, "insertSpaces": true}}), &edits)
	if len(edits) != 1 || edits[0].Range.Start.Line != 11 || edits[0].Range.End.Line != 12 || edits[0].NewText != "    return 3\n" {
		t.Fatalf("range edits %+v, want line 11 set to `    return 3`", edits)
	}
	for _, e := range edits {
		if e.Range.Start.Line <= 3 {
			t.Errorf("an edit outside the range: %+v", e)
		}
	}
	// The whole file as the range: the result is the formatter's.
	c.result(c.call("textDocument/rangeFormatting", map[string]any{"textDocument": map[string]any{"uri": uri},
		"range": Range{Start: Position{0, 0}, End: Position{14, 0}}, "options": map[string]any{"tabSize": 4, "insertSpaces": true}}), &edits)
	if got := applyEdits(src, edits); got != string(out) {
		t.Errorf("whole-file range:\n%s\nwant\n%s", got, out)
	}
	c.shutdown()
}

// TestLineHunks: the diff's hunks, against a brute check that applying
// them gives the new lines.
func TestLineHunks(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"a b c d e", "a c d e"},
		{"a b c", "a x b c y"},
		{"x a b y y c", "a b c"},
		{"", "a b"},
		{"a b", ""},
		{"a b c d", "d c b a"},
	} {
		a, b := strings.Fields(tc.a), strings.Fields(tc.b)
		var got []string
		at := 0
		for _, h := range lineHunks(a, b) {
			got = append(got, a[at:h[0]]...)
			got = append(got, b[h[2]:h[3]]...)
			at = h[1]
		}
		got = append(got, a[at:]...)
		if strings.Join(got, " ") != strings.Join(b, " ") {
			t.Errorf("%q to %q: hunks %v give %q", tc.a, tc.b, lineHunks(a, b), got)
		}
	}
	if h := lineHunks(strings.Fields("a b c d e"), strings.Fields("a c d e")); len(h) != 1 || h[0] != [4]int{1, 2, 1, 1} {
		t.Errorf("one deleted line: %v", h)
	}
}

// TestLSPNavChecks: after an edit, documentHighlight at an item name
// costs at most one check however often it is asked; after the next edit
// it costs none (that edit's check kept the syntax); and on a buffer that
// does not reach types it costs none.
func TestLSPNavChecks(t *testing.T) {
	c, uri := openInferFixture(t)
	line, col := lineOf(t, inferFixture, "p := MakePt(3)")
	highlight := func() int {
		var hs []documentHighlight
		c.result(c.call("textDocument/documentHighlight", at(uri, line, col+5)), &hs)
		return len(hs)
	}
	edit := func(v int, text string) {
		c.change(uri, v, text)
		c.diagnostics(uri)
	}
	text := strings.Replace(inferFixture, "a := 1", "a := 2", 1)
	edit(2, text)
	before := c.srv.checks.Load()
	for range 10 {
		if n := highlight(); n < 2 {
			t.Fatalf("highlight of MakePt: %d, want its declaration and use", n)
		}
	}
	if n := c.srv.checks.Load() - before; n > 1 {
		t.Errorf("10 highlights after an edit ran %d checks, want at most 1", n)
	}
	edit(3, strings.Replace(text, "a := 2", "a := 3", 1))
	before = c.srv.checks.Load()
	for range 10 {
		highlight()
	}
	pr := at(uri, line, col+5)
	c.call("textDocument/prepareRename", pr)
	pr["newName"] = "MakePoint"
	c.call("textDocument/rename", pr)
	if n := c.srv.checks.Load() - before; n != 0 {
		t.Errorf("highlights and a rename after the next edit ran %d checks, want 0 (its check kept the syntax)", n)
	}
	edit(4, text+"\nBroken( {\n")
	before = c.srv.checks.Load()
	for range 5 {
		highlight()
	}
	if n := c.srv.checks.Load() - before; n != 0 {
		t.Errorf("5 highlights on a buffer that does not parse ran %d checks, want 0", n)
	}
	c.shutdown()
}

// TestLSPDeterministicOrder: bare-word completion lists the same
// labels in the same order every time, and the roots one
// didChangeWatchedFiles names are checked in key order.
func TestLSPDeterministicOrder(t *testing.T) {
	text := "import std.fs\nimport j = std.json\nimport std.strings\n\nF(): int {\n    \n    return 0\n}\n"
	c := start(t)
	c.init(true)
	uri := openText(t, c, "order.mk", text)
	var first string
	for i := range 20 {
		var list completionList
		c.result(c.call("textDocument/completion", at(uri, 5, 4)), &list)
		var labels []string
		for _, it := range list.Items {
			labels = append(labels, it.Label)
		}
		got := strings.Join(labels, ",")
		if i == 0 {
			first = got
			if !strings.Contains(got, "fs") || !strings.Contains(got, "j") {
				t.Fatalf("completion %s: no import bindings", got)
			}
		} else if got != first {
			t.Fatalf("completion %d:\n%s\nwant\n%s", i, got, first)
		}
	}
	// Two single-file roots, named in reverse order.
	ws := t.TempDir()
	var uris []string
	for _, d := range []string{"a", "b"} {
		p := filepath.Join(ws, d, d+".mk")
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, writeFile(p, "F(): int {\n    return 1\n}\n"))
		uris = append(uris, pathURI(p))
		c.open(pathURI(p), "F(): int {\n    return 1\n}\n")
		c.diagnostics(pathURI(p))
	}
	for range 3 {
		c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []any{
			map[string]any{"uri": uris[1], "type": 2}, map[string]any{"uri": uris[0], "type": 2}}})
		c.call("textDocument/documentSymbol", doc(uris[0]))
		var order []string
		var rest []message
		for _, m := range c.notes {
			var p publishDiagnosticsParams
			if m.Method == "textDocument/publishDiagnostics" && json.Unmarshal(m.Params, &p) == nil {
				order = append(order, filepath.Base(filepath.Dir(p.URI)))
				continue
			}
			rest = append(rest, m)
		}
		c.notes = rest
		if strings.Join(order, ",") != "a,b" {
			t.Errorf("publish order %v, want a then b", order)
		}
	}
	c.shutdown()
}

// TestLSPDependantsRecheck: a change to a path dependency's buffer, or
// to its file on disk, rechecks the open package that depends on it.
func TestLSPDependantsRecheck(t *testing.T) {
	ws := t.TempDir()
	util := "export Two(): int {\n    return 2\n}\n"
	app := "import u = util.src\n\nexport A(): int {\n    return u.Two()\n}\n"
	files := map[string]string{
		"util/pkg.mk":   depsManifest("util", ""),
		"util/src/u.mk": util,
		"app/pkg.mk":    depsManifest("app", `pkg.Dep(name: "util", path: "../util", url: "", hash: "")`),
		"app/src/a.mk":  app,
	}
	for p, text := range files {
		full := filepath.Join(ws, filepath.FromSlash(p))
		must(t, os.MkdirAll(filepath.Dir(full), 0o755))
		must(t, writeFile(full, text))
	}
	uURI := pathURI(filepath.Join(ws, "util", "src", "u.mk"))
	aURI := pathURI(filepath.Join(ws, "app", "src", "a.mk"))
	c := start(t)
	c.init(true)
	c.open(aURI, app)
	if d := c.diagnostics(aURI); len(d.Diagnostics) != 0 {
		t.Fatalf("app does not check: %+v", d.Diagnostics)
	}
	c.open(uURI, util)
	c.diagnostics(uURI)
	renamed := strings.Replace(util, "Two", "Three", 1)
	c.change(uURI, 2, renamed)
	if d := c.diagnostics(aURI); len(d.Diagnostics) == 0 {
		t.Error("after renaming Two in util's buffer: no diagnostic in app")
	}
	c.change(uURI, 3, util)
	if d := c.diagnostics(aURI); len(d.Diagnostics) != 0 {
		t.Errorf("after the rename is undone: %+v, want none", d.Diagnostics)
	}
	// On disk, with util closed.
	c.notify("textDocument/didClose", doc(uURI))
	c.diagnostics(aURI)
	must(t, writeFile(filepath.Join(ws, "util", "src", "u.mk"), renamed))
	c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": uURI, "type": 2}}})
	if d := c.diagnostics(aURI); len(d.Diagnostics) == 0 {
		t.Error("after renaming Two in util on disk: no diagnostic in app")
	}
	c.shutdown()
}

// TestLSPPullReusesCheck: a pull right after a change answers from the
// change's check, so the text is checked once.
func TestLSPPullReusesCheck(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	c.diagnostics(mainURI)
	before := c.srv.checks.Load()
	c.change(mainURI, 2, strings.Replace(fixtureMain, "self.lo", "nope", 1))
	var rep struct {
		Kind  string          `json:"kind"`
		Items []lspDiagnostic `json:"items"`
	}
	c.result(c.call("textDocument/diagnostic", doc(mainURI)), &rep)
	if len(rep.Items) != 1 || rep.Items[0].Code != "E0201" {
		t.Errorf("pull %+v, want one E0201", rep)
	}
	c.result(c.call("textDocument/diagnostic", doc(mainURI)), &rep)
	if n := c.srv.checks.Load() - before; n != 1 {
		t.Errorf("a change and two pulls ran %d checks, want 1", n)
	}
	c.shutdown()
}
