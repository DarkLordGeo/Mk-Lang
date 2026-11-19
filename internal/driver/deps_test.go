// Why: dependencies are how packages share code, and every way of reaching
// one (path, url, vendor/, cache, offline) must build the same program.
// What: tests of path and url dependencies, the import rule, one source
// per name, cycles, vendor/, offline rebuilds, the module cache across
// packages, --no-cache, TLS fetches and forbidden archive names; and the
// helpers the other deps tests share (depManifest, workspace, depCheck,
// e0202, tarGzOf, archiveServer, urlApp, zipOf).
// How: writes workspaces in temp directories, serves archives from
// httptest servers that count requests, and checks or builds against the
// stand-in std.

package driver

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"mk/internal/toolchain"
	"mk/internal/types"
)

// depManifest is a pkg.mk with the given deps list (pkg.Dep literals).
func depManifest(name string, deps ...string) string {
	return manifest(map[string]string{"name": `"` + name + `"`, "deps": "[" + strings.Join(deps, ", ") + "]"})
}

// pathDep is a pkg.Dep literal for a path dependency.
func pathDep(name, path string) string {
	return `pkg.Dep(name: "` + name + `", path: "` + path + `", url: "", hash: "")`
}

// urlDep is a pkg.Dep literal for a url dependency.
func urlDep(name, url, hash string) string {
	return `pkg.Dep(name: "` + name + `", path: "", url: "` + url + `", hash: "` + hash + `")`
}

// writeFiles writes files under dir (slash paths).
func writeFiles(t testing.TB, dir string, files map[string]string) {
	t.Helper()
	for p, text := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// workspace writes a two-package workspace: app depends on util by path.
// It returns the workspace directory.
func workspace(t testing.TB, appDeps []string, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"app/pkg.mk":    depManifest("app", appDeps...),
		"app/src/a.mk":  "import u = util.src\n\nexport A(): int {\n    return u.Two() + 1\n}\n",
		"util/pkg.mk":   depManifest("util"),
		"util/src/u.mk": "import h = src.help\n\nexport Two(): int {\n    return h.One() + h.One()\n}\n",
		// A dependency imports its own module by its local path.
		"util/src/help/h.mk": "export One(): int {\n    return 1\n}\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return writeTree(t, files)
}

// depCheck checks root with the stand-in std and a cache of its own unless
// cfg names one.
func depCheck(t testing.TB, root string, cfg Config) Result {
	t.Helper()
	cfg.Root = root
	if cfg.StdDir == "" {
		cfg.StdDir = fakeStd(t)
	}
	return check(t, cfg)
}

// e0202 fails unless r holds exactly one E0202 whose reason is reason, at
// a path with the prefix at, and returns it.
func e0202(t *testing.T, r Result, reason, at string) {
	t.Helper()
	got := lines(r)
	if len(got) != 1 || !strings.HasSuffix(got[0], ":E0202") || !strings.HasPrefix(got[0], at) {
		t.Fatalf("want one E0202 at %s, got %q\n%s", at, got, r.Report.Text(r.Sources))
	}
	if d := r.Report.Diags[0].Diag; fact(d, "reason") != reason {
		t.Fatalf("reason %q, want %q: %s", fact(d, "reason"), reason, d.Message)
	}
}

// utilID is the canonical id of workspace's util, a path package at
// ../util from the root.
var utilID = canonicalID("util", "../util")

func TestDep_PathImport(t *testing.T) {
	ws := workspace(t, []string{pathDep("util", "../util")}, nil)
	r := depCheck(t, filepath.Join(ws, "app"), Config{})
	clean(t, r)
	for _, m := range []string{utilID + ".src", utilID + ".src.help", "src"} {
		if !slices.Contains(r.Trace.ModulesResolved, m) {
			t.Errorf("module %s not resolved: %v", m, r.Trace.ModulesResolved)
		}
	}
	// A dependency's diagnostic cites its file relative to the package.
	writeFiles(t, ws, map[string]string{"util/src/help/h.mk": "export One(): int {\n    return true\n}\n"})
	r = depCheck(t, filepath.Join(ws, "app"), Config{})
	if got := lines(r); len(got) != 1 || !strings.HasPrefix(got[0], "../util/src/help/h.mk:2:") {
		t.Errorf("got %q", got)
	}
}

func TestDep_RootModuleAndTransitive(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": depManifest("app", pathDep("util", "../util")),
		"app/a.mk":   "import util\n\nexport A(): int {\n    return util.Three()\n}\n",
		// util's root files form module util; util depends on base.
		"util/pkg.mk":   depManifest("util", pathDep("base", "../base")),
		"util/u.mk":     "import b = base.lib\n\nexport Three(): int {\n    return b.Three()\n}\n",
		"base/pkg.mk":   depManifest("base"),
		"base/lib/b.mk": "export Three(): int {\n    return 3\n}\n",
	})
	clean(t, depCheck(t, filepath.Join(ws, "app"), Config{}))
	// base is util's dependency, not app's: app cannot import it.
	writeFiles(t, ws, map[string]string{"app/b.mk": "import base.lib\n"})
	r := depCheck(t, filepath.Join(ws, "app"), Config{})
	if got := lines(r); len(got) != 1 || got[0] != "b.mk:1:8:E0202" {
		t.Errorf("got %q\n%s", got, r.Report.Text(r.Sources))
	}
}

