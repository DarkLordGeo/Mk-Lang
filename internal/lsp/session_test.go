// Why: one test per request kind, plus broken input, cancellation and size.
// What: the scripted-session tests of every request, malformed input, broken
// framing, cancellation, a large file and the position encodings.
// How: sessions over pipes against the fixture package.

package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mk/internal/driver"
	"mk/internal/format"
)

func TestLSPInitializeShutdownExit(t *testing.T) {
	c := start(t)
	res := c.init(true)
	caps, _ := res["capabilities"].(map[string]any)
	if caps["positionEncoding"] != "utf-8" {
		t.Errorf("positionEncoding %v, want utf-8 when offered", caps["positionEncoding"])
	}
	for _, k := range []string{"hoverProvider", "definitionProvider", "referencesProvider", "documentSymbolProvider",
		"documentFormattingProvider", "completionProvider", "codeActionProvider", "semanticTokensProvider", "textDocumentSync"} {
		if caps[k] == nil {
			t.Errorf("capability %s missing", k)
		}
	}
	c.shutdown()
}

func TestLSPExitWithoutShutdown(t *testing.T) {
	c := start(t)
	c.init(false)
	c.notify("exit", nil)
	select {
	case err := <-c.served:
		if err == nil {
			t.Error("exit before shutdown returned nil, want an error (exit code 1)")
		}
		c.served <- err
	case <-time.After(waitFor):
		t.Fatal("server did not exit")
	}
}

func TestLSPDiagnosticsOnOpenAndChange(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	bad := strings.Replace(fixtureMain, "return self.hi - self.lo", "return self.hi - nope", 1)
	c.open(mainURI, bad)
	p := c.diagnostics(mainURI)
	if len(p.Diagnostics) != 1 {
		t.Fatalf("diagnostics on open: %+v, want one", p.Diagnostics)
	}
	d := p.Diagnostics[0]
	line, col := lineOf(t, bad, "nope")
	if d.Code != "E0201" || d.Source != "mk" || d.Range.Start != (Position{line, col}) || d.Range.End != (Position{line, col + 4}) {
		t.Errorf("diagnostic %+v, want E0201 at %d:%d-%d", d, line, col, col+4)
	}
	if p.Version == nil || *p.Version != 1 {
		t.Errorf("version %v, want 1", p.Version)
	}
	// The fix: the file's diagnostics are published empty.
	c.change(mainURI, 2, fixtureMain)
	if p := c.diagnostics(mainURI); len(p.Diagnostics) != 0 {
		t.Errorf("diagnostics after the fix: %+v, want none", p.Diagnostics)
	}
	// A new error elsewhere, in the unsaved buffer only.
	bad2 := strings.Replace(fixtureMain, "var t := 0", "var t := zork", 1)
	c.change(mainURI, 3, bad2)
	p = c.diagnostics(mainURI)
	line, _ = lineOf(t, bad2, "zork")
	if len(p.Diagnostics) != 1 || p.Diagnostics[0].Range.Start.Line != line || *p.Version != 3 {
		t.Errorf("diagnostics after the second change: %+v, want one on line %d", p, line)
	}
	c.notify("textDocument/didClose", doc(mainURI))
	if p := c.diagnostics(mainURI); len(p.Diagnostics) != 0 {
		t.Errorf("diagnostics after close: %+v, want none", p.Diagnostics)
	}
	c.shutdown()
}

