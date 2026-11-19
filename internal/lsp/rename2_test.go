// Why: an exported rename must reach the dependants and refuse when one is closed.
// What: TestLSPRenameAcrossRoots.
// How: two packages, one a path dependency of the other.

package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	renUtil = `import h = src.help

export struct Box {
    export count: int
}

export Two(): int {
    return h.One() + h.One()
}
`
	renApp = `import u = util.src

export A(): int {
    b := u.Box(count: 1)
    return u.Two() + b.count
}
`
	renTool = `import w = util.src

export T(): int {
    return w.Two()
}
`
)

// TestLSPRenameAcrossRoots: an exported name of a package renames
// in the open packages that name it as a path dependency; the rename is
// refused while one of them is not open; a package that vendors the
// dependency is not a dependant; the rename works from a dependant's use
// too, and a field renames with its uses in the dependants.
func TestLSPRenameAcrossRoots(t *testing.T) {
	ws := t.TempDir()
	dep := `pkg.Dep(name: "util", path: "../util", url: "", hash: "")`
	files := map[string]string{
		"util/pkg.mk":              depsManifest("util", ""),
		"util/src/u.mk":            renUtil,
		"util/src/help/h.mk":       "export One(): int {\n    return 1\n}\n",
		"app/pkg.mk":               depsManifest("app", dep),
		"app/src/a.mk":             renApp,
		"tool/pkg.mk":              depsManifest("tool", dep),
		"tool/src/t.mk":            renTool,
		"ven/pkg.mk":               depsManifest("ven", dep),
		"ven/src/v.mk":             "import u = util.src\n\nexport V(): int {\n    return u.Two()\n}\n",
		"ven/vendor/util/pkg.mk":   depsManifest("util", ""),
		"ven/vendor/util/src/u.mk": renUtil,
	}
	for p, text := range files {
		full := filepath.Join(ws, filepath.FromSlash(p))
		must(t, os.MkdirAll(filepath.Dir(full), 0o755))
		must(t, writeFile(full, text))
	}
	uri := func(p string) string { return pathURI(filepath.Join(ws, filepath.FromSlash(p))) }
	c := start(t)
	var res map[string]any
	c.result(c.call("initialize", map[string]any{
		"capabilities":     map[string]any{"general": map[string]any{"positionEncodings": []string{"utf-8"}}},
		"workspaceFolders": []any{map[string]any{"uri": pathURI(ws), "name": "ws"}},
	}), &res)
	c.notify("initialized", map[string]any{})
	c.open(uri("util/src/u.mk"), renUtil)
	c.open(uri("app/src/a.mk"), renApp)

	rename := func(file, text, sub string, off int, name string) (map[string]int, *rpcError) {
		line, col := lineOf(t, text, sub)
		p := at(uri(file), line, col+off)
		p["newName"] = name
		m := c.call("textDocument/rename", p)
		if m.Error != nil {
			return nil, m.Error
		}
		var we workspaceEdit
		c.result(m, &we)
		got := map[string]int{}
		for u, es := range we.Changes {
			rel, err := filepath.Rel(ws, mustPath(t, u))
			must(t, err)
			for _, e := range es {
				if e.NewText != name {
					t.Errorf("edit in %s: %q, want %q", rel, e.NewText, name)
				}
			}
			got[filepath.ToSlash(rel)] = len(es)
		}
		return got, nil
	}
	show := func(m map[string]int) string {
		var out []string
		for k, v := range m {
			out = append(out, k+":"+itoa(v))
		}
		sort.Strings(out)
		return strings.Join(out, " ")
	}

	// tool depends on util and is not open: refused, naming it.
	if _, err := rename("util/src/u.mk", renUtil, "Two()", 0, "Three"); err == nil || !strings.Contains(err.Message, "tool") || !strings.Contains(err.Message, "not open") {
		t.Errorf("rename Two with tool closed: %v, want a refusal naming tool", err)
	}
	line, col := lineOf(t, renUtil, "Two()")
	if m := c.call("textDocument/prepareRename", at(uri("util/src/u.mk"), line, col)); m.Error == nil {
		t.Errorf("prepareRename with tool closed: %s, want the same refusal", m.Result)
	}

	c.open(uri("tool/src/t.mk"), renTool)
	want := "app/src/a.mk:1 tool/src/t.mk:1 util/src/u.mk:1"
	if got, err := rename("util/src/u.mk", renUtil, "Two()", 0, "Three"); err != nil || show(got) != want {
		t.Errorf("rename Two at its declaration: %s %v, want %s (ven vendors util)", show(got), err, want)
	}
	if got, err := rename("app/src/a.mk", renApp, "u.Two", 2, "Three"); err != nil || show(got) != want {
		t.Errorf("rename Two from app's use: %s %v, want %s", show(got), err, want)
	}
	want = "app/src/a.mk:2 util/src/u.mk:1"
	if got, err := rename("util/src/u.mk", renUtil, "count", 0, "total"); err != nil || show(got) != want {
		t.Errorf("rename the field count: %s %v, want %s", show(got), err, want)
	}
	// A name the declaring package already uses is refused there.
	if _, err := rename("app/src/a.mk", renApp, "u.Two", 2, "One"); err == nil || !strings.Contains(err.Message, "already uses") {
		t.Errorf("rename Two to One: %v, want a refusal", err)
	}
	c.shutdown()
}
