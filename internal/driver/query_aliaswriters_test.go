// Why: std.text's Capped and Sink are aliases of std.core types, and
// std.core is std-internal, so mk query can miss their Writers.
// What: TestQueryFindsAliasWriters.
// How: on x86_64-freestanding runs query find, members and sig and checks
// that the alias Writers are listed.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

// TestQueryFindsAliasWriters: on the freestanding target,
// find Writer lists Capped.Writer and Sink.Writer in std.text, members of
// either alias lists its target's methods, and sig answers one by the
// alias's name.
func TestQueryFindsAliasWriters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	man := "const Package = pkg.Package(\n    name: \"k\",\n    bins: [pkg.Bin(name: \"k\", root: \"src\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n"
	src := "import std.text\n\nexport extern MkEntry() {\n    _ = text.Width(\"a\")\n    for {\n    }\n}\n"
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: dir, Target: types.X86_64Freestanding, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		q, arg string
		want   []string
	}{
		{"find", "Writer", []string{"Capped.Writer  func  std.text", "Sink.Writer  func  std.text"}},
		{"members", "text.Capped", []string{"New(buf: mut []u8): Capped from buf", "Writer(mut self): Writer from self", "Bytes(&self): []u8 from self", "Dropped(&self): int", "Reset(mut self)"}},
		{"members", "text.Sink", []string{"New(write: fn(mut S, []u8): WriteError!void): Sink[S]", "Writer(&self, s: mut S, buf: mut []u8): Writer from self, s, buf"}},
		{"sig", "text.Capped.Dropped", []string{"Capped.Dropped(&self): int", "module std.text"}},
	} {
		a, _, err := s.Query(c.q, []string{c.arg})
		if err != nil || a == nil || !a.Found {
			t.Errorf("query %s %s: %v\n%v", c.q, c.arg, err, a)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(a.Text(), w) {
				t.Errorf("query %s %s: no %q in\n%s", c.q, c.arg, w, a.Text())
			}
		}
	}
}