// TestLSPHoverMatchesQuerySig is the one test that compares the
// editor's answer with the command's: hover shows `mk query sig` (with the
// `///` lines) and, for a type, `kind`.
func TestLSPHoverMatchesQuerySig(t *testing.T) {
	dir, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	c.diagnostics(mainURI)
	query := func(q, subject string) []string {
		s, err := driver.NewSession(driver.Config{Root: dir, CacheDir: testCache})
		if err != nil {
			t.Fatal(err)
		}
		a, _, err := s.Query(q, []string{subject})
		if err != nil || a == nil || !a.Found {
			t.Fatalf("mk query %s %s: %v %+v", q, subject, err, a)
		}
		return a.Answer
	}
	for _, tc := range []struct {
		at, subject string
		off         int
		kind        bool
	}{
		{"p.Width()", "Pair.Width", 2, false},
		{"Pair(lo: 1", "Pair", 0, true},
		{"util.Twice", "Twice", 5, false},
		{"Sum([1", "Sum", 0, false},
	} {
		line, col := lineOf(t, fixtureMain, tc.at)
		var h hover
		c.result(c.call("textDocument/hover", at(mainURI, line, col+tc.off)), &h)
		want := query("sig", tc.subject)
		if tc.kind {
			want = append(want, query("kind", tc.subject)...)
		}
		if got := h.Contents.Value; !strings.HasPrefix(got, "```mk\n"+strings.Join(want, "\n")+"\n```") {
			t.Errorf("hover at %q:\n%s\nwant mk query's\n%s", tc.at, got, strings.Join(want, "\n"))
		}
	}
	// The doc comment is in the answer.
	line, col := lineOf(t, fixtureMain, "p.Width()")
	var h hover
	c.result(c.call("textDocument/hover", at(mainURI, line, col+2)), &h)
	if !strings.Contains(h.Contents.Value, "/// Width is hi minus lo.") {
		t.Errorf("hover has no doc comment: %s", h.Contents.Value)
	}
	// A local shows its declared type; a keyword shows nothing.
	line, col = lineOf(t, fixtureMain, "for x in xs")
	c.result(c.call("textDocument/hover", at(mainURI, line, col+9)), &h)
	if !strings.HasPrefix(h.Contents.Value, "```mk\n(param) xs") {
		t.Errorf("hover on a parameter: %s", h.Contents.Value)
	}
	c.result(c.call("textDocument/hover", at(mainURI, line, col+1)), &h)
	if !strings.Contains(h.Contents.Value, "reference: Statements") {
		t.Errorf("hover on a keyword: %s, want its rule from the reference's Statements chapter", h.Contents.Value)
	}
	c.shutdown()
}

func TestLSPDefinition(t *testing.T) {
	_, mainURI, utilURI := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	for _, tc := range []struct {
		at   string
		off  int
		uri  string
		decl string // the text whose first occurrence is the declaration
		doff int
	}{
		{"Pair(lo: 1", 0, mainURI, "struct Pair", 7},
		{"p.Width()", 2, mainURI, "Pair.Width(&self)", 5},
		{"p.Width()", 0, mainURI, "p := Pair", 0},
		{"util.Twice", 5, utilURI, "Twice(n", 0},
		{"t += x", 5, mainURI, "for x in", 4},
		{"Sum([1", 0, mainURI, "Sum(xs", 0},
		{"return self.hi", 12, mainURI, "export hi", 7},
	} {
		line, col := lineOf(t, fixtureMain, tc.at)
		var locs []Location
		c.result(c.call("textDocument/definition", at(mainURI, line, col+tc.off)), &locs)
		text := fixtureMain
		if tc.uri == utilURI {
			text = fixtureUtil
		}
		dl, dc := lineOf(t, text, tc.decl)
		if len(locs) != 1 || !sameURI(locs[0].URI, tc.uri) || locs[0].Range.Start != (Position{dl, dc + tc.doff}) {
			t.Errorf("definition at %q+%d: %+v, want %s %d:%d", tc.at, tc.off, locs, tc.uri, dl, dc+tc.doff)
		}
	}
	c.shutdown()
}

