// Why: the corpus can build and run ok blocks, and the judge must read build
// and run outcomes right.
// What: TestJudgeBuild on synthetic outcomes, JudgeUnit's rule 1b (E0001),
// TestCorpusRunTimeout, TestCorpusRunPanicFails and the report's build counts.
// How: the run tests write a one-block repository and pass it through
// corpus.Run with a real driver session; they skip without LLVM.

package corpus

import (
	"strings"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
	"mk/internal/toolchain"
)

// JudgeBuild's rules on synthetic outcomes, for every mode.
func TestJudgeBuild(t *testing.T) {
	f := newFixture(src)
	ok := BlockUnit(block(VerdictOK, src, nil))
	rej := BlockUnit(block(VerdictReject, src, []diag.Code{diag.E0501}, Marker{Line: 2, Code: diag.E0501}))
	pass := Judgement{Status: StatusPass}
	pending := Judgement{Status: StatusPending, Reason: "needs StageInstances"}
	failed := Judgement{Status: StatusFail, Reason: "ok block has diagnostics: x"}
	res := func(built, ran bool, status int, timedOut bool, skipped string) driver.BlockResult {
		r := f.result(diag.PhaseFlow)
		r.Built, r.Ran, r.RunStatus, r.TimedOut, r.RunSkipped = built, ran, status, timedOut, skipped
		return r
	}
	cases := []struct {
		name   string
		u      *Unit
		r      driver.BlockResult
		mode   driver.UnitMode
		j      Judgement
		st     Status
		reason string
	}{
		{"check mode ignores the build", ok, res(false, false, -1, false, ""), driver.UnitCheck, pass, StatusPass, ""},
		{"built, exit 0", ok, res(true, true, 0, false, ""), driver.UnitRun, pass, StatusPass, ""},
		{"Main returns an error, exit 1", ok, res(true, true, 1, false, ""), driver.UnitRun, pass, StatusPass, ""},
		{"built, no Main", ok, res(true, false, -1, false, ""), driver.UnitRun, pass, StatusPass, ""},
		{"UnitBuild, built", ok, res(true, false, -1, false, ""), driver.UnitBuild, pass, StatusPass, ""},
		{"reaches a bodiless prelude function", ok, res(true, false, -1, false, "Acquire is reached but has no body"), driver.UnitRun, pass, StatusPass, ""},
		{"panic", ok, res(true, true, 101, false, ""), driver.UnitRun, pass, StatusFail, "run: Main panicked (exit 101)"},
		{"timeout", ok, res(true, true, -1, true, ""), driver.UnitRun, pass, StatusFail, "run: Main did not finish"},
		{"not started", ok, res(true, true, -1, false, ""), driver.UnitRun, pass, StatusFail, "run: Main could not be started"},
		{"panic while pending", ok, res(true, true, 101, false, ""), driver.UnitRun, pending, StatusFail, "run: Main panicked"},
		{"panic of a failing unit keeps its reason", ok, res(true, true, 101, false, ""), driver.UnitRun, failed, StatusFail, "ok block has diagnostics"},
		{"pass not built", ok, res(false, false, -1, false, ""), driver.UnitRun, pass, StatusFail, "build: the unit was not built"},
		{"UnitBuild pass not built", ok, res(false, false, -1, false, ""), driver.UnitBuild, pass, StatusFail, "build: the unit was not built"},
		{"pending not built", ok, res(false, false, -1, false, ""), driver.UnitRun, pending, StatusPending, "needs StageInstances"},
		{"failing not built", ok, res(false, false, -1, false, ""), driver.UnitRun, failed, StatusFail, "ok block has diagnostics"},
		{"reject units are never built", rej, res(false, true, 101, false, ""), driver.UnitRun, pass, StatusPass, ""},
	}
	for _, c := range cases {
		expectJ(t, c.name, JudgeBuild(c.u, c.r, c.mode, c.j), c.st, c.reason)
	}
	// Rule 2 needs a unit judged through flow: before flow the driver
	// builds nothing, and a pass there cannot be (rule 5 wants flow).
	r := res(false, false, -1, false, "")
	r.Through = diag.PhaseTypes
	expectJ(t, "pass through types, not built", JudgeBuild(ok, r, driver.UnitRun, pass), StatusPass, "")
}

