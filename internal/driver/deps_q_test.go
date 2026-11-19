// Why: the dependency walk must give the root the last word, apply replace
// chains in any order, and say when vendor/ lacks a package.
// What: TestWorkspace_MemberReplaceNoted, TestReplace_ChainOrderFree and
// TestVendor_IndexMissNoted.
// How: regression tests over small workspaces and manifests.

package driver

import (
	"path/filepath"
	"strings"
	"testing"
)

// A member's own replace and unsafe_deps are the root's to give: they are
// ignored, with a note.
func TestWorkspace_MemberReplaceNoted(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"pkg.mk":   "const Package = pkg.Package(\n    name: \"ws\",\n    members: [\"a\", \"b\"],\n)\n",
		"a/pkg.mk": "const Package = pkg.Package(\n    name: \"a\",\n)\n",
		"a/a.mk":   "export A(): int {\n    return 1\n}\n",
		"b/pkg.mk": "const Package = pkg.Package(\n    name: \"b\",\n    unsafe_deps: [\"example.com/x/y\"],\n)\n",
		"b/b.mk":   "export B(): int {\n    return 1\n}\n",
	})
	r := depCheck(t, filepath.Join(ws, "a"), Config{})
	clean(t, r)
	if !strings.Contains(strings.Join(r.Notes, "\n"), "unsafe_deps") {
		t.Errorf("notes %q, want b's unsafe_deps named", r.Notes)
	}
}

// replace entries chain whatever their order: A to B and B to a path is
// the path in both orders.
func TestReplace_ChainOrderFree(t *testing.T) {
	a, b := "example.com/x/a", "example.com/x/b"
	for _, order := range []string{
		`pkg.Dep(name: "` + a + `", source: "` + b + `", version: "1.0.0"), pkg.Dep(name: "` + b + `", path: "../local")`,
		`pkg.Dep(name: "` + b + `", path: "../local"), pkg.Dep(name: "` + a + `", source: "` + b + `", version: "1.0.0")`,
	} {
		ws := writeTree(t, map[string]string{
			"app/pkg.mk":     "const Package = pkg.Package(name: \"app\", deps: [" + srcDep("t", a, "1.0.0") + "], replace: [" + order + "])\n",
			"app/a.mk":       "import t.src\n\nexport A(): int {\n    return src.V()\n}\n",
			"local/pkg.mk":   depManifest("t"),
			"local/src/t.mk": "export V(): int {\n    return 1\n}\n",
		})
		clean(t, depCheck(t, filepath.Join(ws, "app"), Config{Offline: true}))
	}
}

// With a non-empty vendor/index.txt, a path dependency it lacks is read
// from its path, with a note naming mk vendor.
func TestVendor_IndexMissNoted(t *testing.T) {
	ws := workspace(t, []string{pathDep("util", "../util")}, nil)
	app := filepath.Join(ws, "app")
	writeFiles(t, app, map[string]string{"vendor/index.txt": "../other - other_00000000 tree-sha256:" + strings.Repeat("0", 64) + "\n"})
	r := depCheck(t, app, Config{})
	clean(t, r)
	if !strings.Contains(strings.Join(r.Notes, "\n"), "vendor/index.txt") {
		t.Errorf("notes %q", r.Notes)
	}
}
