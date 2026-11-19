// Why: one package may be named differently by every manifest that uses
// it, so the compiler needs one identity for it and must still speak the
// reader's names.
// What: tests of canonical ids, local names as aliases, id-clash, the
// display of a dependency by its local name, and vendor/'s index layout
// and old layout.
// How: writes packages in temp directories, checks them and compares
// module ids, diagnostics and vendor/index.txt.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDep_CanonicalIDForm(t *testing.T) {
	id := canonicalID("util", "../util")
	if !strings.HasPrefix(id, "util_") || len(id) != len("util_")+8 || !validDepName(id) {
		t.Errorf("id %q", id)
	}
	for in, want := range map[string]string{"Toml": "toml", "my-lib": "my_lib", "9lives": "_9lives", "a.b": "a_b"} {
		if got := identifierOf(in); got != want {
			t.Errorf("identifierOf(%q) = %q, want %q", in, got, want)
		}
	}
	if canonicalID("util", "../util") == canonicalID("util", "../util2") {
		t.Error("two directories, one id")
	}
}

// One package named by two local names in two manifests is one package,
// whose modules carry its canonical id; the id is the same in another
// checkout (it is of the relative directory).
func TestDep_LocalNamesAreAliases(t *testing.T) {
	files := map[string]string{
		"app/pkg.mk":    depManifest("app", pathDep("u", "../util"), pathDep("other", "../other")),
		"app/a.mk":      "import u.src\nimport other\n\nexport A(): int {\n    return src.Two() + other.Four()\n}\n",
		"other/pkg.mk":  depManifest("other", pathDep("helper", "../util")),
		"other/o.mk":    "import helper.src\n\nexport Four(): int {\n    return src.Two() * 2\n}\n",
		"util/pkg.mk":   depManifest("util"),
		"util/src/u.mk": "export Two(): int {\n    return 2\n}\n",
	}
	var seen [][]string
	for range 2 {
		ws := writeTree(t, files)
		r := depCheck(t, filepath.Join(ws, "app"), Config{})
		clean(t, r)
		var mods []string
		for _, m := range r.Trace.ModulesResolved {
			if !strings.HasPrefix(m, "std.") {
				mods = append(mods, m)
			}
		}
		seen = append(seen, mods)
	}
	want := []string{canonicalID("other", "../other"), "main", utilID + ".src"}
	for _, mods := range seen {
		for _, m := range want {
			if m != "main" && !slices.Contains(mods, m) {
				t.Errorf("module %s not resolved: %v", m, mods)
			}
		}
	}
	if !slices.Equal(seen[0], seen[1]) {
		t.Errorf("two checkouts, two module sets: %v and %v", seen[0], seen[1])
	}
}

// A diagnostic names a dependency's module as the importer's manifest
// does, never by its canonical id.
func TestDep_DiagnosticUsesLocalName(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"app/pkg.mk":    depManifest("app", pathDep("u", "../util")),
		"app/a.mk":      "import u.src\n\nexport A(): int {\n    return src.Hidden()\n}\n",
		"util/pkg.mk":   depManifest("util"),
		"util/src/u.mk": "Hidden(): int {\n    return 2\n}\n",
	})
	r := depCheck(t, filepath.Join(ws, "app"), Config{})
	if len(r.Report.Diags) != 1 {
		t.Fatalf("got %q", lines(r))
	}
	text := r.Report.Text(r.Sources)
	if strings.Contains(text, utilID) || !strings.Contains(text, "u.src") {
		t.Errorf("the diagnostic names the canonical id or not the local name:\n%s", text)
	}
	// An unknown module of a dependency: the alternative is spelled with
	// the importer's local name.
	writeFiles(t, ws, map[string]string{"app/a.mk": "import u.SRC\n"})
	r = depCheck(t, filepath.Join(ws, "app"), Config{})
	if len(r.Report.Diags) != 1 || len(r.Report.Diags[0].Diag.Alternatives) != 1 || r.Report.Diags[0].Diag.Alternatives[0] != "import u.src" {
		t.Errorf("got %q alternatives %q", lines(r), r.Report.Diags[0].Diag.Alternatives)
	}
}

// A canonical id equal to the first segment of one of the root's own
// modules is E0202 id-clash at the Dep.
func TestDep_IDClashWithRootModule(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"app/pkg.mk":              depManifest("app", pathDep("util", "../util")),
		"app/" + utilID + "/x.mk": "export X(): int {\n    return 1\n}\n",
		"util/pkg.mk":             depManifest("util"),
		"util/u.mk":               "export One(): int {\n    return 1\n}\n",
	})
	e0202(t, depCheck(t, filepath.Join(ws, "app"), Config{}), "id-clash", "pkg.mk:")
}

// A vendor/ in the old layout (vendor/<name>, no index.txt) is read, with
// a note naming mk vendor.
func TestDep_VendorOldLayoutNote(t *testing.T) {
	ws := workspace(t, []string{pathDep("util", "../util")}, nil)
	app := filepath.Join(ws, "app")
	if err := copyPackage(filepath.Join(ws, "util"), filepath.Join(app, "vendor", "util")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(ws, "util")); err != nil {
		t.Fatal(err)
	}
	r := depCheck(t, app, Config{})
	clean(t, r)
	if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "vendor/util is in the older layout") || !strings.Contains(r.Notes[0], "mk vendor") {
		t.Errorf("notes %q", r.Notes)
	}
	if !slices.Contains(r.Trace.ModulesResolved, utilID+".src") {
		t.Errorf("modules %v", r.Trace.ModulesResolved)
	}
}

func TestVendor_IndexRoundTrip(t *testing.T) {
	vi := &vendorIndex{entries: []vendorEntry{
		{key: "https://example.com/ini.tar.gz", version: "-", cid: "ini_00000001"},
		{key: "../util", version: "-", cid: utilID},
	}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, vendorIndexFile), []byte(vi.text()), 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := readVendorIndex(dir)
	if err != nil || back.text() != vi.text() || !strings.HasPrefix(vi.text(), "../util - "+utilID+" tree-sha256:") {
		t.Errorf("%v\n%s", err, vi.text())
	}
	if err := os.WriteFile(filepath.Join(dir, vendorIndexFile), []byte("a b c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readVendorIndex(dir); err == nil {
		t.Error("a malformed index was read")
	}
}
