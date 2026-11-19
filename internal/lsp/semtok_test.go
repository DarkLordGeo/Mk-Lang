// Why: token classes must match what the names are.
// What: TestLSPSemanticTokenClasses.
// How: decodes the token stream of a fixture and checks the classes.

package lsp

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// decodeTokens reads semantic token data into (line, char, len, class,
// mods) by absolute position.
func decodeTokens(data []uint32) map[[2]int][3]int {
	out := map[[2]int][3]int{}
	line, char := 0, 0
	for i := 0; i+5 <= len(data); i += 5 {
		dl, dc := int(data[i]), int(data[i+1])
		if dl > 0 {
			line, char = line+dl, dc
		} else {
			char += dc
		}
		out[[2]int{line, char}] = [3]int{int(data[i+2]), int(data[i+3]), int(data[i+4])}
	}
	return out
}

const semFixture = `import std.fs

linear struct Handle {
    fd: int
}

enum Color {
    Red
    Green
}

Use(h: Handle, c: Color): int {
    n := h.fd
    _ = fs.ReadText
    unsafe "test" {
        n = n + 1
    }
    _ = Color.Red
    return Twice(n)
}

Twice(x: int): int {
    return x * 2
}
`

// TestLSPSemanticTokenClasses: the name classes and modifiers beyond the
// highlighter's six, and full/delta answering one edit.
func TestLSPSemanticTokenClasses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sem.mk")
	must(t, writeFile(path, semFixture))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, semFixture)
	c.diagnostics(uri)
	var st semanticTokens
	c.result(c.call("textDocument/semanticTokens/full", doc(uri)), &st)
	toks := decodeTokens(st.Data)
	check := func(sub string, off int, cl tokClass, mods int) {
		t.Helper()
		line, col := lineOf(t, semFixture, sub)
		got, ok := toks[[2]int{line, col + off}]
		if !ok || got[1] != int(cl) || got[2]&mods != mods {
			t.Errorf("%q+%d: %v (ok %v), want class %s with modifiers %b", sub, off, got, ok, tokenLegend[cl], mods)
		}
	}
	check("struct Handle", 7, classType, modDeclaration|modLinear)
	check("h: Handle", 3, classType, modLinear)
	check("h: Handle", 0, classParameter, 0)
	check("n := h.fd", 0, classVariable, 0)
	check("n := h.fd", 7, classProperty, 0)
	check("fs.ReadText", 0, classNamespace, 0)
	check("n = n + 1", 0, classVariable, modUnsafe)
	check("Color.Red", 6, classEnumMember, 0)
	check("Twice(n)", 0, classFunction, 0)
	check("Twice(x", 0, classFunction, modDeclaration)
	check("    Green", 4, classEnumMember, modDeclaration)
	if st.ResultID == "" {
		t.Fatal("no resultId")
	}
	// An edit, then delta: one edit that turns the old data into the new.
	text2 := semFixture + "\nThrice(y: int): int {\n    return y * 3\n}\n"
	c.change(uri, 2, text2)
	var delta struct {
		ResultID string `json:"resultId"`
		Edits    []struct {
			Start       int      `json:"start"`
			DeleteCount int      `json:"deleteCount"`
			Data        []uint32 `json:"data"`
		} `json:"edits"`
	}
	m := c.call("textDocument/semanticTokens/full/delta", map[string]any{"textDocument": map[string]any{"uri": uri}, "previousResultId": st.ResultID})
	c.result(m, &delta)
	if len(delta.Edits) != 1 || delta.ResultID == st.ResultID {
		t.Fatalf("delta %s, want one edit and a new result id", m.Result)
	}
	e := delta.Edits[0]
	got := append(append(append([]uint32{}, st.Data[:e.Start]...), e.Data...), st.Data[e.Start+e.DeleteCount:]...)
	var full semanticTokens
	c.result(c.call("textDocument/semanticTokens/full", doc(uri)), &full)
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(full.Data)
	if string(a) != string(b) {
		t.Errorf("old data with the delta applied:\n%s\nwant\n%s", a, b)
	}
	c.shutdown()
}
