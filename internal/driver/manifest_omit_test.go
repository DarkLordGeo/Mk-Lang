// Why: a pkg.mk may leave out any std.pkg field that holds its empty value,
// and may name the oldest mk it needs; both change how every manifest loads.
// What: tests of field omission (decoding, hashing, spans, mk fix --canonical,
// mk apidiff) and of the mk minimum (E0216), over fixed texts and every
// pkg.mk in the repository.
// How: loadManifestText runs the manifest step on a text with the embedded
// std and a fixed running version; the tests compare codes and fields.

package driver

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/source"
)

// loadManifestText runs the manifest step on text as a package's pkg.mk,
// with the embedded std, and returns the manifest, the diagnostics and the
// SourceMap they print with.
func loadManifestText(t testing.TB, text string) (*Manifest, []*diag.Diagnostic, *source.SourceMap) {
	t.Helper()
	return loadManifestRunning(t, text, "0.1.0")
}

// loadManifestRunning is loadManifestText with the running mk version
// injected, so no test depends on the release version.
func loadManifestRunning(t testing.TB, text, running string) (*Manifest, []*diag.Diagnostic, *source.SourceMap) {
	t.Helper()
	cfg, err := Config{}.normalize()
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(cfg)
	ms := w.newManifestStep(w.stdTree())
	ms.running = running
	man, ds := ms.load(&srcTree{mem: map[string]string{"pkg.mk": text, "src/main.mk": "Main() {\n}\n"}}, "pkg.mk", "main")
	return man, ds, w.sm
}

func diagCodes(ds []*diag.Diagnostic) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Code.String())
	}
	return out
}

func TestManifest_OmitOnlyNameAndBins(t *testing.T) {
	text := "const Package = pkg.Package(\n    name: \"app\",\n    bins: [pkg.Bin(name: \"app\", root: \"src\")],\n)\n"
	man, ds, sm := loadManifestText(t, text)
	if len(ds) > 0 {
		t.Fatalf("want a clean manifest, got %v: %s", diagCodes(ds), ds[0].Message)
	}
	if man.Name != "app" || len(man.Bins) != 1 || man.Bins[0].Root != "src" || man.Bins[0].Subsystem != "" {
		t.Errorf("decoded %+v", man)
	}
	// The SourceMap holds the file once, as written.
	if sm.Len() < 1 {
		t.Fatal("no file")
	}
	for _, f := range sm.Files() {
		if f.Path == "pkg.mk" && f.Text != text {
			t.Errorf("the manifest's text changed:\n%s", f.Text)
		}
	}
}

func TestManifest_OmittedHashesAsEmpty(t *testing.T) {
	short := "const Package = pkg.Package(name: \"app\", bins: [pkg.Bin(name: \"app\", root: \"src\")])\n"
	long := manifest(map[string]string{"name": `"app"`,
		"bins": `[pkg.Bin(name: "app", root: "src", link_script: "", cpu: "", subsystem: "")]`})
	a, ds, _ := loadManifestText(t, short)
	if len(ds) > 0 {
		t.Fatal(ds[0].Message)
	}
	b, ds, _ := loadManifestText(t, long)
	if len(ds) > 0 {
		t.Fatal(ds[0].Message)
	}
	if len(a.Fields) != len(b.Fields) {
		t.Fatalf("fields %d and %d", len(a.Fields), len(b.Fields))
	}
	for k, h := range a.Fields {
		if b.Fields[k] != h {
			t.Errorf("field %s hashes differently when omitted", k)
		}
	}
}

func TestManifest_OmitStillE0305(t *testing.T) {
	for _, tc := range []struct{ name, text, at string }{
		// A positional call builds no struct.
		{"positional", "const Package = pkg.Package(name: \"app\", bins: [pkg.Bin(\"app\", \"src\")])\n", "pkg.mk:1:"},
		// A misspelled field is unknown, and the field it meant is not missing.
		{"misspelled", "const Package = pkg.Package(name: \"app\", bins: [], dep: [])\n", "pkg.mk:1:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ds, sm := loadManifestText(t, tc.text)
			if len(ds) == 0 {
				t.Fatal("want E0305, got a clean manifest")
			}
			found := false
			for _, d := range ds {
				if d.Code == diag.E0305 && strings.HasPrefix(diag.Location(sm, d.At), tc.at) {
					found = true
				}
			}
			if !found {
				t.Errorf("want E0305 at %s, got %v: %s", tc.at, diagCodes(ds), ds[0].Message)
			}
		})
	}
}

