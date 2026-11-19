// Why: the compile-speed table is fair only if every language builds the
// same generated program with its own default flags and 8 jobs.
// What: tests of compile.go, from TestCompileGenerators to
// TestRenderCarriesSections.
// How: generates the program in each language, checks the build steps and
// environment, and renders fixed results.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCompileGenerators: every language's program has one file per
// module plus its main, and an edit rewrites only the edited function's
// constant in the edited module, to a value no earlier edit had.
func TestCompileGenerators(t *testing.T) {
	g := genSpec{Modules: 5, Funcs: 4, Structs: 2}
	ei, _ := g.editAt()
	for _, l := range compileLangs {
		files := l.files(g)
		mods := 0
		for i := 0; i < g.Modules; i++ {
			p, text := l.module(g, i, 0)
			if files[p] != text {
				t.Errorf("%s: module %d file %s differs from the program's", l.ID, i, p)
			}
			mods++
		}
		if len(files) <= mods {
			t.Errorf("%s: %d files for %d modules: no main", l.ID, len(files), mods)
		}
		_, e0 := l.module(g, ei, 0)
		_, e1 := l.module(g, ei, 1)
		_, e2 := l.module(g, ei, 2)
		if e0 == e1 || e1 == e2 || e0 == e2 {
			t.Errorf("%s: edits do not give new text", l.ID)
		}
		if d := lineDiff(e0, e1); d != 1 {
			t.Errorf("%s: an edit changes %d lines, want 1", l.ID, d)
		}
		for i := 0; i < g.Modules; i++ {
			if i == ei {
				continue
			}
			_, a := l.module(g, i, 0)
			_, b := l.module(g, i, 5)
			if a != b {
				t.Errorf("%s: an edit changed module %d", l.ID, i)
			}
		}
	}
	if g.checksum(0) == g.checksum(1) {
		t.Error("the checksum does not see the edit")
	}
	if err := (genSpec{Modules: 1, Funcs: 1, Structs: 1}).check(); err == nil {
		t.Error("one module accepted")
	}
	if err := (genSpec{Modules: 4, Funcs: 2, Structs: 3}).check(); err == nil {
		t.Error("more structs than functions accepted")
	}
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

// TestCompileGoProgramPrintsTheChecksum: the generated Go program, built
// by the go command, prints genSpec.checksum before and after an edit, so
// the generator's evaluation is the programs' arithmetic (every other
// language is checked against the same number on every build).
func TestCompileGoProgramPrintsTheChecksum(t *testing.T) {
	g := genSpec{Modules: 7, Funcs: 3, Structs: 2}
	dir := t.TempDir()
	s := compileSetup{Lang: compileLangs[3], Profile: "dev", Src: filepath.Join(dir, "src")}
	if s.Lang.ID != "go" {
		t.Fatal("compileLangs[3] is not go")
	}
	if _, err := s.writeSources(g); err != nil {
		t.Fatal(err)
	}
	run := func(edit int) {
		t.Helper()
		cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "run", ".")
		cmd.Dir = s.Src
		cmd.Env = compileEnv(os.Environ(), []string{"GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local"})
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go run: %v\n%s", err, out)
		}
		if got, want := strings.TrimSpace(string(out)), strconv.FormatUint(g.checksum(edit), 10); got != want {
			t.Fatalf("edit %d: printed %s, want %s", edit, got, want)
		}
	}
	run(0)
	if err := s.edit(g, 3); err != nil {
		t.Fatal(err)
	}
	run(3)
}

