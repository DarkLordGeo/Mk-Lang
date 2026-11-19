// Why: the judge decides PASS, FAIL or PENDING for every block and unit, so
// each of its rules needs a test of its own.
// What: TestJudgeTable (rules 1 to 9 and 8b), rule 7b, NeedOf, every stage
// wired, the std gap, two-group codes and the placement-sensitive blocks.
// How: synthetic results on a small source map (fixture), plus real runs
// over doc/ for the placement blocks.

package corpus

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
)

var (
	stagesResolve = driver.StageSet(0).With(driver.StageManifest, driver.StageLoad, driver.StageParse, driver.StageResolve)
	stagesTypes   = stagesResolve.With(driver.StageSignatures, driver.StageBodies)
	stagesNative  = stagesTypes.With(driver.StageFlow, driver.StageInstances, driver.StageCodegen, driver.StageNative)
	stagesAll     = stagesNative.With(driver.StageGen, driver.StageIface)
)

// fixture is a source map holding one unit's block.mk and prelude.mk and a
// std file, as a corpus session's map does.
type fixture struct {
	sm                  *source.SourceMap
	block, prelude, std *source.File
	paths               map[source.FileID]string
}

func newFixture(src string) *fixture {
	f := &fixture{sm: source.NewSourceMap()}
	f.block = f.sm.Add("doc/t.md#3/block.mk", []byte(src))
	f.prelude = f.sm.Add("doc/t.md#3/prelude.mk", []byte("struct Point {\n    x: i32\n}\n"))
	f.std = f.sm.Add("std/core/core.mk", []byte("export Print(s: str) { }\n"))
	f.paths = map[source.FileID]string{f.block.ID: BlockFile, f.prelude.ID: PreludeFile}
	return f
}

// at is the span of the first occurrence of text on 1-based line of file.
func (f *fixture) at(file *source.File, line int, text string) source.Span {
	l := file.Line(line)
	i := strings.Index(l, text)
	if i < 0 {
		panic(fmt.Sprintf("%q not on line %d: %q", text, line, l))
	}
	start := file.LineStart(line) + uint32(i)
	return source.NewSpan(file.ID, start, start+uint32(len(text)))
}

func (f *fixture) result(through diag.Phase, ds ...*diag.Diagnostic) driver.BlockResult {
	return driver.BlockResult{Report: diag.Order(ds, f.sm, diag.NoCap), Sources: f.sm, Paths: f.paths, Through: through, RunStatus: -1}
}

// raw is a result whose report is ds as given: no phase rule, no sorting,
// as a driver that did not filter would return it.
func (f *fixture) raw(through diag.Phase, ds ...*diag.Diagnostic) driver.BlockResult {
	r := f.result(through)
	for i, d := range ds {
		r.Report.Diags = append(r.Report.Diags, diag.Emitted{Root: i + 1, Diag: d})
	}
	return r
}

func (f *fixture) d(code diag.Code, file *source.File, line int, text string) *diag.Diagnostic {
	return diag.New(code, f.at(file, line, text), "synthetic "+code.String())
}

func block(v Verdict, src string, codes []diag.Code, markers ...Marker) *Block {
	return &Block{Document: "doc/t.md", Line: 3, Verdict: v, Codes: codes, Source: src, Expected: markers}
}

const src = "F() {\n    a := Print(1)\n    b := 2\n}\n"

func expectJ(t *testing.T, name string, j Judgement, st Status, reason string) {
	t.Helper()
	if j.Status != st || !strings.HasPrefix(j.Reason, reason) {
		t.Errorf("%s: %s %q, want %s %q...", name, j.Status, j.Reason, st, reason)
	}
}

