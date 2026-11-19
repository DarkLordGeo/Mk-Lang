// Why: the core of the server (sync, diagnostics, progress) must hold before
// anything else is useful.
// What: tests of incremental sync, pull diagnostics, work done progress on the
// first check, and hover on an error code.
// How: scripted sessions over the fixture package.

package lsp

import (
	"encoding/json"
	"strings"
	"testing"

	"mk/internal/driver"
)

func TestLSPIncrementalSync(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	res := c.init(false)
	sync, _ := res["capabilities"].(map[string]any)["textDocumentSync"].(map[string]any)
	if sync["change"] != float64(2) {
		t.Errorf("textDocumentSync.change %v, want 2 (incremental)", sync["change"])
	}
	c.open(mainURI, fixtureMain)
	if p := c.diagnostics(mainURI); len(p.Diagnostics) != 0 {
		t.Fatalf("fixture: %+v", p.Diagnostics)
	}
	// Two ranged edits in one change: `self.lo` becomes `nope`, then a
	// comment is inserted at the start of the file.
	line, col := lineOf(t, fixtureMain, "self.lo")
	c.notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{"uri": mainURI, "version": 2},
		"contentChanges": []any{
			map[string]any{"range": Range{Start: Position{line, col}, End: Position{line, col + 7}}, "text": "nope"},
			map[string]any{"range": Range{}, "text": "// head\n"},
		}})
	p := c.diagnostics(mainURI)
	if len(p.Diagnostics) != 1 || p.Diagnostics[0].Code != "E0201" || p.Diagnostics[0].Range.Start != (Position{line + 1, col}) {
		t.Errorf("after ranged edits: %+v, want E0201 at %d:%d", p.Diagnostics, line+1, col)
	}
	c.shutdown()
}

func TestLSPPullDiagnostics(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	res := c.init(true)
	if res["capabilities"].(map[string]any)["diagnosticProvider"] == nil {
		t.Error("no diagnosticProvider")
	}
	c.open(mainURI, strings.Replace(fixtureMain, "self.lo", "nope", 1))
	c.diagnostics(mainURI)
	var rep struct {
		Kind  string          `json:"kind"`
		Items []lspDiagnostic `json:"items"`
	}
	c.result(c.call("textDocument/diagnostic", doc(mainURI)), &rep)
	if rep.Kind != "full" || len(rep.Items) != 1 || rep.Items[0].Code != "E0201" {
		t.Errorf("pull diagnostics %+v, want one E0201", rep)
	}
	c.change(mainURI, 2, fixtureMain)
	c.diagnostics(mainURI)
	c.result(c.call("textDocument/diagnostic", doc(mainURI)), &rep)
	if rep.Kind != "full" || len(rep.Items) != 0 {
		t.Errorf("pull diagnostics after the fix %+v, want none", rep)
	}
	c.shutdown()
}

func TestLSPWorkDoneProgress(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	var init struct {
		ServerInfo struct{ Name, Version string } `json:"serverInfo"`
	}
	c.result(c.call("initialize", map[string]any{"capabilities": map[string]any{"window": map[string]any{"workDoneProgress": true}}}), &init)
	if init.ServerInfo.Name != "mk lsp" || init.ServerInfo.Version != driver.Release {
		t.Errorf("serverInfo %+v, want mk lsp %s", init.ServerInfo, driver.Release)
	}
	c.open(mainURI, fixtureMain)
	c.diagnostics(mainURI)
	// The end comes before the diagnostics: the check is over when they go.
	kinds := progressKinds(c)
	if strings.Join(kinds, " ") != "create begin end" {
		t.Errorf("progress %v, want create begin end", kinds)
	}
	// A second check reports no progress; the server still answers.
	c.seen = nil
	c.change(mainURI, 2, fixtureMain+"\n")
	c.diagnostics(mainURI)
	if kinds := progressKinds(c); len(kinds) != 0 {
		t.Errorf("progress for the second check: %v", kinds)
	}
	c.shutdown()
}

// progressKinds reads the progress messages seen so far, answering each
// create request as a client does (the server ignores responses).
func progressKinds(c *client) []string {
	var kinds []string
	for _, m := range c.seen {
		switch m.Method {
		case "window/workDoneProgress/create":
			kinds = append(kinds, "create")
			c.raw(`{"jsonrpc":"2.0","id":` + string(m.ID) + `,"result":null}`)
		case "$/progress":
			var p struct {
				Token string
				Value struct{ Kind string }
			}
			_ = json.Unmarshal(m.Params, &p)
			kinds = append(kinds, p.Value.Kind)
		}
	}
	return kinds
}

func TestLSPHoverErrorCode(t *testing.T) {
	dir := t.TempDir()
	text := "// see E0501 here\nexport Main(sys: &System) {\n}\n"
	path := dir + "/c.mk"
	must(t, writeFile(path, text))
	c := start(t)
	c.init(true)
	var h hover
	c.result(c.call("textDocument/hover", at(pathURI(path), 0, 9)), &h)
	e, _ := driver.Explain("E0501")
	if want := "```text\n" + strings.TrimRight(e.Text(), "\n") + "\n```"; h.Contents.Value != want {
		t.Errorf("hover on E0501:\n%s\nwant mk explain's\n%s", h.Contents.Value, want)
	}
	c.shutdown()
}

// TestLSPHoverKeywords: a keyword, a flow word, a rule word and an
// operator show their rule and section; a string's text shows nothing.
func TestLSPHoverKeywords(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/k.mk"
	text := "First(a: []u8, b: []u8): []u8 from a {\n    defer { }\n    _ = 1 + 2 == 3\n    _ = \"a + b\"\n    return a\n}\n"
	must(t, writeFile(path, text))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	for _, tc := range []struct {
		sub  string
		off  int
		want string // "" for no hover
	}{
		{"from a", 0, "memory model"},
		{"defer", 0, "reference: Statements"},
		{"1 + 2", 2, "reference: Expressions"},
		{"2 == 3", 2, "test equality"},
		{"a + b\"", 2, ""},
	} {
		line, col := lineOf(t, text, tc.sub)
		m := c.call("textDocument/hover", at(uri, line, col+tc.off))
		if tc.want == "" {
			if string(m.Result) != "null" {
				t.Errorf("hover at %q: %s, want null", tc.sub, m.Result)
			}
			continue
		}
		var h hover
		c.result(m, &h)
		if !strings.Contains(h.Contents.Value, tc.want) {
			t.Errorf("hover at %q: %s, want %q", tc.sub, h.Contents.Value, tc.want)
		}
	}
	c.shutdown()
}

// TestLSPHandlerPanic: a handler that panics answers InternalError and the
// server keeps answering.
func TestLSPHandlerPanic(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	testPanicIn = "textDocument/foldingRange"
	defer func() { testPanicIn = "" }()
	c := start(t)
	c.init(true)
	m := c.call("textDocument/foldingRange", doc(mainURI))
	if m.Error == nil || m.Error.Code != codeInternalError || !strings.Contains(m.Error.Message, "test panic") {
		t.Errorf("panicking handler: %+v, want -32603 with the panic's text", m)
	}
	var syms []documentSymbol
	c.result(c.call("textDocument/documentSymbol", doc(mainURI)), &syms)
	if len(syms) == 0 {
		t.Error("no answer after the panic")
	}
	c.shutdown()
}
