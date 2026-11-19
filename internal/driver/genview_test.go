// Why: generated code is written to a per-target gen/ view so diagnostics
// can cite real lines; the view must stay exact, and a hand edit of it must
// be refused (E0904) warm as cold.
// What: tests of genview.go: TestGenViewWritten, TestHandEditedView,
// TestViewRewritten, TestGenViewNotForSingleFileOrStd and TestGenViewOwned.
// How: checks packages with gen lines, edits or deletes the view, and
// checks again. A gen module with a diagnostic is never stored (unstorable,
// "gen diagnostics"); a clean one is, through GenRef (genref.go), so a warm
// run of a clean package takes the gen module from the cache too.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/types"
)

// genViewPkg is a package whose module a has one gen line, used by b.
func genViewPkg() map[string]string {
	return map[string]string{
		"pkg.mk": libManifest(),
		"g/g.mk": "export Make(g: mut Gen) {\n    g.Emit(quote {\n        export Helper(): int {\n            return 1\n        }\n    })\n}\n",
		"a/a.mk": "import g\n\ngen g.Make()\n",
		"b/b.mk": "import a\n\nexport Use(): int {\n    return a.Helper() + 1\n}\n",
	}
}

// genViewRun checks root with the view on and returns the result and the
// generated file's text as Sources holds it ("" when there is none).
func genViewRun(t *testing.T, cfg Config) (Result, string) {
	t.Helper()
	cfg.GenView = true
	r := frontCheck(t, cfg)
	text := ""
	if f, ok := r.Sources.ByPath("a/gen/" + cfg.Target.Name + "/gen.mk"); ok {
		text = f.Text
	}
	return r, text
}