func TestJudgeTable(t *testing.T) {
	f := newFixture(src)
	none := driver.StdState{}
	rej := block(VerdictReject, src, []diag.Code{diag.E0501}, Marker{Line: 2, Code: diag.E0501})
	ok := block(VerdictOK, src, nil)
	parse := block(VerdictParse, src, nil)

	// Rule 1: an E0000 fails whatever the verdict.
	ice := diag.FromInternal(&source.InternalError{Where: "x", What: "boom"})
	for _, b := range []*Block{rej, ok, parse} {
		expectJ(t, "rule 1 "+b.Verdict.String(), Judge(b, f.result(diag.PhaseFlow, ice), none, stagesAll), StatusFail, "internal error")
	}
	// Rule 2: a diagnostic in the prelude or std is outside the unit.
	expectJ(t, "rule 2 prelude", Judge(ok, f.result(diag.PhaseFlow, f.d(diag.E0101, f.prelude, 2, "x")), none, stagesAll), StatusFail, "diagnostic outside the unit")
	expectJ(t, "rule 2 std", Judge(rej, f.result(diag.PhaseFlow, f.d(diag.E0501, f.std, 1, "Print")), none, stagesAll), StatusFail, "diagnostic outside the unit")
	// A diagnostic with no position fails.
	expectJ(t, "rule 2 no span", Judge(ok, f.result(diag.PhaseFlow, diag.New(diag.E0201, source.Span{}, "m")), none, stagesAll), StatusFail, "diagnostic with no position")
	// Rule 3: parse passes iff no parse-group diagnostic; never pending.
	expectJ(t, "rule 3 pass", Judge(parse, f.result(diag.PhaseParse), none, driver.StageSet(0)), StatusPass, "")
	expectJ(t, "rule 3 fail", Judge(parse, f.result(diag.PhaseParse, f.d(diag.E0101, f.block, 3, "b")), none, stagesResolve), StatusFail, "does not parse")
	// Rule 4: the exact markers pass, even with nothing wired.
	expectJ(t, "rule 4", Judge(rej, f.result(diag.PhaseParse, f.d(diag.E0501, f.block, 2, "a")), none, driver.StageSet(0)), StatusPass, "")
	// Rule 5: ok, nothing emitted, every need wired and judged through flow.
	expectJ(t, "rule 5", Judge(ok, f.result(diag.PhaseFlow), none, stagesNative), StatusPass, "")
	// Rule 8: nothing emitted, a stage missing.
	expectJ(t, "rule 8 ok", Judge(ok, f.result(diag.PhaseNames), none, stagesResolve), StatusPending, "needs StageSignatures")
	expectJ(t, "rule 8 reject", Judge(rej, f.result(diag.PhaseTypes), none, stagesTypes), StatusPending, "needs StageFlow")
	// Rule 9: wrong line, extra code, missing code, earlier phase, ok with diagnostics.
	expectJ(t, "rule 9 wrong line", Judge(rej, f.result(diag.PhaseFlow, f.d(diag.E0501, f.block, 3, "b")), none, stagesNative), StatusFail, "expected [block.mk:2 E0501], emitted [block.mk:3 E0501")
	expectJ(t, "rule 9 extra", Judge(rej, f.result(diag.PhaseFlow, f.d(diag.E0501, f.block, 2, "a"), f.d(diag.E0502, f.block, 3, "b")), none, stagesNative), StatusFail, "expected")
	expectJ(t, "rule 9 earlier phase", Judge(rej, f.result(diag.PhaseFlow, f.d(diag.E0301, f.block, 2, "a")), none, stagesNative), StatusFail, "expected")
	expectJ(t, "rule 9 nothing", Judge(rej, f.result(diag.PhaseFlow), none, stagesNative), StatusFail, "expected [block.mk:2 E0501], emitted nothing")
	expectJ(t, "rule 9 ok", Judge(ok, f.result(diag.PhaseFlow, f.d(diag.E0301, f.block, 2, "a")), none, stagesNative), StatusFail, "ok block has diagnostics")
	// Pending is never reached when an emitted diagnostic is trustworthy:
	// an ok block with a parse error, with only parse and resolve wired, fails.
	expectJ(t, "rule 9 ok through resolve", Judge(ok, f.result(diag.PhaseParse, f.d(diag.E0101, f.block, 2, "a")), none, stagesResolve), StatusFail, "ok block has diagnostics")
	// Rule 4 on two codes of one line and a set comparison (duplicates).
	two := block(VerdictReject, src, []diag.Code{diag.E0501, diag.E0502}, Marker{Line: 2, Code: diag.E0501}, Marker{Line: 2, Code: diag.E0502})
	expectJ(t, "rule 4 two codes", Judge(two, f.result(diag.PhaseFlow, f.d(diag.E0502, f.block, 2, "a"), f.d(diag.E0501, f.block, 2, "Print")), none, stagesNative), StatusPass, "")
}

