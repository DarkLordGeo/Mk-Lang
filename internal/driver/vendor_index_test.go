// Why: vendor/index.txt is a file anyone can edit, so the package ids in it
// must not decide which package a build gets.
// What: TestVendor_IndexIDNotTrusted and TestVendor_CanonicalIDForm.
// How: an id of another form (std, a reserved segment, a path) refuses the
// index; a well-formed id that is not the one the identity and manifest give
// is E0202 at the Dep.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vendoredUtil is a workspace whose app has vendored util, with util's
// sources removed, and the path of its index.
func vendoredUtil(t *testing.T) (app, index string) {
	t.Helper()
	ws := workspace(t, []string{pathDep("util", "../util")}, nil)
	app = filepath.Join(ws, "app")
	vendorRun(t, app)
	if err := os.RemoveAll(filepath.Join(ws, "util")); err != nil {
		t.Fatal(err)
	}
	return app, filepath.Join(app, "vendor", vendorIndexFile)
}

// rewriteIndex renames util's vendored directory to id and points the
// index at it.
func rewriteIndex(t *testing.T, app, index, id string) {
	t.Helper()
	b, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(app, "vendor", utilID), filepath.Join(app, "vendor", id)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, []byte(strings.Replace(string(b), utilID, id, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVendor_IndexIDNotTrusted(t *testing.T) {
	for _, tc := range []struct{ name, id, reason string }{
		// std would give the dependency std's module paths.
		{"std", "std", "invalid"},
		{"main", "main", "invalid"},
		{"no hash", "util", "invalid"},
		// A stale or foreign id of the right form.
		{"stale", "util_00000000", "invalid"},
		{"other name", "units_" + utilID[len("util_"):], "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, index := vendoredUtil(t)
			rewriteIndex(t, app, index, tc.id)
			r := depCheck(t, app, Config{})
			if len(r.Report.Diags) != 1 || fact(r.Report.Diags[0].Diag, "reason") != tc.reason ||
				!strings.Contains(r.Report.Diags[0].Diag.Message, "mk vendor") {
				t.Errorf("got %q: %s", lines(r), r.Report.Text(r.Sources))
			}
		})
	}
	// The index mk vendor wrote is accepted.
	app, _ := vendoredUtil(t)
	clean(t, depCheck(t, app, Config{}))
}

func TestVendor_CanonicalIDForm(t *testing.T) {
	for id, ok := range map[string]bool{utilID: true, "std": false, "std_0123abcd": true, "main": false,
		"util_0123ABCD": false, "util_0123abc": false, "../x_0123abcd": false, "_0123abcd": false} {
		if canonicalIDForm(id) != ok {
			t.Errorf("canonicalIDForm(%q) = %v", id, !ok)
		}
	}
}