// A diagnostic after an omission keeps its span: nothing was inserted in
// the text.
func TestManifest_OmitKeepsSpans(t *testing.T) {
	text := "const Package = pkg.Package(\n    name: \"app\",\n    bins: [pkg.Bin(name: \"app\", root: \"src\"), pkg.Bin(name: \"b\", root: 3)],\n)\n"
	_, ds, sm := loadManifestText(t, text)
	if len(ds) != 1 {
		t.Fatalf("want one diagnostic, got %v", diagCodes(ds))
	}
	line := strings.Split(text, "\n")[2]
	want := fmt.Sprintf("pkg.mk:3:%d", strings.Index(line, "3)")+1)
	if got := diag.Location(sm, ds[0].At); got != want {
		t.Errorf("at %s, want %s (the 3)", got, want)
	}
}

// Every pkg.mk of the repository (std, tests, Dogfoods, kits and the rest)
// loads as it did: a manifest the repository expects to load cleanly
// still does, and none fails with E0305 from omission.
func TestManifest_RepositoryManifestsLoad(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (strings.HasPrefix(d.Name(), ".") && p != root || d.Name() == "node_modules") {
			return fs.SkipDir
		}
		if !d.IsDir() && d.Name() == "pkg.mk" {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 300 {
		t.Fatalf("found %d pkg.mk files, want the repository's (over 300)", len(files))
	}
	cfg, err := Config{}.normalize()
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(cfg)
	ms := w.newManifestStep(w.stdTree())
	bad := 0
	for _, p := range files {
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "std/") {
			continue // std's own manifest and std.pkg: rootName "" below
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Dir(p)
		_, ds := ms.load(w.diskTree(dir, filepath.ToSlash(filepath.Dir(rel))+"/"), "pkg.mk", "main")
		for _, d := range ds {
			// A manifest the repository means to fail says so with a
			// marker or a verdict. E0202 here is the host's (a linker
			// script off freestanding, a C source kept outside the
			// repository), which omission cannot cause.
			if d.Code != diag.E0202 && !strings.Contains(string(raw), "<- E") && !strings.Contains(string(raw), "verdict") {
				t.Errorf("%s: %s %s", rel, d.Code, d.Message)
				bad++
			}
		}
	}
	_, ds := ms.load(w.diskTree(filepath.Join(root, "std"), "std/"), "pkg.mk", "")
	for _, d := range ds {
		t.Errorf("std/pkg.mk: %s %s", d.Code, d.Message)
	}
	t.Logf("%d manifests loaded, %d failed", len(files), bad)
}

// pkgText is a manifest of package app with one bin and the given extra
// arguments of its pkg.Package call.
func pkgText(extra string) string {
	return "const Package = pkg.Package(\n    name: \"app\",\n    bins: [pkg.Bin(name: \"app\", root: \"src\")],\n" + extra + ")\n"
}

func TestManifest_NewFieldsDecode(t *testing.T) {
	text := pkgText(`    mk: "0.1",
    deps: [pkg.Dep(name: "toml", source: "github.com/someone/toml", version: "0.9.3")],
    test_deps: [pkg.Dep(name: "golden", source: "github.com/someone/golden", version: "2.0.0-rc.1")],
    unsafe_deps: ["github.com/someone/toml", "golden"],
    replace: [pkg.Dep(name: "github.com/someone/toml", path: "../toml")],
`)
	man, ds, _ := loadManifestText(t, text)
	if len(ds) > 0 {
		t.Fatalf("%v: %s", diagCodes(ds), ds[0].Message)
	}
	if man.Mk != "0.1" || len(man.Deps) != 1 || man.Deps[0].Source != "github.com/someone/toml" || man.Deps[0].Version != "0.9.3" ||
		len(man.TestDeps) != 1 || man.TestDeps[0].Field != "test_deps[0]" || len(man.UnsafeDeps) != 2 ||
		len(man.Replace) != 1 || man.Replace[0].Path != "../toml" {
		t.Errorf("decoded %+v", man)
	}
}

func TestManifest_NewFieldsValidate(t *testing.T) {
	for _, tc := range []struct{ name, extra, field string }{
		{"three sources", `    deps: [pkg.Dep(name: "a", path: "../a", source: "github.com/x/a", version: "1.0.0")],` + "\n", "deps[0]"},
		{"no source", `    deps: [pkg.Dep(name: "a")],` + "\n", "deps[0]"},
		{"source without version", `    deps: [pkg.Dep(name: "a", source: "github.com/x/a")],` + "\n", "deps[0]"},
		{"version without source", `    deps: [pkg.Dep(name: "a", path: "../a", version: "1.0.0")],` + "\n", "deps[0].version"},
		{"hash with source", `    deps: [pkg.Dep(name: "a", source: "github.com/x/a", version: "1.0.0", hash: "sha256:00")],` + "\n", "deps[0].hash"},
		{"scheme", `    deps: [pkg.Dep(name: "a", source: "https://github.com/x/a", version: "1.0.0")],` + "\n", "deps[0].source"},
		{"dot git", `    deps: [pkg.Dep(name: "a", source: "github.com/x/a.git", version: "1.0.0")],` + "\n", "deps[0].source"},
		{"query", `    deps: [pkg.Dep(name: "a", source: "github.com/x/a?x=1", version: "1.0.0")],` + "\n", "deps[0].source"},
		{"host without dot", `    deps: [pkg.Dep(name: "a", source: "localhost/x/a", version: "1.0.0")],` + "\n", "deps[0].source"},
		{"no path", `    deps: [pkg.Dep(name: "a", source: "github.com", version: "1.0.0")],` + "\n", "deps[0].source"},
		{"escaping path", `    deps: [pkg.Dep(name: "a", source: "github.com/../a", version: "1.0.0")],` + "\n", "deps[0].source"},
		{"short version", `    deps: [pkg.Dep(name: "a", source: "github.com/x/a", version: "1.0")],` + "\n", "deps[0].version"},
		{"leading zero", `    deps: [pkg.Dep(name: "a", source: "github.com/x/a", version: "1.01.0")],` + "\n", "deps[0].version"},
		{"bad pre-release", `    deps: [pkg.Dep(name: "a", source: "github.com/x/a", version: "1.0.0-rc..1")],` + "\n", "deps[0].version"},
		{"name twice", `    deps: [pkg.Dep(name: "a", source: "github.com/x/a", version: "1.0.0")],` + "\n" +
			`    test_deps: [pkg.Dep(name: "a", source: "github.com/x/b", version: "1.0.0")],` + "\n", "test_deps[0].name"},
		{"name twice in deps", `    deps: [pkg.Dep(name: "a", path: "../a"), pkg.Dep(name: "a", path: "../b")],` + "\n", "deps[1].name"},
		{"mk", `    mk: "1",` + "\n", "mk"},
		{"unsafe_deps", `    unsafe_deps: ["not a name"],` + "\n", "unsafe_deps[0]"},
		{"member outside", `    members: ["../x"],` + "\n", "members[0]"},
		{"member absent", `    members: ["lib"],` + "\n", "members[0]"},
		{"replace name", `    replace: [pkg.Dep(name: "toml", path: "../toml")],` + "\n", "replace[0].name"},
		{"replace url", `    replace: [pkg.Dep(name: "github.com/x/a", url: "https://example.com/a.tar.gz", hash: "")],` + "\n", "replace[0].url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ds, _ := loadManifestText(t, pkgText(tc.extra))
			if len(ds) == 0 {
				t.Fatal("want E0202, got a clean manifest")
			}
			d := ds[0]
			if d.Code != diag.E0202 || fact(d, "manifest") != tc.field || fact(d, "reason") != "invalid" {
				t.Errorf("got %v manifest %q reason %q (%s), want E0202 at %s", diagCodes(ds), fact(d, "manifest"), fact(d, "reason"), d.Message, tc.field)
			}
		})
	}
}

