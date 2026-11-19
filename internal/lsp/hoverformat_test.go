// Why: a derived method (Format on an error) has no source line of its own.
// What: TestLSPHoverProvidedFormat.
// How: hovers `e.Format` on an error and checks the answer names the method.

package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLSPHoverProvidedFormat: hover of `e.Format` on an error
// shows the compiler's Format, its signature and the impl noted as
// provided by the compiler.
func TestLSPHoverProvidedFormat(t *testing.T) {
	const src = "error NotFound(id: i64)\n\nShow(w: mut Writer, e: NotFound): WriteError!void {\n    try e.Format(w)\n}\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "main.mk")
	must(t, writeFile(path, src))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, src)
	if d := c.diagnostics(uri); len(d.Diagnostics) != 0 {
		t.Fatalf("fixture does not check: %+v", d.Diagnostics)
	}
	line, col := lineOf(t, src, "e.Format(w)")
	var h hover
	c.result(c.call("textDocument/hover", at(uri, line, col+2)), &h)
	for _, w := range []string{"Format(&self, w: mut Writer): WriteError!void", "impl Format for NotFound (provided by the compiler)"} {
		if !strings.Contains(h.Contents.Value, w) {
			t.Errorf("hover on e.Format: %q, want %q", h.Contents.Value, w)
		}
	}
}