func TestDep_Faults(t *testing.T) {
	good := "sha256:" + strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name   string
		deps   []string
		extra  map[string]string
		reason string
	}{
		{"both", []string{`pkg.Dep(name: "util", path: "../util", url: "https://example.com/u.tar.gz", hash: "` + good + `")`}, nil, "invalid"},
		{"neither", []string{`pkg.Dep(name: "util", path: "", url: "", hash: "")`}, nil, "invalid"},
		// A url with no hash is valid (its tree is a pkg.sum line), but
		// not one pkg.sum cannot hold.
		{"no hash, query", []string{urlDep("util", "https://example.com/u.tar.gz?token=x", "")}, nil, "invalid"},
		{"b3 hash", []string{urlDep("util", "https://example.com/u.tar.gz", "b3:"+strings.Repeat("ab", 32))}, nil, "invalid"},
		{"http", []string{urlDep("util", "http://example.com/u.tar.gz", good)}, nil, "invalid"},
		{"path with hash", []string{`pkg.Dep(name: "util", path: "../util", url: "", hash: "` + good + `")`}, nil, "invalid"},
		{"reserved", []string{pathDep("std", "../util")}, nil, "invalid"},
		{"main", []string{pathDep("main", "../util")}, nil, "invalid"},
		{"not a segment", []string{pathDep("a.b", "../util")}, nil, "invalid"},
		{"own module", []string{pathDep("src", "../util")}, nil, "invalid"},
		{"missing", []string{pathDep("util", "../nothing")}, nil, "invalid"},
		{"cycle", []string{pathDep("util", "../util"), pathDep("other", "../other")},
			map[string]string{"other/pkg.mk": depManifest("other", pathDep("third", "../third")),
				"third/pkg.mk": depManifest("third", pathDep("other", "../other"))}, "cycle"},
		{"cycle to root", []string{pathDep("util", "../util"), pathDep("other", "../other")},
			map[string]string{"other/pkg.mk": depManifest("other", pathDep("app", "../app"))}, "cycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := workspace(t, tc.deps, tc.extra)
			r := depCheck(t, filepath.Join(ws, "app"), Config{})
			at := "pkg.mk:"
			if tc.reason == "conflict" || tc.reason == "cycle" {
				at = "../" // in the dependency's manifest
			}
			e0202(t, r, tc.reason, at)
		})
	}
	// The same source twice is one dependency.
	ws := workspace(t, []string{pathDep("util", "../util"), pathDep("other", "../other")},
		map[string]string{"other/pkg.mk": depManifest("other", pathDep("util", "../util"))})
	clean(t, depCheck(t, filepath.Join(ws, "app"), Config{}))
	// One local name for two packages in two manifests is no conflict: a
	// name is an alias in the manifest that writes it.
	ws = workspace(t, []string{pathDep("util", "../util"), pathDep("other", "../other")},
		map[string]string{"other/pkg.mk": depManifest("other", pathDep("util", "../util2")),
			"util2/pkg.mk": depManifest("util2")})
	clean(t, depCheck(t, filepath.Join(ws, "app"), Config{}))
}