func TestManifest_SemverOrder(t *testing.T) {
	// the precedence example of Semantic Versioning 2.0.0, in ascending order.
	order := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11",
		"1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0", "10.0.0"}
	for i := range order {
		a, ok := parseSemver(order[i])
		if !ok || a.String() != order[i] {
			t.Fatalf("parse %q: %v %q", order[i], ok, a.String())
		}
		for j := range order {
			b, _ := parseSemver(order[j])
			c := compareSemver(a, b)
			if want := cmpInt(i, j); c != want && !(c < 0 && want < 0) && !(c > 0 && want > 0) {
				t.Errorf("compare %s %s = %d, want sign %d", order[i], order[j], c, want)
			}
		}
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// E0216 comes before anything a newer std.pkg's fields would cause: a
// manifest for a newer mk, with a field this std.pkg lacks, gives E0216
// alone.
func TestManifest_MinimumTooNew(t *testing.T) {
	_, ds, sm := loadManifestText(t, pkgText(`    mk: "99.0",`+"\n"+`    future_field: ["x"],`+"\n"))
	if len(ds) != 1 || ds[0].Code != diag.E0216 {
		t.Fatalf("want E0216 alone, got %v", diagCodes(ds))
	}
	if got := diag.Location(sm, ds[0].At); got != "pkg.mk:4:9" || fact(ds[0], "needed") != "99.0" {
		t.Errorf("at %s facts %v", got, ds[0].Facts)
	}
	if len(ds[0].Alternatives) != 1 || !strings.Contains(ds[0].Alternatives[0], "mk upgrade") {
		t.Errorf("alternatives %q", ds[0].Alternatives)
	}
	// With the running mk injected: 0.2 is too new for 0.1.0 and right
	// for 0.2.0; the running version and older ones pass.
	for _, tc := range []struct {
		running, mk string
		tooNew      bool
	}{
		{"0.1.0", "0.2", true}, {"0.1.0", "0.1.1", true}, {"0.1.0", "0.1", false}, {"0.1.0", "0.1.0", false},
		{"0.2.0", "0.2", false}, {"0.2.0", "0.2.0", false}, {"0.2.0", "0.1", false}, {"0.2.0", "0.0.1", false}, {"0.2.0", "0.3", true},
	} {
		_, ds, _ := loadManifestRunning(t, pkgText(`    mk: "`+tc.mk+`",`+"\n"), tc.running)
		if got := len(ds) == 1 && ds[0].Code == diag.E0216; got != tc.tooNew || !tc.tooNew && len(ds) > 0 {
			t.Errorf("running %s, mk %s: %v, want too new %v", tc.running, tc.mk, diagCodes(ds), tc.tooNew)
		}
	}
}

// mk apidiff: a field added to a std.pkg struct is compatible; a
// field removed still breaks.
func TestManifest_APIDiffStdPkgFields(t *testing.T) {
	cur := stdPkgText()
	old := strings.Replace(cur, "    export abi: str\n", "", 1)
	if old == cur {
		t.Fatal("std/pkg/pkg.mk has no Bin.abi field to remove")
	}
	v1 := apiStd(t, "word", apiFsV1, apiTlsV1)
	v2 := apiStd(t, "word", apiFsV1, apiTlsV1)
	writeFiles(t, v1, map[string]string{"pkg/pkg.mk": old})
	if got := stdChanges(t, v1, v2); got["std.pkg.Bin fields added"] != Compatible {
		t.Errorf("abi added: %q, want compatible (%v)", got["std.pkg.Bin fields added"], got)
	}
	if got := stdChanges(t, v2, v1); got["std.pkg.Bin fields"] != Breaking {
		t.Errorf("abi removed: %q, want breaking (%v)", got["std.pkg.Bin fields"], got)
	}
}

// mk fix --canonical removes a std.pkg field holding its empty value:
// the result is the omitted manifest, a fixed point of mk fix.
func TestManifest_CanonicalRemovesEmptyFields(t *testing.T) {
	full := manifest(map[string]string{"name": `"app"`,
		"bins": `[pkg.Bin(name: "app", root: "src", link_script: "", cpu: "", subsystem: "console")]`})
	want := "const Package = pkg.Package(\n    name: \"app\",\n    bins: [pkg.Bin(name: \"app\", root: \"src\", subsystem: \"console\")],\n)\n"
	line := `const Package = pkg.Package(name: "app", bins: [], deps: [], unsafe_modules: ["src"])` + "\n"
	lineWant := `const Package = pkg.Package(name: "app", unsafe_modules: ["src"])` + "\n"
	for _, tc := range []struct{ name, text, want string }{
		{"one per line", full, want},
		{"one line", line, lineWant},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTree(t, map[string]string{"pkg.mk": tc.text, "src/main.mk": "export Main(sys: &System) {\n}\n"})
			s, err := NewSession(Config{Root: dir, CacheDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			res, err := s.Fix(FixOptions{Canonical: true})
			if err != nil || res.Stopped != "clean" {
				t.Fatalf("mk fix: %v %+v", err, res)
			}
			got := readFile(t, filepath.Join(dir, "pkg.mk"))
			if got != tc.want {
				t.Errorf("pkg.mk:\n%s\nwant:\n%s", got, tc.want)
			}
			s, _ = NewSession(Config{Root: dir, CacheDir: t.TempDir()})
			if again, err := s.Fix(FixOptions{Canonical: true}); err != nil || len(again.Rounds) != 0 {
				t.Errorf("second mk fix: %v %+v", err, again.Rounds)
			}
		})
	}
}

// A computed mk, which the pre-check cannot read, is compared after the
// manifest is evaluated.
func TestManifest_MinimumComputed(t *testing.T) {
	text := "const V = \"99.0\"\n\n" + pkgText("    mk: V,\n")
	_, ds, sm := loadManifestText(t, text)
	if len(ds) != 1 || ds[0].Code != diag.E0216 || fact(ds[0], "needed") != "99.0" {
		t.Fatalf("want E0216, got %v", diagCodes(ds))
	}
	if got := diag.Location(sm, ds[0].At); got != "pkg.mk:6:9" {
		t.Errorf("at %s, want the mk argument", got)
	}
}

// A diagnostic about an omitted field lands on the call that left it out,
// not on an empty span.
func TestManifest_OmittedFieldDiagnosticAtCall(t *testing.T) {
	text := "const Package = pkg.Package(\n    name: \"app\",\n    bins: [pkg.Bin(name: \"x\")],\n)\n"
	_, ds, sm := loadManifestText(t, text)
	if len(ds) != 1 || ds[0].Code != diag.E0202 {
		t.Fatalf("want one E0202 for the empty root, got %v", diagCodes(ds))
	}
	if ds[0].At.IsEmpty() || diag.Location(sm, ds[0].At) != "pkg.mk:3:12" {
		t.Errorf("at %s (%v), want the pkg.Bin call at pkg.mk:3:12", diag.Location(sm, ds[0].At), ds[0].At)
	}
}

// members and replace in a dependency's manifest are ignored, with a note.
func TestManifest_IgnoredFieldsNote(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"app/pkg.mk":    depManifest("app", pathDep("util", "../util")),
		"app/a.mk":      "import util\n\nexport A(): int {\n    return util.One()\n}\n",
		"util/pkg.mk":   "const Package = pkg.Package(name: \"util\", members: [\"sub\"])\n",
		"util/u.mk":     "export One(): int {\n    return 1\n}\n",
		"util/sub/s.mk": "export S(): int {\n    return 1\n}\n",
	})
	r := depCheck(t, filepath.Join(ws, "app"), Config{})
	clean(t, r)
	if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "read in the root manifest only") || !strings.Contains(r.Notes[0], "util") {
		t.Errorf("notes %q", r.Notes)
	}
}

// A Dep repeated as written inside deps checks: only a name given two
// different packages is refused.
func TestManifest_RepeatedDepAccepted(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"app/pkg.mk":  depManifest("app", pathDep("util", "../util"), pathDep("util", "../util")),
		"app/a.mk":    "import util\n\nexport A(): int {\n    return util.One()\n}\n",
		"util/pkg.mk": depManifest("util"),
		"util/u.mk":   "export One(): int {\n    return 1\n}\n",
	})
	clean(t, depCheck(t, filepath.Join(ws, "app"), Config{}))
}

// The pre-check reads only a pkg.Package call: another struct's mk field
// gives no E0216.
func TestManifest_MinimumOnlyPkgPackage(t *testing.T) {
	text := "struct S {\n    mk: str\n}\n\nconst Package = S(mk: \"99.0\")\n"
	_, ds, _ := loadManifestText(t, text)
	for _, d := range ds {
		if d.Code == diag.E0216 {
			t.Fatalf("E0216 for a call of S: %v", diagCodes(ds))
		}
	}
	if len(ds) == 0 {
		t.Error("a manifest whose Package is no pkg.Package was accepted")
	}
}
