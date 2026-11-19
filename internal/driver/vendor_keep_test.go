// Why: mk vendor rewrites vendor/, and must never delete what it did not
// write.
// What: TestVendor_KeepsWhatItDidNotWrite, TestVendor_KeepsADependencySource,
// TestVendor_RemovesWhatTheOldIndexNamed and TestVendor_OldLayoutNoteNoDelete.
// How: mk vendor removes only what the previous vendor/index.txt named, never
// a directory it did not write and never a package's own source; a directory
// in the old layout gets a note and stays.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func vendorRun(t *testing.T, app string) VendorResult {
	t.Helper()
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Vendor()
	if err != nil {
		t.Fatal(err)
	}
	clean(t, res.Result)
	return res
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// A hand-made vendor/<dir> holding a pkg.mk survives mk vendor.
func TestVendor_KeepsWhatItDidNotWrite(t *testing.T) {
	ws := workspace(t, []string{pathDep("util", "../util")},
		map[string]string{"app/vendor/handmade/pkg.mk": depManifest("handmade")})
	app := filepath.Join(ws, "app")
	vendorRun(t, app)
	vendorRun(t, app)
	if !exists(filepath.Join(app, "vendor", "handmade", "pkg.mk")) {
		t.Error("mk vendor deleted vendor/handmade, which it did not write")
	}
}

// A path dependency inside vendor/ is a package's source: mk vendor never
// removes it, and runs twice.
func TestVendor_KeepsADependencySource(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"app/pkg.mk":          depManifest("app", pathDep("x", "vendor/x")),
		"app/a.mk":            "import x\n\nexport A(): int {\n    return x.One()\n}\n",
		"app/vendor/x/pkg.mk": depManifest("x"),
		"app/vendor/x/x.mk":   "export One(): int {\n    return 1\n}\n",
	})
	app := filepath.Join(ws, "app")
	vendorRun(t, app)
	vendorRun(t, app)
	if !exists(filepath.Join(app, "vendor", "x", "x.mk")) {
		t.Fatal("mk vendor deleted vendor/x, a dependency's source")
	}
	clean(t, depCheck(t, app, Config{}))
}

// A package that leaves the build loses its vendor/<id>, which the
// previous index named.
func TestVendor_RemovesWhatTheOldIndexNamed(t *testing.T) {
	ws := workspace(t, []string{pathDep("util", "../util")}, nil)
	app := filepath.Join(ws, "app")
	vendorRun(t, app)
	if !exists(filepath.Join(app, "vendor", utilID)) {
		t.Fatal("not vendored")
	}
	writeFiles(t, ws, map[string]string{"app/pkg.mk": depManifest("app"), "app/src/a.mk": "export A(): int {\n    return 1\n}\n"})
	vendorRun(t, app)
	if exists(filepath.Join(app, "vendor", utilID)) {
		t.Error("vendor/" + utilID + " stayed after util left the build")
	}
}

// An old-layout vendor/<name> is never deleted; mk vendor notes it.
func TestVendor_OldLayoutNoteNoDelete(t *testing.T) {
	ws := workspace(t, []string{pathDep("util", "../util")}, nil)
	app := filepath.Join(ws, "app")
	if err := copyPackage(filepath.Join(ws, "util"), filepath.Join(app, "vendor", "util")); err != nil {
		t.Fatal(err)
	}
	res := vendorRun(t, app)
	if !exists(filepath.Join(app, "vendor", "util", "pkg.mk")) {
		t.Error("mk vendor deleted the 1.2-layout vendor/util")
	}
	if len(res.Result.Notes) != 1 || !strings.Contains(res.Result.Notes[0], "vendor/util is in the older layout") {
		t.Errorf("notes %q", res.Result.Notes)
	}
}