// JudgeUnit rule 1b: an E0001 (spanless, from the build) fails the unit with
// its own reason, not rule 2's "diagnostic with no position".
func TestJudgeE0001HasItsOwnReason(t *testing.T) {
	f := newFixture(src)
	ok := block(VerdictOK, src, nil)
	e := diag.Toolchain("lld-link", "lld-link /out:x.exe a.o", "undefined symbol: Foo", "the link failed")
	expectJ(t, "E0001", Judge(ok, f.result(diag.PhaseFlow, e), driver.StdState{}, stagesNative), StatusFail, "E0001 (toolchain or file system, from the build): the link failed")
	ice := diag.FromInternal(&source.InternalError{Where: "x", What: "boom"})
	expectJ(t, "E0000 still first", Judge(ok, f.result(diag.PhaseFlow, e, ice), driver.StdState{}, stagesNative), StatusFail, "internal error")
}

// unitRoot writes a repository whose doc/reference.md holds one ok
// block with body at line 4 (key doc/reference.md:4) and the real
// reference's test prelude under its heading, so DriverCompiler reads it.
func unitRoot(t *testing.T, body string) string {
	t.Helper()
	prelude, err := Prelude(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	return writeRoot(t, "# Ref\n\n"+fence+"mk\n// verdict: ok\n"+body+fence+"\n\n"+
		PreludeHeading+"\n\n"+fence+"mk\n// verdict: parse\n"+prelude+fence+"\n")
}

// runUnitThroughCorpus runs the one ok block of unitRoot(body) through
// corpus.Run with --build and the embedded std, which must be judged through
// flow. It skips when LLVM is missing (fatal under MK_GATE=1).
func runUnitThroughCorpus(t *testing.T, body string) UnitResult {
	t.Helper()
	if _, err := toolchain.Discover(toolchain.Options{}); err != nil {
		if gateRun() {
			t.Fatalf("needs LLVM: %v", err)
		}
		t.Skipf("needs LLVM: %v", err)
	}
	root := unitRoot(t, body)
	r, err := Run(Options{Root: root, Only: "doc/reference.md:4", Build: true, Jobs: 1,
		Driver: driver.Config{CacheDir: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Units) != 1 {
		t.Fatalf("judged %d units, want 1", len(r.Units))
	}
	if r.Std.Through < diag.PhaseFlow {
		t.Fatalf("std judged through %s, want flow:\n%s", r.Std.Through, r.Std.Text)
	}
	return r.Units[0]
}

// An ok unit whose Main loops forever, judged with
// Build, fails within the 10 s timeout and does not hang.
func TestCorpusRunTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the 10 s run timeout")
	}
	start := time.Now()
	u := runUnitThroughCorpus(t, "export Main(sys: &System) {\n    var n := 0\n    for {\n        n = n ^ 1\n    }\n}\n")
	if !u.Built || !u.Ran {
		t.Errorf("Built %v Ran %v; want a built unit whose Main ran", u.Built, u.Ran)
	}
	if u.Status != StatusFail || !strings.HasPrefix(u.Reason, "run: Main did not finish") {
		t.Errorf("%s %q; want FAIL run: Main did not finish ...", u.Status, u.Reason)
	}
	if d := time.Since(start); d > 40*time.Second {
		t.Errorf("took %v; the run timeout is %s", d, runTimeout)
	}
}

// An ok unit whose Main panics fails with exit 101.
func TestCorpusRunPanicFails(t *testing.T) {
	u := runUnitThroughCorpus(t, "export Main(sys: &System): int {\n    big := 9223372036854775807\n    return big + sys.args.Len()\n}\n")
	if !u.Built || !u.Ran || u.RunStatus != 101 {
		t.Errorf("Built %v Ran %v RunStatus %d; want a run with status 101", u.Built, u.Ran, u.RunStatus)
	}
	if u.Status != StatusFail || u.Reason != "run: Main panicked (exit 101)" {
		t.Errorf("%s %q; want FAIL run: Main panicked (exit 101)", u.Status, u.Reason)
	}
}

// The same session passes an ok unit whose Main exits 0, so the two tests
// above fail for the run, not for the build.
func TestCorpusRunExitZeroPasses(t *testing.T) {
	u := runUnitThroughCorpus(t, "export Main(sys: &System) {\n}\n")
	if !u.Built || !u.Ran || u.RunStatus != 0 {
		t.Errorf("Built %v Ran %v RunStatus %d; want a run with status 0", u.Built, u.Ran, u.RunStatus)
	}
	if u.Status != StatusPass {
		t.Errorf("%s %q; want PASS", u.Status, u.Reason)
	}
}

// The report prints the build counts under --build only, in text and in
// the JSON summary object; without it both are as before.
func TestReportBuildCounts(t *testing.T) {
	u := BlockUnit(block(VerdictOK, src, nil))
	r := &Result{Units: []UnitResult{
		{Unit: u, Judgement: Judgement{Status: StatusPass}, Built: true, Ran: true},
		{Unit: u, Judgement: Judgement{Status: StatusPass}, Built: true, RunSkipped: "Acquire has no body"},
		{Unit: u, Judgement: Judgement{Status: StatusPending, Reason: "needs StageFlow"}},
	}}
	r.Summary = summarize(r.Units)
	for _, build := range []bool{false, true} {
		r.Build = build
		var text, js strings.Builder
		if err := WriteText(&text, r); err != nil {
			t.Fatal(err)
		}
		if err := WriteJSON(&js, r); err != nil {
			t.Fatal(err)
		}
		const line = "build: 2 units built, 1 Mains run, 1 built but not run\n"
		const obj = `"build":{"built":2,"ran":1,"run_skipped":1}`
		if strings.Contains(text.String(), line) != build || strings.Contains(js.String(), obj) != build {
			t.Errorf("Build %v: text has the line %v, JSON has the object %v\n%s%s", build,
				strings.Contains(text.String(), line), strings.Contains(js.String(), obj), text.String(), js.String())
		}
	}
}

// An E0000 or E0001 from the build part appears in Report like any other
// diagnostic: an ok unit built with --llvm naming
// a directory without clang reaches the judge with the driver's E0001 ("no
// usable LLVM toolchain", with the directories searched), so rule 1b names
// the cause. Needs no LLVM.
//
// The driver's CheckUnit keeps a build E0001 although its phase (native) is
// past Through (flow); internal/driver's TestCheckUnitKeepsBuildE0001
// pins that side. Should the E0001 be dropped again, this test fails.
func TestBuildE0001ReachesTheJudge(t *testing.T) {
	root := unitRoot(t, "F(): int {\n    return 1\n}\n")
	r, err := Run(Options{Root: root, Only: "doc/reference.md:4", Build: true, Jobs: 1,
		Driver: driver.Config{LLVMDir: t.TempDir(), CacheDir: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Units) != 1 {
		t.Fatalf("judged %d units, want 1", len(r.Units))
	}
	if r.Std.Through < diag.PhaseFlow {
		t.Fatalf("std judged through %s, want flow:\n%s", r.Std.Through, r.Std.Text)
	}
	u := r.Units[0]
	var e0001 int
	for _, g := range u.Emitted {
		if g.Code == diag.E0001 {
			e0001++
		}
	}
	if u.Status != StatusFail || !strings.HasPrefix(u.Reason, "E0001 (toolchain or file system, from the build): no usable LLVM toolchain") || e0001 != 1 || u.Built {
		t.Errorf("%s %q, %d E0001 emitted, Built %v; want FAIL by rule 1b naming the toolchain, one E0001, nothing built", u.Status, u.Reason, e0001, u.Built)
	}
}
