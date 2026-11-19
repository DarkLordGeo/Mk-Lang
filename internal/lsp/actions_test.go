// Why: the extra code actions must edit the right text.
// What: TestLSPCodeActionsExtra.
// How: opens a fixture with an unknown binding, an enum match and unsorted imports
// and applies each action's edit.

package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func actionsAt(t *testing.T, c *client, uri string, rng Range) []codeAction {
	t.Helper()
	var acts []codeAction
	c.result(c.call("textDocument/codeAction", map[string]any{"textDocument": map[string]any{"uri": uri},
		"range": rng, "context": map[string]any{"diagnostics": []any{}}}), &acts)
	return acts
}

func applyEdits(text string, es []textEdit) string {
	l := newLines(text)
	for i := len(es) - 1; i >= 0; i-- {
		a, b := l.off(es[i].Range.Start, true), l.off(es[i].Range.End, true)
		text = text[:a] + es[i].NewText + text[b:]
	}
	return text
}

func titled(acts []codeAction, prefix string) (codeAction, bool) {
	for _, a := range acts {
		if strings.HasPrefix(a.Title, prefix) {
			return a, true
		}
	}
	return codeAction{}, false
}

// TestLSPCodeActionsExtra: add the import for an unknown module binding,
// fill a match's missing arms, organise imports, and explain a code (the
// command answers with window/showDocument of the explain file).
func TestLSPCodeActionsExtra(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mk")
	uri := pathURI(path)
	text := `import std.json
import std.fs
import std.json

enum Shape {
    Circle(r: f64)
    Rect(w: f64, h: f64)
    Empty
}

Area(s: Shape): f64 {
    match s {
        .Empty {
            return 0.0
        }
    }
    return 0.0
}

export Main(sys: &System) {
    _ = math.Abs(1.0)
}
`
	must(t, os.WriteFile(path, []byte(text), 0o644))
	c := start(t)
	c.init(true)
	c.open(uri, text)
	p := c.diagnostics(uri)
	var unknown *lspDiagnostic
	for i, d := range p.Diagnostics {
		if d.Code == "E0201" {
			unknown = &p.Diagnostics[i]
		}
	}
	if unknown == nil {
		t.Fatalf("diagnostics %+v, want an E0201 on math", p.Diagnostics)
	}
	acts := actionsAt(t, c, uri, unknown.Range)
	imp, ok := titled(acts, "Add import std.math")
	if !ok {
		imp, ok = titled(acts, "proven: ")
	}
	if !ok || imp.Edit == nil {
		t.Fatalf("actions %+v, want one adding import std.math", acts)
	}
	for _, es := range imp.Edit.Changes {
		if got := applyEdits(text, es); !strings.Contains(got, "import std.math\n") {
			t.Errorf("after adding the import:\n%s", got)
		}
	}
	ex, ok := titled(acts, "Explain E0201")
	if !ok || ex.Command == nil {
		t.Fatalf("actions %+v, want Explain E0201", acts)
	}
	m := c.call("workspace/executeCommand", map[string]any{"command": ex.Command.Command, "arguments": ex.Command.Arguments})
	if m.Error != nil {
		t.Fatalf("executeCommand: %v", m.Error)
	}
	shown := false
	for _, n := range c.notes {
		if n.Method == "window/showDocument" {
			var sp struct {
				URI string `json:"uri"`
			}
			_ = json.Unmarshal(n.Params, &sp)
			shown = strings.HasSuffix(sp.URI, "E0201.txt")
		}
	}
	if !shown {
		t.Errorf("no window/showDocument of E0201's explain file in %+v", c.notes)
	}

	line, col := lineOf(t, text, "match s {")
	acts = actionsAt(t, c, uri, Range{Start: Position{line, col}, End: Position{line, col}})
	fill, ok := titled(acts, "Fill match arms (2)")
	if !ok {
		t.Fatalf("actions %+v, want Fill match arms (2)", acts)
	}
	for _, es := range fill.Edit.Changes {
		got := applyEdits(text, es)
		if !strings.Contains(got, "        .Circle(r) {\n        }\n        .Rect(w, h) {\n        }\n    }\n") {
			t.Errorf("after filling the arms:\n%s", got)
		}
	}
	org, ok := titled(acts, "Organise imports")
	if !ok || org.Kind != "source.organizeImports" {
		t.Fatalf("actions %+v, want Organise imports", acts)
	}
	for _, es := range org.Edit.Changes {
		if got := applyEdits(text, es); !strings.HasPrefix(got, "import std.fs\nimport std.json\n\nenum") {
			t.Errorf("after organising imports:\n%s", got)
		}
	}
	c.shutdown()
}
