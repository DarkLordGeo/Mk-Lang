// Why: a vendored build must work offline and still be the same build as
// one from the sources.
// What: TestVendor_SourcePackageRead, TestGrant_VendoredSourcePackage and
// TestPkgCmd_VendoredPackage.
// How: mk vendor writes `<source> v<version> <canonical id> tree-sha256:<hex>`,
// and a build that reads vendor/ takes its versions from the index, with no
// version walk, no pkg.sum and no network. A vendored package keeps its
// source's identity, so it still needs its grant.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// vendorSourceApp vendors app, which requires toml 1.0.0 (which requires
// dt 1.0.0) and grants it, from gs, and returns app's directory.
func vendorSourceApp(t *testing.T, gs *gitServer, tomlFiles map[string]string) (app, toml, dt string) {
	t.Helper()
	dt, toml = gs.source("someone/dt"), gs.source("someone/toml")
	gs.tag("someone/dt", "1.0.0", map[string]string{"pkg.mk": depManifest("dt"), "src/dt.mk": "export D(): int {\n    return 4\n}\n"})
	tomlFiles["pkg.mk"] = strings.Replace(tomlFiles["pkg.mk"], "pkg.Package(\n", "pkg.Package(\n    deps: ["+srcDep("dt", dt, "1.0.0")+"],\n", 1)
	gs.tag("someone/toml", "1.0.0", tomlFiles)
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": grantRoot("app", `"toml"`, srcDep("toml", toml, "1.0.0")),
		"app/a.mk":   "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n",
	})
	app = filepath.Join(ws, "app")
	cfg := sourceCfg(gs, t.TempDir())
	cfg.Root, cfg.StdDir = app, fakeStd(t)
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Vendor()
	if err != nil {
		t.Fatal(err)
	}
	clean(t, res.Result)
	return app, toml, dt
}

