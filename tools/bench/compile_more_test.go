// Why: the warm and `mk check` kinds of the compile table, and its Linux
// run, each have a way to time the wrong thing.
// What: TestCompileMkCheckVariants to TestCompileMkKinds; compile_real_test.go
// tests the real-program table and buildtime_test.go the build-time table.
// How: checks the check-row variants, the verdicts, make's no-change plan,
// the rendering of the warm and check kinds, the Linux refusals and the
// carried tables.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCompileMkCheckVariants: the check rows' variants change only the
// edited module, each in the one place it names: the bad body replaces the
// edited function's constant by its token, the signature variants add one
// exported function after the others, and "" is the program itself.
func TestCompileMkCheckVariants(t *testing.T) {
	g := genSpec{Modules: 5, Funcs: 4, Structs: 2}
	ei, _ := g.editAt()
	for _, n := range []int{1, 7, 14} { // 7 and 14 make the edited c3 another function's c3 + 0
		_, plain := genMkModule(g, ei, n)
		if _, v := genMkModuleAt(g, ei, n, ""); v != plain {
			t.Fatalf("edit %d: the empty variant differs from the program", n)
		}
		_, bad := genMkModuleAt(g, ei, n, mkBadBody)
		tok := g.mkCheckToken(mkBadBody, n)
		if d := lineDiff(plain, bad); d != 1 || strings.Count(bad, tok) != 1 {
			t.Errorf("edit %d: the bad body changes %d lines and has %d tokens %s, want 1 and 1", n, d, strings.Count(bad, tok), tok)
		}
		// The signature variants are in sigAt's module, appended to it.
		si := g.sigAt()
		_, sigPlain := genMkModule(g, si, n)
		_, sig := genMkModuleAt(g, si, n, mkSig)
		_, badSig := genMkModuleAt(g, si, n, mkBadSig)
		for name, v := range map[string]string{"sig": sig, "bad-sig": badSig} {
			if !strings.HasPrefix(v, sigPlain) || !strings.Contains(v[len(sigPlain):], "export G") {
				t.Errorf("edit %d: %s does not append one exported function: %q", n, name, v[len(sigPlain):])
			}
		}
		if !strings.Contains(badSig, "(x: "+g.mkCheckToken(mkBadSig, n)+")") || strings.Contains(sig, "mkbenchUndefined") {
			t.Errorf("edit %d: the signature variants' tokens are wrong:\n%s\n%s", n, sig[len(sigPlain):], badSig[len(sigPlain):])
		}
		for i := 0; i < g.Modules; i++ {
			_, a := genMkModule(g, i, n)
			for _, v := range []string{mkBadBody, mkSig, mkBadSig} {
				if (i == ei && v == mkBadBody) || (i == si && v != mkBadBody) {
					continue
				}
				if _, b := genMkModuleAt(g, i, n, v); a != b {
					t.Errorf("edit %d: variant %s changed module %d", n, v, i)
				}
			}
		}
	}
	if g.mkCheckToken(mkBadBody, 3) == g.mkCheckToken(mkBadBody, 4) || g.mkCheckToken(mkBadSig, 3) == g.mkCheckToken(mkBadSig, 4) {
		t.Error("a token repeats across edits")
	}
}

