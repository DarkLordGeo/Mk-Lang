// Why: source dependencies come from git servers, and version choice,
// pkg.sum checks and replace must work on the build path without a
// network.
// What: tests of source dependencies through a synthetic git server
// (gitsrv_test.go): minimal version selection over the manifests reached,
// pkg.sum's lines and refusals, replace, and a version change that builds
// warm as it does cold.
// How: tomlFiles writes versions of a package; sourceCfg points a Config's
// fetches at the server, which counts what is fetched.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/types"
)

// tomlFiles is one version of package toml: module src exports V() of
// the given body, and its manifest the given deps.
func tomlFiles(body string, deps ...string) map[string]string {
	return map[string]string{
		"pkg.mk":      depManifest("toml", deps...),
		"src/toml.mk": "export V(): int {\n    return " + body + "\n}\n",
	}
}

// sourceCfg is a Config whose fetches reach gs, with a cache of its own
// unless cache is set.
func sourceCfg(gs *gitServer, cache string) Config {
	return Config{CacheDir: cache, TestHooks: TestHooks{HTTPClient: gs.client()}}
}

func TestDep_SourceVersionChoice(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "0.9.0", tomlFiles("90"))
	gs.tag("someone/toml", "0.9.3", tomlFiles("93"))
	gs.tag("someone/toml", "0.10.0", tomlFiles("100")) // never named: never read
	src := gs.source("someone/toml")
	ws := writeTree(t, map[string]string{
		// app asks for 0.9.0; util, a path package, for 0.9.3: 0.9.3 wins.
		"app/pkg.mk":  depManifest("app", srcDep("toml", src, "0.9.0"), pathDep("util", "../util")),
		"app/a.mk":    "import toml.src\nimport util\n\nexport A(): int {\n    return src.V() + util.U()\n}\n",
		"util/pkg.mk": depManifest("util", srcDep("t", src, "0.9.3")),
		"util/u.mk":   "import t.src\n\nexport U(): int {\n    return src.V()\n}\n",
	})
	app := filepath.Join(ws, "app")
	cache := t.TempDir()
	r := depCheck(t, app, sourceCfg(gs, cache))
	clean(t, r)
	_, cid := sourceID(src, semver{Major: 0})
	if !slices.Contains(r.Trace.ModulesResolved, cid+".src") {
		t.Errorf("modules %v, want %s.src", r.Trace.ModulesResolved, cid)
	}
	sum, err := readPkgSum(app)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range sum.sorted() {
		f := strings.Fields(l.String())
		lines = append(lines, f[0]+" "+f[1])
	}
	want := []string{src + " v0.9.0/pkg.mk", src + " v0.9.3", src + " v0.9.3/pkg.mk"}
	if !slices.Equal(lines, want) {
		t.Errorf("pkg.sum %q, want %q", lines, want)
	}
	if gs.filtered != 2 {
		t.Errorf("%d filtered manifest fetches, want 2", gs.filtered)
	}
	// Again: the manifests come from the cache, which pkg.sum vouches for;
	// the chosen tree is fetched again.
	before := gs.filtered
	clean(t, depCheck(t, app, sourceCfg(gs, cache)))
	if gs.filtered != before {
		t.Errorf("a warm walk fetched %d manifests", gs.filtered-before)
	}
}

func TestDep_SourceNoFilterFallsBack(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.noFilt = true
	gs.tag("someone/toml", "1.0.0", tomlFiles("1"))
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": depManifest("app", srcDep("toml", gs.source("someone/toml"), "1.0.0")),
		"app/a.mk":   "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n",
	})
	clean(t, depCheck(t, filepath.Join(ws, "app"), sourceCfg(gs, "")))
	if gs.filtered != 0 || gs.fetches != 2 {
		t.Errorf("fetches %d, filtered %d; want 2 whole fetches", gs.fetches, gs.filtered)
	}
}

