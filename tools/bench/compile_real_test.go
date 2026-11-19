// Why: the real-program compile table edits real packages and must time
// fresh builds of them, never a stale one.
// What: TestCompileRealSites, TestCompileRealEditText, TestCompileRealCopy,
// TestRenderReal and TestCompileRealFixture (compile_real.go).
// How: checks the edit sites and text, the package copy and the rendering,
// and builds a small fixture program.

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func repoRootForTest(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestCompileRealSites: every program of the table is a package of the
// tree with one bin, and its edit site is there exactly once, indented
// (inside a function body, not a module-level declaration such as kvd's
// Help const), with one string literal that the good edit extends by a
// new token and the bad edit replaces by an unknown name. Nothing is built.
func TestCompileRealSites(t *testing.T) {
	root := repoRootForTest(t)
	ids := map[string]bool{}
	for _, p := range realPrograms {
		if ids[p.ID] {
			t.Errorf("program %s twice", p.ID)
		}
		ids[p.ID] = true
		pkg, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p.Path), "pkg.mk"))
		if err != nil {
			t.Errorf("%s: %v", p.ID, err)
			continue
		}
		if n := strings.Count(string(pkg), "pkg.Bin("); n != 1 {
			t.Errorf("%s: %d bins, want 1 (the table builds without --bin)", p.ID, n)
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p.Path), filepath.FromSlash(p.File)))
		if err != nil {
			t.Errorf("%s: %v", p.ID, err)
			continue
		}
		orig := string(b)
		if !regexp.MustCompile(`(?m)^[ \t]+` + regexp.QuoteMeta(p.Context)).MatchString(orig) {
			t.Errorf("%s: the edit site %q is not an indented line of %s (a body)", p.ID, p.Context, p.File)
		}
		good, err := p.editText(orig, 7, false)
		if err != nil {
			t.Errorf("%s: %v", p.ID, err)
			continue
		}
		bad, err := p.editText(orig, 7, true)
		if err != nil {
			t.Errorf("%s: %v", p.ID, err)
			continue
		}
		if d := lineDiff(orig, good); d != 1 || !strings.Contains(good, p.token(7)+`\n"`) {
			t.Errorf("%s: the good edit changes %d lines or lacks its token", p.ID, d)
		}
		if d := lineDiff(orig, bad); d != 1 || !strings.Contains(bad, p.undefined(7)) || strings.Contains(bad, p.Literal) && strings.Count(orig, p.Literal) == 1 {
			t.Errorf("%s: the bad edit changes %d lines or keeps the literal", p.ID, d)
		}
		if g8, _ := p.editText(orig, 8, false); g8 == good {
			t.Errorf("%s: two edits give one text", p.ID)
		}
	}
}

// TestCompileRealEditText: an edit site that is missing or repeated, or a
// literal that is not one string literal of it, is refused.
func TestCompileRealEditText(t *testing.T) {
	p := realProgram{ID: "x", File: "m.mk", Context: `Print("a")`, Literal: `"a"`}
	if _, err := p.editText("    Print(\"b\")\n", 1, false); err == nil {
		t.Error("a missing site accepted")
	}
	if _, err := p.editText("    Print(\"a\")\n    Print(\"a\")\n", 1, false); err == nil {
		t.Error("a repeated site accepted")
	}
	q := p
	q.Literal = "Print"
	if _, err := q.editText("    Print(\"a\")\n", 1, false); err == nil {
		t.Error("a literal without quotes accepted")
	}
	got, err := p.editText("    Print(\"a\")\n", 3, false)
	if err != nil || got != "    Print(\"amkbench-edit-3\\n\")\n" {
		t.Errorf("good edit: %q %v", got, err)
	}
	got, err = p.editText("    Print(\"a\")\n", 3, true)
	if err != nil || got != "    Print(mkbenchUndefined3)\n" {
		t.Errorf("bad edit: %q %v", got, err)
	}
	if _, err := parseVs("a=x,b"); err == nil {
		t.Error("-vs without a path accepted")
	}
	if m, err := parseVs("v1.2.0=x"); err != nil || len(m) != 1 || m[0].Label != "v1.2.0" || !filepath.IsAbs(m[0].Path) {
		t.Errorf("-vs: %v %v", m, err)
	}
	if err := compileReal(realOptions{Mks: []realMk{{Label: "a/b", Path: "x"}}}, func(string) {}); err == nil {
		t.Error("a label with a path separator accepted")
	}
	if err := compileReal(realOptions{Mks: []realMk{{Label: "a"}, {Label: "a"}}}, func(string) {}); err == nil {
		t.Error("two binaries with one label accepted")
	}
	if err := compileReal(realOptions{Mks: []realMk{{Label: "a"}}, Programs: []string{"nope"}}, func(string) {}); err == nil || !strings.Contains(err.Error(), "kvd") {
		t.Errorf("an unknown program: %v", err)
	}
}

// TestCompileRealCopy: the build copy has pkg.mk and src/, without the
// gen/ views mk build writes or a PGO profile, and counts the .mk lines.
func TestCompileRealCopy(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "pkg")
	writeFile(t, filepath.Join(pkg, "pkg.mk"), "a\nb\n")
	writeFile(t, filepath.Join(pkg, "src", "main", "main.mk"), "1\n2\n3\n")
	writeFile(t, filepath.Join(pkg, "src", "main", "gen", "x86_64-windows", "v.mk"), "view\n")
	writeFile(t, filepath.Join(pkg, "src", "default.mkprof"), "p")
	writeFile(t, filepath.Join(pkg, "cases", "c.txt"), "case")
	dst := filepath.Join(dir, "dst")
	lines, err := copyPackage(pkg, dst)
	if err != nil {
		t.Fatal(err)
	}
	if lines != 5 {
		t.Errorf("%d lines, want 5", lines)
	}
	for p, want := range map[string]bool{"pkg.mk": true, "src/main/main.mk": true, "src/main/gen": false, "src/default.mkprof": false, "cases": false} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(p))); (err == nil) != want {
			t.Errorf("%s in the copy: %v, want %v", p, err == nil, want)
		}
	}
}

