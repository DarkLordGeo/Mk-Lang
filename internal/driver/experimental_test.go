// Why: a user of an Experimental std module should be told so, once, and
// tools should show the class.
// What: tests of mk check's note per package that imports an Experimental
// module, and of the class mk query shows in modules, sig, import and uses.
// How: checks packages over a stand-in std (experimentalStd) with an
// Experimental std.tls importing an Experimental std.x509.

package driver

import (
	"path/filepath"
	"strings"
	"testing"
)

// experimentalStd is the stand-in std with an Experimental std.tls that
// imports an Experimental std.x509, as 1.2's do.
func experimentalStd(t *testing.T) string {
	t.Helper()
	files := fakeStdFiles()
	files["x509/x509.mk"] = "/// A parsed certificate.\nexport struct Certificate {\n    export n: int\n}\n"
	files["tls/tls.mk"] = "import std.x509\n\nexport Version(c: &x509.Certificate): int {\n    return 3\n}\n"
	return writeTree(t, files)
}

// TestCheckNotesExperimentalImport: one note per package that
// imports an Experimental module, directly or through a dependency; a
// module reached only through another Experimental one is not named again.
func TestCheckNotesExperimentalImport(t *testing.T) {
	std := experimentalStd(t)
	ws := workspace(t, []string{pathDep("util", "../util")}, map[string]string{
		"util/src/u.mk": "import h = src.help\nimport std.tls\nimport std.x509\n\nexport Two(): int {\n    c := x509.Certificate(n: 1)\n    return h.One() + tls.Version(&c) - 2\n}\n",
	})
	r := check(t, Config{Root: filepath.Join(ws, "app"), StdDir: std})
	clean(t, r)
	want := []string{
		"note: std.tls and std.x509 are Experimental, not yet reviewed by an outside cryptographer (package app imports them through dependency util)",
		"note: std.tls and std.x509 are Experimental, not yet reviewed by an outside cryptographer (package util imports them)",
	}
	if strings.Join(r.Notes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("notes:\n%s\nwant:\n%s", strings.Join(r.Notes, "\n"), strings.Join(want, "\n"))
	}
	var text strings.Builder
	if err := r.WriteReport(&text, false); err != nil || text.String() != strings.Join(want, "\n")+"\n" {
		t.Errorf("text report %q (%v)", text.String(), err)
	}
	if js := string(r.ReportJSON()); !strings.Contains(js, `"notes":["note: std.tls and std.x509 are Experimental`) || !strings.HasSuffix(js, "]}\n") && !strings.HasSuffix(js, "]}") {
		t.Errorf("json report lacks the notes as its last field: %s", js)
	}

	// Both imported directly by the root: one note, both named.
	dir := testPkg(t, map[string]string{"main.mk": "import std.tls\nimport std.x509\n\nexport F(): int {\n    c := x509.Certificate(n: 1)\n    return tls.Version(&c)\n}\n"})
	r = check(t, Config{Root: filepath.Join(dir, "main.mk"), StdDir: std})
	clean(t, r)
	if len(r.Notes) != 1 || r.Notes[0] != "note: std.tls and std.x509 are Experimental, not yet reviewed by an outside cryptographer (package main imports them)" {
		t.Errorf("notes %q", r.Notes)
	}

	// No Experimental import: no note, and a clean report prints nothing.
	dir = testPkg(t, map[string]string{"main.mk": "export F(): int {\n    return 1\n}\n"})
	r = check(t, Config{Root: filepath.Join(dir, "main.mk"), StdDir: std})
	clean(t, r)
	if len(r.Notes) != 0 || strings.Contains(string(r.ReportJSON()), "notes") {
		t.Errorf("notes %q on a package with no Experimental import", r.Notes)
	}
}

// TestQueryShowsClass: mk query modules, sig and import show a
// module's class when it is not Stable.
func TestQueryShowsClass(t *testing.T) {
	std := experimentalStd(t)
	dir := testPkg(t, map[string]string{"q.mk": "export F(): int {\n    return 1\n}\n"})
	q := func(query string, args ...string) string {
		t.Helper()
		s, err := NewSession(Config{Root: filepath.Join(dir, "q.mk"), CacheDir: t.TempDir(), StdDir: std})
		if err != nil {
			t.Fatal(err)
		}
		a, res, err := s.Query(query, args)
		if err != nil || a == nil {
			t.Fatalf("query %s %v: %v\n%s", query, args, err, res.Report.Text(res.Sources))
		}
		return a.Text()
	}
	m := q("modules")
	if _, after, _ := strings.Cut(m, "\nstd classes: "); after != "std.tls Experimental, std.x509 Experimental" && !strings.HasPrefix(after, "std.tls Experimental, std.x509 Experimental\n") {
		t.Errorf("modules:\n%s", m)
	}
	// The std: line holds module paths only, which the LSP reads one per
	// field.
	for _, l := range strings.Split(m, "\n") {
		if rest, ok := strings.CutPrefix(l, "std:"); ok {
			for _, f := range strings.Fields(rest) {
				if !strings.HasPrefix(f, "std.") || strings.ContainsAny(f, "()") {
					t.Errorf("std: line field %q is no module path", f)
				}
			}
		}
	}
	if s := q("sig", "Certificate"); !strings.Contains(s, "(module std.x509, Experimental)") {
		t.Errorf("sig:\n%s", s)
	}
	if s := q("import", "Version"); !strings.Contains(s, "std.tls is Experimental: it may change or be withdrawn in any minor release") {
		t.Errorf("import:\n%s", s)
	}
	if s := q("sig", "ReadText"); strings.Contains(s, "(Experimental)") || strings.Contains(s, "(Provisional)") {
		t.Errorf("a Stable module shows a class:\n%s", s)
	}
}

// TestQueryUsesExperimental: mk query uses lists the uses of an
// Experimental module's items as of any other std module's, which
// tools/dogfood cover reads.
func TestQueryUsesExperimental(t *testing.T) {
	std := experimentalStd(t)
	dir := testPkg(t, map[string]string{"main.mk": "import std.tls\nimport std.x509\n\nexport F(): int {\n    c := x509.Certificate(n: 1)\n    return tls.Version(&c)\n}\n"})
	s, err := NewSession(Config{Root: filepath.Join(dir, "main.mk"), CacheDir: t.TempDir(), StdDir: std})
	if err != nil {
		t.Fatal(err)
	}
	a, res, err := s.Query("uses", nil)
	if err != nil || a == nil {
		t.Fatalf("query uses: %v\n%s", err, res.Report.Text(res.Sources))
	}
	for _, w := range []string{"std.tls.Version  main.mk:6:16", "std.x509.Certificate  main.mk:5:15"} {
		if !strings.Contains(a.Text(), w) {
			t.Errorf("uses lacks %q:\n%s", w, a.Text())
		}
	}
}
