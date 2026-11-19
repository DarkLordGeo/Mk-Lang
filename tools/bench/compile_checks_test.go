// Why: the compile and build-time tables can time a state other than the
// one they name, or build an older mk with the wrong std, unless checked.
// What: TestCompileSigEditImporters, TestCompileCheckTimedState,
// TestCompileRealVsStd and TestBuildTimeRefusals.
// How: each test builds the setup in a temporary tree, with a fake runner
// where timing is involved, and asserts the right state or the refusal.

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestCompileSigEditImporters: the signature edit is made in a module
// every other module imports through its parents, so main and the whole
// tree are its importers, and it changes no function body: the bodies of
// the sig module stay at the body edit across every G.
func TestCompileSigEditImporters(t *testing.T) {
	for _, m := range []int{5, 50, 139} {
		g := genSpec{Modules: m, Funcs: 4, Structs: 2}
		si := g.sigAt()
		// The transitive importers of si: every module whose parent chain
		// reaches it.
		n := 0
		for i := 0; i < g.Modules; i++ {
			for j := i; j > 0; j = g.parent(j) {
				if g.parent(j) == si {
					n++
					break
				}
			}
		}
		if n != g.Modules-1 {
			t.Errorf("%d modules: the sig module m%d has %d transitive importers among the modules, want %d", m, si, n, g.Modules-1)
		}
		_, plain := genMkModule(g, si, 0)
		for _, sig := range []int{3, 4, 9} {
			for _, v := range []string{mkSig, mkBadSig} {
				_, text := genMkModuleSig(g, si, 7, sig, v)
				if !strings.HasPrefix(text, plain) || !strings.Contains(text[len(plain):], "export G") {
					t.Errorf("%d modules, G%d %s: not the plain module plus one function", m, sig, v)
				}
			}
		}
		// The body-edit variants stay in the edited module.
		ei, _ := g.editAt()
		if ei == si {
			t.Errorf("%d modules: the body and signature edits share module m%d", m, ei)
		}
		if _, a := genMkModuleAt(g, si, 5, mkBadBody); strings.Contains(a, "mkbenchUndefined") {
			t.Errorf("%d modules: the bad body landed in the sig module", m)
		}
	}
}

// TestCompileCheckTimedState: measureCheck times the check of the
// state with the opposite verdict of the one before it, never the
// untimed state, and the signature edit's bodies stay at the base edit.
func TestCompileCheckTimedState(t *testing.T) {
	dir := t.TempDir()
	g := genSpec{Modules: 5, Funcs: 4, Structs: 2}
	var s compileSetup
	for _, l := range compileLangs {
		if l.ID == "mk" {
			s = newCompileSetup(dir, l, "dev")
		}
	}
	if _, err := s.writeSources(g); err != nil {
		t.Fatal(err)
	}
	ei, _ := g.editAt()
	ep, _ := genMkModule(g, ei, 0)
	sp, _ := genMkModule(g, g.sigAt(), 0)
	edited := filepath.Join(s.Src, filepath.FromSlash(ep))
	sigFile := filepath.Join(s.Src, filepath.FromSlash(sp))
	token := regexp.MustCompile(`mkbenchUndefined[0-9]+`)
	// The fake check: its time names the state it saw.
	durs := map[string]time.Duration{"plain": time.Second, mkBadBody: 2 * time.Second, mkBadSig: 3 * time.Second, mkSig: 4 * time.Second}
	var bodies []string
	old := checkRunner
	defer func() { checkRunner = old }()
	checkRunner = func(buildCmd) (time.Duration, int, string, error) {
		e, _ := os.ReadFile(edited)
		sg, _ := os.ReadFile(sigFile)
		bodies = append(bodies, strings.Split(string(sg), "export G")[0])
		switch {
		case strings.Contains(string(sg), "export G") && token.Match(sg):
			return durs[mkBadSig], 1, "error E0201: unknown type `" + token.FindString(string(sg)) + "`", nil
		case strings.Contains(string(sg), "export G"):
			return durs[mkSig], 0, "", nil
		case token.Match(e):
			return durs[mkBadBody], 1, "error E0201: unknown name `" + token.FindString(string(e)) + "`", nil
		}
		return durs["plain"], 0, "", nil
	}
	tc := &toolchain{Mk: "mk", Work: dir}
	o := compileOptions{Spec: g, CheckRuns: 3}
	body := compileCell{Kind: "check-body"}
	if err := measureCheck(tc, s, o, 2, mkBadBody, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Times) != 3 || slices.ContainsFunc(body.Times, func(x float64) bool { return x != 2 }) {
		t.Errorf("check-body timed %v, want three checks of the bad body (2 s each)", body.Times)
	}
	bodies = nil
	sig := compileCell{Kind: "check-sig"}
	if err := measureCheck(tc, s, o, 2+o.CheckRuns+1, mkSig, &sig); err != nil {
		t.Fatal(err)
	}
	if len(sig.Times) != 3 || slices.ContainsFunc(sig.Times, func(x float64) bool { return x != 4 }) {
		t.Errorf("check-sig timed %v, want three checks of the valid signature (4 s each)", sig.Times)
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Fatal("a signature edit changed the sig module's bodies")
		}
	}
	if b, _ := os.ReadFile(sigFile); strings.Contains(string(b), "export G") {
		t.Error("the sources do not end without G")
	}
}

// TestCompileRealVsStd: -mk-std with -vs is refused, since every mk
// would build that std rather than its own.
func TestCompileRealVsStd(t *testing.T) {
	old := mkStd
	defer func() { mkStd = old }()
	mkStd = t.TempDir()
	o := realOptions{Root: repoRootForTest(t), Work: t.TempDir(), Programs: []string{"kvd"},
		Mks: []realMk{{Label: "head", Path: "mk-a.exe"}, {Label: "old", Path: "mk-b.exe"}}, Log: func(string) {}}
	if err := compileReal(o, func(string) {}); err == nil || !strings.Contains(err.Error(), "-mk-std") {
		t.Fatalf("-mk-std with -vs: %v", err)
	}
}

// TestBuildTimeRefusals: buildtime refuses another OS, and a row
// name no run-time table has.
func TestBuildTimeRefusals(t *testing.T) {
	if runtime.GOOS != "windows" {
		if err := buildTimeTable(buildTimeOptions{Runs: 1}, func(string) {}); err == nil || !strings.Contains(err.Error(), "Windows") {
			t.Fatalf("off Windows: %v", err)
		}
		return
	}
	root := fakeTree(t)
	err := buildTimeTable(buildTimeOptions{Root: root, Runs: 1, Rows: []string{"no-such-row"}, Work: t.TempDir()}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "no-such-row") {
		t.Fatalf("a mistyped row: %v", err)
	}
}