// Rule 8b: the need is wired, but the driver ran nothing past Through
// because std is missing.
func TestJudgeThroughBelowNeedIsPending(t *testing.T) {
	f := newFixture(src)
	std := driver.StdState{Missing: []string{"Print", "System"}}
	// A names code (E0201; E0609 is a types and flow code).
	above := block(VerdictReject, src, []diag.Code{diag.E0201}, Marker{Line: 2, Code: diag.E0201})
	expectJ(t, "E0201 through parse", Judge(above, f.result(diag.PhaseParse), std, stagesResolve), StatusPending, "std: judged through parse only (2 std names undeclared, first Print)")
	expectJ(t, "E0201 through names", Judge(above, f.result(diag.PhaseNames), std, stagesResolve), StatusFail, "expected")
	// An E01xx reject block is judged through parse.
	e0101 := block(VerdictReject, src, []diag.Code{diag.E0101}, Marker{Line: 3, Code: diag.E0101})
	expectJ(t, "E0101 through parse", Judge(e0101, f.result(diag.PhaseParse), std, stagesResolve), StatusFail, "expected")
	expectJ(t, "E0101 pass", Judge(e0101, f.result(diag.PhaseParse, f.d(diag.E0101, f.block, 3, "b")), std, stagesResolve), StatusPass, "")
}

// NeedOf for every catalogue code, and the judgements with the types
// stages wired and flow not.
func TestJudgeNeedIsTheEmittingStage(t *testing.T) {
	for _, e := range diag.Catalogue() {
		c := e.Code
		got := NeedOf(c)
		var want driver.StageSet
		switch c {
		case diag.E0001:
			want = allStages()
		case diag.E0708:
			want = phaseStages(diag.PhaseTypes).With(driver.StageInstances)
		case diag.E0903, diag.E0905, diag.E0906, diag.E0910: // E0910 from Gen.Input too
			want = phaseStages(diag.PhaseTypes).With(driver.StageGen)
		default:
			want = phaseStages(GroupOf(c))
		}
		if got != want || !got.Has(driver.StageLoad) || !got.Has(driver.StageParse) {
			t.Errorf("NeedOf(%s) = %v, want %v", c, got.Stages(), want.Stages())
		}
		if got.Has(driver.StageGen) != (c == diag.E0903 || c == diag.E0905 || c == diag.E0906 || c == diag.E0910 || c == diag.E0001) {
			t.Errorf("NeedOf(%s) has StageGen = %v", c, got.Has(driver.StageGen))
		}
	}
	f := newFixture(src)
	none := driver.StdState{}
	for _, c := range []diag.Code{diag.E0903, diag.E0905, diag.E0906, diag.E0910} {
		b := block(VerdictReject, src, []diag.Code{c}, Marker{Line: 2, Code: c})
		expectJ(t, c.String(), Judge(b, f.result(diag.PhaseTypes), none, stagesTypes), StatusPending, "needs StageGen")
	}
	e0708 := block(VerdictReject, src, []diag.Code{diag.E0708}, Marker{Line: 2, Code: diag.E0708})
	expectJ(t, "E0708", Judge(e0708, f.result(diag.PhaseTypes), none, stagesTypes), StatusPending, "needs StageInstances")
	e0301 := block(VerdictReject, src, []diag.Code{diag.E0301}, Marker{Line: 2, Code: diag.E0301})
	expectJ(t, "E0301", Judge(e0301, f.result(diag.PhaseTypes), none, stagesTypes), StatusFail, "expected")
	ok := block(VerdictOK, src, nil)
	expectJ(t, "ok through types", Judge(ok, f.result(diag.PhaseTypes), none, stagesTypes), StatusPending, "needs StageFlow")
	expectJ(t, "ok through native", Judge(ok, f.result(diag.PhaseFlow), none, stagesNative), StatusPass, "")
	r := f.result(diag.PhaseFlow)
	r.Skipped = driver.StageSet(0).With(driver.StageGen)
	expectJ(t, "ok through native skipped gen", Judge(ok, r, none, stagesNative), StatusPending, "needs StageGen")
}

// No rule compares a phase by equality; every stage wired passes an ok
// block whatever the stage set holds beyond its need.
func TestJudgeOKWithEveryStage(t *testing.T) {
	f := newFixture(src)
	ok := block(VerdictOK, src, nil)
	expectJ(t, "every stage", Judge(ok, f.result(diag.PhaseFlow), driver.StdState{}, allStages()), StatusPass, "")
	expectJ(t, "every stage, through native", Judge(ok, f.result(diag.PhaseNative), driver.StdState{}, allStages()), StatusPass, "")
}