// TestCompileSteps: every build runs 8 jobs, each profile has its
// language's flags, a clean Go build is -a, and C recompiles only the
// translation units whose object is older than the source (and links).
func TestCompileSteps(t *testing.T) {
	dir := t.TempDir()
	tc := &toolchain{Mk: "mk", Clang: "clang", Cargo: "cargo", Zig: "zig", Go: "go", Work: dir}
	want := map[string][]string{
		"c dev":        {"-g", "-march=x86-64-v2"},
		"c release":    {"-O3", "-DNDEBUG"},
		"rust dev":     {"cargo build --offline -j 8", "RUSTFLAGS=-C target-cpu=x86-64-v2"},
		"rust release": {"--release"},
		"zig dev":      {"-O Debug", "-j8", "-mcpu=x86_64_v2", "--global-cache-dir"},
		"zig release":  {"-O ReleaseFast"},
		"go dev":       {"go build -p 8", "GOCACHE=", "-a", "GOAMD64=v2"},
		"mk dev":       {"mk build -j 8 --cpu x86-64-v2", "MK_CACHE="},
		"mk release":   {"--release --pgo=off"},
	}
	g := genSpec{Modules: 3, Funcs: 2, Structs: 1}
	for _, l := range compileLangs {
		for _, p := range l.Profiles {
			s := newCompileSetup(dir, l, p)
			if _, err := s.writeSources(g); err != nil {
				t.Fatal(err)
			}
			steps, err := s.steps(tc, true)
			if err != nil {
				t.Fatal(err)
			}
			var all []string
			for _, st := range steps {
				for _, c := range st {
					all = append(all, c.String())
				}
			}
			text := strings.Join(all, "\n")
			for _, w := range want[l.ID+" "+p] {
				if !strings.Contains(text, w) {
					t.Errorf("%s %s: commands lack %q:\n%s", l.ID, p, w, text)
				}
			}
			if l.ID == "c" && (len(steps) != 2 || len(steps[0]) != g.Modules+1) {
				t.Errorf("c %s: clean build %d steps, %d TUs", p, len(steps), len(steps[0]))
			}
		}
	}
	if (compileLangs[3].has("release")) || !compileLangs[3].has("dev") {
		t.Error("go must have one profile")
	}
	// C as a make: objects newer than their sources are not recompiled.
	s := newCompileSetup(dir, compileLangs[0], "dev")
	if _, err := s.writeSources(g); err != nil {
		t.Fatal(err)
	}
	obj := filepath.Join(s.State, "obj")
	old := time.Now().Add(-time.Hour)
	for _, src := range []string{"gen.h", "main.c", "m0.c", "m1.c", "m2.c"} {
		os.Chtimes(filepath.Join(s.Src, src), old, old)
		if src != "gen.h" {
			writeFile(t, filepath.Join(obj, strings.TrimSuffix(src, ".c")+".o"), "")
		}
	}
	if err := s.edit(g, 1); err != nil {
		t.Fatal(err)
	}
	steps, err := s.steps(tc, false)
	if err != nil {
		t.Fatal(err)
	}
	ei, _ := g.editAt()
	if len(steps[0]) != 1 || !strings.Contains(steps[0][0].String(), "m"+strconv.Itoa(ei)+".c") {
		t.Errorf("edit build compiles %d TUs, want only m%d.c: %v", len(steps[0]), ei, steps[0])
	}
	if len(steps[1]) != 1 || !strings.Contains(steps[1][0].String(), "-fuse-ld=lld") {
		t.Errorf("no link step: %v", steps[1])
	}
}

// TestCompileEnv: a command's variables replace the caller's, and an
// empty one clears it (CARGO_INCREMENTAL, GOFLAGS), except MK_STD, which is
// always stated.
func TestCompileEnv(t *testing.T) {
	env := compileEnv([]string{"GOFLAGS=-p=4", "PATH=x", "CARGO_INCREMENTAL=1", "MK_STD=/old"}, []string{"GOFLAGS=", "CARGO_INCREMENTAL=", "MK_STD=", "GOCACHE=c"})
	got := strings.Join(env, " ")
	if got != "PATH=x MK_STD= GOCACHE=c" {
		t.Fatalf("env %q", got)
	}
}