// offlineCheck checks app from vendor/ alone: no network, a fresh cache
// and no pkg.sum.
func offlineCheck(t *testing.T, app string) (*Session, Result) {
	t.Helper()
	if err := os.Remove(filepath.Join(app, sumFile)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir(), Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, s.Check()
}

func TestVendor_SourcePackageRead(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	f := tomlFiles("10")
	f["pkg.mk"] = "const Package = pkg.Package(\n    name: \"toml\",\n)\n"
	app, toml, dt := vendorSourceApp(t, gs, f)
	idx, err := os.ReadFile(filepath.Join(app, "vendor", vendorIndexFile))
	if err != nil {
		t.Fatal(err)
	}
	_, tomlID := sourceID(toml, semver{Major: 1})
	_, dtID := sourceID(dt, semver{Major: 1})
	for _, w := range []string{dt + " v1.0.0 " + dtID + " tree-sha256:", toml + " v1.0.0 " + tomlID + " tree-sha256:"} {
		if !strings.Contains(string(idx), w) {
			t.Errorf("index lacks %q:\n%s", w, idx)
		}
	}
	s, r := offlineCheck(t, app)
	clean(t, r)
	var got []string
	for _, p := range s.deps {
		got = append(got, p.cid+" "+p.version)
		if !p.vendored || p.source == "" {
			t.Errorf("%s: vendored %v, source %q", p.cid, p.vendored, p.source)
		}
	}
	if strings.Join(got, ",") != dtID+" 1.0.0,"+tomlID+" 1.0.0" {
		t.Errorf("build list %q", got)
	}
	if _, err := os.Stat(filepath.Join(app, sumFile)); err == nil {
		t.Errorf("a build from vendor/ wrote pkg.sum")
	}

	// A minimum above the index's version: the index is stale.
	gs.tag("someone/toml", "1.1.0", f)
	writeFiles(t, app, map[string]string{"pkg.mk": grantRoot("app", `"`+toml+`"`, srcDep("toml", toml, "1.1.0"))})
	_, r = offlineCheck(t, app)
	if got := lines(r); len(got) != 1 || !strings.Contains(r.Report.Diags[0].Diag.Message, "run mk vendor again") ||
		!strings.Contains(r.Report.Diags[0].Diag.Message, "v1.0.0") {
		t.Errorf("stale index: %q\n%s", got, r.Report.Text(r.Sources))
	}
	// A source the index does not hold.
	writeFiles(t, app, map[string]string{"pkg.mk": grantRoot("app", `"`+toml+`"`, srcDep("toml", toml, "1.0.0"),
		srcDep("other", gs.source("someone/other"), "1.0.0"))})
	_, r = offlineCheck(t, app)
	if got := lines(r); len(got) != 1 || !strings.Contains(r.Report.Diags[0].Diag.Message, "not in vendor/index.txt") {
		t.Errorf("missing source: %q\n%s", got, r.Report.Text(r.Sources))
	}
}

// A vendored source package keeps its identity: unsafe code needs the grant.
func TestGrant_VendoredSourcePackage(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	app, toml, _ := vendorSourceApp(t, gs, unsafeToml())
	writeFiles(t, app, map[string]string{"pkg.mk": grantRoot("app", "", srcDep("toml", toml, "1.0.0"))})
	_, r := offlineCheck(t, app)
	d := e0215(t, r, "pkg.mk:3:")
	if !strings.Contains(d.Message, "toml ("+toml+" v1.0.0)") {
		t.Errorf("message %q", d.Message)
	}
	writeFiles(t, app, map[string]string{"pkg.mk": grantRoot("app", `"`+toml+`"`, srcDep("toml", toml, "1.0.0"))})
	_, r = offlineCheck(t, app)
	clean(t, r)
}

// mk install, mk update and mk remove change requirements, so they walk
// with vendor/ ignored (the index is the old build list) and say that
// vendor/ is now stale; a build then reads the index and asks for mk vendor.
func TestPkgCmd_VendoredPackage(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	f := tomlFiles("10")
	f["pkg.mk"] = "const Package = pkg.Package(\n    name: \"toml\",\n)\n"
	app, toml, _ := vendorSourceApp(t, gs, f)
	gs.tag("someone/toml", "1.1.0", f)
	gs.tag("someone/other", "1.0.0", map[string]string{"pkg.mk": depManifest("other"), "src/o.mk": "export O(): int {\n    return 3\n}\n"})
	other := gs.source("someone/other")
	cfg := sourceCfg(gs, t.TempDir())
	stale := "note: vendor/ was not changed; run mk vendor"
	res, err := cmdSession(t, cfg, app).Install(other+"@1.0.0", PkgOptions{})
	if err != nil {
		t.Fatal(err)
	}
	clean(t, res.Check)
	if !slices.Contains(res.Lines, stale) {
		t.Errorf("install: %q", res.Lines)
	}
	res, err = cmdSession(t, cfg, app).Update("toml", PkgOptions{})
	if err != nil {
		t.Fatal(err)
	}
	clean(t, res.Check)
	if !slices.Contains(res.Lines, stale) || !strings.Contains(strings.Join(res.Lines, "\n"), "v1.1.0") {
		t.Errorf("update: %q", res.Lines)
	}
	res, err = cmdSession(t, cfg, app).Remove("other")
	if err != nil {
		t.Fatal(err)
	}
	clean(t, res.Check)
	if sum, _ := os.ReadFile(filepath.Join(app, sumFile)); strings.Contains(string(sum), other) || !slices.Contains(res.Lines, stale) {
		t.Errorf("remove: %q; pkg.sum:\n%s", res.Lines, sum)
	}
	// The build reads the index, which now holds an old toml.
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir(), Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if r := s.Check(); len(r.Report.Diags) != 1 || !strings.Contains(r.Report.Diags[0].Diag.Message, "run mk vendor again") {
		t.Errorf("build after update: %q", lines(r))
	}
	_ = toml
}