// A gap in std makes a block PENDING, not FAIL.
func TestJudgeStdGapIsPending(t *testing.T) {
	f := newFixture(src)
	ok := block(VerdictOK, src, nil)
	e0201 := f.d(diag.E0201, f.block, 2, "Print")
	gap := driver.StdState{Missing: []string{"Print"}}
	expectJ(t, "Print missing", Judge(ok, f.result(diag.PhaseNames, e0201), gap, stagesResolve), StatusPending, "std: Print undeclared")
	expectJ(t, "Print declared", Judge(ok, f.result(diag.PhaseNames, e0201), driver.StdState{}, stagesResolve), StatusFail, "ok block has diagnostics")
	// A missing std module gives E0202 on its import line.
	imp := newFixture("import std.fs\nF() { }\n")
	e0202 := diag.New(diag.E0202, imp.at(imp.block, 1, "std"), "no module")
	expectJ(t, "std.fs missing", Judge(block(VerdictOK, "import std.fs\nF() { }\n", nil), imp.result(diag.PhaseImports, e0202),
		driver.StdState{Missing: []string{"std.fs"}}, stagesResolve), StatusPending, "std: std.fs undeclared")
	// Counter and Racy use Cell.
	cell := newFixture("var Counter: Cell[i32] = 0 // <- E1002\n")
	cb := block(VerdictReject, "var Counter: Cell[i32] = 0 // <- E1002\n", []diag.Code{diag.E1002}, Marker{Line: 1, Code: diag.E1002})
	expectJ(t, "Counter", Judge(cb, cell.result(diag.PhaseNames, cell.d(diag.E0201, cell.block, 1, "Cell")),
		driver.StdState{Missing: []string{"Cell"}}, stagesTypes), StatusPending, "std: Cell undeclared")
	// Std's own report holding a diagnostic of a group the block needs.
	broken := driver.StdState{Report: diag.Order([]*diag.Diagnostic{f.d(diag.E0201, f.std, 1, "Print")}, f.sm, diag.NoCap)}
	expectJ(t, "std broken", Judge(ok, f.result(diag.PhaseParse), broken, stagesNative), StatusPending, "std: E0201 std/core/core.mk:1:8")
	// A reject block whose exact markers are emitted passes even then.
	rej := block(VerdictReject, src, []diag.Code{diag.E0501}, Marker{Line: 2, Code: diag.E0501})
	expectJ(t, "std broken, reject matches", Judge(rej, f.result(diag.PhaseFlow, f.d(diag.E0501, f.block, 2, "a")), broken, stagesNative), StatusPass, "")
	// A parse-group std error does not concern a parse block.
	expectJ(t, "std broken, parse block", Judge(block(VerdictParse, src, nil), f.result(diag.PhaseParse), broken, stagesResolve), StatusPass, "")
}