// TestRenderReal: the section names every binary and program, prints each
// kind's median for every binary with its ratio to the measured one, an
// excluded cell's reason, and no dev-only kind under release.
func TestRenderReal(t *testing.T) {
	cell := func(mk, profile, kind string, times ...float64) realCell {
		return realCell{Program: "lox", Mk: mk, Profile: profile, Kind: kind, Times: times, Sum: summarize(times)}
	}
	res := &realResults{Jobs: 8, Runs: "r",
		Mks:      []realMk{{Label: "head", Path: `V:\x\mk.exe`, Version: "mk built from abc", SHA256: "0123456789abcdef"}, {Label: "v1.2.0", Path: `V:\y\mk.exe`}},
		Programs: []realProgramFact{{ID: "lox", Path: "Dogfoods/lox", Commit: "a2fa29d5aaaa", Lines: 3139, Edit: `src/main/main.mk: "usage"`, Args: []string{"--help"}}},
		Cells: []realCell{cell("head", "dev", "clean", 2), cell("v1.2.0", "dev", "clean", 3), cell("head", "dev", "edit", 0.5),
			{Program: "lox", Mk: "v1.2.0", Profile: "dev", Kind: "edit", Excluded: "edit build 1: E0201"},
			cell("head", "dev", "check-body", 0.1), cell("head", "release", "clean", 4), cell("v1.2.0", "release", "clean", 2)},
	}
	md := renderReal(res, "2026-11-16")
	for _, w := range []string{"## Table \"compile-real\"", "`tests/bench/results/2026-11-16/compile-real/results.json`",
		"| head | `V:\\x\\mk.exe` | mk built from abc |", "| lox | `Dogfoods/lox` | `a2fa29d5aaaa` | false | 3139 |",
		"| Profile | Build | head median (min, MAD) | v1.2.0 median (min, MAD) | v1.2.0 / head |",
		"| dev | clean (fresh MK_CACHE) | 2.000 s (2.000 s, 0 us) | 3.000 s (3.000 s, 0 us) | 1.50 |",
		"| dev | one-body edit | 500.0 ms (500.0 ms, 0 us) | excluded: edit build 1: E0201 |  |",
		"| dev | `mk check`, body edit to diagnostic | 100.0 ms (100.0 ms, 0 us) | not run |  |",
		"| release | clean (fresh MK_CACHE) | 4.000 s (4.000 s, 0 us) | 2.000 s (2.000 s, 0 us) | 0.50 |"} {
		if !strings.Contains(md, w) {
			t.Errorf("render lacks %q:\n%s", w, md)
		}
	}
	if strings.Contains(md, "| release | `mk check`") || strings.Contains(md, "| release | no change") {
		t.Error("a kind no cell has is rendered")
	}
}

// TestCompileRealFixture builds a small package with a real mk through
// measureReal, dev and release, with every kind on: every cell measures,
// so every build printed its edit's token and every check gave its
// verdict; and a run that looks for a later token fails, as a stale build
// would. The times are not looked at.
func TestCompileRealFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a small Mk package with a real mk")
	}
	root := repoRootForTest(t)
	dir := t.TempDir()
	tc, _ := findToolchain(dir, "")
	mk, err := testMkExe(t, tc, root, "compile-real")
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(dir, "fixture")
	writeFile(t, filepath.Join(pkg, "pkg.mk"), "const Package = pkg.Package(\n    name: \"fixture\",\n    bins: [pkg.Bin(name: \"fixture\", root: \"src/main\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n")
	writeFile(t, filepath.Join(pkg, "src", "main", "main.mk"), "export Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n    try out.Str(\"usage: fixture\\n\")\n}\n")
	p := realProgram{ID: "fixture", File: "src/main/main.mk", Context: `try out.Str("usage: fixture\n")`, Literal: `"usage: fixture\n"`, Args: []string{"--help"}}
	m := realMk{Label: "head", Path: mk}
	o := realOptions{CleanRuns: 1, EditRuns: 1, WarmRuns: 1, CheckRuns: 1}
	for _, profile := range []string{"dev", "release"} {
		base := filepath.Join(dir, "work", profile)
		s := realSetup{P: p, Mk: m, Profile: profile, Pkg: pkg, Src: filepath.Join(base, "src"), State: filepath.Join(base, "state"), Out: filepath.Join(base, "out"+exeSuffix())}
		cells := measureReal(s, o, "")
		want := 3
		if profile == "dev" {
			want = 4
		}
		if len(cells) != want {
			t.Fatalf("%s: %d cells, want %d: %+v", profile, len(cells), want, cells)
		}
		for _, c := range cells {
			if c.Excluded != "" || c.Sum.N != 1 {
				t.Errorf("%s %s: excluded %q, %d times", profile, c.Kind, c.Excluded, c.Sum.N)
			}
		}
		if err := s.verifyRun(1000); err == nil || !strings.Contains(err.Error(), "stale") {
			t.Errorf("%s: a missing token was not caught: %v", profile, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(pkg, "src", "main", "main.mk")); !strings.Contains(string(b), `"usage: fixture\n"`) || strings.Contains(string(b), "mkbench") {
		t.Error("the package itself was edited")
	}
}
