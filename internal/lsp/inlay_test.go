// Why: hints must be right or absent.
// What: TestLSPInlayHints.
// How: a fixture with calls and locals; compares the hints.

package lsp

import (
	"path/filepath"
	"testing"
)

// TestLSPInlayHints: a parameter name before each positional argument, for
// a call into the package, into another module and into std; none for a
// named argument, an argument that spells the parameter, a receiver or a
// declaration's own parameters.
func TestLSPInlayHints(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	c.diagnostics(mainURI)
	var hints []inlayHint
	c.result(c.call("textDocument/inlayHint", map[string]any{"textDocument": map[string]any{"uri": mainURI},
		"range": map[string]any{"start": pos(0, 0), "end": pos(1000, 0)}}), &hints)
	hints = paramHints(hints) // the type hints are TestLSPInlayTypeHints'
	got := map[string]Position{}
	for _, h := range hints {
		got[h.Label] = h.Position
	}
	line, col := lineOf(t, fixtureMain, "Sum([1, 2]")
	if p, ok := got["n:"]; !ok || p != (Position{line, col}) {
		t.Errorf("hints %+v: want n: at %d:%d (util.Twice's parameter)", hints, line, col)
	}
	if p, ok := got["xs:"]; !ok || p != (Position{line, col + 4}) {
		t.Errorf("hints %+v: want xs: at %d:%d (Sum's parameter)", hints, line, col+4)
	}
	if len(hints) != 2 {
		t.Errorf("hints %+v: want only n: and xs: (Pair's arguments are named, Width has none)", hints)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.mk")
	must(t, writeFile(path, stdFixture))
	uri := pathURI(path)
	c.open(uri, stdFixture)
	c.diagnostics(uri)
	hints = nil
	c.result(c.call("textDocument/inlayHint", map[string]any{"textDocument": map[string]any{"uri": uri},
		"range": map[string]any{"start": pos(0, 0), "end": pos(1000, 0)}}), &hints)
	hints = paramHints(hints)
	labels := map[string]int{}
	for _, h := range hints {
		labels[h.Label]++
	}
	// fs.ReadText(sys.arena, sys.io, "x"): a: and p:, not io: (spelled);
	// Count("abc"): s:.
	if labels["a:"] != 1 || labels["p:"] != 1 || labels["io:"] != 0 || labels["s:"] != 1 || len(hints) != 3 {
		t.Errorf("std hints %+v, want a:, p: and s:", hints)
	}
	c.shutdown()
}

// paramHints keeps the parameter-name hints.
func paramHints(hs []inlayHint) []inlayHint {
	var out []inlayHint
	for _, h := range hs {
		if h.Kind == inlayParameter {
			out = append(out, h)
		}
	}
	return out
}