// A code emitted in two groups waits for the later one.
func TestJudgeTwoGroupCodeTakesLaterGroup(t *testing.T) {
	// The group each multi-group code of diag.GroupForms waits for, pinned
	// as the policy stood before the table moved to diag, so a change
	// of the contract's rows shows here instead of silently moving a need.
	want := map[diag.Code]diag.Phase{
		diag.E0101: diag.PhaseParse, diag.E0204: diag.PhaseTypes, diag.E0206: diag.PhaseNames,
		diag.E0207: diag.PhaseTypes, diag.E0406: diag.PhaseFlow, diag.E0507: diag.PhaseFlow, diag.E0609: diag.PhaseFlow,
		diag.E0905: diag.PhaseTypes, diag.E1002: diag.PhaseFlow, diag.E1102: diag.PhaseFlow,
		diag.E1204: diag.PhaseTypes, diag.E1221: diag.PhaseTypes,
	}
	for _, f := range diag.GroupForms() {
		if !EmittedIn(f.Code, f.Phase) {
			t.Errorf("%s: EmittedIn(%s) is false for its GroupForms row %q", f.Code, f.Phase, f.Form)
		}
		if _, ok := want[f.Code]; !ok {
			t.Errorf("%s: a multi-group code with no pinned group here; decide which group it waits for", f.Code)
		}
	}
	for c, g := range want {
		if !diag.MultiGroup(c) {
			t.Errorf("%s: pinned here but has no GroupForms row", c)
		}
		if GroupOf(c) != g || NeedOf(c)&phaseStages(g) != phaseStages(g) {
			t.Errorf("%s: GroupOf %s, NeedOf %v; want the %s group's stages", c, GroupOf(c), NeedOf(c).Stages(), g)
		}
		if g < diag.DefaultPhase(c) {
			t.Errorf("%s: later group %s is earlier than the default %s", c, g, diag.DefaultPhase(c))
		}
		if TwoGroup(c) != (g != diag.DefaultPhase(c)) {
			t.Errorf("%s: TwoGroup %v with group %s and default %s", c, TwoGroup(c), g, diag.DefaultPhase(c))
		}
	}
	// E0409 has one group, flow (its var-global form included).
	if diag.MultiGroup(diag.E0409) || EmittedIn(diag.E0409, diag.PhaseTypes) || GroupOf(diag.E0409) != diag.PhaseFlow {
		t.Errorf("E0409: MultiGroup %v, EmittedIn(types) %v, GroupOf %s; want false, false, flow",
			diag.MultiGroup(diag.E0409), EmittedIn(diag.E0409, diag.PhaseTypes), GroupOf(diag.E0409))
	}
	f := newFixture(src)
	none := driver.StdState{}
	unit := func(needs diag.Phase) *Unit {
		u := BlockUnit(block(VerdictReject, src, []diag.Code{diag.E0204}, Marker{Line: 2, Code: diag.E0204}))
		u.Meta.Needs = needs
		return u
	}
	expectJ(t, "no header", JudgeUnit(unit(diag.PhaseInternal), f.result(diag.PhaseNames), none, stagesResolve), StatusPending, "needs StageSignatures")
	expectJ(t, "needs types", JudgeUnit(unit(diag.PhaseTypes), f.result(diag.PhaseNames), none, stagesResolve), StatusPending, "needs StageSignatures")
	expectJ(t, "needs names", JudgeUnit(unit(diag.PhaseNames), f.result(diag.PhaseNames), none, stagesResolve), StatusFail, "expected")
	expectJ(t, "emitted in G3", JudgeUnit(unit(diag.PhaseInternal), f.result(diag.PhaseNames, f.d(diag.E0204, f.block, 2, "a")), none, stagesResolve), StatusPass, "")
	if _, _, err := parseVerdict("// verdict: reject E0204 E0301"); err != nil {
		t.Errorf("a verdict naming codes of two groups was refused: %v", err)
	}
}

// "// needs: names" on a resolve-form E0101 (a second emitter GroupOf leaves
// out) is honoured, so an empty report judged through parse is PENDING (rule
// 8b), not FAIL; without the header the parse group applies and the same
// report is FAIL.
func TestNeedsNamesOnE0101IsHonoured(t *testing.T) {
	if !EmittedIn(diag.E0101, diag.PhaseNames) || TwoGroup(diag.E0101) || GroupOf(diag.E0101) != diag.PhaseParse {
		t.Fatalf("E0101: EmittedIn(names) %v, TwoGroup %v, GroupOf %s; want true, false, parse",
			EmittedIn(diag.E0101, diag.PhaseNames), TwoGroup(diag.E0101), GroupOf(diag.E0101))
	}
	f := newFixture(src)
	unit := func(needs diag.Phase) *Unit {
		u := BlockUnit(block(VerdictReject, src, []diag.Code{diag.E0101}, Marker{Line: 2, Code: diag.E0101}))
		u.Meta.Needs = needs
		return u
	}
	none := driver.StdState{}
	expectJ(t, "needs names, through parse", JudgeUnit(unit(diag.PhaseNames), f.result(diag.PhaseParse), none, stagesResolve), StatusPending, "std: judged through parse")
	expectJ(t, "needs names, before resolve", JudgeUnit(unit(diag.PhaseNames), f.result(diag.PhaseParse), none, driver.StageSet(0).With(driver.StageLoad, driver.StageParse)), StatusPending, "needs StageResolve")
	expectJ(t, "needs names, through names", JudgeUnit(unit(diag.PhaseNames), f.result(diag.PhaseNames), none, stagesResolve), StatusFail, "expected")
	expectJ(t, "no header", JudgeUnit(unit(diag.PhaseInternal), f.result(diag.PhaseParse), none, stagesResolve), StatusFail, "expected")
	// Every code's own groups are emitting groups; a code with one emitter has
	// no other.
	for _, e := range diag.Catalogue() {
		c := e.Code
		if !EmittedIn(c, diag.DefaultPhase(c)) || !EmittedIn(c, GroupOf(c)) {
			t.Errorf("%s: its default or later group is not an emitting group", c)
		}
	}
	if EmittedIn(diag.E0501, diag.PhaseNames) || EmittedIn(diag.E0301, diag.PhaseParse) {
		t.Errorf("a one-emitter code reports a second group")
	}
}

