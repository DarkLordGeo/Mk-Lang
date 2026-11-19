// Why: a published table is only as true as the runner's own facts about
// the environment, the build and the toolchain it measured.
// What: tests of the pinned runtime environment, the build stamp, merges of
// runs and the mk identity named in the table header.
// How: runs the runner's functions on planted environments, binaries and
// result files, and checks what they report or refuse.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
)

// TestProgramsGetPinnedRuntimeEnv: a GOGC=off, a GODEBUG or an Mk
// runtime knob in the caller's environment does not reach a program
// tools/bench runs, and the facts report the value the programs get.
func TestProgramsGetPinnedRuntimeEnv(t *testing.T) {
	base := []string{"PATH=x", "GOGC=off", "GODEBUG=gctrace=1", "GOMAXPROCS=8", "GOMEMLIMIT=1MiB", "MK_CARRIERS=7", "MK_TEST_SEED=3", "MK_STD=keep"}
	if runtime.GOOS == "windows" {
		base = append(base, "gogc=off") // Windows names are case-insensitive
	}
	got := runEnv(base)
	want := []string{"PATH=x", "MK_STD=keep", "GOGC=100", "GOMAXPROCS=1"}
	if !slices.Equal(got, want) {
		t.Fatalf("runEnv = %q, want %q", got, want)
	}

	// The real exec path: the test binary prints what it sees.
	t.Setenv("GOGC", "off")
	t.Setenv("GODEBUG", "madvdontneed=1")
	t.Setenv("MK_CARRIERS", "7")
	for name, want := range map[string]string{"GOGC": "100", "GOMAXPROCS": "1", "GODEBUG": "(unset)", "MK_CARRIERS": "(unset)"} {
		t.Setenv("BENCH_HELPER_ENV", name)
		out, _, err := runOnce(os.Args[0], nil)
		if err != nil || out != want {
			t.Errorf("a program run by tools/bench sees %s=%q (%v), want %q", name, out, err, want)
		}
	}
	t.Setenv("BENCH_HELPER_ENV", "")

	cmd := hyperfineCmd("hyperfine", "x.exe", nil, hfOptions{Warmup: 1, Runs: 1}, "o.json")
	if v, _ := envValue(cmd.Env, "GOGC"); v != "100" || slices.Contains(cmd.Env, "GOGC=off") {
		t.Errorf("hyperfine's environment has GOGC %q: %q", v, cmd.Env)
	}
	if _, ok := envValue(cmd.Env, "GODEBUG"); ok {
		t.Errorf("hyperfine's environment keeps GODEBUG: %q", cmd.Env)
	}

	f := gatherFacts(t.TempDir(), &toolchain{})
	if f.GOGC != "100" || !strings.Contains(f.RunEnv, "GOGC=100 GOMAXPROCS=1 set") || !strings.Contains(f.RunEnv, "GODEBUG") {
		t.Errorf("facts GOGC %q, run env %q; want the pinned 100 read back", f.GOGC, f.RunEnv)
	}
	md := renderResults(&results{Runs: []facts{f}, Columns: columns}, "D")
	if !strings.Contains(md, "| GOGC | 100 |") || !strings.Contains(md, "| Program environment | GOGC=100 GOMAXPROCS=1 set") {
		t.Errorf("RESULTS.md facts lack the pinned environment:\n%s", md)
	}
}

// TestStampCoversToolchainAndStd: a std edited at the same -mk-std
// path, or a compiler at the same path (same bytes, as a rustup proxy)
// that now reports another version, changes the stamp, so publish
// rebuilds instead of reusing the stale executable.
func TestStampCoversToolchainAndStd(t *testing.T) {
	dir := t.TempDir()
	rowDir := filepath.Join(dir, "row")
	writeFile(t, filepath.Join(rowDir, "mk", "x.mk"), "// x, Mk\n")
	writeFile(t, filepath.Join(rowDir, "x.c"), "// x, C\n")
	std := filepath.Join(dir, "std")
	writeFile(t, filepath.Join(std, "core", "a.mk"), "const A = 1\n")
	r := &row{Name: "x", Dir: rowDir, Sources: map[string]string{"mk": "mk", "c": "x.c"}}
	mkCol, _ := columnByID("mk")
	cCol, _ := columnByID("c")

	fakeMk := helperExe(t, "mk build 0123456789abcdef (go)\nLLVM 21.1.8 from X")
	tc := &toolchain{Mk: fakeMk, Work: filepath.Join(dir, "work"), MkStd: std}
	b := &built{Row: r, Col: mkCol, Exe: exeName(tc.Work, "t", r, mkCol)}
	var err error
	if b.Cmd, err = tc.command(r, mkCol, b.Exe); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.Cmd.Deps, "LLVM 21.1.8") {
		t.Fatalf("the mk build's deps %q do not name mk's LLVM", b.Cmd.Deps)
	}
	var log []string
	logf := func(s string) { log = append(log, s) }
	built := func() bool {
		log = nil
		if _, err := buildOne(b, logf); err != nil {
			t.Fatal(err)
		}
		return len(log) == 1 && strings.HasPrefix(log[0], "built ")
	}
	if !built() {
		t.Fatalf("first build did not run: %q", log)
	}
	writeFile(t, b.Exe, "exe") // the fake mk writes no program
	if built() {
		t.Fatalf("an unchanged column was rebuilt: %q", log)
	}
	writeFile(t, filepath.Join(std, "core", "a.mk"), "const A = 2\n")
	if !built() {
		t.Fatal("a std edited at the same -mk-std path did not rebuild the Mk column")
	}

	fakeClang := helperExe(t, "clang version 21.1.8")
	stampOf := func() string {
		cmd, err := (&toolchain{Clang: fakeClang, Work: tc.Work}).command(r, cCol, "o.exe")
		if err != nil {
			t.Fatal(err)
		}
		s, err := stamp(r, cCol, cmd)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := stampOf()
	exeBytes, _ := os.ReadFile(fakeClang)
	writeFile(t, fakeClang+".out", "clang version 21.1.9")
	if after, _ := os.ReadFile(fakeClang); !slices.Equal(after, exeBytes) {
		t.Fatal("the fixture changed the compiler's bytes")
	}
	if stampOf() == before {
		t.Fatal("a compiler at the same path with the same bytes and another version kept its stamp")
	}
}