// tarGzOf is a .tar.gz of files (slash paths, sorted), under top/ when top
// is set, with extra raw entries appended.
func tarGzOf(t testing.TB, top string, files map[string]string, extra ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		p := n
		if top != "" {
			p = top + "/" + n
		}
		if err := tw.WriteHeader(&tar.Header{Name: p, Mode: 0o644, Size: int64(len(files[n])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(files[n]))
	}
	for _, h := range extra {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write(make([]byte, h.Size))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// iniFiles is a small package fetched by url.
func iniFiles() map[string]string {
	return map[string]string{
		"pkg.mk":         depManifest("ini"),
		"src/ini.mk":     "import p = src.parse\n\nexport Keys(): int {\n    return p.Count() * 2\n}\n",
		"src/parse/p.mk": "export Count(): int {\n    return 21\n}\n",
	}
}

// archiveServer serves archives by path and counts the requests.
func archiveServer(t *testing.T, files map[string][]byte) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// urlApp writes a package whose src/a.mk imports ini.src from a url dep.
func urlApp(t testing.TB, url, hash string) string {
	return writeTree(t, map[string]string{
		"pkg.mk":   depManifest("app", urlDep("ini", url, hash)),
		"src/a.mk": "import ini.src\n\nexport A(): int {\n    return src.Keys()\n}\n",
	})
}

func TestDep_URLImport(t *testing.T) {
	arch := tarGzOf(t, "ini-1.2", iniFiles())
	srv, n := archiveServer(t, map[string][]byte{"/ini-1.2.tar.gz": arch})
	app := urlApp(t, srv.URL+"/ini-1.2.tar.gz", sha(arch))
	cache := t.TempDir()
	r := depCheck(t, app, Config{CacheDir: cache})
	clean(t, r)
	if !slices.Contains(r.Trace.ModulesResolved, urlCID(srv.URL+"/ini-1.2.tar.gz")+".src.parse") {
		t.Errorf("modules: %v", r.Trace.ModulesResolved)
	}
	// Fetched once: the second check reads the cache.
	clean(t, depCheck(t, app, Config{CacheDir: cache}))
	if n.Load() != 1 {
		t.Errorf("requests: %d, want 1", n.Load())
	}
	// A fetched dependency's file is cited under <canonical id>@<hash>/.
	bad := iniFiles()
	bad["src/parse/p.mk"] = "export Count(): int {\n    return false\n}\n"
	arch2 := tarGzOf(t, "", bad)
	srv2, _ := archiveServer(t, map[string][]byte{"/b.tar.gz": arch2})
	r = depCheck(t, urlApp(t, srv2.URL+"/b.tar.gz", sha(arch2)), Config{CacheDir: cache})
	want := urlCID(srv2.URL+"/b.tar.gz") + "@" + sha(arch2)[7:19] + "/src/parse/p.mk:2:"
	if got := lines(r); len(got) != 1 || !strings.HasPrefix(got[0], want) {
		t.Errorf("got %q, want %s...", got, want)
	}
}

func TestDep_WrongHash(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/a.tar.gz": arch})
	wrong := sha([]byte("another archive"))
	r := depCheck(t, urlApp(t, srv.URL+"/a.tar.gz", wrong), Config{})
	e0202(t, r, "hash-mismatch", "pkg.mk:")
	// A 404 is a fetch failure.
	r = depCheck(t, urlApp(t, srv.URL+"/missing.tar.gz", sha(arch)), Config{})
	e0202(t, r, "fetch", "pkg.mk:")
}

func TestDep_PathEscapeArchive(t *testing.T) {
	files := iniFiles()
	evil := tarGzOf(t, "", files, &tar.Header{Name: "../../escape.mk", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg})
	srv, _ := archiveServer(t, map[string][]byte{"/e.tar.gz": evil})
	cache := t.TempDir()
	r := depCheck(t, urlApp(t, srv.URL+"/e.tar.gz", sha(evil)), Config{CacheDir: cache})
	e0202(t, r, "unsafe-archive", "pkg.mk:")
	// Nothing was written outside the cache.
	if _, err := os.Stat(filepath.Join(filepath.Dir(cache), "escape.mk")); err == nil {
		t.Error("escape.mk was written")
	}
	link := tarGzOf(t, "", files, &tar.Header{Name: "src/l.mk", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
	srv2, _ := archiveServer(t, map[string][]byte{"/l.tar.gz": link})
	e0202(t, depCheck(t, urlApp(t, srv2.URL+"/l.tar.gz", sha(link)), Config{}), "unsafe-archive", "pkg.mk:")
}

func TestDep_OfflineRebuild(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, n := archiveServer(t, map[string][]byte{"/a.tar.gz": arch})
	app := urlApp(t, srv.URL+"/a.tar.gz", sha(arch))
	cache := t.TempDir()
	clean(t, depCheck(t, app, Config{CacheDir: cache}))
	srv.Close()
	// The server is gone: offline, and online too, the cache serves it.
	clean(t, depCheck(t, app, Config{CacheDir: cache, Offline: true}))
	clean(t, depCheck(t, app, Config{CacheDir: cache}))
	if n.Load() != 1 {
		t.Errorf("requests: %d, want 1", n.Load())
	}
	// A cache that never saw it: offline is E0202 offline, at the Dep.
	e0202(t, depCheck(t, app, Config{CacheDir: t.TempDir(), Offline: true}), "offline", "pkg.mk:")
}

func TestDep_VendorDirReadFirst(t *testing.T) {
	ws := workspace(t, []string{pathDep("util", "../util")}, nil)
	app := filepath.Join(ws, "app")
	// vendor/util differs from ../util: the build reads vendor/util.
	writeFiles(t, app, map[string]string{
		"vendor/util/pkg.mk":   depManifest("util"),
		"vendor/util/src/u.mk": "export Two(): int {\n    return true\n}\n",
	})
	r := depCheck(t, app, Config{})
	if got := lines(r); len(got) != 1 || !strings.HasPrefix(got[0], "vendor/util/src/u.mk:2:") {
		t.Errorf("got %q", got)
	}
	// vendor forms no module of the package (vendor.util.src would clash
	// with nothing, but it must not be read twice).
	if slices.Contains(r.Trace.ModulesResolved, "vendor.util.src") {
		t.Error("vendor/ formed a module")
	}
	// A url dependency in vendor/ fetches nothing.
	app2 := urlApp(t, "https://example.invalid/never.tar.gz", sha([]byte("x")))
	writeFiles(t, app2, map[string]string{
		"vendor/ini/pkg.mk":         iniFiles()["pkg.mk"],
		"vendor/ini/src/ini.mk":     iniFiles()["src/ini.mk"],
		"vendor/ini/src/parse/p.mk": iniFiles()["src/parse/p.mk"],
	})
	clean(t, depCheck(t, app2, Config{Offline: true}))
}

func TestDep_InTreePathDep(t *testing.T) {
	// A path dependency inside the package tree is the dependency's, not
	// a module of the package.
	root := writeTree(t, map[string]string{
		"pkg.mk":             depManifest("app", pathDep("util", "libs/util")),
		"src/a.mk":           "import u = util.src\n\nexport A(): int {\n    return u.Two()\n}\n",
		"libs/util/pkg.mk":   depManifest("util"),
		"libs/util/src/u.mk": "export Two(): int {\n    return 2\n}\n",
	})
	r := depCheck(t, root, Config{})
	clean(t, r)
	if slices.Contains(r.Trace.ModulesResolved, "libs.util.src") {
		t.Error("the dependency's directory formed a module of the package")
	}
}

func TestDep_InvalidationAcrossPackages(t *testing.T) {
	ws := workspace(t, []string{pathDep("util", "../util")}, nil)
	app := filepath.Join(ws, "app")
	cache := t.TempDir()
	clean(t, depCheck(t, app, Config{CacheDir: cache}))
	r := depCheck(t, app, Config{CacheDir: cache})
	clean(t, r)
	for _, m := range []string{"src", utilID + ".src", utilID + ".src.help"} {
		if !slices.Contains(r.Trace.ModulesFromCache, m) {
			t.Fatalf("warm: %s not from the cache: %v", m, r.Trace.ModulesFromCache)
		}
	}
	// A body edit in the dependency: its module is checked again, the
	// importer's is reused.
	writeFiles(t, ws, map[string]string{"util/src/help/h.mk": "export One(): int {\n    return 2 - 1\n}\n"})
	r = depCheck(t, app, Config{CacheDir: cache})
	clean(t, r)
	if slices.Contains(r.Trace.ModulesFromCache, utilID+".src.help") || !slices.Contains(r.Trace.ModulesFromCache, "src") {
		t.Errorf("body edit: from cache %v", r.Trace.ModulesFromCache)
	}
	// An exported signature edit: the importer is checked again.
	writeFiles(t, ws, map[string]string{"util/src/u.mk": "import h = src.help\n\nexport Two(): i64 {\n    return 2\n}\n"})
	r = depCheck(t, app, Config{CacheDir: cache})
	if slices.Contains(r.Trace.ModulesFromCache, "src") || slices.Contains(r.Trace.ModulesFromCache, utilID+".src") {
		t.Errorf("signature edit: from cache %v", r.Trace.ModulesFromCache)
	}
}

// TestVendor_RoundTrip: mk vendor copies a path and a url dependency into
// vendor/<canonical id> with vendor/index.txt; with the sources gone and
// the network off, the package still checks from vendor/ alone.
func TestVendor_RoundTrip(t *testing.T) {
	arch := tarGzOf(t, "ini-1", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	ws := workspace(t, []string{pathDep("util", "../util"), urlDep("ini", srv.URL+"/ini.tar.gz", sha(arch))},
		map[string]string{"app/src/b.mk": "import ini.src\n\nexport B(): int {\n    return src.Keys()\n}\n"})
	app := filepath.Join(ws, "app")
	cfg := Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Vendor()
	if err != nil {
		t.Fatal(err)
	}
	clean(t, res.Result)
	var got []string
	for _, d := range res.Deps {
		got = append(got, d.Dir+" "+d.From)
	}
	iniID := urlCID(srv.URL + "/ini.tar.gz")
	want := []string{"vendor/" + iniID + " " + srv.URL + "/ini.tar.gz", "vendor/" + utilID + " ../util"}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("vendored %q, want %q", got, want)
	}
	for _, f := range []string{"vendor/" + utilID + "/pkg.mk", "vendor/" + utilID + "/src/help/h.mk", "vendor/" + iniID + "/pkg.mk",
		"vendor/" + iniID + "/src/parse/p.mk", "vendor/index.txt"} {
		if _, err := os.Stat(filepath.Join(app, filepath.FromSlash(f))); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	srv.Close()
	if err := os.RemoveAll(filepath.Join(ws, "util")); err != nil {
		t.Fatal(err)
	}
	r := depCheck(t, app, Config{CacheDir: t.TempDir(), Offline: true})
	clean(t, r)
	if !slices.Contains(r.Trace.ModulesResolved, iniID+".src.parse") || !slices.Contains(r.Trace.ModulesResolved, utilID+".src") {
		t.Errorf("modules %v", r.Trace.ModulesResolved)
	}
	// Vendoring again replaces the copy (the sources are gone: the walk
	// ignores vendor/, so it fails at the Dep).
	s2, _ := NewSession(cfg)
	res, err = s2.Vendor()
	if err != nil || len(res.Result.Report.Diags) == 0 {
		t.Errorf("re-vendor without sources: err %v, diags %d", err, len(res.Result.Report.Diags))
	}
}

// testThreePackageWorkspace: app -> lib1 (path) -> lib2 (url, from a local
// server) builds natively at dev and release and prints lib2's value
// through lib1; lib1's bin is not built.
func testThreePackageWorkspace(t *testing.T, target types.Target) {
	tools := needTools(t)
	if prefix, ok := toolchain.Runner(target.LLVMTriple); ok && len(prefix) > 0 && !linuxRun() {
		t.Skip(linuxOff)
	}
	lib2 := map[string]string{
		"pkg.mk":     depManifest("lib2"),
		"num/num.mk": "export Base(): int {\n    return 40\n}\n",
	}
	arch := tarGzOf(t, "lib2-1.0", lib2)
	srv, _ := archiveServer(t, map[string][]byte{"/lib2.tar.gz": arch})
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": manifest(map[string]string{"name": `"app"`, "bins": `[pkg.Bin(name: "app", root: "src", link_script: "", cpu: "", subsystem: "console")]`,
			"deps": "[" + pathDep("lib1", "../lib1") + "]"}),
		"app/src/main.mk": "import lib1.calc\n\nexport Main(sys: &System): WriteError!void {\n" +
			"    var out := sys.Stdout()\n    n := calc.Answer()\n    try Print(mut out, \"{n}\\n\", (n))\n}\n",
		"lib1/pkg.mk": manifest(map[string]string{"name": `"lib1"`, "bins": `[pkg.Bin(name: "tool", root: "tool", link_script: "", cpu: "", subsystem: "console")]`,
			"deps": "[" + urlDep("lib2", srv.URL+"/lib2.tar.gz", sha(arch)) + "]"}),
		"lib1/calc/calc.mk": "import lib2.num\n\nexport Answer(): int {\n    return num.Base() + 2\n}\n",
		"lib1/tool/tool.mk": "export Main(sys: &System): int {\n    return 7\n}\n",
	})
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			s, err := NewSession(Config{Root: filepath.Join(ws, "app"), Target: target, Profile: prof, Tools: tools, CacheDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			r, paths, err := s.Build(filepath.Join(t.TempDir(), "app"+exeSuffix(target)))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Report.Diags) > 0 || len(paths) != 1 {
				t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
			}
			out, errOut, code := runExe(t, target, paths[0], "")
			if code != 0 || out != "42\n" {
				t.Errorf("exit %d, stdout %q, stderr %q; want 42", code, out, errOut)
			}
		})
	}
}

