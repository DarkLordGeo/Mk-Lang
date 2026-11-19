// Why: the checked answers must map back to the editor's text after edits.
// What: TestLSPExactTypes, TestTextDiffMapping, TestLSPExactReferences,
// TestLSPExactRename.
// How: checks, edits and asks again; textDiff's mapping is tested directly.

package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exactFixture has locals whose types only the checker proves: an
// operator, a generic call's result, and a field of one.
const exactFixture = `struct Pt {
    x: int
    y: int
}

Pt.Norm(&self): int {
    return self.x + self.y
}

Same[T](x: T): T {
    return x
}

export Main(sys: &System) {
    a := 1
    p := Pt(x: 1, y: 2)
    z := a + 1
    g := Same(p)
    h := Same(p).x
    _ = z
    _ = g
    _ = h
}
`

// TestLSPExactTypes: the inferred types of locals the text cannot prove
// come from the last check that reached the types phase (driver TypeAt),
// as inlay hints, on hover and for member completion; they survive an edit
// that breaks the check, where the edit left the local's declaration
// alone; and no answer starts a session of its own (QueryChecked).
func TestLSPExactTypes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.mk")
	must(t, writeFile(path, exactFixture))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, exactFixture)
	if d := c.diagnostics(uri); len(d.Diagnostics) != 0 {
		t.Fatalf("fixture does not check: %+v", d.Diagnostics)
	}

	hints := func(text string, want map[string]string) {
		t.Helper()
		var hs []inlayHint
		c.result(c.call("textDocument/inlayHint", map[string]any{"textDocument": map[string]any{"uri": uri},
			"range": map[string]any{"start": pos(0, 0), "end": pos(1000, 0)}}), &hs)
		got := map[Position]string{}
		for _, h := range hs {
			if h.Kind == inlayType {
				got[h.Position] = h.Label
			}
		}
		for decl, w := range want {
			line, col := lineOf(t, text, decl)
			if g := got[Position{line, col + len(strings.Fields(decl)[0])}]; g != w {
				t.Errorf("%q: hint %q, want %q (all: %+v)", decl, g, w, hs)
			}
		}
	}
	want := map[string]string{"z := a + 1": ": int", "g := Same": ": Pt", "h := Same": ": int"}
	hints(exactFixture, want)

	line, col := lineOf(t, exactFixture, "g := Same")
	var h hover
	c.result(c.call("textDocument/hover", at(uri, line, col)), &h)
	for _, w := range []string{"(local) g: Pt", "Pt is plain"} {
		if !strings.Contains(h.Contents.Value, w) {
			t.Errorf("hover on g: %s, want %q", h.Contents.Value, w)
		}
	}

	// An edit that breaks the check: `g.` typed on a new line.
	broken := strings.Replace(exactFixture, "    _ = z\n", "    g.\n    _ = z\n", 1)
	c.change(uri, 2, broken)
	if d := c.diagnostics(uri); len(d.Diagnostics) == 0 {
		t.Fatal("the broken text checks")
	}
	hints(broken, want)
	line, col = lineOf(t, broken, "    g.")
	col += 4
	var list completionList
	c.result(c.call("textDocument/completion", at(uri, line, col+2)), &list)
	labels := map[string]bool{}
	for _, it := range list.Items {
		labels[it.Label] = true
	}
	for _, w := range []string{"x", "y", "Norm"} {
		if !labels[w] {
			t.Errorf("completion after g.: %+v, want %q", list.Items, w)
		}
	}
	if n := c.srv.querySessions.Load(); n != 0 {
		t.Errorf("%d sessions started to answer a query after a check reached types", n)
	}
	c.shutdown()
}

// TestTextDiffMapping: an offset maps across one changed region through
// the common prefix and suffix, and not inside the region.
func TestTextDiffMapping(t *testing.T) {
	old := "abc XYZ def"
	cur := "abc XYZ12 def"
	for _, tc := range []struct {
		off, want int
		ok        bool
	}{{0, 0, true}, {7, 7, true}, {8, 0, false}, {9, 7, true}, {13, 11, true}} {
		got, ok := toChecked(old, cur, tc.off)
		if ok != tc.ok || ok && got != tc.want {
			t.Errorf("toChecked(%d) = %d, %v; want %d, %v", tc.off, got, ok, tc.want, tc.ok)
		}
	}
	if got, ok := fromChecked(old, cur, 8); !ok || got != 10 {
		t.Errorf("fromChecked(8) = %d, %v; want 10", got, ok)
	}
	if got, ok := toChecked(old, old, 5); !ok || got != 5 {
		t.Errorf("toChecked on equal texts = %d, %v", got, ok)
	}
}