// TestMergeRefusesMixedToolchains: a merge keeps no row measured with
// another mk, std or toolchain, and a file merged before that rule says
// per run which toolchain each row used and prints no pooled mk / c median.
func TestMergeRefusesMixedToolchains(t *testing.T) {
	run := func(sha, std, rust string) facts {
		return facts{MkSHA256: sha, MkStd: std, Versions: map[string]string{"rust": rust, "c": "clang 21"}}
	}
	prev := results{Runs: []facts{run("a", "", "1.99")}, Builds: map[string]buildCmd{}, Rows: []rowInfo{{ID: "old", Run: 0}}}
	for name, tc := range map[string]struct {
		next facts
		want string
	}{
		"mk":   {run("b", "", "1.99"), "mk sha256"},
		"std":  {run("a", `S:\std`, "1.99"), "MK_STD"},
		"rust": {run("a", "", "1.100"), `rust "1.99", this run with "1.100"`},
	} {
		next := results{Runs: []facts{tc.next}, Builds: map[string]buildCmd{}}
		if _, err := mergeResults(prev, next, []string{"new"}); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "old") {
			t.Errorf("%s: merge of a different toolchain: %v, want an error naming %q and the kept row", name, err, tc.want)
		}
	}
	next := results{Runs: []facts{run("a", "", "1.99")}, Builds: map[string]buildCmd{}}
	if got, err := mergeResults(prev, next, []string{"old"}); err != nil || len(got.Rows) != 0 {
		t.Errorf("replacing every row of the old run: %v, %d rows", err, len(got.Rows))
	}
	if _, err := mergeResults(prev, next, []string{"new"}); err != nil {
		t.Errorf("same toolchain refused: %v", err)
	}

	// A file that already mixes two runs.
	tm := func(m float64) []float64 { return []float64{m, m, m} }
	mixed := &results{Runs: []facts{run("a", "", "1.99"), run("b", "", "1.100")}, Columns: columns}
	for i, id := range []string{"p", "q"} {
		mixed.Rows = append(mixed.Rows, rowInfo{ID: id, Table: "same", Run: i})
		for col, m := range map[string]float64{"mk": 1.2, "c": 1.0, "rust": 1.1} {
			c := cell{Row: id, Table: "same", Col: col, Times: tm(m)}
			c.Summary = summarize(c.Times)
			mixed.Cells = append(mixed.Cells, c)
		}
	}
	md := renderResults(mixed, "D")
	if !strings.Contains(md, "| rust | run 1: 1.99; run 2: 1.100 |") {
		t.Errorf("the column legend names one run's toolchain:\n%s", md)
	}
	if strings.Contains(md, "Median of mk / c") || !strings.Contains(md, "No median of mk / c") {
		t.Errorf("a median pooled over two mk binaries was printed:\n%s", md)
	}
}

// TestMkVersionNamesTheBinary: the mk toolchain of the table header is
// the commit the measured binary was built from, read from the binary, not
// the working tree's HEAD.
func TestMkVersionNamesTheBinary(t *testing.T) {
	got := mkIdentityFrom([]debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: "deadbeefcafe"},
		{Key: "vcs.time", Value: "2026-11-06T15:28:41Z"}, {Key: "vcs.modified", Value: "true"}})
	if got != "mk built from deadbeefcafe (2026-11-06T15:28:41Z) with uncommitted changes" {
		t.Errorf("mkIdentityFrom = %q", got)
	}
	if got := mkIdentityFrom(nil); !strings.Contains(got, "no VCS revision") {
		t.Errorf("no revision: %q", got)
	}

	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	fakeMk := helperExe(t, "mk build 0123456789abcdef (go)\nLLVM 21.1.8 from C:\\LLVM\\bin (default location)")
	f := gatherFacts(root, &toolchain{Mk: fakeMk})
	want := mkIdentity(fakeMk, "") + ", LLVM 21.1.8 from C:\\LLVM\\bin (default location)"
	if v := f.Versions["mk"]; v != want || strings.HasPrefix(v, "mk at ") || (f.Commit != "" && strings.Contains(v, f.Commit)) {
		t.Errorf("Versions[mk] = %q, want %q (HEAD is %s)", v, want, f.Commit)
	}
}