func TestDep_ThreePackageWorkspaceWindows(t *testing.T) {
	testThreePackageWorkspace(t, types.X86_64Windows)
}
func TestDep_ThreePackageWorkspaceLinux(t *testing.T) {
	testThreePackageWorkspace(t, types.X86_64Linux)
}

// TestDep_TransitiveNameClash: a transitive dependency named like the
// first segment of one of the root package's own modules: its modules
// carry its canonical id, so the name clashes with nothing and the package
// checks, whether or not the two declare the same items.
func TestDep_TransitiveNameClash(t *testing.T) {
	for _, tc := range []struct{ name, item string }{{"distinct items", "Nine"}, {"shared item", "Three"}} {
		t.Run(tc.name, func(t *testing.T) {
			ws := writeTree(t, map[string]string{
				"app/pkg.mk":    depManifest("app", pathDep("util", "../util")),
				"app/a.mk":      "import util\n\nexport A(): int {\n    return util.Three()\n}\n",
				"app/base/x.mk": "export " + tc.item + "(): int {\n    return 9\n}\n",
				"util/pkg.mk":   depManifest("util", pathDep("base", "../base")),
				"util/u.mk":     "import b = base.lib\n\nexport Three(): int {\n    return b.Three()\n}\n",
				"base/pkg.mk":   depManifest("base"),
				"base/lib/b.mk": "export Three(): int {\n    return 3\n}\n",
				"base/b.mk":     "export Three(): int {\n    return 3\n}\n",
			})
			// base's modules carry its canonical id, so the
			// package's own base module and util's dependency base never
			// share a module path.
			clean(t, depCheck(t, filepath.Join(ws, "app"), Config{}))
		})
	}
}