// TestCompileCheckVerdict: a valid check must exit 0; an invalid one must
// exit 1 and quote its token, or it is a stale check.
func TestCompileCheckVerdict(t *testing.T) {
	for _, c := range []struct {
		code int
		out  string
		bad  bool
		ok   bool
	}{
		{0, "", false, true},
		{1, "x.mk:1:1: E0201 unknown name `tok`", false, false},
		{1, "x.mk:1:1: E0201 unknown name `tok`", true, true},
		{0, "", true, false},
		{1, "x.mk:1:1: E0201 unknown name `other`", true, false},
		{2, "mk check: tok", true, false},
	} {
		if err := checkVerdict(c.code, c.out, c.bad, "tok"); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

// TestCompileCNoChange: C's make runs nothing for a no-change build when
// the executable is newer than every object, and links when it is not.
func TestCompileCNoChange(t *testing.T) {
	dir := t.TempDir()
	tc := &toolchain{Clang: "clang", Work: dir}
	g := genSpec{Modules: 3, Funcs: 2, Structs: 1}
	s := newCompileSetup(dir, compileLangs[0], "dev")
	if _, err := s.writeSources(g); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	mid := time.Now().Add(-time.Hour)
	obj := filepath.Join(s.State, "obj")
	for _, src := range []string{"gen.h", "main.c", "m0.c", "m1.c", "m2.c"} {
		os.Chtimes(filepath.Join(s.Src, src), old, old)
		if src != "gen.h" {
			o := filepath.Join(obj, strings.TrimSuffix(src, ".c")+".o")
			writeFile(t, o, "")
			os.Chtimes(o, mid, mid)
		}
	}
	steps, err := s.steps(tc, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || len(steps[0]) != 0 || len(steps[1]) != 1 {
		t.Fatalf("no executable yet: want the link alone, got %v", steps)
	}
	writeFile(t, s.Out, "")
	steps, err = s.steps(tc, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 0 {
		t.Fatalf("the executable is newer than every object: want nothing, got %v", steps)
	}
	// An object newer than the executable links again (a check of the
	// executable's existence alone would pass the no-change case too).
	newer := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(obj, "m1.o"), newer, newer)
	if steps, err := s.steps(tc, false); err != nil || len(steps) != 2 || len(steps[0]) != 0 || len(steps[1]) != 1 {
		t.Fatalf("an object newer than the executable: want the link alone, got %v (%v)", steps, err)
	}
	if steps, _ := s.steps(tc, true); len(steps) != 2 || len(steps[0]) != g.Modules+1 {
		t.Fatalf("a clean build must compile every TU and link: %v", steps)
	}
}

// TestCompileRenderNewKinds: a table with the no-change and check kinds
// renders their columns and rows, and the budgets block, with a verdict
// only at a budget's stated size; the Linux run has its own heading and
// path and judges only the ELF row; a table without the warm and check
// kinds renders as TestRenderCompile expects.
func TestCompileRenderNewKinds(t *testing.T) {
	cell := func(lang, profile, kind string, lines int, times ...float64) compileCell {
		return compileCell{Lang: lang, Profile: profile, Kind: kind, Times: times, Sum: summarize(times), Lines: lines}
	}
	res := &compileResults{Spec: genSpec{Modules: 139, Funcs: 40, Structs: 8}, Jobs: 8, Runs: "r"}
	res.Cells = []compileCell{
		cell("c", "dev", "clean", 100, 1), cell("c", "dev", "edit", 100, 0.2), cell("c", "dev", "warm", 100, 0.01),
		cell("mk", "dev", "clean", 100_000, 1.5), cell("mk", "dev", "edit", 100_000, 0.4), cell("mk", "dev", "warm", 100_000, 0.1),
		cell("mk", "dev", "check-body", 100_000, 0.05), cell("mk", "dev", "check-sig", 100_000, 0.25),
		cell("mk", "release", "clean", 100_000, 3), cell("mk", "release", "edit", 100_000, 1),
	}
	md := renderCompile(res, "2026-11-16")
	for _, w := range []string{
		"| No change median (min, MAD) | No change / best |",
		"| mk | 100000 | 1.500 s (1.500 s, 0 us) | 1.50 | 400.0 ms (400.0 ms, 0 us) | 2.00 | 100.0 ms (100.0 ms, 0 us) | 10.00 |",
		"| go | | Go has one build profile: see the dev table | | | | | |",
		"**Edit to diagnostic",
		"| body edit to diagnostic | 100000 | 50.0 ms",
		"| signature edit | 100000 | 250.0 ms",
		"| Edit to diagnostic, body edit (at most 100 ms at 1 MLOC) | dev check-body | 100000 | no verdict: the program has 100000 lines, and the budget is stated at 1000000 |",
		"| Edit to diagnostic, signature edit (at most 300 ms at 100 KLOC) | dev check-sig | 100000 | met: median 250.0 ms |",
		"| Dev build, one-function edit, COFF (at most 350 ms at 100 KLOC, no PDB) | dev edit | 100000 | **missed**: median 400.0 ms |",
		"| Clean dev build, user code (at most 2 s at 100 KLOC) | dev clean | 100000 | met: median 1.500 s for the whole build, std included, an upper bound of the user code's |",
		"`tests/bench/results/2026-11-16/compile/results.json`",
	} {
		if !strings.Contains(md, w) {
			t.Errorf("render lacks %q:\n%s", w, md)
		}
	}
	// The clean dev cell includes std, so over the bound it is no miss of
	// the user-code budget.
	res.Cells[3] = cell("mk", "dev", "clean", 100_000, 2.5)
	if md2 := renderCompile(res, "2026-11-16"); !strings.Contains(md2, "| dev clean | 100000 | no verdict: the cell includes std (median 2.500 s for the whole build, over the bound; the user code's share is not measured) |") || strings.Contains(md2, "| dev clean | 100000 | **missed**") {
		t.Errorf("an over-bound clean dev cell:\n%s", md2)
	}
	res.Cells[3] = cell("mk", "dev", "clean", 100_000, 1.5)
	if strings.Contains(md, "ELF") {
		t.Error("the Windows table judges the ELF budget")
	}
	// C's make runs nothing on a no-change build: its loop's time is
	// printed as such and is never the best, so no ratio divides by it.
	res.Cells[2] = compileCell{Lang: "c", Profile: "dev", Kind: "warm", Lines: 100, Times: []float64{0}, Sum: summarize([]float64{0}), Nothing: true}
	md = renderCompile(res, "2026-11-16")
	for _, w := range []string{"| nothing to build (make runs no command) |  |", "| 100.0 ms (100.0 ms, 0 us) | 1.00 |"} {
		if !strings.Contains(md, w) {
			t.Errorf("render with C's empty make lacks %q:\n%s", w, md)
		}
	}
	if strings.Contains(md, "Inf") || strings.Contains(md, "NaN") {
		t.Errorf("a ratio divided by zero:\n%s", md)
	}
	res.Platform = "linux"
	res.Cells = []compileCell{cell("mk", "dev", "clean", 100_000, 1), cell("mk", "dev", "edit", 100_000, 0.15), cell("mk", "dev", "warm", 100_000, 0.05)}
	md = renderCompile(res, "2026-11-16")
	for _, w := range []string{"## Table \"compile\" on x86_64-linux (WSL 2)", "`tests/bench/results/2026-11-16/compile-linux/results.json`",
		"| Dev build, one-function edit, ELF (at most 200 ms at 100 KLOC) | dev edit | 100000 | met: median 150.0 ms |"} {
		if !strings.Contains(md, w) {
			t.Errorf("Linux render lacks %q:\n%s", w, md)
		}
	}
	if strings.Contains(md, "COFF") || strings.Contains(md, "Clean dev build") {
		t.Error("the Linux table judges a Windows budget")
	}
	if strings.Contains(md, "| rust |") || strings.Contains(md, "| go |") {
		t.Error("the Linux table lists a language it does not build")
	}
	for tok, out := range map[string]bool{"mkbench-edit-1\nx": true, "a mkbench-edit-1": true, "mkbench-edit-12": false, "mkbench-edit-12 mkbench-edit-1": true, "": false} {
		if hasToken(tok, "mkbench-edit-1") != out {
			t.Errorf("hasToken(%q) != %v", tok, out)
		}
	}
}

// TestCompileLinuxRefusals: compile-linux starts wsl.exe only under the
// WSL lock, with a Linux work directory and a Linux mk; the run inside
// WSL is Mk only; and its command line carries the table's settings, the
// 8 CPUs and the caller's lock.
func TestCompileLinuxRefusals(t *testing.T) {
	dir := t.TempDir()
	notELF := filepath.Join(dir, "mk.exe")
	writeFile(t, notELF, "MZ")
	elf := filepath.Join(dir, "mk-linux")
	writeFile(t, elf, "\x7fELF....")
	o := compileLinuxOptions{Root: dir, Work: dir, Mk: elf, LinuxWork: "/tmp/x", Spec: genSpec{Modules: 3, Funcs: 2, Structs: 1}, Log: func(string) {}}
	if runtime.GOOS == "windows" {
		t.Setenv("MK_WSL_LOCKED", "")
		if err := compileLinux(o); err == nil || !strings.Contains(err.Error(), "wsl-lock.ps1") {
			t.Errorf("outside the WSL lock: %v", err)
		}
		t.Setenv("MK_WSL_LOCKED", "1")
		bad := o
		bad.LinuxWork = "/mnt/v/x"
		if err := compileLinux(bad); err == nil || !strings.Contains(err.Error(), "Linux file system") {
			t.Errorf("a work directory under /mnt: %v", err)
		}
		bad = o
		bad.Mk = notELF
		if err := compileLinux(bad); err == nil || !strings.Contains(err.Error(), "not a Linux executable") {
			t.Errorf("a Windows mk: %v", err)
		}
	}
	if !isELF(elf) || isELF(notELF) {
		t.Error("isELF")
	}
	args := strings.Join(compileLinuxArgs(o, `V:\w\linux\benchtool`, "mk built from abc"), " ")
	for _, w := range []string{"-e taskset -c " + linuxCompileCPUs + " /mnt/v/w/linux/benchtool compile", "-platform linux -no-lock -publish -langs mk",
		"-modules 3 -funcs 2 -structs 1", "-work /tmp/x", "(mk built from abc)"} {
		if !strings.Contains(args, w) {
			t.Errorf("wsl.exe args lack %q: %s", w, args)
		}
	}
	err := compileTable(compileOptions{Platform: "linux", Spec: genSpec{Modules: 3, Funcs: 2, Structs: 1}, Langs: []string{"c"}}, func(string) {})
	if runtime.GOOS == "linux" {
		if err == nil || !strings.Contains(err.Error(), "Mk only") {
			t.Errorf("the Linux run accepted C: %v", err)
		}
	} else if err == nil || !strings.Contains(err.Error(), "inside WSL") {
		t.Errorf("-platform linux on %s: %v", runtime.GOOS, err)
	}
}

// TestCompileLinuxRefusesExperimentELF: compile-linux reads a compiler
// experiment out of the Linux mk itself, before it takes the machine lock or
// starts WSL. Two real ELF executables of a module that declares
// mk/internal/toolchain's experiment variable, both built with -trimpath
// (which leaves -ldflags out of Go's build information): the default one
// passes and the one built with -X is refused, as is an ELF with no symbol
// table.
func TestCompileLinuxRefusesExperimentELF(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "go.mod"), "module mk\n\ngo 1.21\n")
	writeFile(t, filepath.Join(src, "internal", "toolchain", "t.go"), "package toolchain\n\nvar experiment string\n\nfunc Experiment() string { return experiment }\n")
	writeFile(t, filepath.Join(src, "main.go"), "package main\n\nimport \"mk/internal/toolchain\"\n\nfunc main() { println(toolchain.Experiment()) }\n")
	dir := t.TempDir()
	build := func(name string, flags ...string) string {
		out := filepath.Join(dir, name)
		cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), append(append([]string{"build", "-trimpath"}, flags...), "-o", out, ".")...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=", "GOTOOLCHAIN=local")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", name, err, b)
		}
		return out
	}
	def := build("mk-default")
	exp := build("mk-exp", "-ldflags", "-X mk/internal/toolchain.experiment=unroll=300,noslp")
	stripped := build("mk-stripped", "-ldflags", "-s")
	if e, err := elfExperiment(def); e != "" || err != nil {
		t.Errorf("the default ELF: experiment %q, %v; want none", e, err)
	}
	if e, err := elfExperiment(exp); e != "unroll=300,noslp" || err != nil {
		t.Errorf("the experiment ELF: experiment %q, %v; want unroll=300,noslp", e, err)
	}
	if err := refuseELFExperiment(def); err != nil {
		t.Errorf("the default ELF refused: %v", err)
	}
	if err := refuseELFExperiment(stripped); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Errorf("an ELF with no symbol table: %v, want a refusal", err)
	}
	if runtime.GOOS != "windows" {
		return
	}
	t.Setenv("MK_WSL_LOCKED", "1")
	lock := filepath.Join(dir, "machine.lock")
	o := compileLinuxOptions{Root: dir, Work: dir, Mk: exp, LinuxWork: "/tmp/x", Spec: genSpec{Modules: 3, Funcs: 2, Structs: 1}, LockPath: lock, Log: func(string) {}}
	if err := compileLinux(o); err == nil || !strings.Contains(err.Error(), "experiment: unroll=300,noslp") {
		t.Errorf("compile-linux with an experiment ELF: %v, want the refusal", err)
	}
	if _, err := os.Stat(lock); err == nil {
		t.Error("compile-linux took the machine lock before it refused the experiment ELF")
	}
}