// placementBlocks is the table of placement-sensitive blocks: blocks found by
// their column-0 name, with the verdict each must have.
var placementBlocks = []struct {
	doc, name string
	code      diag.Code
}{
	{"doc/reference.md", "Biggest", diag.E0703},
	{"doc/reference.md", "Above", diag.E0609},
	{"doc/reference.md", "Register", diag.E0411},
	{"doc/memory.md", "Counter", diag.E1002},
	{"doc/memory.md", "Racy", diag.E1002},
	{"doc/memory.md", "Same", diag.E0501},
}

// usesMissing reports whether u's source names one of std's missing names
// or modules as a whole word: then std does not yet declare what u uses.
func usesMissing(u *Unit, missing []string) bool {
	for _, n := range missing {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(n) + `\b`)
		for _, f := range u.Files {
			if re.MatchString(f.Text) {
				return true
			}
		}
	}
	return false
}

// checkPlacementBlocks is part (a) of TestPlacementSensitiveBlocks over
// one run: each block of placementBlocks exists once with its verdict; once
// every stage of its need is wired, the run judged through its group, std
// holds no diagnostic of a group it needs (rule 6) and std declares what it
// uses, it is PASS and its emitted (file, line, code) set equals its markers;
// before that it is PENDING with a needs or std: reason, never FAIL. It
// returns the names judged by the strict branch.
func checkPlacementBlocks(t *testing.T, r *Result, label string) []string {
	t.Helper()
	var strict []string
	for _, p := range placementBlocks {
		re := regexp.MustCompile(`(?m)^(var\s+)?` + p.name + `\b`)
		var found []UnitResult
		for _, u := range r.Units {
			if u.Unit.Block != nil && u.Unit.Block.Document == p.doc && re.MatchString(u.Unit.Block.Source) {
				found = append(found, u)
			}
		}
		if len(found) != 1 {
			t.Errorf("%s: %s in %s: %d blocks, want 1", label, p.name, p.doc, len(found))
			continue
		}
		u := found[0]
		if u.Unit.Verdict != VerdictReject || len(u.Unit.Codes) != 1 || u.Unit.Codes[0] != p.code {
			t.Errorf("%s: %s (%s): verdict %s, want reject %s", label, p.name, u.Unit.Key, verdictText(u.Unit), p.code)
		}
		need, group := unitNeed(u.Unit)
		_, broken := stdBroken(r.Std.State, nil, group)
		judged := need&r.Avail == need && group <= r.Std.Through && !broken &&
			!usesMissing(u.Unit, r.Std.State.Missing)
		switch {
		case judged:
			strict = append(strict, p.name)
			var emitted []UnitMarker
			for _, g := range u.Emitted {
				emitted = append(emitted, UnitMarker{File: g.File, Line: g.Line, Code: g.Code})
			}
			if u.Status != StatusPass || !slices.Equal(sortMarkers(emitted), u.Unit.Expected) {
				t.Errorf("%s: %s (%s) is judged (need wired, through %s, std declares what it uses): %s %q, emitted %v, want PASS with exactly %v",
					label, p.name, u.Unit.Key, r.Std.Through, u.Status, u.Reason, emitted, u.Unit.Expected)
			}
		case u.Status == StatusFail:
			t.Errorf("%s: %s (%s) FAIL: %s", label, p.name, u.Unit.Key, u.Reason)
		case u.Status == StatusPending:
			if !strings.HasPrefix(u.Reason, "needs ") && !strings.HasPrefix(u.Reason, "std: ") {
				t.Errorf("%s: %s pending reason %q", label, p.name, u.Reason)
			}
		}
		t.Logf("%s: %s %s: %s %s", label, p.name, u.Unit.Key, u.Status, u.Reason)
	}
	return strict
}

