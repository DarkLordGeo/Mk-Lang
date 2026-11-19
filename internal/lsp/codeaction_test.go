// Why: a fix that does not compile must never be offered.
// What: TestLSPCodeActionsVerified.
// How: asks code actions on a diagnostic whose fix is recompiled first.

package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLSPCodeActionsVerified: a compiles fix is
// recompiled before the editor offers it. The review's fixprobe: `try` in
// Main, whose set lacks fs.ReadText's errors, does not compile, so no
// "compiles:" action is offered for its E0802; a `try` that compiles keeps
// its action.
func TestLSPCodeActionsVerified(t *testing.T) {
	for _, c := range []struct {
		name, text string
		want       bool // a compiles action stays
	}{
		{"downgraded", "import std.fs\n\nexport Main(sys: &System): WriteError!void {\n    s := fs.ReadText(sys.arena, sys.io, \"x.txt\")\n    var out := sys.Stdout()\n    try Print(mut out, \"{s}\", (s))\n}\n", false},
		{"kept", "Half(n: int): OutOfMemory!int {\n    return n / 2\n}\n\nUse(): OutOfMemory!void {\n    x := Half(3)\n    _ = x\n}\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.mk")
			must(t, os.WriteFile(path, []byte(c.text), 0o644))
			uri := pathURI(path)
			cl := start(t)
			cl.init(true)
			cl.open(uri, c.text)
			p := cl.diagnostics(uri)
			var at []lspDiagnostic
			for _, d := range p.Diagnostics {
				if d.Code == "E0802" {
					at = append(at, d)
				}
			}
			if len(at) != 1 {
				t.Fatalf("diagnostics %+v, want one E0802", p.Diagnostics)
			}
			var acts []codeAction
			cl.result(cl.call("textDocument/codeAction", map[string]any{"textDocument": map[string]any{"uri": uri},
				"range": at[0].Range, "context": map[string]any{"diagnostics": at}}), &acts)
			kept := false
			for _, a := range acts {
				if strings.HasPrefix(a.Title, compilesTitle) {
					kept = true
				}
			}
			if kept != c.want {
				t.Errorf("compiles action offered %v, want %v: %+v", kept, c.want, acts)
			}
		})
	}
}