// TestRenderCompile: the compile section prints both profiles, Go's one
// profile, each cell's median with min and MAD, ratios to the best of the
// same profile and kind, and an excluded cell's reason.
func TestRenderCompile(t *testing.T) {
	cell := func(lang, profile, kind string, times ...float64) compileCell {
		return compileCell{Lang: lang, Profile: profile, Kind: kind, Times: times, Sum: summarize(times), Lines: 100}
	}
	res := &compileResults{Spec: genSpec{Modules: 50, Funcs: 40, Structs: 8}, Jobs: 8, Runs: "3 clean",
		Run: facts{Versions: map[string]string{"mk": "mk built from abc", "cargo": "cargo 1.99"}}}
	res.Cells = []compileCell{
		cell("c", "dev", "clean", 1, 1, 1), cell("c", "dev", "edit", 0.5, 0.5),
		cell("mk", "dev", "clean", 2, 2, 2), cell("mk", "dev", "edit", 0.25, 0.25),
		cell("go", "dev", "clean", 4, 4), cell("go", "dev", "edit", 1, 1),
		cell("mk", "release", "clean", 3, 3), {Lang: "mk", Profile: "release", Kind: "edit", Excluded: "a stale build"},
	}
	md := renderCompile(res, "2026-11-08")
	for _, w := range []string{"## Table \"compile\"", "**dev profile.**", "**release profile.**",
		"| mk | 100 | 2.000 s (2.000 s, 0 us) | 2.00 | 250.0 ms (250.0 ms, 0 us) | 1.00 |",
		"| go | | Go has one build profile", "excluded: a stale build", "cargo 1.99", "50 modules of 8 structs and 40 functions"} {
		if !strings.Contains(md, w) {
			t.Errorf("render lacks %q:\n%s", w, md)
		}
	}
}

// TestRenderCarriesSections: a date that has not measured the codegen,
// Linux or compile-speed table renders the newest earlier date's, under that
// date's name and with a line saying it was carried; a date that has them
// renders its own, with no such line.
func TestRenderCarriesSections(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join("..", "..", "tests", "bench", "results", "2026-11-06")
	old := filepath.Join(root, "tests", "bench", "results", "2026-11-06")
	for _, rel := range []string{"results.json", "codegen/results.json", "linux/results.json"} {
		b, err := os.ReadFile(filepath.Join(src, rel))
		if err != nil {
			t.Skip("the 2026-11-06 results are not in this tree:", err)
		}
		writeFile(t, filepath.Join(old, rel), string(b))
	}
	b, _ := os.ReadFile(filepath.Join(old, "results.json"))
	writeFile(t, filepath.Join(root, "tests", "bench", "results", "2026-11-08", "results.json"), string(b))
	writeFile(t, filepath.Join(root, "tests", "bench", "results", "2026-11-09", "codegen", "results.json"), "{}")
	if err := writeResultsMD(root, "2026-11-08"); err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(filepath.Join(root, "tests", "bench", "RESULTS.md"))
	s := string(md)
	for _, w := range []string{"## Table \"codegen\"", "From `tests/bench/results/2026-11-06/codegen/results.json`",
		"*Carried from the publish of 2026-11-06: the codegen table", "# Linux (x86_64-linux in WSL 2)",
		"From `tests/bench/results/2026-11-06/linux/results.json`", "*Carried from the publish of 2026-11-06: the Linux table"} {
		if !strings.Contains(s, w) {
			t.Errorf("RESULTS.md lacks %q", w)
		}
	}
	if strings.Contains(s, "2026-11-09") {
		t.Error("a later date's section was carried back")
	}
	if err := writeResultsMD(root, "2026-11-06"); err != nil {
		t.Fatal(err)
	}
	md, _ = os.ReadFile(filepath.Join(root, "tests", "bench", "RESULTS.md"))
	if strings.Contains(string(md), "Carried from") {
		t.Error("a date's own sections are marked carried")
	}
}