// (a) Over the real doc/, each block exists once with its
// verdict and is PENDING with a reason until its need and std land, never
// FAIL; once judged it is PASS with exactly its markers, checked with the
// embedded std and with the stand-in std of names_test.go. (b) The judge
// does not re-filter.
func TestPlacementSensitiveBlocks(t *testing.T) {
	checkPlacementBlocks(t, designRun(t), "embedded std")
	// The stand-in std with its planted types error: units are judged
	// through names, so the types and flow blocks are PENDING std: (the
	// member-less stand-in cannot judge them).
	stand, err := Run(Options{Driver: driver.Config{StdDir: standInStdNames(t, repoRoot(t))}})
	if err != nil {
		t.Fatal(err)
	}
	checkPlanted(t, stand)
	strict := checkPlacementBlocks(t, stand, "stand-in std")
	// Above's E0609 waits for flow and no placement block is a names code,
	// so the strict branch has no witness under the stand-in std (the
	// switch below reports one if a names block is added).
	namesBlock := false
	for _, b := range placementBlocks {
		namesBlock = namesBlock || GroupOf(b.code) <= diag.PhaseNames
	}
	switch {
	case namesBlock && driver.Available()&stagesResolve == stagesResolve && len(strict) == 0:
		t.Errorf("stand-in std: no placement block is judged by the strict branch; the PASS requirement is not exercised")
	case !namesBlock:
		t.Logf("stand-in std: no placement block is a names code, so the strict branch is exercised only with the embedded std; judged strictly: %v", strict)
	}
	// (b) Synthetic: the judge keeps what the driver reports.
	f := newFixture(src)
	above := block(VerdictReject, src, []diag.Code{diag.E0609}, Marker{Line: 2, Code: diag.E0609})
	expectJ(t, "Above with E0705", Judge(above, f.raw(diag.PhaseTypes, f.d(diag.E0609, f.block, 2, "a"), f.d(diag.E0705, f.block, 2, "Print")), driver.StdState{}, stagesTypes), StatusFail, "expected")
	racy := block(VerdictReject, src, []diag.Code{diag.E1002}, Marker{Line: 2, Code: diag.E1002})
	expectJ(t, "Racy with E1001", Judge(racy, f.raw(diag.PhaseFlow, f.d(diag.E1002, f.block, 2, "a"), f.d(diag.E1001, f.block, 2, "Print")), driver.StdState{}, stagesNative), StatusFail, "expected")
}

// Rule 7b (a missing stage is PENDING, never FAIL; seen on codes/E0903,
// E0905, E0906): a unit whose Skipped holds a stage avail lacks is PENDING
// needs that stage whatever its report holds, because the report is not a
// whole compile; rules 1, 2 and 4 still decide first, and once the stage is
// wired the rule is gone.
func TestJudgeSkippedStageIsPendingWhateverE(t *testing.T) {
	f := newFixture(src)
	none := driver.StdState{}
	gen := driver.StageSet(0).With(driver.StageGen)
	withSkipped := func(r driver.BlockResult, s driver.StageSet) driver.BlockResult {
		r.Skipped = s
		return r
	}
	e0906 := block(VerdictReject, src, []diag.Code{diag.E0906}, Marker{Line: 2, Code: diag.E0906})
	e0501 := block(VerdictReject, src, []diag.Code{diag.E0501}, Marker{Line: 2, Code: diag.E0501})
	e0201 := block(VerdictReject, src, []diag.Code{diag.E0201}, Marker{Line: 3, Code: diag.E0201})
	ok := block(VerdictOK, src, nil)
	unknown := func() *diag.Diagnostic { return f.d(diag.E0201, f.block, 3, "b") }
	cases := []struct {
		name   string
		b      *Block
		r      driver.BlockResult
		avail  driver.StageSet
		st     Status
		reason string
	}{
		{"E0906, gen stubbed, through types", e0906, withSkipped(f.result(diag.PhaseTypes, unknown()), gen), stagesTypes, StatusPending, "needs StageGen"},
		{"E0906, gen stubbed, through native", e0906, withSkipped(f.result(diag.PhaseFlow, unknown()), gen), stagesNative, StatusPending, "needs StageGen"},
		{"E0501, gen stubbed, through types", e0501, withSkipped(f.result(diag.PhaseTypes, unknown()), gen), stagesTypes, StatusPending, "needs StageGen"},
		{"ok, gen stubbed, through native", ok, withSkipped(f.result(diag.PhaseFlow, unknown()), gen), stagesNative, StatusPending, "needs StageGen"},
		// Nothing skipped: the report is a whole compile through Through.
		{"E0906, nothing skipped, through types", e0906, f.result(diag.PhaseTypes, unknown()), stagesTypes, StatusFail, "expected"},
		{"ok, nothing skipped, through native", ok, f.result(diag.PhaseFlow, unknown()), stagesNative, StatusFail, "ok block has diagnostics"},
		// The skipped stage is wired: rule 7b does not apply.
		{"E0906, gen wired, all stages", e0906, withSkipped(f.result(diag.PhaseFlow, unknown()), gen), stagesAll, StatusFail, "expected"},
		// Rules 1 and 2 come first.
		{"E0000, gen stubbed", e0906, withSkipped(f.result(diag.PhaseTypes, diag.New(diag.E0000, f.at(f.block, 2, "Print"), "boom")), gen), stagesTypes, StatusFail, "internal error"},
		{"outside the unit, gen stubbed", e0906, withSkipped(f.result(diag.PhaseTypes, f.d(diag.E0201, f.std, 1, "Print")), gen), stagesTypes, StatusFail, "diagnostic outside the unit"},
		// Rule 4 does not: matching markers with a stubbed stage may be the
		// stub's doing (TestStubbedStageNeverPasses).
		{"markers match, gen stubbed", e0201, withSkipped(f.result(diag.PhaseTypes, unknown()), gen), stagesTypes, StatusPending, "needs StageGen"},
		{"markers match, gen wired", e0201, withSkipped(f.result(diag.PhaseFlow, unknown()), gen), stagesAll, StatusPass, ""},
	}
	for _, c := range cases {
		expectJ(t, c.name, Judge(c.b, c.r, none, c.avail), c.st, c.reason)
	}
}