// TestDep_NoCacheFetchesAndWritesNothing (--no-cache skips every cache read
// and write): a url dependency is fetched on every --no-cache run and
// nothing is written to the cache directory; an offline --no-cache run has
// no archive.
func TestDep_NoCacheFetchesAndWritesNothing(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, n := archiveServer(t, map[string][]byte{"/a.tar.gz": arch})
	app := urlApp(t, srv.URL+"/a.tar.gz", sha(arch))
	dir := filepath.Join(t.TempDir(), "cache")
	for range 2 {
		clean(t, depCheck(t, app, Config{CacheDir: dir, NoCache: true}))
	}
	if n.Load() != 2 {
		t.Errorf("requests: %d, want 2 (one per --no-cache run)", n.Load())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		ents, _ := os.ReadDir(dir)
		t.Errorf("the cache directory was created under --no-cache: %v %v", err, ents)
	}
	e0202(t, depCheck(t, app, Config{CacheDir: dir, NoCache: true, Offline: true}), "offline", "pkg.mk:")
	// With the cache, the archive is kept: one more request, then none.
	clean(t, depCheck(t, app, Config{CacheDir: dir}))
	clean(t, depCheck(t, app, Config{CacheDir: dir, Offline: true}))
	if n.Load() != 3 {
		t.Errorf("requests: %d, want 3", n.Load())
	}
}