// TestRenderCarriesCompileTables: the three extra compile tables live in
// their own directories of a date (compile-linux, compile-real, buildtime;
// never a date suffix), and a later date that did not measure them
// carries each under its own date with the carried line.
func TestRenderCarriesCompileTables(t *testing.T) {
	root := t.TempDir()
	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "bench", "results", "2026-11-06", "results.json"))
	if err != nil {
		t.Skip("the 2026-11-06 results are not in this tree:", err)
	}
	res := func(d string) string { return filepath.Join(root, "tests", "bench", "results", d) }
	writeFile(t, filepath.Join(res("2026-11-08"), "results.json"), string(b))
	// The earlier date has its own results, so the carried-back check
	// below runs.
	writeFile(t, filepath.Join(res("2026-11-06"), "results.json"), string(b))
	writeFile(t, filepath.Join(res("2026-11-07"), "compile-linux", "results.json"), `{"spec":{"modules":3,"funcs":2,"structs":1},"jobs":8,"platform":"linux","cells":[]}`)
	writeFile(t, filepath.Join(res("2026-11-07"), "compile-real", "results.json"), `{"mks":[{"label":"head"}],"jobs":8,"cells":[]}`)
	writeFile(t, filepath.Join(res("2026-11-07"), "buildtime", "results.json"), `{"runs":3,"cells":[]}`)
	if err := writeResultsMD(root, "2026-11-08"); err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(filepath.Join(root, "tests", "bench", "RESULTS.md"))
	s := string(md)
	for _, w := range []string{"## Table \"compile\" on x86_64-linux (WSL 2)", "*Carried from the publish of 2026-11-07: the Linux compile-speed table",
		"## Table \"compile-real\"", "*Carried from the publish of 2026-11-07: the real-program compile table",
		"## Table \"buildtime\"", "*Carried from the publish of 2026-11-07: the build-time table"} {
		if !strings.Contains(s, w) {
			t.Errorf("RESULTS.md lacks %q", w)
		}
	}
	if err := writeResultsMD(root, "2026-11-06"); err != nil {
		t.Fatal(err)
	}
	md, _ = os.ReadFile(filepath.Join(root, "tests", "bench", "RESULTS.md"))
	if strings.Contains(string(md), "compile-real") || !strings.Contains(string(md), "2026-11-06") {
		t.Error("a later date's table was carried back, or the earlier date did not render")
	}
}