func TestDep_SourceFaults(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("1"))
	src := gs.source("someone/toml")
	app := func(t *testing.T, version string, sum string) string {
		ws := writeTree(t, map[string]string{
			"app/pkg.mk": depManifest("app", srcDep("toml", src, version)),
			"app/a.mk":   "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n",
		})
		if sum != "" {
			writeFiles(t, ws, map[string]string{"app/pkg.sum": sum})
		}
		return filepath.Join(ws, "app")
	}
	// A version with no tag.
	e0202(t, depCheck(t, app(t, "2.0.0", ""), sourceCfg(gs, "")), "no-version", "pkg.mk:")
	// pkg.sum records another commit for the tag, or another tree.
	a := app(t, "1.0.0", "")
	clean(t, depCheck(t, a, sourceCfg(gs, "")))
	good, _ := os.ReadFile(filepath.Join(a, sumFile))
	moved := strings.Replace(string(good), " v1.0.0 ", " v1.0.0 "+strings.Repeat("0", 40)+" X", 1)
	moved = strings.Replace(moved, " X"+strings.Fields(string(good))[2], "", 1)
	e0202(t, depCheck(t, app(t, "1.0.0", moved), sourceCfg(gs, "")), "tag-moved", "pkg.mk:")
	tree := strings.Fields(strings.Split(string(good), "\n")[0])
	bad := strings.Replace(string(good), tree[3], "tree-sha256:"+strings.Repeat("0", 64), 1)
	e0202(t, depCheck(t, app(t, "1.0.0", bad), sourceCfg(gs, "")), "sum-mismatch", "pkg.mk:")
	// Offline with nothing cached: no pkg.sum line is sum-missing; a line
	// with no cached tree is offline.
	cfg := sourceCfg(gs, "")
	cfg.Offline = true
	e0202(t, depCheck(t, app(t, "1.0.0", ""), cfg), "sum-missing", "pkg.mk:")
	e0202(t, depCheck(t, app(t, "1.0.0", string(good)), cfg), "offline", "pkg.mk:")
}

// replace in the root manifest points a source at a path.
func TestDep_SourceReplaceByPath(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("1"))
	src := gs.source("someone/toml")
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": "const Package = pkg.Package(name: \"app\", deps: [" + srcDep("toml", src, "1.0.0") +
			"], replace: [pkg.Dep(name: \"" + src + "\", path: \"../fork\")])\n",
		"app/a.mk":      "import toml.src\n\nexport A(): int {\n    return src.V() + src.Fork()\n}\n",
		"fork/pkg.mk":   depManifest("toml"),
		"fork/src/t.mk": "export V(): int {\n    return 7\n}\n\nexport Fork(): int {\n    return 1\n}\n",
	})
	clean(t, depCheck(t, filepath.Join(ws, "app"), sourceCfg(gs, "")))
	if gs.fetches != 0 {
		t.Errorf("a replaced source was fetched %d times", gs.fetches)
	}
}

// A version change of a source dependency builds warm as it builds cold
// (a cold and warm table with the version step).
func TestDep_WarmMatchesColdAfterVersionChange(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("1"))
	gs.tag("someone/toml", "1.1.0", map[string]string{"pkg.mk": depManifest("toml"),
		"src/toml.mk": "export V(): str {\n    return \"one\"\n}\n"})
	src := gs.source("someone/toml")
	ws := writeTree(t, map[string]string{"app/a.mk": "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n"})
	app := filepath.Join(ws, "app")
	warm := t.TempDir()
	for _, st := range []struct {
		version string
		clean   bool
	}{{"1.0.0", true}, {"1.1.0", false}, {"1.0.0", true}} {
		writeFiles(t, ws, map[string]string{"app/pkg.mk": depManifest("app", srcDep("toml", src, st.version))})
		os.Remove(filepath.Join(app, sumFile))
		w := check(t, withTarget(sourceCfg(gs, warm), app))
		c := check(t, withTarget(sourceCfg(gs, t.TempDir()), app))
		if !slices.Equal(lines(w), lines(c)) {
			t.Errorf("v%s: warm %q, cold %q", st.version, lines(w), lines(c))
		}
		if got := len(c.Report.Diags) == 0; got != st.clean {
			t.Errorf("v%s: clean %v, want %v: %q", st.version, got, st.clean, lines(c))
		}
	}
}

func withTarget(c Config, root string) Config {
	c.Root, c.Target = root, types.X86_64Windows
	return c
}
