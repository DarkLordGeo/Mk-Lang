// Why: definition into generated code must land on real files.
// What: TestLSPDefinitionIntoGen, TestLSPGenNotInScope.
// How: a package with a generator, asked for definitions of generated names.

package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const genMain = `import std.derive

struct User {
    id: int
}

gen derive.Eq(User)

export Main(sys: &System) {
    u := User(id: 1)
    _ = u.Eq(&u)
}
`

// genTwo has two gen lines of one generator, so the trace comment must
// pick the one that emitted User.Eq.
const genTwo = `import std.derive

struct Team {
    n: int
}

struct User {
    id: int
}

gen derive.Eq(Team)
gen derive.Eq(User)

export Main(sys: &System) {
    u := User(id: 1)
    _ = u.Eq(&u)
}
`

// TestLSPDefinitionIntoGen: a method a generator emitted resolves to its
// declaration in the gen/ view, written read-only under the cache (never
// into the tree), then to the one gen line that emitted it (by the trace
// comment), then to the generator in std.
func TestLSPDefinitionIntoGen(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(fixtureManifest), 0o644))
	path := filepath.Join(dir, "main.mk")
	genMain := genTwo
	must(t, writeFile(path, genMain))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, genMain)
	if p := c.diagnostics(uri); len(p.Diagnostics) != 0 {
		t.Fatalf("fixture: %+v", p.Diagnostics)
	}
	line, col := lineOf(t, genMain, "u.Eq(&u)")
	var locs []Location
	c.result(c.call("textDocument/definition", at(uri, line, col+2)), &locs)
	if len(locs) != 3 {
		t.Fatalf("definition of u.Eq: %+v, want the gen view, the gen line and the generator", locs)
	}
	gp, ok := uriPath(locs[0].URI)
	if !ok || !strings.HasPrefix(pathKey(gp), pathKey(filepath.Join(testCache, "lsp", "gen"))) || !strings.HasSuffix(filepath.ToSlash(gp), "/gen.mk") {
		t.Errorf("first location %s, want the gen view under the cache", locs[0].URI)
	} else if raw, err := os.ReadFile(gp); err != nil || !strings.Contains(newLines(string(raw)).line(locs[0].Range.Start.Line), "Eq(") {
		t.Errorf("gen view line %d of %s does not declare Eq", locs[0].Range.Start.Line, gp)
	}
	gl, _ := lineOf(t, genMain, "gen derive.Eq(User)")
	if l := locs[1]; !sameURI(l.URI, uri) || l.Range.Start.Line != gl {
		t.Errorf("second location %+v, want the gen line %d of main.mk", l, gl)
	}
	if gp, ok := uriPath(locs[2].URI); !ok || !strings.Contains(filepath.ToSlash(gp), "/derive/") {
		t.Errorf("third location %s, want std.derive's generator", locs[2].URI)
	} else if raw, err := os.ReadFile(gp); err != nil || !strings.Contains(newLines(string(raw)).line(locs[2].Range.Start.Line), "export Eq(") {
		t.Errorf("generator line %d of %s does not declare Eq", locs[2].Range.Start.Line, gp)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen")); err == nil {
		t.Error("a gen/ directory was written into the tree")
	}
	c.shutdown()
}

// TestLSPGenNotInScope: a generated method is no bare-word completion,
// though definition finds it (the prelude's interface of that name is).
func TestLSPGenNotInScope(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(fixtureManifest), 0o644))
	path := filepath.Join(dir, "main.mk")
	text := strings.Replace(genMain, "    _ = u.Eq(&u)\n", "    _ = u.Eq(&u)\n    _ = E\n", 1)
	must(t, writeFile(path, text))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, text)
	c.diagnostics(uri)
	got := completeAt(t, c, uri, text, "    _ = E", 9)
	// The prelude's interface Eq is in scope; the generated method is not.
	if it, ok := got["Eq"]; ok && it.Kind != complModule && it.Kind != complStruct {
		t.Errorf("bare-word completion offers the generated %+v", it)
	}
	c.shutdown()
}
