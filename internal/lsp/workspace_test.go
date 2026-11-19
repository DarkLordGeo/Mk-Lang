// Why: edits, disk changes and several packages at once.
// What: TestLSPWatchedFiles, TestLSPManifestEdit, TestLSPSeveralRoots,
// TestLSPEditsDuringCheck, TestRootCeiling.
// How: scripted sessions across several roots.

package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLSPWatchedFiles: a module changed on disk, not open in the editor,
// rechecks the open file that uses it; the client is asked to watch .mk
// files when it registers watchers dynamically.
func TestLSPWatchedFiles(t *testing.T) {
	dir, mainURI, utilURI := writeFixture(t)
	c := start(t)
	var res map[string]any
	c.result(c.call("initialize", map[string]any{"capabilities": map[string]any{
		"general":   map[string]any{"positionEncodings": []string{"utf-8"}},
		"workspace": map[string]any{"didChangeWatchedFiles": map[string]any{"dynamicRegistration": true}}}}), &res)
	c.notify("initialized", map[string]any{})
	c.open(mainURI, fixtureMain)
	if p := c.diagnostics(mainURI); len(p.Diagnostics) != 0 {
		t.Fatalf("fixture: %+v", p.Diagnostics)
	}
	registered := false
	for _, m := range c.seen {
		if m.Method == "client/registerCapability" && strings.Contains(string(m.Params), "**/*.mk") {
			registered = true
		}
	}
	if !registered {
		t.Error("no client/registerCapability for **/*.mk")
	}
	util := filepath.Join(dir, "util", "util.mk")
	must(t, writeFile(util, strings.Replace(fixtureUtil, "Twice", "Thrice", -1)))
	c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": utilURI, "type": 2}}})
	if p := c.diagnostics(mainURI); len(p.Diagnostics) == 0 {
		t.Error("no diagnostic on main.mk after util.mk lost Twice on disk")
	}
	must(t, writeFile(util, fixtureUtil))
	c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": utilURI, "type": 2}}})
	if p := c.diagnostics(mainURI); len(p.Diagnostics) != 0 {
		t.Errorf("diagnostics after util.mk was restored: %+v", p.Diagnostics)
	}
	c.shutdown()
}

// TestLSPManifestEdit: an edit of pkg.mk in the editor is checked with the
// package, its error shown, and its fix clears it.
func TestLSPManifestEdit(t *testing.T) {
	dir, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	c.diagnostics(mainURI)
	pkgURI := pathURI(filepath.Join(dir, "pkg.mk"))
	bad := strings.Replace(fixtureManifest, "unsafe_modules: []", `unsafe_modules: ["nope"]`, 1)
	c.open(pkgURI, bad)
	got := false
	deadline := time.Now().Add(waitFor)
	for !got && time.Now().Before(deadline) {
		var m message
		if len(c.notes) > 0 {
			m, c.notes = c.notes[0], c.notes[1:]
		} else {
			m = c.next()
		}
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p publishDiagnosticsParams
		must(t, json.Unmarshal(m.Params, &p))
		for _, d := range p.Diagnostics {
			if strings.Contains(d.Message, "nope") {
				got = true
			}
		}
	}
	if !got {
		t.Fatal("no diagnostic naming the manifest's missing unsafe module")
	}
	c.change(pkgURI, 2, fixtureManifest)
	if p := c.diagnostics(mainURI); len(p.Diagnostics) != 0 {
		t.Errorf("main.mk after the manifest's fix: %+v", p.Diagnostics)
	}
	c.shutdown()
}

