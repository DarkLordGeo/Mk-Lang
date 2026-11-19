// Why: the Tier A rows (tests/bench/README.md, "Tier A") run Mk dogfood
// packages alone, and need their own placeholder and contract.
// What: TestTierAPkgArg and TestTierARowsAreDogfoods.
// How: the {pkg} placeholder that names the dogfood package's directory in
// an app row's arguments, and the contract the tree's Mk-only rows keep.

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestTierAPkgArg: {pkg} becomes the package's absolute directory in args,
// quick and then, and the digest writes it back as $PKG in either slash
// form; a row that names no package is refused when it loads.
func TestTierAPkgArg(t *testing.T) {
	root := appRepo(t, map[string]string{
		"Dogfoods/one/pkg.mk":           appPkg,
		"Dogfoods/one/src/main/main.mk": "Main() {}\n",
		"Dogfoods/one/cases/data/in.k":  "key\n",
		"tests/bench/app/r/mk/pkg.mk":   appPkg,
	})
	dir := filepath.Join(root, "tests", "bench", "app", "r")
	p := filepath.Join(dir, "bench.json")
	writeFile(t, p, `{"name": "r", "table": "app", "package": {"path": "Dogfoods/one"}, "kinds": {"mk": "std"}, "digest": true,
		"args": ["run", "{pkg}/cases/data/in.k"], "quick": ["run", "--key={pkg}/cases/data/in.k"],
		"output": {"file": "o.pk", "check": "then", "then": ["sum", "{pkg}/cases/data/in.k", "{out}"]},
		"checksum": "x", "quick_checksum": "y"}`)
	r, err := loadRow(p, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareRows(root, []*row{r}); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "Dogfoods", "one")
	if r.pkgPath != pkg {
		t.Fatalf("pkgPath %q, want %q", r.pkgPath, pkg)
	}
	want := pkg + "/cases/data/in.k"
	if !slices.Equal(r.runArgs, []string{"run", want}) || !slices.Equal(r.runQuick, []string{"run", "--key=" + want}) || !slices.Equal(r.runThen, []string{"sum", want, "{out}"}) {
		t.Fatalf("run arguments: %q %q %q", r.runArgs, r.runQuick, r.runThen)
	}
	text := func(pkg string) []byte {
		return []byte("read " + pkg + `\cases\data\in.k` + "\r\nand " + strings.ReplaceAll(pkg, `\`, "/") + "/cases\n")
	}
	out := text(pkg)
	if got := string(normalise(out, "", "", r.pkgPath)); got != "read $PKG\\cases\\data\\in.k\nand $PKG/cases\n" {
		t.Errorf("normalise: %q", got)
	}
	elsewhere := `X:\elsewhere\one`
	if a, b := stdoutSum(r, out, ""), stdoutSum(r, text(elsewhere), ""); a == b {
		t.Errorf("the digest of two different texts is one: %s", a)
	}
	// The same digest wherever the repository is: only the package
	// directory differs between the two outputs.
	moved := *r
	moved.pkgPath = elsewhere
	if a, b := stdoutSum(r, out, ""), stdoutSum(&moved, text(elsewhere), ""); a != b {
		t.Errorf("the digest moves with the repository: %s and %s", a, b)
	}
	for _, body := range []string{
		`{"name": "r", "table": "app", "sources": {"mk": "mk"}, "kinds": {"mk": "std"}, "args": ["{pkg}/x"], "quick": []}`,
		`{"name": "r", "table": "app", "sources": {"mk": "mk"}, "kinds": {"mk": "std"}, "args": [], "quick": [], "variants": [{"name": "v", "args": ["a"], "quick": ["--f={pkg}"]}]}`,
		`{"name": "r", "table": "app", "sources": {"mk": "mk"}, "kinds": {"mk": "std"}, "args": ["{out}"], "quick": [], "output": {"file": "o", "check": "then", "then": ["{pkg}", "{out}"]}}`,
	} {
		writeFile(t, p, body)
		if _, err := loadRow(p, ""); err == nil || !strings.Contains(err.Error(), "names {pkg}") {
			t.Errorf("%s: %v", body, err)
		}
	}
	// prepareRows refuses it on any table's row with no package.
	same := &row{Name: "s", Table: "same", Dir: dir, Args: []string{"{pkg}"}}
	if err := prepareRows(root, []*row{same}); err == nil || !strings.Contains(err.Error(), "names no package") {
		t.Errorf("a same row's {pkg}: %v", err)
	}
}

// tierARows are the tree's Tier A rows: every app row whose
// only source is Mk's.
func tierARows(t *testing.T) []*row {
	t.Helper()
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := discoverTree(t, root)
	if err != nil {
		t.Fatal(err)
	}
	var out []*row
	for _, r := range rows {
		if r.Table == "app" && r.Lane == "" && len(r.Sources) == 1 && r.Sources["mk"] != "" {
			out = append(out, r)
		}
	}
	return out
}

// TestTierARowsAreDogfoods: each Tier A row of the tree takes a dogfood
// unchanged (package), is its kind std cell alone, digests its output, runs
// on one carrier, and has a README that says it is an Mk-only row; the
// eleven dogfoods the Tier A rows cover are each a Tier A row or, for
// orderbook and mgrep, the Mk column of their twinned row.
func TestTierARowsAreDogfoods(t *testing.T) {
	rows := tierARows(t)
	got := map[string]bool{}
	for _, r := range rows {
		if r.Package == nil || !strings.HasPrefix(r.Package.Path, "Dogfoods/") {
			t.Errorf("%s: a Tier A row names a dogfood as its package", r.id())
			continue
		}
		got[strings.TrimPrefix(r.Package.Path, "Dogfoods/")] = true
		if !r.Digest || r.Env["MK_CARRIERS"] != "1" {
			t.Errorf("%s: digest %v, MK_CARRIERS %q; want a digest and one carrier", r.id(), r.Digest, r.Env["MK_CARRIERS"])
		}
		b, err := os.ReadFile(filepath.Join(r.Dir, "README.md"))
		if err != nil || !strings.Contains(string(b), "An Mk-only row") {
			t.Errorf("%s: no README.md that says it is an Mk-only row (%v)", r.id(), err)
		}
	}
	for _, d := range []string{"pack", "kvd", "textkit", "lox", "tracer", "imgproc", "bigcalc", "raft", "cron"} {
		if !got[d] {
			t.Errorf("no Tier A row takes Dogfoods/%s", d)
		}
	}
	// orderbook and mgrep are the twinned rows' Mk columns, compared with
	// -cols mk: one row each, under their twinned rows' names.
	wd, _ := os.Getwd()
	root, _ := repoRoot(wd)
	all, err := discoverTree(t, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, pkg string }{{"orderbook", "Dogfoods/orderbook"}, {"grep-re", "Dogfoods/mgrep"}} {
		found := false
		for _, r := range all {
			if r.id() == c.id && r.Package != nil && r.Package.Path == c.pkg && len(r.Sources) > 1 {
				found = true
			}
		}
		if !found {
			t.Errorf("no twinned row %s whose Mk column is %s", c.id, c.pkg)
		}
		if got[strings.TrimPrefix(c.pkg, "Dogfoods/")] {
			t.Errorf("%s is a Tier A row too: one row per dogfood", c.pkg)
		}
	}
}
