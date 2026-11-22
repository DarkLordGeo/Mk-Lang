// Why: the editor must not offer as a preferred proven quick fix a clause
// that mk check labels not proven.
//
// What: TestLSPClauseActionsVerified.
//
// How: a code-action request on an E0604 whose only fix is a clause runs
// the driver's verified check, as for a compiles fix.
package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLSPClauseActionsVerified: the clause E0604 names for Keys leaves Main
// with an E0505 (the map's loan into `sys.arena`), so no proven action is
// offered; the clause on Index.Add, whose caller then checks, stays a
// preferred proven action, and so does the receiver's `&self into arena`
// on Names.Keys.
func TestLSPClauseActionsVerified(t *testing.T) {
	for _, c := range []struct {
		name, text string
		want       bool // a proven action stays
	}{
		{"downgraded", "Keys(m: &TextMap[int], arena: &Arena): OutOfMemory!List[str] from arena, m {\n    var out := List[str].New(arena)\n    for (key, value) in m.Iter() {\n        _ = value\n        try out.Push(key)\n    }\n    return out\n}\n\nexport Main(sys: &System): OutOfMemory|WriteError!void {\n    a := sys.heap.Alloc()\n    var m := TextMap[int].New(a)\n    defer m.Free(a)\n    try m.Set(a, \"x\", 1)\n    keys := try Keys(&m, sys.arena)\n    try sys.Print(\"{n}\\n\", (n: keys.Len()))\n}\n", false},
		{"kept", "struct Index {\n    words: List[str]\n}\n\nIndex.Add(mut self, word: str): OutOfMemory!void {\n    try self.words.Push(word)\n}\n\nexport Main(sys: &System): OutOfMemory!void {\n    var ix := Index(words: List[str].New(sys.arena))\n    try ix.Add(\"x\")\n}\n", true},
		{"receiver", "struct Names {\n    map: TextMap[int]\n}\n\nNames.Keys(&self, arena: &Arena): OutOfMemory!List[str] from arena, self {\n    var out := List[str].New(arena)\n    for (key, value) in self.map.Iter() {\n        _ = value\n        try out.Push(key)\n    }\n    return out\n}\n\nexport Main(sys: &System): OutOfMemory|WriteError!void {\n    var ar := try Arena.New(sys.heap)\n    a := sys.heap.Alloc()\n    var m := TextMap[int].New(a)\n    m.Set(a, \"x\", 1) else {}\n    n := Names(map: m)\n    keys := n.Keys(&ar) else { List[str].New(&ar) }\n    count := keys.Len()\n    ar.Free()\n    Names(map) := n\n    map.Free(a)\n    try sys.Print(\"{n}\\n\", (n: count))\n}\n", true},
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
				if d.Code == "E0604" {
					at = append(at, d)
				}
			}
			if len(at) != 1 {
				t.Fatalf("diagnostics %+v, want one E0604", p.Diagnostics)
			}
			var acts []codeAction
			cl.result(cl.call("textDocument/codeAction", map[string]any{"textDocument": map[string]any{"uri": uri},
				"range": at[0].Range, "context": map[string]any{"diagnostics": at}}), &acts)
			kept := false
			for _, a := range acts {
				if strings.HasPrefix(a.Title, provenTitle) && a.IsPreferred {
					kept = true
				}
			}
			if kept != c.want {
				t.Errorf("preferred proven action offered %v, want %v: %+v", kept, c.want, acts)
			}
		})
	}
}
