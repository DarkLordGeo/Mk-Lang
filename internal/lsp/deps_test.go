// Why: dependencies must resolve in the editor as the compiler resolves them.
// What: TestLSPDependencies, TestManifestDeps, TestLSPTestDepE0217,
// TestLSPSourceDependencyVendored.
// How: packages with path, vendored and test dependencies, checked through a
// scripted session.

package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func depsManifest(name, deps string) string {
	return `const Package = pkg.Package(
    name: "` + name + `",
    bins: [],
    deps: [` + deps + `],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
`
}

const depsApp = `import u = util.src

export A(): int {
    return u.Two() + 1
}
`

// TestLSPDependencies: a path dependency's files reach the server through
// the check's SourceMap: definition into the dependency, the members of
// its module after `u.`, and its exported names in workspace symbols.
func TestLSPDependencies(t *testing.T) {
	ws := t.TempDir()
	files := map[string]string{
		"app/pkg.mk":         depsManifest("app", `pkg.Dep(name: "util", path: "../util", url: "", hash: "")`),
		"app/src/a.mk":       depsApp,
		"util/pkg.mk":        depsManifest("util", ""),
		"util/src/u.mk":      "import h = src.help\n\nexport Two(): int {\n    return h.One() + h.One()\n}\n",
		"util/src/help/h.mk": "export One(): int {\n    return 1\n}\n",
	}
	for p, text := range files {
		full := filepath.Join(ws, filepath.FromSlash(p))
		must(t, os.MkdirAll(filepath.Dir(full), 0o755))
		must(t, writeFile(full, text))
	}
	uri := pathURI(filepath.Join(ws, "app", "src", "a.mk"))
	c := start(t)
	c.init(true)
	c.open(uri, depsApp)
	if p := c.diagnostics(uri); len(p.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %+v", p.Diagnostics)
	}
	line, col := lineOf(t, depsApp, "u.Two()")
	var locs []Location
	c.result(c.call("textDocument/definition", at(uri, line, col+2)), &locs)
	if len(locs) != 1 || !strings.HasSuffix(filepath.ToSlash(mustPath(t, locs[0].URI)), "util/src/u.mk") {
		t.Errorf("definition of u.Two: %+v, want util/src/u.mk", locs)
	}
	var l completionList
	c.result(c.call("textDocument/completion", at(uri, line, col+2)), &l)
	var labels []string
	for _, it := range l.Items {
		labels = append(labels, it.Label)
	}
	if strings.Join(labels, " ") != "Two" {
		t.Errorf("completion after u.: %v, want Two", labels)
	}
	var syms []symbolInformation
	c.result(c.call("workspace/symbol", map[string]any{"query": "One"}), &syms)
	found := false
	for _, s := range syms {
		if s.Name == "One" && strings.HasSuffix(filepath.ToSlash(mustPath(t, s.Location.URI)), "util/src/help/h.mk") {
			found = true
		}
	}
	if !found {
		t.Errorf("workspace symbols for One: %+v, want util's One", syms)
	}
	c.shutdown()
}

// TestManifestDeps: pkg.mk's dependencies, read lexically.
func TestManifestDeps(t *testing.T) {
	dir := t.TempDir()
	must(t, writeFile(filepath.Join(dir, "pkg.mk"), depsManifest("app",
		`pkg.Dep(name: "util", path: "../util", url: "", hash: ""), pkg.Dep(name: "ini", path: "", url: "https://example.com/ini.zip", hash: "sha256:00")`)))
	s := newServer(nil, Options{CacheDir: testCache})
	got := s.manifestDeps(&root{dir: dir})
	if len(got) != 2 || got["util"] != "../util" || got["ini"] != "" {
		t.Errorf("manifestDeps: %v", got)
	}
	if !isFetchedPath("ini@0123456789ab/src/x.mk") || isFetchedPath("../util/x.mk") {
		t.Error("isFetchedPath")
	}
}

// mk lsp reports E0217 for a test dependency's name used
// outside a test block, as mk check does.
func TestLSPTestDepE0217(t *testing.T) {
	ws := t.TempDir()
	app := "import u = util.src\n\nexport A(): int {\n    return u.Two()\n}\n"
	files := map[string]string{
		"app/pkg.mk":    "const Package = pkg.Package(\n    name: \"app\",\n    test_deps: [pkg.Dep(name: \"util\", path: \"../util\")],\n)\n",
		"app/src/a.mk":  app,
		"util/pkg.mk":   depsManifest("util", ""),
		"util/src/u.mk": "export Two(): int {\n    return 2\n}\n",
	}
	for p, text := range files {
		full := filepath.Join(ws, filepath.FromSlash(p))
		must(t, os.MkdirAll(filepath.Dir(full), 0o755))
		must(t, writeFile(full, text))
	}
	uri := pathURI(filepath.Join(ws, "app", "src", "a.mk"))
	c := start(t)
	c.init(true)
	c.open(uri, app)
	p := c.diagnostics(uri)
	if len(p.Diagnostics) != 1 || !strings.Contains(fmt.Sprint(p.Diagnostics[0]), "E0217") {
		t.Errorf("diagnostics: %+v, want one E0217", p.Diagnostics)
	}
	c.shutdown()
}

// materializeDeps' source branch end to end: a vendored
// 1.3 source package is found through vendor/index.txt by the driver's
// canonical id, and definition goes into its files.
func TestLSPSourceDependencyVendored(t *testing.T) {
	ws := t.TempDir()
	src := "example.com/someone/toml"
	cid := sourceCIDOf(src, "1.0.0")
	app := "import t = toml.src\n\nexport A(): int {\n    return t.V() + 1\n}\n"
	files := map[string]string{
		"app/pkg.mk":                         "const Package = pkg.Package(\n    name: \"app\",\n    deps: [pkg.Dep(name: \"toml\", source: \"" + src + "\", version: \"1.0.0\")],\n)\n",
		"app/src/a.mk":                       app,
		"app/vendor/index.txt":               src + " v1.0.0 " + cid + " tree-sha256:" + strings.Repeat("0", 64) + "\n",
		"app/vendor/" + cid + "/pkg.mk":      "const Package = pkg.Package(\n    name: \"toml\",\n)\n",
		"app/vendor/" + cid + "/src/toml.mk": "export V(): int {\n    return 1\n}\n",
	}
	for p, text := range files {
		full := filepath.Join(ws, filepath.FromSlash(p))
		must(t, os.MkdirAll(filepath.Dir(full), 0o755))
		must(t, writeFile(full, text))
	}
	uri := pathURI(filepath.Join(ws, "app", "src", "a.mk"))
	c := start(t)
	c.init(true)
	c.open(uri, app)
	if p := c.diagnostics(uri); len(p.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %+v", p.Diagnostics)
	}
	line, col := lineOf(t, app, "t.V()")
	var locs []Location
	c.result(c.call("textDocument/definition", at(uri, line, col+2)), &locs)
	if len(locs) != 1 || !strings.HasSuffix(filepath.ToSlash(mustPath(t, locs[0].URI)), "vendor/"+cid+"/src/toml.mk") {
		t.Errorf("definition of t.V: %+v, want vendor/%s/src/toml.mk", locs, cid)
	}
	c.shutdown()
}
