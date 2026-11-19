// Why: navigation and writing requests, the capabilities and the latency budget.
// What: tests of highlight, folding, workspace symbols, signature help, scope
// completion, the capabilities golden, and hover and completion latency.
// How: scripted sessions; the capabilities are compared with
// testdata/capabilities.golden.

package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestLSPDocumentHighlight(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	line, col := lineOf(t, fixtureMain, "var t")
	var hs []documentHighlight
	c.result(c.call("textDocument/documentHighlight", at(mainURI, line, col+4)), &hs)
	if len(hs) != 3 {
		t.Errorf("highlights of t: %+v, want 3", hs)
	}
	c.shutdown()
}

func TestLSPFoldingRanges(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	var fs []foldingRange
	c.result(c.call("textDocument/foldingRange", doc(mainURI)), &fs)
	pair, _ := lineOf(t, fixtureMain, "export struct Pair")
	sum, _ := lineOf(t, fixtureMain, "Sum(xs")
	loop, _ := lineOf(t, fixtureMain, "for x in xs")
	want := map[[2]int]bool{{pair, pair + 2}: true, {sum, sum + 5}: true, {loop, loop + 1}: true}
	for _, f := range fs {
		delete(want, [2]int{f.StartLine, f.EndLine})
	}
	if len(want) > 0 {
		t.Errorf("folding ranges %+v miss %v", fs, want)
	}
	c.shutdown()
}

func TestLSPWorkspaceSymbols(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	var syms []symbolInformation
	c.result(c.call("workspace/symbol", map[string]any{"query": "twi"}), &syms)
	if len(syms) != 1 || syms[0].Name != "Twice" || !strings.HasSuffix(syms[0].Location.URI, "util/util.mk") {
		t.Errorf("workspace symbols for twi: %+v", syms)
	}
	c.result(c.call("workspace/symbol", map[string]any{"query": "w"}), &syms)
	var names []string
	for _, s := range syms {
		names = append(names, s.ContainerName+"."+s.Name)
	}
	sort.Strings(names)
	if strings.Join(names, " ") != ".Twice Pair.Width" {
		t.Errorf("workspace symbols for w: %v", names)
	}
	c.shutdown()
}

func TestLSPSignatureHelp(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	text := strings.Replace(fixtureMain, "    _ = util.Twice(Sum([1, 2][0..2]))\n", "    _ = util.Twice(Sum([1, 2][0..2]))\n    _ = Pair(lo: 1, hi: 2)\n", 1)
	c.open(mainURI, text)
	c.diagnostics(mainURI)
	line, col := lineOf(t, text, "util.Twice(")
	var sh signatureHelp
	c.result(c.call("textDocument/signatureHelp", at(mainURI, line, col+11)), &sh)
	if len(sh.Signatures) != 1 || sh.Signatures[0].Label != "export Twice(n: int): int" || sh.ActiveParameter != 0 ||
		len(sh.Signatures[0].Parameters) != 1 || sh.Signatures[0].Parameters[0].Label != "n: int" {
		t.Errorf("signature help in Twice(: %+v", sh)
	}
	if d := sh.Signatures[0].Documentation; d == nil || !strings.Contains(d.Value, "Twice doubles n.") || strings.Contains(d.Value, "///") {
		t.Errorf("signature help has no doc: %+v", sh.Signatures[0].Documentation)
	}
	if got := paramLabels("F[T: Hash](a: int, f: fn(int, int): int, xs: []T): int"); strings.Join(got, "|") != "a: int|f: fn(int, int): int|xs: []T" {
		t.Errorf("paramLabels: %q", got)
	}
	// Outside any call: no help.
	m := c.call("textDocument/signatureHelp", at(mainURI, 0, 3))
	if string(m.Result) != "null" {
		t.Errorf("signature help outside a call: %s", m.Result)
	}
	c.shutdown()
}

func TestLSPCompletionScope(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	text := strings.Replace(fixtureMain, "    return t\n", "    return t\n    \n", 1)
	c.open(mainURI, text)
	line, _ := lineOf(t, text, "    return t")
	var l completionList
	c.result(c.call("textDocument/completion", at(mainURI, line+1, 4)), &l)
	have := map[string]int{}
	for _, it := range l.Items {
		have[it.Label] = it.Kind
	}
	for name, kind := range map[string]int{"t": complValue, "xs": complValue, "Sum": complMethod, "Pair": complStruct, "util": complModule, "match": complSnippet, "return": complKeyword} {
		if have[name] != kind {
			t.Errorf("scope completion: %s has kind %d, want %d", name, have[name], kind)
		}
	}
	if _, ok := have["p"]; ok {
		t.Error("Main's local p offered inside Sum")
	}
	c.shutdown()
}

// TestLSPCapabilitiesGolden: the initialize capabilities an editor client
// relies on change only with testdata/capabilities.golden.
func TestLSPCapabilitiesGolden(t *testing.T) {
	got, err := json.MarshalIndent(Capabilities("utf-16"), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "capabilities.golden")
	if *update {
		must(t, os.MkdirAll("testdata", 0o755))
		must(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))) {
		t.Errorf("capabilities changed:\n%s\nupdate with go test ./internal/lsp -run TestLSPCapabilitiesGolden -update", got)
	}
}

// TestLSPLatencyHoverCompletion logs hover, completion and semantic tokens
// p50 and p95 over the fixture, warm. Logged, never
// asserted.
func TestLSPLatencyHoverCompletion(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	c.diagnostics(mainURI)
	hl, hc := lineOf(t, fixtureMain, "p.Width()")
	cl, cc := lineOf(t, fixtureMain, "p.Width()")
	measure := func(method string, params any) (time.Duration, time.Duration) {
		var ds []time.Duration
		for range 20 {
			s := time.Now()
			if m := c.call(method, params); m.Error != nil {
				t.Fatalf("%s: %v", method, m.Error)
			}
			ds = append(ds, time.Since(s))
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return ds[len(ds)/2], ds[len(ds)*95/100]
	}
	// The first semantic tokens request asks `mk query kind` of each type
	// the file names (semtok.go typeKinds); later ones read the cache.
	st := time.Now()
	if m := c.call("textDocument/semanticTokens/full", doc(mainURI)); m.Error != nil {
		t.Fatalf("semanticTokens: %v", m.Error)
	}
	first := time.Since(st)
	h50, h95 := measure("textDocument/hover", at(mainURI, hl, hc+2))
	c50, c95 := measure("textDocument/completion", at(mainURI, cl, cc+2))
	s50, s95 := measure("textDocument/semanticTokens/full", doc(mainURI))
	t.Logf("hover p50 %v p95 %v; completion p50 %v p95 %v; semantic tokens first %v, then p50 %v p95 %v (20 each, warm, the fixture package)", h50, h95, c50, c95, first, s50, s95)
	c.shutdown()
}