func TestLSPReferences(t *testing.T) {
	_, mainURI, utilURI := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	refs := func(uri, text, sub string, off int, decl bool) []Location {
		line, col := lineOf(t, text, sub)
		p := at(uri, line, col+off)
		p["context"] = map[string]any{"includeDeclaration": decl}
		var locs []Location
		c.result(c.call("textDocument/references", p), &locs)
		return locs
	}
	if got := refs(mainURI, fixtureMain, "Sum(xs", 0, true); len(got) != 2 {
		t.Errorf("references of Sum with its declaration: %+v, want 2", got)
	}
	callLine, _ := lineOf(t, fixtureMain, "Sum([1")
	if got := refs(mainURI, fixtureMain, "Sum(xs", 0, false); len(got) != 1 || got[0].Range.Start.Line != callLine {
		t.Errorf("references of Sum without its declaration: %+v, want the call on line %d", got, callLine)
	}
	// Across modules: Twice from its declaration in util.
	got := refs(utilURI, fixtureUtil, "Twice(n", 0, true)
	files := map[string]bool{}
	for _, l := range got {
		files[filepath.Base(mustPath(t, l.URI))] = true
	}
	if len(got) != 2 || !files["main.mk"] || !files["util.mk"] {
		t.Errorf("references of Twice: %+v, want util.mk and main.mk", got)
	}
	// A local stays in its function: t in Sum, not the t of another item.
	if got := refs(mainURI, fixtureMain, "var t", 4, true); len(got) != 3 {
		t.Errorf("references of local t: %+v, want 3", got)
	}
	c.shutdown()
}

func mustPath(t *testing.T, uri string) string {
	p, ok := uriPath(uri)
	if !ok {
		t.Fatalf("not a file URI: %s", uri)
	}
	return p
}

func TestLSPDocumentSymbols(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	var syms []documentSymbol
	c.result(c.call("textDocument/documentSymbol", doc(mainURI)), &syms)
	var got []string
	var walk func(prefix string, ss []documentSymbol)
	walk = func(prefix string, ss []documentSymbol) {
		for _, s := range ss {
			got = append(got, prefix+s.Name+":"+fmt.Sprint(s.Kind))
			walk(prefix+s.Name+".", s.Children)
		}
	}
	walk("", syms)
	want := "Pair:23 Pair.lo:8 Pair.hi:8 Pair.Width:6 Kind:10 Kind.Small:22 Kind.Zero:22 Sum:12 Main:12"
	if strings.Join(got, " ") != want {
		t.Errorf("symbols\n%s\nwant\n%s", strings.Join(got, " "), want)
	}
	line, _ := lineOf(t, fixtureMain, "export struct Pair")
	if syms[0].Range.Start.Line != line || syms[0].Range.End.Line != line+3 {
		t.Errorf("Pair's range %+v, want lines %d to %d", syms[0].Range, line, line+3)
	}
	c.shutdown()
}

func TestLSPFormatting(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	messy := strings.Replace(fixtureMain, "    var t := 0\n", "    var t   :=  0\n\n\n", 1)
	c.open(mainURI, messy)
	var edits []textEdit
	c.result(c.call("textDocument/formatting", doc(mainURI)), &edits)
	want, err := format.Source("main.mk", []byte(messy))
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 1 || edits[0].NewText != string(want) || edits[0].Range.Start != (Position{}) {
		t.Fatalf("formatting edits %+v, want one whole-file edit with mk fmt's text", edits)
	}
	end := newLines(messy).endPos(true)
	if edits[0].Range.End != end {
		t.Errorf("edit ends at %+v, want %+v", edits[0].Range.End, end)
	}
	// Formatted text: no edit. A file that does not parse: no edit.
	c.change(mainURI, 2, string(want))
	c.result(c.call("textDocument/formatting", doc(mainURI)), &edits)
	if len(edits) != 0 {
		t.Errorf("edits on formatted text: %+v", edits)
	}
	c.change(mainURI, 3, "Broken( {\n")
	c.result(c.call("textDocument/formatting", doc(mainURI)), &edits)
	if len(edits) != 0 {
		t.Errorf("edits on a file that does not parse: %+v", edits)
	}
	c.shutdown()
}

