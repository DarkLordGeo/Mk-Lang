// Why: a workspace builds several packages as one, with one version choice,
// one pkg.sum and the root's grants and replaces for all.
// What: TestWorkspace_OneVersionOneSum, TestWorkspace_RootGrantsAndReplace
// and TestWorkspace_Nested.
// How: wsFiles writes a workspace of two members over a git server's toml
// package; the tests build members and compare versions, files and grants.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wsFiles is a workspace at the root of the tree: members a and b; a
// requires toml 1.0.0, b requires toml 1.1.0 and depends on a by path.
func wsFiles(src, rootExtra string) map[string]string {
	return map[string]string{
		"pkg.mk":   "const Package = pkg.Package(\n    name: \"ws\",\n    members: [\"a\", \"b\"],\n" + rootExtra + ")\n",
		"a/pkg.mk": "const Package = pkg.Package(\n    name: \"a\",\n    deps: [" + srcDep("toml", src, "1.0.0") + "],\n)\n",
		"a/a.mk":   "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n",
		"b/pkg.mk": "const Package = pkg.Package(\n    name: \"b\",\n    deps: [" + srcDep("toml", src, "1.1.0") + ", " + pathDep("a", "../a") + "],\n)\n",
		"b/b.mk":   "import a\nimport toml.src\n\nexport B(): int {\n    return a.A() + src.V()\n}\n",
	}
}

func TestWorkspace_OneVersionOneSum(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("10"))
	gs.tag("someone/toml", "1.1.0", tomlFiles("11"))
	src := gs.source("someone/toml")
	ws := writeTree(t, wsFiles(src, ""))
	for _, m := range []string{"a", "b"} {
		cfg := sourceCfg(gs, "")
		cfg.Root, cfg.StdDir = filepath.Join(ws, m), fakeStd(t)
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		clean(t, s.Check())
		// One version choice over every member: a builds toml 1.1.0, which
		// b requires, though a asks for 1.0.0 only.
		var got []string
		for _, dp := range s.deps {
			got = append(got, dp.cid+" "+dp.version)
		}
		_, tomlID := sourceID(src, semver{Major: 1})
		want := tomlID + " 1.1.0"
		if m == "b" {
			want = "a " + want // a member keeps its manifest name as its id
			if got[0] != tomlID+" 1.1.0" || got[1] != "a " {
				t.Errorf("%s: build list %q", m, got)
			}
			continue
		}
		if strings.Join(got, ",") != want {
			t.Errorf("%s: build list %q, want %q", m, got, want)
		}
	}
	// One pkg.sum, at the workspace root.
	sum, err := os.ReadFile(filepath.Join(ws, sumFile))
	if err != nil || !strings.Contains(string(sum), src+" v1.1.0 ") {
		t.Errorf("workspace pkg.sum %q, %v", sum, err)
	}
	for _, m := range []string{"a", "b"} {
		if _, err := os.Stat(filepath.Join(ws, m, sumFile)); err == nil {
			t.Errorf("member %s has a pkg.sum of its own", m)
		}
	}
	got, err := WorkspaceMembers(Config{Root: filepath.Join(ws, "b"), StdDir: fakeStd(t)})
	if err != nil || len(got) != 2 || got[0] != filepath.Join(ws, "a") || got[1] != filepath.Join(ws, "b") {
		t.Errorf("WorkspaceMembers %q, %v", got, err)
	}
}

// The workspace root's grants and replace apply to every member.
func TestWorkspace_RootGrantsAndReplace(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", unsafeToml())
	gs.tag("someone/toml", "1.1.0", unsafeToml())
	src := gs.source("someone/toml")
	check := func(rootExtra string) Result {
		ws := writeTree(t, wsFiles(src, rootExtra))
		return depCheck(t, filepath.Join(ws, "a"), sourceCfg(gs, ""))
	}
	e0215(t, check(""), "pkg.mk:3:")
	clean(t, check("    unsafe_deps: [\""+src+"\"],\n"))
	// A replace by path in the root makes toml a path package (no grant).
	files := wsFiles(src, "    replace: [pkg.Dep(name: \""+src+"\", path: \"local/toml\")],\n")
	for k, v := range unsafeToml() {
		files["local/toml/"+k] = v
	}
	root := writeTree(t, files)
	clean(t, depCheck(t, filepath.Join(root, "a"), sourceCfg(gs, "")))
	// The replaced source is never read for any member.
	if sum, _ := os.ReadFile(filepath.Join(root, sumFile)); strings.Contains(string(sum), src+" ") {
		t.Errorf("pkg.sum records the replaced source:\n%s", sum)
	}
}

func TestWorkspace_Nested(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"pkg.mk":     "const Package = pkg.Package(\n    name: \"ws\",\n    members: [\"a\", \"b\"],\n)\n",
		"a/pkg.mk":   "const Package = pkg.Package(\n    name: \"a\",\n)\n",
		"a/a.mk":     "export A(): int {\n    return 1\n}\n",
		"b/pkg.mk":   "const Package = pkg.Package(\n    name: \"b\",\n    members: [\"x\"],\n)\n",
		"b/x/pkg.mk": "const Package = pkg.Package(\n    name: \"x\",\n)\n",
		"b/x/x.mk":   "export X(): int {\n    return 1\n}\n",
		"b/b.mk":     "export B(): int {\n    return 1\n}\n",
	})
	e0202(t, depCheck(t, filepath.Join(ws, "a"), Config{}), "nested-workspace", "../pkg.mk:3:")
}
