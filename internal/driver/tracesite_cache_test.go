// Why: a trace site names a dependency by the root package's local name,
// yet the stored lowered bodies of that dependency's modules are shared by
// every package that uses it through one cache.
// What: TestPanicDisplay_TraceSiteSharedCache.
// How: the MIR keeps the canonical-id path and codegen maps it to the
// display name (cgEnv.Path, as panic sites do). Two packages naming one
// fetched, vendored or source dependency differently must get, warm, the
// bytes a cold build gives each, and MK_CACHE_VERIFY must stay clean. A
// path dependency is named by its path and is unchanged.

package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mk/internal/types"
)

func TestPanicDisplay_TraceSiteSharedCache(t *testing.T) {
	tools := needTools(t)
	target := types.X86_64Windows
	if runtime.GOOS != "windows" {
		target = types.X86_64Linux
	}
	emptyNetrc(t)
	lib := map[string]string{
		"pkg.mk": depManifest("lib"),
		"lib.mk": "export error Bad(code: i64)\n\nexport Fail(): Bad!i64 {\n    fail Bad(code: 7)\n}\n",
	}
	arch := tarGzOf(t, "lib-1.0", lib)
	srv, _ := archiveServer(t, map[string][]byte{"/lib.tar.gz": arch})
	gs := newGitServer(t)
	gs.tag("someone/lib", "1.0.0", lib)
	kinds := []string{"url", "ven", "src", "path"}
	dep := func(kind, name string) string {
		switch kind {
		case "src":
			return srcDep(name, gs.source("someone/lib"), "1.0.0")
		case "path":
			return pathDep(name, "../../lib")
		}
		return urlDep(name, srv.URL+"/lib.tar.gz", sha(arch))
	}
	bin := `[pkg.Bin(name: "app", root: ".", link_script: "", cpu: "", subsystem: "console")]`
	files := map[string]string{"lib/pkg.mk": lib["pkg.mk"], "lib/lib.mk": lib["lib.mk"]}
	for _, kind := range kinds {
		for _, name := range []string{"lib", "lib2"} {
			files[kind+"/"+name+"/pkg.mk"] = manifest(map[string]string{"name": `"app"`, "bins": bin, "deps": "[" + dep(kind, name) + "]"})
			files[kind+"/"+name+"/main.mk"] = "import " + name + "\n\nexport Main(sys: &System): " + name +
				".Bad!void {\n    v := try " + name + ".Fail()\n    _ = v\n}\n"
		}
	}
	ws := writeTree(t, files)
	for _, name := range []string{"lib", "lib2"} {
		s, err := NewSession(Config{Root: filepath.Join(ws, "ven", name), CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Vendor()
		if err != nil {
			t.Fatal(err)
		}
		clean(t, res.Result)
	}
	out := filepath.Join(t.TempDir(), "app"+exeSuffix(target))
	// build returns the binary and the dependency's canonical id (nil when
	// the build failed, reported).
	build := func(kind, name, cache string, verify bool) ([]byte, string) {
		t.Helper()
		cfg := Config{CacheDir: cache}
		if kind == "src" {
			cfg = sourceCfg(gs, cache)
		}
		cfg.Root, cfg.Target, cfg.Tools = filepath.Join(ws, kind, name), target, tools
		cfg.Offline, cfg.CacheVerify = kind == "ven", verify
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r, paths, err := s.Build(out)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Report.Diags) > 0 || len(paths) != 1 {
			t.Errorf("%s/%s (verify %v): %d paths\n%s", kind, name, verify, len(paths), r.Report.Text(r.Sources))
			return nil, ""
		}
		exe, err := os.ReadFile(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, _, code := runExe(t, target, paths[0], ""); code != 1 {
			t.Errorf("%s/%s: exit %d, want 1", kind, name, code)
		}
		cid := ""
		for id, local := range s.displayNames(nil) {
			if local == name {
				cid = id
			}
		}
		return exe, cid
	}
	// site is the start of the site string naming lib.mk by name.
	site := func(kind, name string) string {
		switch kind {
		case "ven":
			return "vendor/" + name + "/lib.mk"
		case "path":
			return "../../lib/lib.mk"
		}
		return name + "@"
	}
	for _, kind := range kinds {
		cold := map[string][]byte{}
		for _, name := range []string{"lib", "lib2"} {
			exe, cid := build(kind, name, t.TempDir(), false)
			cold[name] = exe
			if !bytes.Contains(exe, []byte(site(kind, name))) {
				t.Errorf("%s/%s: the cold binary has no site string %s", kind, name, site(kind, name))
			}
			// The trace site, like every other site, never shows the
			// canonical id.
			if kind != "path" {
				if cid == "" || cid == name {
					t.Fatalf("%s/%s: canonical id %q; the test needs one apart from the local name", kind, name, cid)
				}
				if bytes.Contains(exe, []byte(cid+"@")) || bytes.Contains(exe, []byte("vendor/"+cid+"/")) {
					t.Errorf("%s/%s: the cold binary names lib.mk by the canonical id %s", kind, name, cid)
				}
			}
		}
		// One cache: lib's package first, then lib2's, then lib's again.
		shared := t.TempDir()
		for _, name := range []string{"lib", "lib2", "lib"} {
			warm, _ := build(kind, name, shared, false)
			if !bytes.Equal(warm, cold[name]) {
				other := "lib2"
				if name == "lib2" {
					other = "lib"
				}
				stale := kind != "path" && bytes.Contains(warm, []byte(site(kind, other)))
				t.Errorf("%s/%s: the warm binary differs from the cold one (names %s: %v)", kind, name,
					strings.TrimSuffix(site(kind, other), "@"), stale)
			}
		}
		for _, name := range []string{"lib2", "lib"} {
			if v, _ := build(kind, name, shared, true); !bytes.Equal(v, cold[name]) {
				t.Errorf("%s/%s: MK_CACHE_VERIFY's binary differs from the cold one", kind, name)
			}
		}
	}
}