// refsFixture has two types with a method of one name, so the uses of one
// are not the tokens spelling it.
const refsFixture = `struct Pt {
    x: int
}

struct Qt {
    x: int
}

Pt.Norm(&self): int {
    return self.x
}

Qt.Norm(&self): int {
    return self.x + 1
}

Same[T](x: T): T {
    return x
}

export Main(sys: &System) {
    p := Pt(x: 1)
    q := Qt(x: 2)
    a := p.Norm()
    b := q.Norm()
    c := Same(p).Norm()
    s := "abc"
    _ = a + b + c + s.Len()
}
`

// TestLSPExactReferences: references of a method are its resolved uses
// (driver UsesOf), never another type's method of the same name, from its
// use and from its declaration, and they follow an edit; definition of a
// std method goes where the check resolves it (driver Lookup).
func TestLSPExactReferences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.mk")
	must(t, writeFile(path, refsFixture))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, refsFixture)
	if d := c.diagnostics(uri); len(d.Diagnostics) != 0 {
		t.Fatalf("fixture does not check: %+v", d.Diagnostics)
	}
	refs := func(text, sub string, off int) []int {
		t.Helper()
		line, col := lineOf(t, text, sub)
		var locs []Location
		c.result(c.call("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": uri},
			"position": pos(line, col+off), "context": map[string]any{"includeDeclaration": true}}), &locs)
		var lines []int
		for _, l := range locs {
			if l.Range.End.Character-l.Range.Start.Character != len("Norm") {
				t.Errorf("reference %+v does not span Norm", l)
			}
			lines = append(lines, l.Range.Start.Line)
		}
		return lines
	}
	lineNo := func(text, sub string) int { l, _ := lineOf(t, text, sub); return l }
	want := []int{lineNo(refsFixture, "Pt.Norm"), lineNo(refsFixture, "a := p.Norm"), lineNo(refsFixture, "c := Same")}
	for _, at := range []struct {
		sub string
		off int
	}{{"a := p.Norm", 7}, {"Pt.Norm", 3}} {
		if got := refs(refsFixture, at.sub, at.off); !equalInts(got, want) {
			t.Errorf("references from %q: lines %v, want %v", at.sub, got, want)
		}
	}

	// An edit that still checks: the uses move with it.
	moved := "// moved\n" + refsFixture
	c.change(uri, 2, moved)
	c.diagnostics(uri)
	for i := range want {
		want[i]++
	}
	if got := refs(moved, "a := p.Norm", 7); !equalInts(got, want) {
		t.Errorf("references after the edit: lines %v, want %v", got, want)
	}

	// Definition of a std method: the declaration Lookup names.
	line, col := lineOf(t, moved, "s.Len()")
	var locs []Location
	c.result(c.call("textDocument/definition", at(uri, line, col+2)), &locs)
	if len(locs) != 1 {
		t.Fatalf("definition of s.Len: %+v, want one location", locs)
	}
	p, ok := uriPath(locs[0].URI)
	if !ok {
		t.Fatalf("definition URI %q", locs[0].URI)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	l := newLines(string(raw))
	if a := l.off(locs[0].Range.Start, true); !strings.HasPrefix(l.s[a:], "Len") {
		t.Errorf("definition of s.Len at %s:%d does not name Len", p, locs[0].Range.Start.Line)
	}
	c.shutdown()
}

// TestLSPExactRename: a method that shares its name with another type's
// is renamed at its resolved uses only (the lexical rules refuse it: two
// declarations share the name).
func TestLSPExactRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.mk")
	must(t, writeFile(path, refsFixture))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, refsFixture)
	if d := c.diagnostics(uri); len(d.Diagnostics) != 0 {
		t.Fatalf("fixture does not check: %+v", d.Diagnostics)
	}
	line, col := lineOf(t, refsFixture, "a := p.Norm")
	var we workspaceEdit
	c.result(c.call("textDocument/rename", map[string]any{"textDocument": map[string]any{"uri": uri},
		"position": pos(line, col+7), "newName": "Size"}), &we)
	edited := applyEdits(refsFixture, we.Changes[uri])
	for _, w := range []string{"Pt.Size(&self)", "Qt.Norm(&self)", "a := p.Size()", "b := q.Norm()", "c := Same(p).Size()"} {
		if !strings.Contains(edited, w) {
			t.Errorf("after the rename, no %q in:\n%s", w, edited)
		}
	}
	c.shutdown()
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