func TestLSPCompletionMembers(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	if p := c.diagnostics(mainURI); len(p.Diagnostics) != 0 {
		t.Fatalf("fixture has diagnostics: %+v", p.Diagnostics)
	}
	labels := func(text, sub string, off int) []string {
		line, col := lineOf(t, text, sub)
		var l completionList
		c.result(c.call("textDocument/completion", at(mainURI, line, col+off)), &l)
		var out []string
		for _, it := range l.Items {
			out = append(out, it.Label)
		}
		return out
	}
	// Typing `p.` does not parse: the answer comes from the last good text.
	typing := strings.Replace(fixtureMain, "    _ = p.Width()\n", "    _ = p.\n", 1)
	c.change(mainURI, 2, typing)
	c.diagnostics(mainURI)
	if got := strings.Join(labels(typing, "_ = p.", 6), " "); got != "lo hi Width" {
		t.Errorf("completion after p.: %q, want the members of Pair", got)
	}
	if got := strings.Join(labels(typing, "util.Twice", 5), " "); got != "Twice" {
		t.Errorf("completion after util.: %q, want Twice", got)
	}
	// self: the method's owner.
	if got := strings.Join(labels(typing, "self.hi - self.lo", 5), " "); got != "lo hi Width" {
		t.Errorf("completion after self.: %q, want the members of Pair", got)
	}
	c.shutdown()
}

func TestLSPCompletionImport(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	c.diagnostics(mainURI)
	var l completionList
	c.result(c.call("textDocument/completion", at(mainURI, 0, 7)), &l)
	have := map[string]bool{}
	for _, it := range l.Items {
		have[it.Label] = true
	}
	if !have["util"] || !have["main"] || !have["std.fs"] {
		t.Errorf("import completion %v, want util, main and std.fs", l.Items)
	}
	c.shutdown()
}

func TestLSPSemanticTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.mk")
	text := "export Id(x: &str): &str from x { // c\n    return \"\"\"\n    a\n    \"\"\"\n}\n"
	must(t, os.WriteFile(path, []byte(text), 0o644))
	c := start(t)
	c.init(true)
	var st semanticTokens
	c.result(c.call("textDocument/semanticTokens/full", doc(pathURI(path))), &st)
	type tok struct{ line, char, n, class int }
	var got []tok
	line, char := 0, 0
	for i := 0; i+5 <= len(st.Data); i += 5 {
		dl, dc := int(st.Data[i]), int(st.Data[i+1])
		if dl > 0 {
			line, char = line+dl, dc
		} else {
			char += dc
		}
		got = append(got, tok{line, char, int(st.Data[i+2]), int(st.Data[i+3])})
	}
	// export, Id (function), x (parameter), str, str, from, x, the comment; ...
	want := []tok{{0, 0, 6, 3}, {0, 7, 2, int(classFunction)}, {0, 10, 1, int(classParameter)}, {0, 14, 3, 5}, {0, 21, 3, 5}, {0, 25, 4, 4}, {0, 30, 1, int(classParameter)}, {0, 34, 4, 0},
		{1, 4, 6, 3}, {1, 11, 3, 1}, {2, 0, 5, 1}, {3, 0, 7, 1}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("tokens %v\nwant   %v", got, want)
	}
	c.shutdown()
}