func readView(t *testing.T, path string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

// TestGenViewWritten: a check with GenView writes <module dir>/gen/<target>/
// gen.mk holding the generated file byte for byte (so every line a
// diagnostic cites is the view's) and one trailer line; a second check
// finds it unchanged and does not write it (its modification time stays),
// and the other modules still come from the cache (the view is not
// source). A session without GenView writes nothing.
func TestGenViewWritten(t *testing.T) {
	cfg := Config{Root: writeTree(t, genViewPkg()), CacheDir: t.TempDir(), Target: types.X86_64Windows}
	off := frontCheck(t, cfg)
	clean(t, off)
	view := filepath.Join(cfg.Root, "a", "gen", "x86_64-windows", "gen.mk")
	if _, ok := readView(t, view); ok {
		t.Fatalf("GenView off wrote %s", view)
	}
	r, text := genViewRun(t, cfg)
	clean(t, r)
	got, ok := readView(t, view)
	if !ok || text == "" {
		t.Fatalf("no view written (file %v, generated %q)", ok, text)
	}
	if !strings.HasPrefix(got, text) || !strings.Contains(text, "export Helper(): int") {
		t.Errorf("view does not start with the generated text:\nview %q\ntext %q", got, text)
	}
	rest := strings.TrimPrefix(got, text)
	if strings.Count(rest, "\n") != 1 || !strings.HasPrefix(rest, genViewTrailer) || !strings.Contains(rest, "x86_64-windows") {
		t.Errorf("trailer %q", rest)
	}
	if !genViewOwned([]byte(got)) {
		t.Errorf("the view mk wrote is not owned")
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(view, old, old); err != nil {
		t.Fatal(err)
	}
	warm, _ := genViewRun(t, cfg)
	clean(t, warm)
	if fi, err := os.Stat(view); err != nil || !fi.ModTime().Equal(old) {
		t.Errorf("an unchanged view was written again (%v)", err)
	}
	if !slices.Contains(warm.Trace.ModulesFromCache, "b") || !slices.Contains(warm.Trace.ModulesFromCache, "g") {
		t.Errorf("warm: b and g not from the cache: %q", warm.Trace.ModulesFromCache)
	}
	if entries, _ := os.ReadDir(filepath.Dir(view)); len(entries) != 1 {
		t.Errorf("view directory holds %d entries, want the view alone", len(entries))
	}
}

// TestHandEditedView: a view edited by hand is E0904 at line 1,
// column 1 of the view's path (the generated file's), with the facts view
// and module, on the first check after the edit and on a warm one, and the
// file is left as edited. It is never parsed (a syntax error in it is no
// E0101). Phase types: with a names error elsewhere, the phase rule drops
// it.
func TestHandEditedView(t *testing.T) {
	cfg := Config{Root: writeTree(t, genViewPkg()), CacheDir: t.TempDir(), Target: types.X86_64Windows}
	r, _ := genViewRun(t, cfg)
	clean(t, r)
	view := filepath.Join(cfg.Root, "a", "gen", "x86_64-windows", "gen.mk")
	got, _ := readView(t, view)
	edited := strings.Replace(got, "return 1", "return 2 +", 1)
	if edited == got {
		t.Fatalf("view %q has no `return 1`", got)
	}
	if err := os.WriteFile(view, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, run := range []string{"cold", "warm"} {
		r, _ := genViewRun(t, cfg)
		if got := lines(r); !slices.Equal(got, []string{"a/gen/x86_64-windows/gen.mk:1:1:E0904"}) {
			t.Fatalf("%s: %q, want one E0904 at the view\n%s", run, got, r.Report.Text(r.Sources))
		}
		d := r.Report.Diags[0].Diag
		if fact(d, "view") != "a/gen/x86_64-windows/gen.mk" || fact(d, "module") != "a" || len(d.Alternatives) == 0 ||
			d.Phase != diag.PhaseTypes || ExitStatus(r.Report) != 1 {
			t.Errorf("%s: facts view %q module %q, alternatives %q, phase %v, exit %d", run, fact(d, "view"), fact(d, "module"),
				d.Alternatives, d.Phase, ExitStatus(r.Report))
		}
		if now, _ := readView(t, view); now != edited {
			t.Errorf("%s: the hand-edited view was overwritten", run)
		}
	}
	// A names error elsewhere: the phase rule keeps it alone.
	bad := filepath.Join(cfg.Root, "b", "b.mk")
	if err := os.WriteFile(bad, []byte("import a\n\nexport Use(): int {\n    return a.Nope()\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ = genViewRun(t, cfg)
	for _, l := range lines(r) {
		if strings.HasSuffix(l, ":E0904") {
			t.Errorf("E0904 beside a names error: %q", lines(r))
		}
	}
}

// TestViewRewritten: a deleted view is written again; a view mk wrote
// whose output changed (the generator now emits another body) is rewritten
// with no E0904; a broken generator keeps the last view; a module with no gen line left loses a view
// that is still mk's, and keeps one that was edited by hand.
func TestViewRewritten(t *testing.T) {
	files := genViewPkg()
	cfg := Config{Root: writeTree(t, files), CacheDir: t.TempDir(), Target: types.X86_64Windows}
	r, _ := genViewRun(t, cfg)
	clean(t, r)
	view := filepath.Join(cfg.Root, "a", "gen", "x86_64-windows", "gen.mk")
	first, _ := readView(t, view)
	if err := os.Remove(view); err != nil {
		t.Fatal(err)
	}
	r, _ = genViewRun(t, cfg)
	clean(t, r)
	if again, ok := readView(t, view); !ok || again != first {
		t.Errorf("deleted view not written again the same (%v):\n%q\n%q", ok, again, first)
	}

	g := filepath.Join(cfg.Root, "g", "g.mk")
	if err := os.WriteFile(g, []byte(strings.Replace(files["g/g.mk"], "return 1", "return 3", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	r, text := genViewRun(t, cfg)
	clean(t, r)
	if now, _ := readView(t, view); !strings.HasPrefix(now, text) || !strings.Contains(now, "return 3") || !genViewOwned([]byte(now)) {
		t.Errorf("changed output: view %q, generated %q", now, text)
	}

	// A broken generator (it does not check, so it never runs): the module
	// keeps its gen line, produces no output, and keeps its last view. (A
	// generator that runs and emits broken code has output, so its view
	// shows that output.)
	kept, _ := readView(t, view)
	if err := os.WriteFile(g, []byte(strings.Replace(files["g/g.mk"], "g.Emit(", "g.Nope(", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ = genViewRun(t, cfg)
	if len(r.Report.Diags) == 0 {
		t.Fatal("the broken generator checks")
	}
	if now, ok := readView(t, view); !ok || now != kept {
		t.Errorf("a broken generator removed or changed the view (%v)", ok)
	}
	if err := os.WriteFile(g, []byte(files["g/g.mk"]), 0o644); err != nil {
		t.Fatal(err)
	}

	a := filepath.Join(cfg.Root, "a", "a.mk")
	if err := os.WriteFile(a, []byte("export Helper(): int {\n    return 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ = genViewRun(t, cfg)
	clean(t, r)
	if _, ok := readView(t, view); ok {
		t.Errorf("the view of a module with no gen output was kept")
	}
	mine := "// my notes\n"
	if err := os.MkdirAll(filepath.Dir(view), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(view, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ = genViewRun(t, cfg)
	clean(t, r)
	if now, ok := readView(t, view); !ok || now != mine {
		t.Errorf("a file mk did not write was removed or changed (%v, %q)", ok, now)
	}
}

// TestGenViewNotForSingleFileOrStd: a single file's directory is not its
// module's own, so it gets no view; std (embedded) never does. A unit with
// a gen line in a corpus session writes nothing either (memory tree).
func TestGenViewNotForSingleFileOrStd(t *testing.T) {
	dir := writeTree(t, map[string]string{"one.mk": "import std.derive\n\nstruct P {\n    x: int\n}\n\ngen derive.Eq(P)\n"})
	r := frontCheck(t, Config{Root: filepath.Join(dir, "one.mk"), CacheDir: t.TempDir(), GenView: true, Target: types.X86_64Windows})
	clean(t, r)
	if _, err := os.Stat(filepath.Join(dir, "gen")); !os.IsNotExist(err) {
		t.Errorf("single-file mode wrote a gen/ directory (%v)", err)
	}
}

// TestGenViewOwned: the trailer check accepts exactly what genViewBytes
// writes, for a text with and without a final newline and for the empty
// text, and refuses an edit of the text, of the trailer, a CRLF conversion
// and a file with no trailer.
func TestGenViewOwned(t *testing.T) {
	for _, text := range []string{"export F(): int {\n    return 1\n}\n", "export G(): int {\n    return 2\n}", ""} {
		b := genViewBytes(text, "x86_64-linux")
		if !genViewOwned(b) {
			t.Errorf("%q: not owned", b)
		}
		s := string(b)
		for name, bad := range map[string]string{
			"text edited":    strings.Replace(s, "return", "return 0 +", 1),
			"trailer edited": strings.Replace(s, genViewTrailer, genViewTrailer+"0", 1),
			"crlf":           strings.ReplaceAll(s, "\n", "\r\n"),
			"no trailer":     text,
			"line added":     "// note\n" + s,
		} {
			if bad != s && genViewOwned([]byte(bad)) {
				t.Errorf("%q, %s: owned", text, name)
			}
		}
	}
}
