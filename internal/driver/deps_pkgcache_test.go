// Why: fetched packages are cached by content so builds work offline, and
// the cache must stay honest and shareable between packages.
// What: tests of fetched trees in <cache>/pkg/<tree hash>/, read offline
// and read-only, re-hashed under MK_CACHE_VERIFY; mk cache's package line
// and prune; a vendored tree verified as a cached one; and two packages
// sharing one cache.
// How: serves packages from a test server, builds online then offline, and
// tampers with cached trees.

package driver

import (
	"os"
	"path/filepath"
	"testing"

	"mk/internal/types"
)

func TestCache_SourceTreeCachedAndOffline(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("1"))
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": depManifest("app", srcDep("toml", gs.source("someone/toml"), "1.0.0")),
		"app/a.mk":   "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n",
	})
	app := filepath.Join(ws, "app")
	cache := t.TempDir()
	clean(t, depCheck(t, app, sourceCfg(gs, cache)))
	pc, err := PackageCacheSize(cache)
	if err != nil || pc.Trees != 1 || pc.Bytes == 0 {
		t.Fatalf("package cache %+v, %v", pc, err)
	}
	ents, _ := os.ReadDir(pkgCacheDir(cache))
	tree := filepath.Join(pkgCacheDir(cache), ents[0].Name())
	if fi, err := os.Stat(filepath.Join(tree, "src", "toml.mk")); err != nil || fi.Mode().Perm()&0o222 != 0 {
		t.Errorf("a cached file is writable or missing: %v %v", fi, err)
	}
	// Offline, with the manifests and the tree cached: no fetch at all.
	n := gs.fetches
	cfg := sourceCfg(gs, cache)
	cfg.Offline = true
	clean(t, depCheck(t, app, cfg))
	if gs.fetches != n {
		t.Errorf("an offline build fetched %d times", gs.fetches-n)
	}
	// A cached tree whose bytes changed is caught under MK_CACHE_VERIFY.
	p := filepath.Join(tree, "src", "toml.mk")
	_ = os.Chmod(p, 0o644)
	if err := os.WriteFile(p, []byte("export V(): int {\n    return 2\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.CacheVerify = true
	e0202(t, depCheck(t, app, cfg), "sum-mismatch", "pkg.mk:")
	// prune: a tree no pkg.sum under the named directory names goes.
	removed, err := PrunePackages(cache, []string{t.TempDir()})
	if err != nil || len(removed) != 1 {
		t.Errorf("prune %v %v", removed, err)
	}
}

func TestCache_PruneKeepsReferencedTrees(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("1"))
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": depManifest("app", srcDep("toml", gs.source("someone/toml"), "1.0.0")),
		"app/a.mk":   "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n",
	})
	cache := t.TempDir()
	clean(t, depCheck(t, filepath.Join(ws, "app"), sourceCfg(gs, cache)))
	removed, err := PrunePackages(cache, []string{ws})
	if err != nil || len(removed) != 0 {
		t.Errorf("prune removed %v (%v), which app's pkg.sum names", removed, err)
	}
}

// A vendored tree is verified against vendor/index.txt under
// MK_CACHE_VERIFY.
func TestVendor_VerifiedUnderCacheVerify(t *testing.T) {
	app, _ := vendoredUtil(t)
	p := filepath.Join(app, "vendor", utilID, "src", "u.mk")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	clean(t, depCheck(t, app, Config{}))
	e0202(t, depCheck(t, app, Config{CacheVerify: true}), "sum-mismatch", "pkg.mk:")
}

