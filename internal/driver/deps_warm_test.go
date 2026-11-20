// Why: a local name is an alias in a manifest, so a manifest edit can
// change what a name means without touching any source file.
// What: TestDep_WarmMatchesColdAfterManifestEdit and
// TestDep_WarmBuildMatchesColdAfterManifestEdit over warmSteps, and the std
// seed helpers other tests of this package use (stdSeedCheck, stdSeedBuild,
// seededCache).
// How: every step edits manifests (warmApply), checks or builds on one
// cache (warm) and compares with a fresh cache that holds only std's
// entries (cold).

package driver

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

// stdSeedCheck checks a one-module package with cfg, its Root and CacheDir
// replaced, into a new cache and returns that cache's directory. The cache
// holds std's check results for cfg's target, std, build ID and front-end
// mode and nothing of the package under test, so a seededCache copy of it
// gives a test's cold side a warm std while the package itself is checked
// from nothing.
func stdSeedCheck(t *testing.T, cfg Config) string {
	t.Helper()
	cfg.Root = writeTree(t, map[string]string{"pkg.mk": manifest(map[string]string{"name": `"seed"`}),
		"seed/seed.mk": "export SeedOne(): int {\n    return 1\n}\n"})
	cfg.CacheDir, cfg.NoCache = filepath.Join(t.TempDir(), "seed"), false
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r := s.Check(); len(r.Report.Diags) > 0 {
		t.Fatalf("std seed check:\n%s", r.Report.Text(r.Sources))
	}
	return cfg.CacheDir
}

// stdSeedBuild builds a program whose Main returns 0 with each of cfgs,
// their Root and CacheDir replaced, into one new cache and returns its
// directory: std's packs for each configuration, as stdSeedCheck holds
// std's check results.
func stdSeedBuild(t *testing.T, cfgs ...Config) string {
	t.Helper()
	root := writeTree(t, map[string]string{
		"pkg.mk":       manifest(map[string]string{"name": `"seed"`, "bins": `[pkg.Bin(name: "seed", root: "seed", link_script: "", cpu: "", subsystem: "console")]`}),
		"seed/seed.mk": "export Main(sys: &System): int {\n    return 0\n}\n"})
	dir := filepath.Join(t.TempDir(), "seed")
	for i, cfg := range cfgs {
		cfg.Root, cfg.CacheDir, cfg.NoCache = root, dir, false
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r, _, err := s.Build(filepath.Join(t.TempDir(), "seed"+strconv.Itoa(i)+exeSuffix(cfg.Target)))
		if err != nil || len(r.Report.Diags) > 0 {
			t.Fatalf("std seed build: %v\n%s", err, r.Report.Text(r.Sources))
		}
	}
	return dir
}

// seededCache is a new cache directory holding the entries of seed, hard
// links where the file system allows them, else copies. The store never
// writes an entry in place (a write goes to tmp and is renamed in), and lld
// renames its ThinLTO cache files in too, so the links never carry a write
// back into seed. lld's prune timestamp, which it rewrites in place, is
// copied; seed's tmp directories and GC lock are left out.
func seededCache(t testing.TB, seed string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "cache")
	err := filepath.WalkDir(seed, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(seed, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			if d.Name() == "tmp" && rel != "." {
				return filepath.SkipDir
			}
			return os.MkdirAll(to, 0o755)
		}
		if d.Name() == "gc.lock" {
			return nil
		}
		if d.Name() != "llvmcache.timestamp" && os.Link(p, to) == nil {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err != nil {
		t.Fatalf("seeding a cache from %s: %v", seed, err)
	}
	return dst
}

func TestDep_WarmMatchesColdAfterManifestEdit(t *testing.T) {
	ws := warmWorkspace(t)
	app := filepath.Join(ws, "app")
	warm := t.TempDir()
	// The cold side starts from std's check results alone: x, y, z and app
	// are checked from nothing at every step.
	seed := stdSeedCheck(t, Config{Target: types.X86_64Windows})
	for i, st := range warmSteps {
		warmApply(t, ws, i)
		w := check(t, Config{Root: app, CacheDir: warm, Target: types.X86_64Windows})
		c := check(t, Config{Root: app, CacheDir: seededCache(t, seed), Target: types.X86_64Windows})
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
	// The cold side starts from std's packs alone: the packages' packs are
	// compiled from nothing at every step.
	seed := stdSeedBuild(t, Config{Tools: tools, Target: types.X86_64Windows})
	for i, st := range warmSteps {
		if !st.clean {
			continue
		}
		warmApply(t, ws, i)
		for _, cache := range []string{warm, seededCache(t, seed)} {
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
