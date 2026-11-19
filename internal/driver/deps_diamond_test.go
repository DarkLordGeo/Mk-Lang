// Why: version selection, the package cache and vendoring must agree on a
// diamond of dependencies, online and offline.
// What: TestDep_DiamondWorkspaceWindows and TestDep_DiamondWorkspaceLinux.
// How: a workspace whose member app needs three source packages in a
// diamond (app -> b, cc; b -> d 1.0.0; cc -> d 1.1.0, so d 1.1.0 is
// chosen), served by the synthetic git server. The program is built and
// run from the server, then offline from the cache, then offline from
// vendor/ with an empty cache; all three print the same.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/toolchain"
	"mk/internal/types"
)

func testDiamondWorkspace(t *testing.T, target types.Target) {
	tools := needTools(t)
	if prefix, ok := toolchain.Runner(target.LLVMTriple); ok && len(prefix) > 0 && !linuxRun() {
		t.Skip(linuxOff)
	}
	emptyNetrc(t)
	gs := newGitServer(t)
	d, b, c := gs.source("fx/d"), gs.source("fx/b"), gs.source("fx/cc")
	gs.tag("fx/d", "1.0.0", map[string]string{"pkg.mk": depManifest("d"), "src/d.mk": "export D(): int {\n    return 10\n}\n"})
	gs.tag("fx/d", "1.1.0", map[string]string{"pkg.mk": depManifest("d"), "src/d.mk": "export D(): int {\n    return 11\n}\n"})
	gs.tag("fx/b", "1.0.0", map[string]string{"pkg.mk": depManifest("b", srcDep("d", d, "1.0.0")),
		"src/b.mk": "import d.src\n\nexport B(): int {\n    return src.D() + 100\n}\n"})
	gs.tag("fx/cc", "1.0.0", map[string]string{"pkg.mk": depManifest("cc", srcDep("d", d, "1.1.0")),
		"src/c.mk": "import d.src\n\nexport C(): int {\n    return src.D() + 200\n}\n"})
	ws := writeTree(t, map[string]string{
		"pkg.mk": "const Package = pkg.Package(\n    name: \"ws\",\n    members: [\"app\"],\n)\n",
		"app/pkg.mk": manifest(map[string]string{"name": `"app"`, "bins": `[pkg.Bin(name: "app", root: "main", link_script: "", cpu: "", subsystem: "console")]`,
			"deps": "[" + srcDep("b", b, "1.0.0") + ", " + srcDep("cc", c, "1.0.0") + "]"}),
		"app/main/main.mk": "import bs = b.src\nimport cs = cc.src\n\nexport Main(sys: &System): WriteError!void {\n" +
			"    var out := sys.Stdout()\n    x := bs.B()\n    y := cs.C()\n    try Print(mut out, \"{x} {y}\\n\", (x, y))\n}\n",
	})
	app := filepath.Join(ws, "app")
	cache := t.TempDir()
	run := func(step string, cfg Config) {
		t.Helper()
		cfg.Root, cfg.Target, cfg.Tools = app, target, tools
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r, paths, err := s.Build(filepath.Join(t.TempDir(), "app"+exeSuffix(target)))
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if len(r.Report.Diags) > 0 || len(paths) != 1 {
			t.Fatalf("%s: build: %d paths\n%s", step, len(paths), r.Report.Text(r.Sources))
		}
		out, errOut, code := runExe(t, target, paths[0], "")
		if code != 0 || out != "111 211\n" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want 111 211", step, code, out, errOut)
		}
	}
	run("from the server", sourceCfg(gs, cache))
	sum := readFile(t, filepath.Join(ws, sumFile))
	for _, w := range []string{b + " v1.0.0 ", c + " v1.0.0 ", d + " v1.1.0 ", d + " v1.0.0/pkg.mk "} {
		if !strings.Contains(sum, w) {
			t.Errorf("workspace pkg.sum lacks %q:\n%s", w, sum)
		}
	}
	if strings.Contains(sum, d+" v1.0.0 ") {
		t.Errorf("pkg.sum records d 1.0.0's tree, which was never chosen:\n%s", sum)
	}
	run("offline from the cache", Config{CacheDir: cache, Offline: true, Locked: true})
	vs, err := NewSession(Config{Root: app, CacheDir: cache, Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := vs.Vendor(); err != nil || len(res.Result.Report.Diags) > 0 {
		t.Fatalf("mk vendor: %v\n%s", err, res.Result.Report.Text(res.Result.Sources))
	}
	if _, err := os.Stat(filepath.Join(app, "vendor", vendorIndexFile)); err != nil {
		t.Fatal(err)
	}
	run("offline from vendor/", Config{CacheDir: t.TempDir(), Offline: true})
}

func TestDep_DiamondWorkspaceWindows(t *testing.T) { testDiamondWorkspace(t, types.X86_64Windows) }
func TestDep_DiamondWorkspaceLinux(t *testing.T)   { testDiamondWorkspace(t, types.X86_64Linux) }
