// Why: snippets, construction fields and std completion are what a new user types
// with.
// What: TestLSPCompletionSnippets, TestLSPCompletionConstructionFields,
// TestLSPCompletionStdAndImport.
// How: scripted sessions that ask completion at prepared cursors and check the
// labels and edits.

package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func completeAt(t *testing.T, c *client, uri, text, sub string, off int) map[string]completionItem {
	t.Helper()
	line, col := lineOf(t, text, sub)
	var l completionList
	c.result(c.call("textDocument/completion", at(uri, line, col+off)), &l)
	out := map[string]completionItem{}
	for _, it := range l.Items {
		if _, ok := out[it.Label]; !ok {
			out[it.Label] = it
		}
	}
	return out
}

// TestLSPCompletionSnippets: statement snippets at the start of a line in
// a body, item snippets at the top level, neither in the wrong place.
func TestLSPCompletionSnippets(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	text := strings.Replace(fixtureMain, "    return t\n", "    return t\n    sc\n", 1) + "st\n"
	c.open(mainURI, text)
	body := completeAt(t, c, mainURI, text, "    sc", 6)
	if it := body["scope"]; it.Kind != complSnippet || it.InsertTextFormat != formatSnippet || !strings.Contains(it.InsertText, "s.go") {
		t.Errorf("scope in a body: %+v, want the scope snippet", it)
	}
	for _, w := range []string{"unsafe", "match", "for", "fn"} {
		if body[w].Kind != complSnippet {
			t.Errorf("%s in a body: %+v, want a snippet", w, body[w])
		}
	}
	if body["struct"].Kind == complSnippet {
		t.Error("struct snippet offered inside a body")
	}
	line := strings.Count(text, "\n") - 1
	var l completionList
	c.result(c.call("textDocument/completion", at(mainURI, line, 2)), &l)
	top := map[string]int{}
	for _, it := range l.Items {
		top[it.Label] = it.Kind
	}
	if top["struct"] != complSnippet || top["test"] != complSnippet || top["scope"] == complSnippet {
		t.Errorf("top-level snippets %v: want struct and test, not scope", top)
	}
	c.shutdown()
}

// TestLSPCompletionConstructionFields: inside `Pair(`, the fields not yet
// written come first.
func TestLSPCompletionConstructionFields(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	text := strings.Replace(fixtureMain, "p := Pair(lo: 1, hi: 3)", "p := Pair(lo: 1, )", 1)
	c.open(mainURI, text)
	got := completeAt(t, c, mainURI, text, "Pair(lo: 1, )", len("Pair(lo: 1, "))
	if it := got["hi"]; it.Kind != complField || it.InsertText != "hi: " || !strings.HasPrefix(it.SortText, "0") {
		t.Errorf("field hi: %+v, want a field item first", it)
	}
	if _, ok := got["lo"]; ok {
		t.Error("lo offered though written")
	}
	c.shutdown()
}

// TestLSPCompletionStdAndImport: `fs.` lists std.fs's exported items with
// their lines; a bare word offers an unimported std module with its
// import as an additional edit; resolve fills the doc from mk query sig.
func TestLSPCompletionStdAndImport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.mk")
	must(t, writeFile(path, stdFixture))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, stdFixture)
	c.diagnostics(uri)
	got := completeAt(t, c, uri, stdFixture, "fs.ReadText", 3)
	rt, ok := got["ReadText"]
	if !ok || !strings.HasPrefix(rt.Detail, "ReadText(a: &Arena") || rt.Data == nil || rt.Data.Subject != "fs.ReadText" {
		t.Fatalf("fs. completion: ReadText %+v (%d items), want its line and subject", rt, len(got))
	}
	if _, ok := got["WriteAll"]; !ok {
		t.Error("fs. completion lacks WriteAll")
	}
	var res completionItem
	c.result(c.call("completionItem/resolve", rt), &res)
	if res.Documentation == nil || !strings.Contains(res.Documentation.Value, "ReadText") || !strings.Contains(res.Documentation.Value, surfaceSection) {
		t.Errorf("resolve: %+v, want the sig and the library surface line", res.Documentation)
	}

	got = completeAt(t, c, uri, stdFixture, "    _ = Count", 8)
	js, ok := got["json"]
	if !ok || len(js.AdditionalTextEdits) != 1 || js.AdditionalTextEdits[0].NewText != "import std.json\n" || js.AdditionalTextEdits[0].Range.Start.Line != 1 {
		t.Errorf("json: %+v, want an item adding import std.json after line 0", js)
	}
	if _, ok := got["fs"]; ok && got["fs"].AdditionalTextEdits != nil {
		t.Error("fs offered with an import though imported")
	}
	c.shutdown()
}
