// Why: range formatting must not touch text outside the range.
// What: TestLSPRangeFormatting.
// How: formats a range of a badly laid out file and checks the edits.

package lsp

import (
	"strings"
	"testing"

	"mk/internal/format"
)

// TestLSPRangeFormatting: only the changed lines inside the range are
// edited; a messy line outside it stays as it is.
func TestLSPRangeFormatting(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	messy := strings.Replace(fixtureMain, "    var t := 0\n", "    var t   :=  0\n", 1)
	messy = strings.Replace(messy, "    return n * 2\n", "    return n * 2\n", 1)
	messy = strings.Replace(messy, "    _ = p.Width()\n", "    _ =   p.Width()\n", 1)
	c.open(mainURI, messy)
	line, _ := lineOf(t, messy, "var t   :=")
	var edits []textEdit
	c.result(c.call("textDocument/rangeFormatting", map[string]any{"textDocument": map[string]any{"uri": mainURI},
		"range": Range{Start: Position{line, 0}, End: Position{line + 1, 0}}, "options": map[string]any{"tabSize": 4, "insertSpaces": true}}), &edits)
	if len(edits) != 1 || edits[0].Range.Start.Line != line || edits[0].NewText != "    var t := 0\n" {
		t.Fatalf("range edits %+v, want the one line in the range", edits)
	}
	got := applyEdits(messy, edits)
	if !strings.Contains(got, "_ =   p.Width()") {
		t.Error("a line outside the range was formatted")
	}
	want, err := format.Source("main.mk", []byte(messy))
	must(t, err)
	if string(want) == got {
		t.Error("the whole file was formatted")
	}
	c.shutdown()
}
