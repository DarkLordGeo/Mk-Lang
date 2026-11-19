// Why: the edit-loop numbers depend on the synthetic workspace being
// deterministic, the right size and valid Mk.
// What: tests of determinism, size and shape, the edits' text, the workspace
// form, and that a small instance checks and builds with no diagnostic.
// How: generates workspaces in memory and in temporary directories and
// compares them; the last test builds mk and runs it on a 20,000-line one.

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func small(lines int) config { return config{Lines: lines, Packages: 8, Seed: 1} }

// TestDeterminism: two runs with one set of flags write byte-identical trees,
// and another seed writes another tree.
func TestDeterminism(t *testing.T) {
	cfg := small(30000)
	a, b := t.TempDir(), t.TempDir()
	for _, d := range []string{a, b} {
		if err := run([]string{"-out", d, "-lines", "30000", "-seed", "1"}); err != nil {
			t.Fatal(err)
		}
	}
	ta, tb := readTree(t, a), readTree(t, b)
	if len(ta) == 0 || len(ta) != len(tb) {
		t.Fatalf("trees differ in size: %d and %d files", len(ta), len(tb))
	}
	for p, data := range ta {
		if !bytes.Equal(data, tb[p]) {
			t.Fatalf("%s differs between two runs", p)
		}
	}
	other := generate(config{Lines: cfg.Lines, Packages: 8, Seed: 2}, state{})
	same := 0
	for p, s := range generate(cfg, state{}).files {
		if other.files[p] == s {
			same++
		}
	}
	if same > 10 {
		t.Errorf("seed 2 shares %d files with seed 1; the seed should change the modules", same)
	}
	// A second run into the same directory writes nothing.
	n, _, err := write(a, cfg, state{}, true)
	if err != nil || n != 0 {
		t.Errorf("regenerating in place wrote %d files (%v), want 0", n, err)
	}
	// Other flags into a used directory are refused.
	if err := run([]string{"-out", a, "-lines", "40000"}); err == nil {
		t.Error("other flags into a used directory: want an error")
	}
}

// TestSizeAndShape: the line total is near the target, the package graph is
// a deep chain over a common leaf, every module imports prim, and the
// program imports every module.
func TestSizeAndShape(t *testing.T) {
	for _, tc := range []config{small(20000), small(200000), {Lines: 1000000, Packages: 8, Seed: 1}, {Lines: 50000, Packages: 3, Seed: 7}, {Lines: 80000, Packages: 12, Seed: 3}} {
		o := generate(tc, state{})
		if d := o.lines - tc.Lines; d < -tc.Lines/20 || d > tc.Lines/20 {
			t.Errorf("%+v: %d lines, more than 5%% from the target", tc, o.lines)
		}
		w := plan(tc)
		if len(w.pkgs) != tc.Packages-1 {
			t.Fatalf("%+v: %d library packages", tc, len(w.pkgs))
		}
		for i, p := range w.pkgs {
			if i > 0 && (len(p.deps) == 0 || p.deps[0] != 0) {
				t.Errorf("%+v: package %s does not name base", tc, p.name)
			}
			for _, d := range p.deps {
				if d >= i {
					t.Errorf("%+v: package %s depends on a later package", tc, p.name)
				}
			}
			for _, m := range p.modules {
				if !strings.Contains(o.files["app/main.mk"], "import "+p.name+"."+m.name+"\n") {
					t.Errorf("%+v: the program does not import %s", tc, m.name)
				}
				for _, im := range m.imports {
					if im.pkg != p && !contains(p.deps, im.pkg.index) {
						t.Errorf("%+v: %s imports %s, not a dependency of %s", tc, m.name, im.name, p.name)
					}
				}
			}
		}
		for path, text := range o.files {
			if strings.Count(path, "/") == 2 && !strings.Contains(path, "/prim/") && !strings.Contains(path, "/gens/") &&
				!strings.Contains(text, "import base.prim\n") && !strings.Contains(text, "import prim\n") {
				t.Errorf("%+v: %s does not import prim", tc, path)
			}
		}
	}
	// At the full size: eight packages, several large and the deepest chain
	// of package dependencies running through all of them.
	w := plan(config{Lines: 1000000, Packages: 8, Seed: 1})
	for i := 2; i < len(w.pkgs); i++ {
		if !contains(w.pkgs[i].deps, i-1) {
			t.Errorf("package %s does not depend on %s", w.pkgs[i].name, w.pkgs[i-1].name)
		}
	}
}