func TestLSPCodeAction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h.mk")
	uri := pathURI(path)
	text := "export Main(sys: &System) {\n    let t = 1\n    _ = t\n}\n"
	must(t, os.WriteFile(path, []byte(text), 0o644))
	c := start(t)
	c.init(true)
	c.open(uri, text)
	p := c.diagnostics(uri)
	if len(p.Diagnostics) == 0 || p.Diagnostics[0].Code != "E1207" {
		t.Fatalf("diagnostics %+v, want E1207", p.Diagnostics)
	}
	var acts []codeAction
	c.result(c.call("textDocument/codeAction", map[string]any{"textDocument": map[string]any{"uri": uri},
		"range": p.Diagnostics[0].Range, "context": map[string]any{"diagnostics": p.Diagnostics}}), &acts)
	// The fix, and "explain this error" (a command, no edit).
	var fixes, explains []codeAction
	for _, a := range acts {
		if a.Edit != nil {
			fixes = append(fixes, a)
		} else if a.Command != nil && a.Command.Command == explainCommand {
			explains = append(explains, a)
		}
	}
	if len(explains) != 1 || explains[0].Title != "Explain E1207" {
		t.Errorf("actions %+v, want an Explain E1207 command", acts)
	}
	acts = fixes
	if len(acts) != 1 || !acts[0].IsPreferred || !strings.HasPrefix(acts[0].Title, "proven: ") || acts[0].Kind != "quickfix" {
		t.Fatalf("actions %+v, want one preferred proven quickfix", acts)
	}
	var edits []textEdit
	for u, es := range acts[0].Edit.Changes {
		if !sameURI(u, uri) {
			t.Errorf("edit of %s", u)
		}
		edits = es
	}
	// Apply the edits, last first, and the habit is gone.
	l := newLines(text)
	out := text
	for i := len(edits) - 1; i >= 0; i-- {
		a, b := l.off(edits[i].Range.Start, true), l.off(edits[i].Range.End, true)
		out = out[:a] + edits[i].NewText + out[b:]
	}
	if !strings.Contains(out, "    t := 1\n") {
		t.Errorf("after the fix:\n%s", out)
	}
	// A range with no diagnostic has no action.
	c.result(c.call("textDocument/codeAction", map[string]any{"textDocument": map[string]any{"uri": uri},
		"range": Range{Start: Position{3, 0}, End: Position{3, 1}}}), &acts)
	if len(acts) != 0 {
		t.Errorf("actions away from the diagnostic: %+v", acts)
	}
	c.shutdown()
}

func TestLSPMalformedInput(t *testing.T) {
	pre := start(t)
	// Before initialize: a request is refused, a notification dropped.
	if m := pre.call("textDocument/hover", at("file:///x.mk", 0, 0)); m.Error == nil || m.Error.Code != codeServerNotInitialized {
		t.Errorf("request before initialize: %+v, want -32002", m)
	}
	c := pre
	c.init(true)
	c.raw("this is not json")
	if m := c.next(); m.Error == nil || m.Error.Code != codeParseError || string(m.ID) != "null" {
		t.Errorf("non-JSON body: %+v, want -32700 with id null", m)
	}
	c.raw(`{"jsonrpc":"2.0","id":"abc","method":"mk/unknown","params":{}}`)
	if m := c.next(); string(m.ID) != `"abc"` || m.Error == nil || m.Error.Code != codeMethodNotFound {
		t.Errorf("unknown method: %+v, want -32601 for id \"abc\"", m)
	}
	if m := c.call("textDocument/hover", map[string]any{"textDocument": 5}); m.Error == nil || m.Error.Code != codeInvalidParams {
		t.Errorf("bad params: %+v, want -32602", m)
	}
	if m := c.call("textDocument/hover", at("http://example.com/x.mk", 0, 0)); m.Error == nil || m.Error.Code != codeInvalidParams {
		t.Errorf("non-file URI: %+v, want -32602", m)
	}
	if m := c.call("initialize", map[string]any{}); m.Error == nil || m.Error.Code != codeInvalidRequest {
		t.Errorf("second initialize: %+v, want -32600", m)
	}
	// Out of order: a change of a document never opened, a close of one
	// never opened, and a response to no request are dropped; an unknown
	// notification is ignored; the server still answers.
	c.change("file:///never/opened.mk", 2, "x")
	c.notify("textDocument/didClose", doc("file:///never/opened.mk"))
	c.raw(`{"jsonrpc":"2.0","id":99,"result":null}`)
	c.notify("mk/whatever", map[string]any{})
	var syms []documentSymbol
	if m := c.call("textDocument/documentSymbol", doc("file:///never/opened.mk")); m.Error == nil {
		c.result(m, &syms)
		t.Errorf("symbols of a file that does not exist: %v, want an error", syms)
	}
	if m := c.call("shutdown", nil); m.Error != nil {
		t.Fatalf("shutdown: %+v", m)
	}
	if m := c.call("textDocument/hover", at("file:///x.mk", 0, 0)); m.Error == nil || m.Error.Code != codeInvalidRequest {
		t.Errorf("request after shutdown: %+v, want -32600", m)
	}
	c.notify("exit", nil)
}