// zipOf is a .zip of files (slash paths, sorted), under top/ when top is
// set.
func zipOf(t testing.TB, top string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		p := n
		if top != "" {
			p = top + "/" + n
		}
		w, err := zw.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(files[n]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestDep_URLImportTLS: a .zip fetched over HTTPS from a test
// server with its own certificate (TestHooks.HTTPClient trusts it) checks
// clean, end to end; without that client the certificate is not trusted
// and the Dep fails with reason fetch.
func TestDep_URLImportTLS(t *testing.T) {
	arch := zipOf(t, "ini-1.2", iniFiles())
	var n atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.URL.Path != "/ini.zip" {
			http.NotFound(w, r)
			return
		}
		w.Write(arch)
	}))
	t.Cleanup(srv.Close)
	if !strings.HasPrefix(srv.URL, "https://127.0.0.1:") {
		t.Fatalf("server url %s", srv.URL)
	}
	app := urlApp(t, srv.URL+"/ini.zip", sha(arch))
	hooks := TestHooks{HTTPClient: srv.Client()}
	cache := t.TempDir()
	r := depCheck(t, app, Config{CacheDir: cache, TestHooks: hooks})
	clean(t, r)
	if !slices.Contains(r.Trace.ModulesResolved, urlCID(srv.URL+"/ini.zip")+".src.parse") {
		t.Errorf("modules: %v", r.Trace.ModulesResolved)
	}
	clean(t, depCheck(t, app, Config{CacheDir: cache, TestHooks: hooks}))
	if n.Load() != 1 {
		t.Errorf("requests: %d, want 1", n.Load())
	}
	// The default client does not trust the test certificate (a fresh
	// cache, so the store cannot answer). The handshake fails before any
	// request reaches the handler.
	e0202(t, depCheck(t, app, Config{CacheDir: t.TempDir()}), "fetch", "pkg.mk:")
	if n.Load() != 1 {
		t.Errorf("requests after the untrusted fetch: %d, want 1", n.Load())
	}
}

// TestDep_WindowsForbiddenNameArchive: an archive holding a
// name Windows cannot create is refused at the Dep before anything is
// built or vendored, on every OS.
func TestDep_WindowsForbiddenNameArchive(t *testing.T) {
	files := iniFiles()
	files["src/a?b.mk"] = "export Q(): int {\n    return 1\n}\n"
	arch := tarGzOf(t, "", files)
	srv, _ := archiveServer(t, map[string][]byte{"/q.tar.gz": arch})
	app := urlApp(t, srv.URL+"/q.tar.gz", sha(arch))
	e0202(t, depCheck(t, app, Config{}), "unsafe-archive", "pkg.mk:")
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Vendor()
	if err != nil {
		t.Fatalf("mk vendor: an I/O error, not a diagnostic: %v", err)
	}
	if got := lines(res.Result); len(got) != 1 || !strings.HasSuffix(got[0], ":E0202") {
		t.Errorf("mk vendor: %q", got)
	}
	if _, err := os.Stat(filepath.Join(app, "vendor")); err == nil {
		t.Error("vendor/ was written")
	}
}