// Two packages that both use std, and one dependency, built into one cache,
// check and build what they do with caches of their own (no build-product
// key depends on a package root's path).
func TestCache_TwoPackagesOneCache(t *testing.T) {
	tools := needTools(t)
	ws := writeTree(t, map[string]string{
		"util/pkg.mk":   depManifest("util"),
		"util/src/u.mk": "export Two(): int {\n    return 2\n}\n",
	})
	for _, name := range []string{"one", "two"} {
		writeFiles(t, ws, map[string]string{
			name + "/pkg.mk": manifest(map[string]string{"name": `"` + name + `"`,
				"bins": `[pkg.Bin(name: "` + name + `", root: "src", link_script: "", cpu: "", subsystem: "console")]`,
				"deps": "[" + pathDep("util", "../util") + "]"}),
			// The same module paths and file names in both packages.
			name + "/src/main.mk": "import util.src\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n" +
				"    n := src.Two() + " + map[string]string{"one": "1", "two": "20"}[name] + "\n    try Print(mut out, \"{n}\\n\", (n))\n}\n",
		})
	}
	shared := t.TempDir()
	want := map[string]string{"one": "3\n", "two": "22\n"}
	for _, round := range []string{"shared", "own"} {
		for _, name := range []string{"one", "two", "one"} {
			cache := shared
			if round == "own" {
				cache = t.TempDir()
			}
			s, err := NewSession(Config{Root: filepath.Join(ws, name), Tools: tools, CacheDir: cache, Target: types.X86_64Windows})
			if err != nil {
				t.Fatal(err)
			}
			r, paths, err := s.Build(filepath.Join(t.TempDir(), name+".exe"))
			if err != nil || len(r.Report.Diags) > 0 || len(paths) != 1 {
				t.Fatalf("%s %s: %v\n%s", round, name, err, r.Report.Text(r.Sources))
			}
			out, _, code := runExe(t, types.X86_64Windows, paths[0], "")
			if code != 0 || out != want[name] {
				t.Errorf("%s cache, %s: exit %d %q, want %q", round, name, code, out, want[name])
			}
		}
	}
}

// A hashless url package is cached by its tree once pkg.sum
// records its line: the first fetch records it and fills the cache, a
// later build reads the cached tree with no fetch, offline too.
func TestCache_URLTreeCachedBySumLine(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, n := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	url := srv.URL + "/ini.tar.gz"
	app := urlApp(t, url, "")
	cache := t.TempDir()
	clean(t, depCheck(t, app, Config{CacheDir: cache}))
	pc, err := PackageCacheSize(cache)
	if err != nil || pc.Trees != 1 {
		t.Fatalf("package cache %+v, %v", pc, err)
	}
	fetched := n.Load()
	clean(t, depCheck(t, app, Config{CacheDir: cache}))
	clean(t, depCheck(t, app, Config{CacheDir: cache, Offline: true}))
	if n.Load() != fetched {
		t.Errorf("builds with the tree cached fetched %d more times", n.Load()-fetched)
	}
	// With no pkg.sum line the cache is not consulted: the archive is
	// fetched again and the line recorded again.
	if err := os.Remove(filepath.Join(app, sumFile)); err != nil {
		t.Fatal(err)
	}
	clean(t, depCheck(t, app, Config{CacheDir: cache}))
	if n.Load() != fetched+1 {
		t.Errorf("a build with no pkg.sum line fetched %d times, want 1", n.Load()-fetched)
	}
}

// A fetched package's C sources and links are read in place from its
// cached tree (osDir), fresh or cached, never from cdeps/<hash of the
// identity>, which one major's versions would share in a per-user cache.
func TestCache_FetchedPackageReadInPlace(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("1"))
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": depManifest("app", srcDep("toml", gs.source("someone/toml"), "1.0.0")),
		"app/a.mk":   "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n",
	})
	cache := t.TempDir()
	for _, run := range []string{"fresh", "cached"} {
		cfg := sourceCfg(gs, cache)
		cfg.Root, cfg.StdDir = filepath.Join(ws, "app"), fakeStd(t)
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		clean(t, s.Check())
		if len(s.deps) != 1 || s.deps[0].osDir == "" || s.deps[0].osDir != s.deps[0].treeDir ||
			filepath.Dir(s.deps[0].osDir) != pkgCacheDir(cache) {
			t.Errorf("%s: deps %+v", run, s.deps)
		}
	}
}