func contains(xs []int, x int) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// TestEditsText: body changes one line of one file, sig changes prim and one
// call in every ordinary module, reset restores the baseline's bytes, and
// noop writes nothing.
func TestEditsText(t *testing.T) {
	cfg := small(30000)
	base := generate(cfg, state{})
	body := generate(cfg, state{Body: 1})
	if ch := changed(base, body); len(ch) != 1 || ch[0] != "base/base000/base000_0.mk" {
		t.Errorf("body edit changed %v", ch)
	} else if n := lineDiff(base.files[ch[0]], body.files[ch[0]]); n != 1 {
		t.Errorf("body edit changed %d lines", n)
	}
	sig := generate(cfg, state{Sig: true})
	mods := 0
	for _, p := range plan(cfg).pkgs {
		mods += len(p.modules)
	}
	ch := changed(base, sig)
	if len(ch) != mods+1 || !contains2(ch, "base/prim/prim.mk") {
		t.Errorf("sig edit changed %d files, want prim and %d modules", len(ch), mods)
	}
	for _, p := range ch {
		if !strings.Contains(sig.files[p], "SigProbe(x: i64, k: i64)") && !strings.Contains(sig.files[p], "prim.SigProbe(n, 1)") {
			t.Errorf("sig edit changed %s, which neither declares nor calls SigProbe", p)
		}
	}

	d := t.TempDir()
	if err := run([]string{"-out", d, "-lines", "30000"}); err != nil {
		t.Fatal(err)
	}
	want := readTree(t, d)
	for _, k := range []string{"body", "body", "sig", "noop", "sig", "body", "reset"} {
		if err := run([]string{"-out", d, "-edit", k}); err != nil {
			t.Fatal(k, err)
		}
	}
	got := readTree(t, d)
	for p, data := range want {
		if !bytes.Equal(data, got[p]) {
			t.Errorf("after reset %s differs from the baseline", p)
		}
	}
	if err := run([]string{"-out", d, "-edit", "body", "-lines", "5"}); err == nil {
		t.Error("-lines with -edit: want an error")
	}
	if err := run([]string{"-out", t.TempDir(), "-edit", "body"}); err == nil {
		t.Error("-edit on a directory with no workspace: want an error")
	}
}

func contains2(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func changed(a, b *out) []string {
	var ps []string
	for _, p := range a.paths() {
		if a.files[p] != b.files[p] {
			ps = append(ps, p)
		}
	}
	return ps
}

func lineDiff(a, b string) int {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(la) != len(lb) {
		return -1
	}
	n := 0
	for i := range la {
		if la[i] != lb[i] {
			n++
		}
	}
	return n
}

// TestWorkspaceForm: -workspace writes the 1.3 root manifest with a members
// list naming every package; the members' own manifests are unchanged.
func TestWorkspaceForm(t *testing.T) {
	cfg := small(20000)
	plain := generate(cfg, state{})
	cfg.Workspace = true
	ws := generate(cfg, state{})
	root := ws.files["pkg.mk"]
	for _, s := range []string{"mk: \"1.3\"", "members: [", "\"base\",", "\"interp\","} {
		if !strings.Contains(root, s) {
			t.Errorf("workspace root manifest lacks %q:\n%s", s, root)
		}
	}
	if strings.Contains(plain.files["pkg.mk"], "members") {
		t.Error("the default root manifest names members")
	}
	if ch := changed(plain, ws); len(ch) != 1 || ch[0] != "pkg.mk" {
		t.Errorf("-workspace changed %v, want only pkg.mk", ch)
	}
}

func readTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	m := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		m[filepath.ToSlash(rel)] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// mkBinary is MK_SYNTH_MK, or mk built from this checkout.
func mkBinary(t *testing.T) string {
	if p := os.Getenv("MK_SYNTH_MK"); p != "" {
		return p
	}
	exe := filepath.Join(t.TempDir(), "mk")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, "./cmd/mk")
	cmd.Dir = filepath.Join("..", "..")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/mk: %v\n%s", err, b)
	}
	return exe
}

// TestSmallInstanceChecksAndBuilds: a 20,000-line instance passes mk check
// and mk build with no diagnostic, its program runs, and each edit still
// checks clean (sig with its callers updated) and builds.
func TestSmallInstanceChecksAndBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("builds mk and a program")
	}
	mk := mkBinary(t)
	d := t.TempDir()
	if err := run([]string{"-out", d, "-lines", "20000"}); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "MK_CACHE="+t.TempDir())
	exe := filepath.Join(t.TempDir(), "lux.exe")
	mkRun := func(step string, args ...string) {
		t.Helper()
		cmd := exec.Command(mk, args...)
		cmd.Dir = d
		cmd.Env = env
		b, err := cmd.CombinedOutput()
		if err != nil || len(bytes.TrimSpace(b)) != 0 {
			t.Fatalf("%s: mk %s: %v\n%s", step, strings.Join(args, " "), err, b)
		}
	}
	mkRun("baseline", "check", ".")
	mkRun("baseline", "build", "-o", exe, ".")
	if b, err := exec.Command(exe, "a", "b").CombinedOutput(); err != nil || len(bytes.TrimSpace(b)) == 0 {
		t.Fatalf("the program: %v\n%s", err, b)
	}
	for _, k := range []string{"body", "sig", "body", "sig", "reset"} {
		if err := run([]string{"-out", d, "-edit", k}); err != nil {
			t.Fatal(err)
		}
		mkRun(k, "check", ".")
	}
	if err := run([]string{"-out", d, "-edit", "sig"}); err != nil {
		t.Fatal(err)
	}
	mkRun("sig", "build", "-o", exe, ".")

	// Other package counts and seeds check too: three packages, and twelve,
	// past LuxJS's names (pkg07 to pkg10).
	for _, args := range [][]string{{"-packages", "3", "-seed", "7"}, {"-packages", "12", "-seed", "3"}} {
		d = t.TempDir()
		if err := run(append([]string{"-out", d, "-lines", "12000"}, args...)); err != nil {
			t.Fatal(err)
		}
		mkRun(strings.Join(args, " "), "check", ".")
	}
}