func TestLSPBrokenFraming(t *testing.T) {
	for _, frame := range []string{
		"Content-Length: abc\r\n\r\n{}",
		"X-Other: 1\r\n\r\n{}",
		"no header colon\r\n\r\n",
		"Content-Length: 50\r\n\r\n{\"short\":1}",
	} {
		c := start(t)
		go func() { _, _ = c.w.Write([]byte(frame)); c.w.Close() }()
		select {
		case err := <-c.served:
			if err == nil {
				t.Errorf("frame %q: Serve returned nil", frame)
			}
			c.served <- err
		case <-time.After(waitFor):
			t.Fatalf("frame %q: server did not stop", frame)
		}
	}
}

func TestLSPCancellation(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	var mu sync.Mutex
	hold := make(chan struct{})
	held := false
	testBeforeJob = func() {
		mu.Lock()
		first := !held
		held = true
		mu.Unlock()
		if first {
			<-hold
		}
	}
	defer func() { testBeforeJob = nil }()
	c := start(t)
	c.init(true)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "textDocument/documentSymbol", "params": doc(mainURI)})
	c.send(map[string]any{"jsonrpc": "2.0", "id": 8, "method": "textDocument/documentSymbol", "params": doc(mainURI)})
	c.notify("$/cancelRequest", map[string]any{"id": 7})
	deadline := time.Now().Add(waitFor)
	for {
		c.srv.mu.Lock()
		n := len(c.srv.queue)
		cancelled := c.srv.cancelled["n:7"]
		c.srv.mu.Unlock()
		if n >= 2 && cancelled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the requests never reached the queue")
		}
		time.Sleep(time.Millisecond)
	}
	close(hold)
	if m := c.response(7); m.Error == nil || m.Error.Code != codeRequestCancelled {
		t.Errorf("cancelled request: %+v, want -32800", m)
	}
	var syms []documentSymbol
	c.result(c.response(8), &syms)
	if len(syms) == 0 {
		t.Error("the request after the cancelled one got no symbols")
	}
	c.shutdown()
}

// TestLSPLargeFile opens, checks, edits, lists and formats a 20,000-line
// single-file program; the times are logged, never asserted.
func TestLSPLargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.mk")
	uri := pathURI(path)
	var b strings.Builder
	const n = 5000
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "F%d(n: int): int {\n    return n + %d\n}\n\n", i, i)
	}
	b.WriteString("export Main(sys: &System) {\n    _ = F7(1)\n}\n")
	text := b.String()
	must(t, os.WriteFile(path, []byte(text), 0o644))
	c := start(t)
	c.init(false)
	t0 := time.Now()
	c.open(uri, text)
	if p := c.diagnostics(uri); len(p.Diagnostics) != 0 {
		t.Fatalf("diagnostics on open: %+v", p.Diagnostics[:1])
	}
	t1 := time.Now()
	bad := strings.Replace(text, "return n + 4321\n", "return n + nope\n", 1)
	c.change(uri, 2, bad)
	p := c.diagnostics(uri)
	t2 := time.Now()
	line, _ := lineOf(t, bad, "nope")
	if len(p.Diagnostics) != 1 || p.Diagnostics[0].Range.Start.Line != line {
		t.Errorf("diagnostics after the edit: %+v, want one on line %d", p.Diagnostics, line)
	}
	var syms []documentSymbol
	c.result(c.call("textDocument/documentSymbol", doc(uri)), &syms)
	if len(syms) != n+1 {
		t.Errorf("%d symbols, want %d", len(syms), n+1)
	}
	var edits []textEdit
	c.change(uri, 3, text)
	c.result(c.call("textDocument/formatting", doc(uri)), &edits)
	if len(edits) != 0 {
		t.Errorf("formatting a formatted file: %d edits", len(edits))
	}
	t3 := time.Now()
	t.Logf("%d lines: open to diagnostics %v, edit to diagnostics %v, symbols and formatting %v",
		strings.Count(text, "\n"), t1.Sub(t0), t2.Sub(t1), t3.Sub(t2))
	c.shutdown()
}