// TestLSPSeveralRoots: two packages open at once each get their own
// check; fixing one leaves the other's diagnostic standing.
func TestLSPSeveralRoots(t *testing.T) {
	_, aURI, _ := writeFixture(t)
	_, bURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	badA := strings.Replace(fixtureMain, "return self.hi - self.lo", "return self.hi - nopeA", 1)
	badB := strings.Replace(fixtureMain, "return self.hi - self.lo", "return self.hi - nopeB", 1)
	c.open(aURI, badA)
	pa := c.diagnostics(aURI)
	c.open(bURI, badB)
	pb := c.diagnostics(bURI)
	if len(pa.Diagnostics) != 1 || !strings.Contains(pa.Diagnostics[0].Message, "nopeA") ||
		len(pb.Diagnostics) != 1 || !strings.Contains(pb.Diagnostics[0].Message, "nopeB") {
		t.Fatalf("diagnostics a %+v, b %+v", pa.Diagnostics, pb.Diagnostics)
	}
	c.change(aURI, 2, fixtureMain)
	if p := c.diagnostics(aURI); len(p.Diagnostics) != 0 {
		t.Errorf("a after its fix: %+v", p.Diagnostics)
	}
	var pull struct {
		Items []lspDiagnostic `json:"items"`
	}
	c.result(c.call("textDocument/diagnostic", doc(bURI)), &pull)
	if len(pull.Items) != 1 || !strings.Contains(pull.Items[0].Message, "nopeB") {
		t.Errorf("b after a's fix: %+v, want its own error still", pull.Items)
	}
	c.shutdown()
}

// TestLSPEditsDuringCheck: edits that arrive while the worker is busy
// coalesce into one check of the last text, published with its version;
// no older version is published after it.
func TestLSPEditsDuringCheck(t *testing.T) {
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
	c.open(mainURI, fixtureMain)
	bad := strings.Replace(fixtureMain, "return self.hi - self.lo", "return self.hi - nope", 1)
	bad2 := strings.Replace(fixtureMain, "var t := 0", "var t := zork", 1)
	c.change(mainURI, 2, bad)
	c.change(mainURI, 3, fixtureMain)
	c.change(mainURI, 4, bad2)
	deadline := time.Now().Add(waitFor)
	for {
		c.srv.mu.Lock()
		d := c.srv.docs.get(pathFromURI(t, mainURI))
		v := 0
		if d != nil {
			v = d.version
		}
		c.srv.mu.Unlock()
		if v == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the edits never reached the store")
		}
		time.Sleep(time.Millisecond)
	}
	close(hold)
	p := c.diagnostics(mainURI)
	if p.Version == nil || *p.Version != 4 || len(p.Diagnostics) != 1 || !strings.Contains(p.Diagnostics[0].Message, "zork") {
		t.Fatalf("first publish after the burst: version %v, %+v; want version 4 with the zork error", p.Version, p.Diagnostics)
	}
	// Nothing older follows: a symbols request answers after any queued check.
	var syms []documentSymbol
	c.result(c.call("textDocument/documentSymbol", doc(mainURI)), &syms)
	for _, n := range c.notes {
		if n.Method == "textDocument/publishDiagnostics" {
			var q publishDiagnosticsParams
			must(t, json.Unmarshal(n.Params, &q))
			if sameURI(q.URI, mainURI) && q.Version != nil && *q.Version < 4 {
				t.Errorf("an older version %d was published after version 4", *q.Version)
			}
		}
	}
	c.shutdown()
}

func pathFromURI(t *testing.T, uri string) string {
	t.Helper()
	p, ok := uriPath(uri)
	if !ok {
		t.Fatalf("not a file URI: %s", uri)
	}
	return p
}

// TestRootCeiling: a file with no manifest of its own belongs to the
// nearest pkg.mk above it, but never to one in or above rootCeiling.
func TestRootCeiling(t *testing.T) {
	top := t.TempDir()
	below := filepath.Join(top, "a", "b")
	must(t, os.MkdirAll(below, 0o755))
	must(t, writeFile(filepath.Join(top, "pkg.mk"), fixtureManifest))
	file := filepath.Join(below, "main.mk")
	saved := rootCeiling
	t.Cleanup(func() { rootCeiling = saved })
	rootCeiling = ""
	if got := rootKeyOf(file); got != "pkg:"+top {
		t.Errorf("no ceiling: rootKeyOf = %q, want the package at %s", got, top)
	}
	for _, c := range []string{top, filepath.Join(top, "a")} {
		rootCeiling = pathKey(c)
		if got := rootKeyOf(file); got != "file:"+file {
			t.Errorf("ceiling %s: rootKeyOf = %q, want the file alone", c, got)
		}
	}
}
