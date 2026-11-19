// Why: a local name is an alias in a manifest, so a manifest edit can
// change what a name means without touching any source file.
// What: TestDep_WarmMatchesColdAfterManifestEdit and
// TestDep_WarmBuildMatchesColdAfterManifestEdit over warmSteps.
// How: every step edits manifests (warmApply), checks or builds on one
// cache (warm) and compares with a fresh cache (cold).

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"mk/internal/types"
)

// warmWorkspace has three path packages: x and z export F(): i64 (1 and
// 3), y exports F(): str. app imports a and b; its manifest decides which
// package each name means.
func warmWorkspace(t *testing.T) string {
	return writeTree(t, map[string]string{
		"x/pkg.mk": depManifest("x"),
		"x/x.mk":   "export F(): i64 {\n    return 1\n}\n",
		"y/pkg.mk": depManifest("y"),
		"y/y.mk":   "export F(): str {\n    return \"two\"\n}\n",
		"z/pkg.mk": depManifest("z"),
		"z/z.mk":   "export F(): i64 {\n    return 3\n}\n",
		"app/main.mk": "import a\nimport b\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n" +
			"    p: i64 = a.F()\n    q := b.F()\n    try Print(mut out, \"{p} {q}\\n\", (p, q))\n}\n",
	})
}

// warmSteps are the manifest edits, each naming the packages a and b mean,
// and whether app then checks clean.
var warmSteps = []struct {
	name   string
	a, b   string
	clean  bool
	output string
	// members, when set, makes the tree's root a workspace of app and these:
	// a member's canonical id is its manifest name, and every other path
	// package's id is relative to the root.
	members string
}{
	{"first", "../x", "../y", true, "1 two\n", ""},
	{"swap", "../y", "../x", false, "", ""}, // p: i64 = a.F() is a str now: E0301
	{"swap back", "../x", "../y", true, "1 two\n", ""},
	{"repoint", "../z", "../y", true, "3 two\n", ""},
	{"repoint both", "../z", "../x", true, "3 1\n", ""},
	{"workspace", "../x", "../y", true, "1 two\n", `"app", "x"`},
	{"workspace swap", "../y", "../x", false, "", `"app", "x"`},
	{"workspace repoint", "../z", "../x", true, "3 1\n", `"app", "x"`},
	{"workspace of all", "../x", "../z", true, "1 3\n", `"app", "x", "y", "z"`},
	{"no workspace", "../x", "../y", true, "1 two\n", ""},
}

// warmApply writes app's manifest for step st, and the workspace root's
// manifest or its absence.
func warmApply(t *testing.T, ws string, st int) {
	t.Helper()
	s := warmSteps[st]
	writeFiles(t, ws, map[string]string{"app/pkg.mk": warmManifest(s.a, s.b)})
	root := filepath.Join(ws, "pkg.mk")
	if s.members == "" {
		if err := os.Remove(root); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return
	}
	writeFiles(t, ws, map[string]string{"pkg.mk": "const Package = pkg.Package(\n    name: \"ws\",\n    members: [" + s.members + "],\n)\n"})
}

func warmManifest(a, b string) string {
	return manifest(map[string]string{"name": `"app"`,
		"bins": `[pkg.Bin(name: "app", root: ".", link_script: "", cpu: "", subsystem: "console")]`,
		"deps": "[" + pathDep("a", a) + ", " + pathDep("b", b) + "]"})
}

func TestDep_WarmMatchesColdAfterManifestEdit(t *testing.T) {
	ws := warmWorkspace(t)
	app := filepath.Join(ws, "app")
	warm := t.TempDir()
	for i, st := range warmSteps {
		warmApply(t, ws, i)
		w := check(t, Config{Root: app, CacheDir: warm, Target: types.X86_64Windows})
		c := check(t, Config{Root: app, CacheDir: t.TempDir(), Target: types.X86_64Windows})
		if !slices.Equal(lines(w), lines(c)) {
			t.Errorf("%s: warm %q, cold %q", st.name, lines(w), lines(c))
		}
		if got := len(c.Report.Diags) == 0; got != st.clean {
			t.Errorf("%s: cold clean %v, want %v: %q", st.name, got, st.clean, lines(c))
		}
	}
}

// The same edits, built and run with the real std and toolchain: the
// warm program prints what the cold one prints.
func TestDep_WarmBuildMatchesColdAfterManifestEdit(t *testing.T) {
	tools := needTools(t)
	ws := warmWorkspace(t)
	app := filepath.Join(ws, "app")
	warm := t.TempDir()
	for i, st := range warmSteps {
		if !st.clean {
			continue
		}
		warmApply(t, ws, i)
		for _, cache := range []string{warm, t.TempDir()} {
			s, err := NewSession(Config{Root: app, Tools: tools, CacheDir: cache, Target: types.X86_64Windows})
			if err != nil {
				t.Fatal(err)
			}
			r, paths, err := s.Build(filepath.Join(t.TempDir(), "app.exe"))
			if err != nil || len(r.Report.Diags) > 0 || len(paths) != 1 {
				t.Fatalf("%s: build %v\n%s", st.name, err, r.Report.Text(r.Sources))
			}
			// In a workspace row, member x is built under its manifest name.
			if member := slices.ContainsFunc(s.deps, func(p *depPkg) bool { return p.cid == "x" }); member != (st.members != "") {
				t.Errorf("%s: x built as a member %v, want %v", st.name, member, st.members != "")
			}
			out, _, code := runExe(t, types.X86_64Windows, paths[0], "")
			if code != 0 || out != st.output {
				t.Errorf("%s (cache %s): exit %d, %q, want %q", st.name, filepath.Base(cache), code, out, st.output)
			}
		}
	}
}
