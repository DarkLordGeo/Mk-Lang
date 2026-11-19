// Why: editors start `mk lsp` with no flags and expect a working server;
// the help text is where a user finds that command line.
// What: tests of `mk lsp --help`, `--check-capabilities`, a bad argument,
// and a whole session over the command's stdio with no flags.
// How: run the command through run() with lspStdin replaced by a buffer of
// framed JSON-RPC messages, and read the framed answers from stdout.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestLSPHelp: --help prints how an editor starts the server and exits 0.
func TestLSPHelp(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		var so, se bytes.Buffer
		if st := run([]string{"lsp", arg}, &so, &se); st != 0 {
			t.Fatalf("mk lsp %s: status %d, stderr %q", arg, st, se.String())
		}
		if se.Len() != 0 {
			t.Errorf("mk lsp %s: stderr %q, want the help on stdout only", arg, se.String())
		}
		out := so.String()
		for _, want := range []string{"usage: mk lsp", "\n    mk lsp\n", "standard input and output", "-check-capabilities", "-std"} {
			if !strings.Contains(out, want) {
				t.Errorf("mk lsp %s: output lacks %q:\n%s", arg, want, out)
			}
		}
	}
}

// TestLSPUsageErrors: an argument or an unknown flag is a usage error.
func TestLSPUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"lsp", "x.mk"}, {"lsp", "--nope"}} {
		var so, se bytes.Buffer
		if st := run(args, &so, &se); st != 2 {
			t.Errorf("mk %v: status %d, want 2", args, st)
		}
		if so.Len() != 0 || se.Len() == 0 {
			t.Errorf("mk %v: stdout %q, stderr %q, want the error on stderr only", args, so.String(), se.String())
		}
	}
}

// TestLSPCheckCapabilities: the capabilities print as JSON.
func TestLSPCheckCapabilities(t *testing.T) {
	var so, se bytes.Buffer
	if st := run([]string{"lsp", "--check-capabilities"}, &so, &se); st != 0 {
		t.Fatalf("status %d, stderr %q", st, se.String())
	}
	var caps map[string]any
	if err := json.Unmarshal(so.Bytes(), &caps); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, so.String())
	}
	for _, k := range []string{"hoverProvider", "definitionProvider", "completionProvider", "signatureHelpProvider", "textDocumentSync"} {
		if _, ok := caps[k]; !ok {
			t.Errorf("capabilities lack %s", k)
		}
	}
}

// TestLSPStdioSession: `mk lsp` with no flags answers initialize, hover
// and shutdown over its stdio, and exits 0 after exit.
func TestLSPStdioSession(t *testing.T) {
	t.Setenv("MK_CACHE", t.TempDir())
	frame := func(v any) string {
		b, _ := json.Marshal(v)
		return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(b), b)
	}
	var in strings.Builder
	in.WriteString(frame(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"capabilities": map[string]any{}}}))
	in.WriteString(frame(map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}}))
	in.WriteString(frame(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "shutdown"}))
	in.WriteString(frame(map[string]any{"jsonrpc": "2.0", "method": "exit"}))
	old := lspStdin
	lspStdin = strings.NewReader(in.String())
	defer func() { lspStdin = old }()
	var so, se bytes.Buffer
	if st := run([]string{"lsp"}, &so, &se); st != 0 {
		t.Fatalf("status %d, stderr %q", st, se.String())
	}
	out := so.String()
	if !strings.Contains(out, `"capabilities"`) || !strings.Contains(out, `"hoverProvider":true`) || !strings.Contains(out, `"id":2`) {
		t.Errorf("answers lack initialize's capabilities or shutdown's reply:\n%s", out)
	}
}