// TestCompileMkKinds builds the generated program with a real mk at a
// tiny size, dev and release, with every kind on: each cell measures, so
// the check rows' verdicts held (a valid check exits 0, an invalid one
// names its token) and every build printed its checksum. The times are
// not looked at.
func TestCompileMkKinds(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a generated Mk program with a real mk")
	}
	root := repoRootForTest(t)
	dir := t.TempDir()
	tc, _ := findToolchain(dir, "")
	mk, err := testMkExe(t, tc, root, "compile-kinds")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	platform := ""
	if runtime.GOOS == "linux" {
		platform = "linux" // linux.sh runs this test inside WSL with a Linux mk
	}
	err = compileTable(compileOptions{Root: root, Work: dir, Mk: mk, Spec: genSpec{Modules: 3, Funcs: 2, Structs: 1}, Langs: []string{"mk"},
		CleanRuns: 1, EditRun: 1, WarmRuns: 1, CheckRuns: 1, Log: func(string) {}, Platform: platform}, func(s string) { lines = append(lines, s) })
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(lines, "\n"))
	}
	want := []string{"ok compile mk dev ", "ok compile mk dev warm", "ok compile mk dev check-body", "ok compile mk dev check-sig",
		"ok compile mk release ", "ok compile mk release warm"}
	all := strings.Join(lines, "\n")
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("no line %q:\n%s", w, all)
		}
	}
	if strings.Contains(all, "release check") {
		t.Error("a check row ran in release")
	}
}
