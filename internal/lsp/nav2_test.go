// Why: the navigation requests must land on the right declarations.
// What: TestLSPTypeDefinition, TestLSPImplementation, TestLSPCallHierarchy,
// TestLSPSelectionRange.
// How: a shapes fixture with an interface and impls, asked at each name.

package lsp

import (
	"path/filepath"
	"testing"
)

const shapesFixture = `interface Shape {
    Area(&self): f64
}

struct Square {
    side: f64
}

struct Box {
    sq: Square
}

impl Shape for Square {
    Area(&self): f64 {
        return Twice(self.side) * self.side
    }
}

Twice(x: f64): f64 {
    return x * 2.0
}

Larger[T: Shape](a: &T, b: &T): bool {
    return a.Area() > b.Area()
}

Main() {
    s := Square(side: 1.0)
    bx := Box(sq: s)
    _ = Twice(bx.sq.side)
    _ = Larger(&s, &s)
}
`

func openShapes(t *testing.T) (*client, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "shapes.mk")
	must(t, writeFile(path, shapesFixture))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, shapesFixture)
	c.diagnostics(uri)
	return c, uri
}

func locsAt(t *testing.T, c *client, method, uri, sub string, off int) []Location {
	t.Helper()
	line, col := lineOf(t, shapesFixture, sub)
	var locs []Location
	c.result(c.call(method, at(uri, line, col+off)), &locs)
	return locs
}

func wantAt(t *testing.T, what string, locs []Location, subs ...string) {
	t.Helper()
	if len(locs) != len(subs) {
		t.Errorf("%s: %+v, want %d locations", what, locs, len(subs))
		return
	}
	for i, sub := range subs {
		line, col := lineOf(t, shapesFixture, sub)
		if locs[i].Range.Start != (Position{line, col}) {
			t.Errorf("%s: location %d at %+v, want %q at %d:%d", what, i, locs[i].Range.Start, sub, line, col)
		}
	}
}

// TestLSPTypeDefinition: a local's, a parameter's (a generic one too) and a field's type.
func TestLSPTypeDefinition(t *testing.T) {
	c, uri := openShapes(t)
	wantAt(t, "type of bx.sq", locsAt(t, c, "textDocument/typeDefinition", uri, "bx.sq.side)", 3), "Square {")
	wantAt(t, "type of bx", locsAt(t, c, "textDocument/typeDefinition", uri, "bx.sq.side)", 0), "Box {")
	wantAt(t, "type of a (generic)", locsAt(t, c, "textDocument/typeDefinition", uri, "a.Area() >", 0), "T: Shape")
	c.shutdown()
}

// TestLSPImplementation: an interface to its impl, an interface method to
// the impl's method, a type to the impls for it, an impl to its interface.
func TestLSPImplementation(t *testing.T) {
	c, uri := openShapes(t)
	wantAt(t, "Shape", locsAt(t, c, "textDocument/implementation", uri, "Shape {", 0), "impl Shape for")
	wantAt(t, "Shape.Area", locsAt(t, c, "textDocument/implementation", uri, "Area(&self): f64", 0), "Area(&self): f64 {")
	wantAt(t, "Square", locsAt(t, c, "textDocument/implementation", uri, "Square {", 0), "impl Shape for")
	wantAt(t, "impl", locsAt(t, c, "textDocument/implementation", uri, "impl Shape", 0), "Shape {")
	c.shutdown()
}

// TestLSPCallHierarchy: prepare on a function, its callers (a function and
// an impl's method) and its callees.
func TestLSPCallHierarchy(t *testing.T) {
	c, uri := openShapes(t)
	line, col := lineOf(t, shapesFixture, "Twice(x")
	var items []callHierarchyItem
	c.result(c.call("textDocument/prepareCallHierarchy", at(uri, line, col)), &items)
	if len(items) != 1 || items[0].Name != "Twice" {
		t.Fatalf("prepare: %+v, want Twice", items)
	}
	var in []callHierarchyCall
	c.result(c.call("callHierarchy/incomingCalls", map[string]any{"item": items[0]}), &in)
	callers := map[string]int{}
	for _, call := range in {
		callers[call.From.Name] = len(call.FromRanges)
	}
	if len(in) != 2 || callers["Area"] != 1 || callers["Main"] != 1 {
		t.Errorf("incoming: %+v, want Area and Main once each", in)
	}
	line, col = lineOf(t, shapesFixture, "Main()")
	c.result(c.call("textDocument/prepareCallHierarchy", at(uri, line, col)), &items)
	var outc []callHierarchyCall
	c.result(c.call("callHierarchy/outgoingCalls", map[string]any{"item": items[0]}), &outc)
	callees := map[string]bool{}
	for _, call := range outc {
		callees[call.To.Name] = true
	}
	if !callees["Twice"] || !callees["Larger"] || len(outc) != 2 {
		t.Errorf("outgoing from Main: %+v, want Twice and Larger", outc)
	}
	c.shutdown()
}

// TestLSPSelectionRange: from a name out through its brackets, its item
// and the file, each range containing the last.
func TestLSPSelectionRange(t *testing.T) {
	c, uri := openShapes(t)
	line, col := lineOf(t, shapesFixture, "self.side) *")
	var out []selectionRange
	c.result(c.call("textDocument/selectionRange", map[string]any{"textDocument": map[string]any{"uri": uri},
		"positions": []any{pos(line, col+6)}}), &out)
	if len(out) != 1 {
		t.Fatalf("selection ranges %+v, want one", out)
	}
	var chain []Range
	for sr := &out[0]; sr != nil; sr = sr.Parent {
		chain = append(chain, sr.Range)
	}
	first := chain[0]
	if first.Start != (Position{line, col + 5}) || first.End != (Position{line, col + 9}) {
		t.Errorf("innermost %+v, want the token side at %d:%d", first, line, col+5)
	}
	for i := 1; i < len(chain); i++ {
		if before(chain[i-1].Start, chain[i].Start) || before(chain[i].End, chain[i-1].End) {
			t.Errorf("range %d %+v does not contain %+v", i, chain[i], chain[i-1])
		}
	}
	if len(chain) < 5 || chain[len(chain)-1].Start != (Position{0, 0}) {
		t.Errorf("chain %+v: want token, call inside and parens, the body, the item, the file", chain)
	}
	c.shutdown()
}