// A missing stage is PENDING, never FAIL or PASS: a reject unit whose
// markers match while the driver stubbed a stage the unit entered is
// PENDING "needs <stage>", because the match may come from the stub (an
// E0201 on a member the gen line would have declared). Rule 4 passes it
// again once the stage is wired.
func TestStubbedStageNeverPasses(t *testing.T) {
	f := newFixture(src)
	e0201 := block(VerdictReject, src, []diag.Code{diag.E0201}, Marker{Line: 3, Code: diag.E0201})
	r := f.result(diag.PhaseFlow, f.d(diag.E0201, f.block, 3, "b"))
	r.Skipped = driver.StageSet(0).With(driver.StageGen)
	expectJ(t, "through native, gen stubbed", Judge(e0201, r, driver.StdState{}, stagesNative), StatusPending, "needs StageGen")
	expectJ(t, "all stages, gen wired", Judge(e0201, r, driver.StdState{}, stagesAll), StatusPass, "")
	r.Skipped = 0
	expectJ(t, "through native, nothing stubbed", Judge(e0201, r, driver.StdState{}, stagesNative), StatusPass, "")
}

// Rule 1 fails on E0000 only: a reject unit whose verdict names E0001 and whose
// marked E0001 is reported on its line passes by rule 4. Rule 1b still
// fails an E0001 in any other unit, and a reject E0001 unit whose E0001 is
// spanless still fails (rule 2), as does one with the wrong line (rule 9).
func TestMarkedE0001Passes(t *testing.T) {
	f := newFixture(src)
	none := driver.StdState{}
	e0001 := block(VerdictReject, src, []diag.Code{diag.E0001}, Marker{Line: 2, Code: diag.E0001})
	at2 := diag.New(diag.E0001, f.at(f.block, 2, "Print"), "no body")
	expectJ(t, "marked", Judge(e0001, f.result(diag.PhaseFlow, at2), none, allStages()), StatusPass, "")
	at3 := diag.New(diag.E0001, f.at(f.block, 3, "b"), "no body")
	expectJ(t, "wrong line", Judge(e0001, f.result(diag.PhaseFlow, at3), none, allStages()), StatusFail, "expected [block.mk:2 E0001]")
	spanless := diag.New(diag.E0001, source.Span{}, "the link failed")
	expectJ(t, "spanless", Judge(e0001, f.result(diag.PhaseFlow, spanless), none, allStages()), StatusFail, "diagnostic with no position")
	e0501 := block(VerdictReject, src, []diag.Code{diag.E0501}, Marker{Line: 2, Code: diag.E0501})
	expectJ(t, "other reject", Judge(e0501, f.result(diag.PhaseFlow, at2), none, allStages()), StatusFail, "E0001 (toolchain or file system, from the build)")
	expectJ(t, "ok", Judge(block(VerdictOK, src, nil), f.result(diag.PhaseFlow, at2), none, allStages()), StatusFail, "E0001 (toolchain or file system, from the build)")
}
