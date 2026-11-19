// Why: mk fix, pkg.sum, test_deps and fetch messages each had a hole that
// touched files it should not or showed what it should not.
// What: regression tests of an unrelated pkg.sum, mk fix --locked,
// E0217 per import binding, a dependency's own test_deps, and credentials
// in fetch messages.
// How: each test writes a small package (with an archive server where it
// fetches), runs the command and compares diagnostics and files.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mk fix on a valid package beside an unrelated file named pkg.sum is
// clean, and the file is left as it was; pkg.sum is read only for a Dep
// with an archive hash in the manifest.
func TestSum_FixUnrelatedSumUntouched(t *testing.T) {
	const junk = "this is my personal checksum list\nfoo.txt 1234\n"
	app := writeTree(t, map[string]string{
		"pkg.mk":  depManifest("app"),
		"a.mk":    "export A(): int {\n    return 1\n}\n",
		"pkg.sum": junk,
	})
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{})
	if err != nil || res.Stopped != "clean" {
		t.Fatalf("mk fix: %v %+v", err, res)
	}
	if got := readFile(t, filepath.Join(app, sumFile)); got != junk {
		t.Errorf("pkg.sum changed: %q", got)
	}
	// With a url Dep holding an archive hash, an unreadable pkg.sum is a note:
	// the hash stays and mk fix goes on.
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	app2 := urlApp(t, srv.URL+"/ini.tar.gz", sha(arch))
	if err := os.WriteFile(filepath.Join(app2, sumFile), []byte(junk), 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := NewSession(Config{Root: app2, StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	round, _, notes, err := s2.archiveHashes(false)
	if err != nil || round != nil || len(notes) != 1 || !strings.Contains(notes[0], "pkg.sum") {
		t.Errorf("unreadable pkg.sum with a 1.2 hash: %v %+v %q", err, round, notes)
	}
}

// mk fix --locked writes no pkg.sum; the hash stays, with a note.
func TestSum_FixLockedWritesNoSum(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	app := urlApp(t, srv.URL+"/ini.tar.gz", sha(arch))
	before := readFile(t, filepath.Join(app, "pkg.mk"))
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir(), Locked: true})
	if err != nil {
		t.Fatal(err)
	}
	round, _, notes, err := s.archiveHashes(false)
	if err != nil || round != nil || len(notes) == 0 {
		t.Errorf("--locked: %v %+v %q", err, round, notes)
	}
	if _, err := os.Stat(filepath.Join(app, sumFile)); err == nil {
		t.Error("--locked wrote pkg.sum")
	}
	if readFile(t, filepath.Join(app, "pkg.mk")) != before {
		t.Error("--locked changed pkg.mk")
	}
}

// One package bound under a deps name in one file and a test_deps
// name in another: only the test_deps binding's uses are E0217.
func TestE0217_PerImportBinding(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"h/pkg.mk":   depManifest("h"),
		"h/src/h.mk": "export H(): int {\n    return 1\n}\n",
		"app/pkg.mk": "const Package = pkg.Package(\n    name: \"app\",\n    deps: [" + pathDep("toml", "../h") + "],\n    test_deps: [" + pathDep("t2", "../h") + "],\n)\n",
		"app/a.mk":   "import toml.src\n\nexport A(): int {\n    return src.H()\n}\n",
		"app/b.mk":   "import t2.src\n\nexport B(): int {\n    return src.H()\n}\n",
	})
	r := depCheck(t, filepath.Join(ws, "app"), Config{})
	if got := lines(r); len(got) != 1 || !strings.HasPrefix(got[0], "b.mk:") || !strings.HasSuffix(got[0], ":E0217") {
		t.Errorf("got %q, want one E0217 in b.mk\n%s", got, r.Report.Text(r.Sources))
	}
}

// A dependency's own test_deps are resolved and recorded, but the root never
// grants them and never links them, mk test included.
func TestTestDeps_DependencyTestDepNotGrantedNorLinked(t *testing.T) {
	files := iniFiles()
	files["pkg.mk"] = "const Package = pkg.Package(\n    name: \"ini\",\n    c_sources: [pkg.CSource(path: \"c/ini.c\", flags: [])],\n)\n"
	files["c/ini.c"] = "int ini_c(void) { return 1; }\n"
	arch := tarGzOf(t, "", files)
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	ws := writeTree(t, map[string]string{
		"h/pkg.mk": "const Package = pkg.Package(\n    name: \"h\",\n" +
			"    links: [pkg.Link(name: \"hlib\", kind: \"dynamic\", path: \"\", target: \"x86_64-windows\")],\n)\n",
		"h/src/h.mk": "export H(): int {\n    return 1\n}\n",
		"p/pkg.mk": "const Package = pkg.Package(\n    name: \"p\",\n    test_deps: [" + pathDep("h", "../h") + ", " +
			urlDep("ini", srv.URL+"/ini.tar.gz", "") + "],\n)\n",
		"p/src/p.mk": "import h.src\nimport ini.src\n\nexport P(): int {\n    return 2\n}\n\ntest \"p\" {\n    Assert(src.H() == 1)\n}\n",
		"app/pkg.mk": depManifest("app", pathDep("p", "../p")),
		"app/a.mk":   "import p.src\n\nexport A(): int {\n    return src.P()\n}\n",
	})
	app := filepath.Join(ws, "app")
	for _, testing := range []bool{false, true} {
		s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		s.testing = testing
		r := s.runCheck()
		for _, l := range lines(r) {
			if strings.HasSuffix(l, ":E0215") || strings.HasSuffix(l, ":E0202") {
				t.Errorf("testing %v: %s\n%s", testing, l, r.Report.Text(r.Sources))
			}
		}
		links, ds := s.collectLinks(s.pkgMan, app)
		if len(ds) > 0 {
			t.Fatal(ds)
		}
		for _, l := range links {
			if l.Name == "hlib" {
				t.Errorf("testing %v: the root links its dependency's test dependency", testing)
			}
		}
		for _, p := range s.deps {
			if p.man != nil && len(p.man.CSources) > 0 {
				t.Errorf("testing %v: %s's C sources are the root's to build", testing, p.name)
			}
		}
	}
}

// The fetch and offline messages never show a url's credentials
// (redactDiags; the message is built by the fetcher from the whole url).
func TestGrant_CredentialsRedactedInFetchMessages(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	url := strings.Replace(srv.URL, "http://", "http://user:pw-SECRET@", 1) + "/ini.tar.gz?token=SECRET"
	app := urlApp(t, url, sha(arch))
	for _, cfg := range []Config{{Offline: true}, {}} {
		if !cfg.Offline {
			srv.Close() // the fetch fails, and its message names the url
		}
		r := depCheck(t, app, cfg)
		if len(r.Report.Diags) == 0 {
			t.Fatalf("offline %v: no diagnostic", cfg.Offline)
		}
		for _, d := range r.Report.Diags {
			txt := d.Diag.Message + strings.Join(d.Diag.Alternatives, " ")
			for _, f := range d.Diag.Facts {
				txt += " " + f.Value.Str
			}
			if strings.Contains(txt, "SECRET") {
				t.Errorf("offline %v: %s", cfg.Offline, txt)
			}
		}
	}
}