// TestLSPUTF16Positions: without utf-8 offered, columns count UTF-16 units.
func TestLSPUTF16Positions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "u.mk")
	uri := pathURI(path)
	text := "export Main(sys: &System) {\n    s := \"é😀\" == nope\n    _ = s\n}\n"
	must(t, os.WriteFile(path, []byte(text), 0o644))
	c := start(t)
	res := c.init(false)
	if caps := res["capabilities"].(map[string]any); caps["positionEncoding"] != "utf-16" {
		t.Errorf("positionEncoding %v, want utf-16", caps["positionEncoding"])
	}
	c.open(uri, text)
	p := c.diagnostics(uri)
	// `nope` is at byte 21 of line 1; before it, é is 2 bytes and 1 unit,
	// 😀 is 4 bytes and 2 units: character 18.
	found := false
	for _, d := range p.Diagnostics {
		if d.Code == "E0201" {
			found = true
			if d.Range.Start != (Position{1, 18}) || d.Range.End != (Position{1, 22}) {
				t.Errorf("E0201 at %+v, want 1:18-1:22 in UTF-16", d.Range)
			}
		}
	}
	if !found {
		t.Logf("diagnostics: %+v", p.Diagnostics)
		t.Error("no E0201 for nope")
	}
	c.shutdown()
}

// TestLSPLatency100K records the wall time from a didChange of one function
// body to its publishDiagnostics on the generated 100 KLOC program of
// internal/check/progen_test.go, written by
// `MK_GEN100K_DIR=<dir> go test ./internal/check -run TestWriteBenchProgram`.
// Set MK_LSP_100K=<dir>. Logged, never asserted.
func TestLSPLatency100K(t *testing.T) {
	dir := os.Getenv("MK_LSP_100K")
	if dir == "" {
		t.Skip("set MK_LSP_100K to the generated 100 KLOC program")
	}
	path := filepath.Join(dir, "lib3", "lib3_00.mk")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	uri := pathURI(path)
	c := start(t)
	c.init(false)
	t0 := time.Now()
	c.open(uri, text)
	p := c.diagnostics(uri)
	t1 := time.Now()
	t.Logf("open, first check (cache %s): %v, %d diagnostics", testCache, t1.Sub(t0), len(p.Diagnostics))
	for i, edit := range []struct{ from, to string }{
		{"return self.x + self.y\n", "return self.x + self.y + 1\n"},
		{"return self.x + self.y\n", "return self.x + nope\n"},
		{"return self.x + self.y\n", "return self.x + self.y\n"},
	} {
		changed := strings.Replace(text, edit.from, edit.to, 1)
		s := time.Now()
		c.change(uri, i+2, changed)
		p := c.diagnostics(uri)
		t.Logf("edit %d (%q): diagnostics after %v, %d diagnostics", i+1, edit.to, time.Since(s), len(p.Diagnostics))
	}
	// References after the edits: the first asks a full check of its own
	// (exact.go fullExact), the second reuses it.
	line, col := lineOf(t, text, "P0.Sum(&self)")
	for i := range 2 {
		s := time.Now()
		var locs []Location
		c.result(c.call("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": uri},
			"position": pos(line, col+3), "context": map[string]any{"includeDeclaration": true}}), &locs)
		t.Logf("references of P0.Sum %d: %v, %d locations", i+1, time.Since(s), len(locs))
	}
	c.shutdown()
}
