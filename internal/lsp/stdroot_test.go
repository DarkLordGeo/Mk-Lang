// Why: browsing std's files must not publish false errors.
// What: tests of std roots: diagnostics, hover and module names.
// How: opens std files from a tree under --std.

package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stdPkgManifest = `const Package = pkg.Package(
    name: "std",
    bins: [],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [pkg.Select(os: "windows", modules: [pkg.Module(name: "sys", dir: "sys/windows")])],
    unsafe_modules: [],
    links: [],
)
`

const stdPkgModule = `// The module's header.

/// Fence orders memory.
/// A second line.
export Fence(n: int)
    requires n > 0

export struct Pair {
    x: int
    y: int
}

export Pair.Sum(&self): int {
    return self.x + self.y
}

export Use(): int {
    Fence(1)
    p := Pair(x: 1, y: 2)
    return p.Sum()
}
`

// TestLSPStdPackageBrowsed: a file of a package named "std" (the Mk
// repository's std/, which has a pkg.mk) is not checked as a user package,
// whose bodiless intrinsic would be E0101: it joins the tree's std root,
// checked as std through browse.mk. This tree is no whole std
// (it has no std.core), so that check answers nothing; no diagnostic
// reaches the file, and hover shows each name's declaration with its
// comment and module.
func TestLSPStdPackageBrowsed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "std")
	must(t, os.MkdirAll(filepath.Join(dir, "a"), 0o755))
	must(t, writeFile(filepath.Join(dir, "pkg.mk"), stdPkgManifest))
	path := filepath.Join(dir, "a", "a.mk")
	must(t, writeFile(path, stdPkgModule))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, stdPkgModule)
	if p := c.diagnostics(uri); len(p.Diagnostics) != 0 {
		t.Errorf("diagnostics of a std file: %+v, want none", p.Diagnostics)
	}
	var pull struct{ Items []lspDiagnostic }
	c.result(c.call("textDocument/diagnostic", doc(uri)), &pull)
	if len(pull.Items) != 0 {
		t.Errorf("pulled diagnostics of a std file: %+v, want none", pull.Items)
	}
	for _, tc := range []struct {
		at   string
		off  int
		want []string
	}{
		{"Fence(1)", 0, []string{"```mk\nexport Fence(n: int)\n    requires n > 0\nmodule std.a\n```", "Fence orders memory.\nA second line."}},
		{"Pair(x: 1", 0, []string{"export struct Pair {\n    x: int\n    y: int\n}", "export Pair.Sum(&self): int"}},
		{"p.Sum()", 2, []string{"export Pair.Sum(&self): int\nmodule std.a\n```"}},
		{"export Fence", 7, []string{"export Fence(n: int)"}},
	} {
		line, col := lineOf(t, stdPkgModule, tc.at)
		var h hover
		c.result(c.call("textDocument/hover", at(uri, line, col+tc.off)), &h)
		for _, w := range tc.want {
			if !strings.Contains(h.Contents.Value, w) {
				t.Errorf("hover at %q:\n%s\nwant it to hold %q", tc.at, h.Contents.Value, w)
			}
		}
	}
	if k := rootKeyOf(path); !strings.HasPrefix(k, "std:") {
		t.Errorf("root of a file of the package named std: %s, want a std root", k)
	}
	c.shutdown()
}

// TestLSPStdCopyBrowsed: definition from a user file opens std's read-only
// copy under the cache; that file is checked as std (as a single file it
// would be E0202 and E0101), so it has no diagnostics, and its hovers
// answer from that check and name the std module.
func TestLSPStdCopyBrowsed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.mk")
	must(t, writeFile(path, stdFixture))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, stdFixture)
	c.diagnostics(uri)
	line, col := lineOf(t, stdFixture, "s.Len()")
	var locs []Location
	c.result(c.call("textDocument/definition", at(uri, line, col+2)), &locs)
	if len(locs) == 0 {
		t.Fatal("no definition of s.Len")
	}
	stdPath, ok := uriPath(locs[0].URI)
	if !ok {
		t.Fatalf("definition URI %s", locs[0].URI)
	}
	raw, err := os.ReadFile(stdPath)
	must(t, err)
	checks := c.srv.checks.Load()
	c.open(locs[0].URI, string(raw))
	if p := c.diagnostics(locs[0].URI); len(p.Diagnostics) != 0 {
		t.Errorf("diagnostics of the std copy %s: %+v, want none", stdPath, p.Diagnostics[0])
	}
	var h hover
	c.result(c.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": locs[0].URI}, "position": locs[0].Range.Start}), &h)
	for _, w := range []string{"str.Len(self)", "module std.core"} {
		if !strings.Contains(h.Contents.Value, w) {
			t.Errorf("hover on str.Len in %s:\n%s\nwant it to hold %q", stdPath, h.Contents.Value, w)
		}
	}
	if c.srv.checks.Load() == checks {
		t.Errorf("opening the std copy ran no check, want std checked as std")
	}
	c.shutdown()
}
