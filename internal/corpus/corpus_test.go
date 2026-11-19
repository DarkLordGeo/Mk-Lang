// Why: every fenced mk block in doc/ is a test, and the corpus runner that
// judges them must itself be right.
// What: TestCorpus (one subtest per block), the std seam, the report and JSON
// Lines shapes, determinism, the pending allowlist and the driver adapter.
// How: runs over the real doc/ and the real driver, shared between tests
// through designRun and fullRun.

package corpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"mk/internal/diag"
	"mk/internal/driver"
)

var (
	designOnce sync.Once
	designRes  *Result
	designErr  error
)

// designRun is one corpus run over doc/ with the embedded std, shared by
// the tests that only read it.
func designRun(t *testing.T) *Result {
	t.Helper()
	designOnce.Do(func() { designRes, designErr = Run(Options{}) })
	if designErr != nil {
		t.Fatalf("Run: %v", designErr)
	}
	return designRes
}

var (
	fullOnce sync.Once
	fullRes  *Result
	fullErr  error
)

// fullRun is one run over doc/ and every conformance unit with the
// embedded std (mk corpus --all), shared by the tests that only read it.
func fullRun(t *testing.T) *Result {
	t.Helper()
	fullOnce.Do(func() { fullRes, fullErr = Run(Options{Conformance: true}) })
	if fullErr != nil {
		t.Fatalf("Run: %v", fullErr)
	}
	return fullRes
}

// knownDesignConflicts are doc blocks that fail because the document is
// wrong, each waiting for the edit that fixes it.
// A block is found by its document and a line of its source that only it
// holds (line numbers move with edits). The conflict shows once units are
// judged through phase: TestCorpus then requires the FAIL with that code and
// fails when the block stops failing, so the entry goes with the edit.
var knownDesignConflicts = []struct {
	doc, line string
	code      diag.Code
	phase     diag.Phase
}{}

// checkE0101Lines are lines, each of one doc block, whose E0101 is check's
// (a contract's form), not the parser's. The judge waits for parse alone
// before an E0101 is missing (groupPolicy), so a run through parse or names
// reports these blocks FAIL; through types they pass.
var checkE0101Lines = []struct{ doc, line string }{
	{"doc/reference.md", "    requires Now(io) > 0 // <- E0101"},
}

// checkE0101 reports whether u is a checkE0101Lines block and the run stops
// before types, where its E0101 is emitted.
func checkE0101(u *Unit, through diag.Phase) bool {
	if through >= diag.PhaseTypes || u.Block == nil {
		return false
	}
	for _, c := range checkE0101Lines {
		if u.Block.Document == c.doc && strings.Contains("\n"+u.Block.Source, "\n"+c.line+"\n") {
			return true
		}
	}
	return false
}

// knownConflict returns the index of u's known doc conflict that shows
// when judging through phase through, or -1.
func knownConflict(u *Unit, through diag.Phase) int {
	for i, c := range knownDesignConflicts {
		if u.Block != nil && u.Block.Document == c.doc && c.phase <= through &&
			strings.Contains("\n"+u.Block.Source, "\n"+c.line+"\n") {
			return i
		}
	}
	return -1
}

// checkConflicts handles u if it is a known conflict at through: it must
// FAIL with the conflict's code. It records the match in seen.
func checkConflict(t *testing.T, u UnitResult, through diag.Phase, seen map[int]int) bool {
	t.Helper()
	i := knownConflict(u.Unit, through)
	if i < 0 {
		return false
	}
	seen[i]++
	c := knownDesignConflicts[i]
	if u.Status != StatusFail || !strings.Contains(u.Reason, c.code.String()) {
		t.Errorf("known design conflict %q no longer fails with %s (%s %s): remove it", c.line, c.code, u.Status, u.Reason)
	} else {
		t.Logf("known design conflict, FAIL as expected: %s", u.Reason)
	}
	return true
}

// checkConflictsSeen requires each conflict showing at through to match
// exactly one block.
func checkConflictsSeen(t *testing.T, through diag.Phase, seen map[int]int) {
	t.Helper()
	for i, c := range knownDesignConflicts {
		if c.phase <= through && seen[i] != 1 {
			t.Errorf("known design conflict %q matched %d blocks, want 1", c.line, seen[i])
		}
	}
}

// TestCorpus judges every doc block through the stages this build wires.
// While Available() stops at resolve: every block that must parse parses; no
// block fails; parse blocks pass; every other block is PENDING with a reason.
// Once a later stage is wired, the resolve-only expectation (pending) stops
// applying and only "no FAIL" remains.
// The doc corpus is judged with NoPending, so a PENDING block that
// pending.txt does not explain is FAIL "unexplained pending: ..." and fails
// its subtest. It is a run of its own, not designRun, because
// TestRunZeroOptionsJudgesDocs and TestLinuxNativeMk need the zero
// Options' report.
func TestCorpus(t *testing.T) {
	r, err := Run(Options{NoPending: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	m1 := !driver.Available().Has(driver.StageSignatures)
	seen := map[int]int{}
	mustParseN := 0
	for _, u := range r.Units {
		t.Run(u.Unit.Key, func(t *testing.T) {
			if checkConflict(t, u, r.Std.Through, seen) {
				return
			}
			if mustParse(u) {
				mustParseN++
			}
			for _, p := range corpusUnitProblems(u, m1) {
				t.Error(p)
			}
			checkStage6(t, u, r)
		})
	}
	checkConflictsSeen(t, r.Std.Through, seen)
	s := r.Summary
	t.Logf("%d blocks: %d ok, %d parse, %d reject; parsed %d of %d (%d must parse); pass %d, fail %d, pending %d (listed %d); std through %s, %d names and modules undeclared",
		s.Blocks, s.OK, s.Parse, s.Reject, s.Parsed, s.Blocks, mustParseN, s.Pass, s.Fail, s.Pending, s.Explained, r.Std.Through, len(r.Std.State.Missing))
	if s.Blocks <= 100 || s.Parsed < mustParseN {
		t.Errorf("parsed %d of %d blocks, %d must parse", s.Parsed, s.Blocks, mustParseN)
	}
}

// corpusUnitProblems is TestCorpus's per-block check (known conflicts
// aside): no FAIL; every PENDING has a reason; a parse block passes; a block
// that must parse (mustParse) parses; with only parse and resolve wired (m1)
// every block but a parse block is PENDING.
func corpusUnitProblems(u UnitResult, m1 bool) []string {
	var out []string
	if u.Status == StatusFail {
		out = append(out, "FAIL: "+u.Reason)
	}
	if u.Status == StatusPending && u.Reason == "" {
		out = append(out, "PENDING without a reason")
	}
	if u.Unit.Verdict == VerdictParse && u.Status != StatusPass {
		out = append(out, fmt.Sprintf("parse block %s: %s", u.Status, u.Reason))
	}
	if mustParse(u) && !u.Parsed {
		out = append(out, "block does not parse: "+describeAll(u.Emitted))
	}
	if m1 && u.Unit.Verdict != VerdictParse && u.Status != StatusPending {
		out = append(out, fmt.Sprintf("through resolve: %s block is %s, want PENDING", u.Unit.Verdict, u.Status))
	}
	return out
}

// mustParse reports whether a block's report must hold no parse-group
// diagnostic (the report's "parsed"). It holds for every ok and parse block,
// and for a reject block unless one of its verdict codes is emitted in the
// parse group (E01xx, E12xx, E0609's closure form) and every
// parse-group diagnostic it drew is one of its verdict codes: such a block
// correctly does not parse, and its judgement says whether the codes fired
// at its markers.
// An E0000 is never a verdict code, so a block with one must parse.
func mustParse(u UnitResult) bool {
	if u.Unit.Verdict != VerdictReject {
		return true
	}
	parseCode := false
	for _, c := range u.Unit.Codes {
		if EmittedIn(c, diag.PhaseParse) {
			parseCode = true
		}
	}
	if !parseCode {
		return true
	}
	for _, g := range u.Emitted {
		if (g.Phase == diag.PhaseParse || g.Code == diag.E0000) && !slices.Contains(u.Unit.Codes, g.Code) {
			return true
		}
	}
	return false
}

// A reject block whose parse-group code fires at its marker is
// PASS and does not parse, and TestCorpus accepts it; an ok block, or a
// reject block of a later group, that does not parse is still a problem.
func TestCorpusAcceptsUnparsedParseGroupReject(t *testing.T) {
	rej := func(c diag.Code) *Unit {
		return BlockUnit(block(VerdictReject, src, []diag.Code{c}, Marker{Line: 2, Code: c}))
	}
	at := func(c diag.Code, p diag.Phase) []GotDiag {
		return []GotDiag{{File: BlockFile, Line: 2, Code: c, Phase: p, Own: true}}
	}
	cases := []struct {
		name string
		u    UnitResult
		bad  bool
	}{
		{"reject E0101 PASS", UnitResult{Unit: rej(diag.E0101), Judgement: Judgement{Status: StatusPass, Emitted: at(diag.E0101, diag.PhaseParse)}}, false},
		{"reject E1205 PASS", UnitResult{Unit: rej(diag.E1205), Judgement: Judgement{Status: StatusPass, Emitted: at(diag.E1205, diag.PhaseParse)}}, false},
		{"reject E0609 parse form PASS", UnitResult{Unit: rej(diag.E0609), Judgement: Judgement{Status: StatusPass, Emitted: at(diag.E0609, diag.PhaseParse)}}, false},
		{"reject E0101 with an E0102 too", UnitResult{Unit: rej(diag.E0101), Judgement: Judgement{Status: StatusPending, Reason: "std: x",
			Emitted: append(at(diag.E0101, diag.PhaseParse), at(diag.E0102, diag.PhaseParse)...)}}, true},
		{"reject E0501 unparsed", UnitResult{Unit: rej(diag.E0501), Judgement: Judgement{Status: StatusPending, Reason: "needs StageFlow",
			Emitted: at(diag.E0101, diag.PhaseParse)}}, true},
		{"ok unparsed", UnitResult{Unit: BlockUnit(block(VerdictOK, src, nil)), Judgement: Judgement{Status: StatusPending, Reason: "needs StageFlow",
			Emitted: at(diag.E0101, diag.PhaseParse)}}, true},
	}
	for _, c := range cases {
		c.u.Parsed = len(c.u.Emitted) == 0
		p := corpusUnitProblems(c.u, false)
		if (len(p) != 0) != c.bad {
			t.Errorf("%s: problems %q, want bad %v", c.name, p, c.bad)
		}
	}
}

// checkStage6 is the expectation while the types stages are wired and flow
// is not; see stage6Problem.
func checkStage6(t *testing.T, u UnitResult, r *Result) {
	t.Helper()
	if p := stage6Problem(u, r.Avail); p != "" {
		t.Error(p)
	}
}

// stage6Problem: with the types stages wired and flow not, a reject unit
// whose need is wired is PASS or PENDING std: (std: also when std held the
// run to an earlier phase, rule 8b); a reject unit that needs flow (an
// ownership code) is PASS (its code fired at its marker in an earlier group,
// as E1002's types form can) or PENDING "needs StageFlow" or std:; an ok
// unit is PENDING "needs StageFlow" or std:, never PASS. "needs StageGen" is
// accepted beside "needs StageFlow": a unit holding a gen line has StageGen
// in BlockResult.Skipped, and gen sorts before flow. A FAIL is the
// caller's to report; nothing is checked before the types stages or once
// flow lands.
func stage6Problem(u UnitResult, avail driver.StageSet) string {
	if !avail.Has(driver.StageBodies) || avail.Has(driver.StageFlow) || u.Status == StatusFail {
		return ""
	}
	pendingOn := func(prefixes ...string) bool {
		for _, p := range prefixes {
			if u.Status == StatusPending && strings.HasPrefix(u.Reason, p) {
				return true
			}
		}
		return false
	}
	need, _ := unitNeed(u.Unit)
	switch {
	case u.Unit.Verdict == VerdictOK && !pendingOn("needs StageFlow", "needs StageGen", "std: "):
		return fmt.Sprintf("through types: ok unit is %s %q, want PENDING needs StageFlow or std:", u.Status, u.Reason)
	case u.Unit.Verdict != VerdictReject || u.Status == StatusPass:
	case need&avail == need && !pendingOn("std: "):
		return fmt.Sprintf("through types: reject unit within the wired stages is %s %q, want PASS or PENDING std:", u.Status, u.Reason)
	case need.Has(driver.StageFlow) && !pendingOn("needs StageFlow", "needs StageGen", "std: "):
		return fmt.Sprintf("through types: reject unit that needs flow is %s %q, want PASS, or PENDING needs StageFlow or std:", u.Status, u.Reason)
	}
	return ""
}

// stage6Problem itself, on synthetic judgements with the types stages wired
// and flow not.
func TestStageExpectation(t *testing.T) {
	rej := func(c diag.Code) *Unit {
		return BlockUnit(block(VerdictReject, src, []diag.Code{c}, Marker{Line: 2, Code: c}))
	}
	ok := BlockUnit(block(VerdictOK, src, nil))
	cases := []struct {
		u      *Unit
		st     Status
		reason string
		bad    bool
	}{
		{rej(diag.E0301), StatusPass, "", false},
		{rej(diag.E0301), StatusPending, "std: judged through parse only", false},
		{rej(diag.E0301), StatusPending, "needs StageSignatures", true},
		{rej(diag.E0501), StatusPending, "needs StageFlow", false},
		{rej(diag.E1002), StatusPass, "", false},
		{rej(diag.E0501), StatusPending, "needs StageInstances", true},
		{ok, StatusPending, "needs StageFlow", false},
		{ok, StatusPending, "std: Cell undeclared", false},
		{ok, StatusPass, "", true},
		{ok, StatusPending, "needs StageInstances", true},
		{ok, StatusPending, "needs StageGen", false},
		{rej(diag.E0501), StatusPending, "needs StageGen", false},
	}
	for _, c := range cases {
		p := stage6Problem(UnitResult{Unit: c.u, Judgement: Judgement{Status: c.st, Reason: c.reason}}, stagesTypes)
		if (p != "") != c.bad {
			t.Errorf("%s %s %q: problem %q, want bad %v", verdictText(c.u), c.st, c.reason, p, c.bad)
		}
		if p := stage6Problem(UnitResult{Unit: c.u, Judgement: Judgement{Status: c.st, Reason: c.reason}}, stagesNative); p != "" {
			t.Errorf("through native nothing is checked: %q", p)
		}
	}
}

// With a std that has no std.core, the reference's hello block is Pending,
// not Fail, and no unit fails. The std is a tree of its own (a manifest and
// std.pkg, no core), so the test holds whatever the embedded std declares.
func TestCorpusStdPending(t *testing.T) {
	r, err := Run(Options{Driver: driver.Config{StdDir: noCoreStd(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Std.Through != diag.PhaseParse || len(r.Std.State.Missing) == 0 {
		t.Fatalf("std without std.core: through %s, %d names and modules missing; want parse and some", r.Std.Through, len(r.Std.State.Missing))
	}
	var hello *UnitResult
	for i := range r.Units {
		if u := &r.Units[i]; u.Unit.Block.Document == "doc/reference.md" && strings.Contains(u.Unit.Block.Source, "Main(") {
			hello = u
			break
		}
	}
	if hello == nil {
		t.Fatal("no block declaring Main in doc/reference.md")
	}
	t.Logf("%s: %s %s", hello.Unit.Key, hello.Status, hello.Reason)
	if hello.Status != StatusPending {
		t.Errorf("%s is %s, want PENDING", hello.Unit.Key, hello.Status)
	}
	fail := 0
	for _, u := range r.Units {
		if u.Status == StatusFail && !checkE0101(u.Unit, r.Std.Through) {
			fail++
		}
	}
	if fail > len(knownDesignConflicts) { // at most the known conflicts
		for _, u := range r.Units {
			if u.Status == StatusFail {
				t.Logf("FAIL %s: %s", u.Unit.Key, u.Reason)
			}
		}
		t.Errorf("%d units fail", fail)
	}
}

// The zero Options judge every doc block.
func TestRunZeroOptionsJudgesDocs(t *testing.T) {
	r := designRun(t)
	bs, err := DesignBlocks(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Units) != len(bs) || r.Summary.Blocks != len(bs) || r.Summary.Units != 0 {
		t.Fatalf("judged %d units (%d blocks), want the %d blocks", len(r.Units), r.Summary.Blocks, len(bs))
	}
	for i := range bs {
		if r.Units[i].Unit.Key != bs[i].Key() {
			t.Fatalf("unit %d is %s, want %s (extraction order)", i, r.Units[i].Unit.Key, bs[i].Key())
		}
	}
	if s := r.Summary; s.Pass+s.Fail+s.Pending != len(bs) {
		t.Errorf("pass+fail+pending = %d, want %d", s.Pass+s.Fail+s.Pending, len(bs))
	}
}

func TestRunOnly(t *testing.T) {
	r, err := Run(Options{Only: "doc/memory.md:*", Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Units) == 0 {
		t.Fatal("--only matched nothing")
	}
	for _, u := range r.Units {
		if u.Unit.Block.Document != "doc/memory.md" {
			t.Errorf("--only kept %s", u.Unit.Key)
		}
	}
	if _, err := Run(Options{Only: "["}); err == nil {
		t.Errorf("a bad --only pattern was accepted")
	}
}

// The summary lines and one line per unit, every PENDING with its reason.
func TestReportSummaryLines(t *testing.T) {
	r := designRun(t)
	var b bytes.Buffer
	if err := WriteText(&b, r); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	s := r.Summary
	if want := strings.Join([]string{itoa(s.Blocks), "blocks:", itoa(s.OK), "ok,", itoa(s.Parse), "parse,", itoa(s.Reject), "reject"}, " "); lines[0] != want {
		t.Errorf("line 1 = %q, want %q", lines[0], want)
	}
	if want := "parsed: " + itoa(s.Parsed) + " of " + itoa(s.Blocks); lines[1] != want {
		t.Errorf("line 2 = %q, want %q", lines[1], want)
	}
	unitLines := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "doc/") {
			unitLines++
			if strings.Contains(l, " PENDING") && !strings.Contains(l, " PENDING needs ") && !strings.Contains(l, " PENDING std: ") {
				t.Errorf("pending line without a needs or std reason: %q", l)
			}
		}
	}
	if unitLines != len(r.Units) {
		t.Errorf("%d unit lines, want %d", unitLines, len(r.Units))
	}
	if !strings.Contains(b.String(), "\npass "+itoa(s.Pass)+", fail "+itoa(s.Fail)+", pending "+itoa(s.Pending)) {
		t.Errorf("no totals line in\n%s", b.String())
	}
	if len(r.Std.State.Missing) > 0 && !strings.HasPrefix(lines[2], "std: units judged through ") {
		t.Errorf("line 3 = %q, want the std line", lines[2])
	}
	if ExitStatus(r) != map[bool]int{true: 1, false: 0}[s.Fail > 0] || ExitStatus(nil) != 2 {
		t.Errorf("ExitStatus = %d with %d failures", ExitStatus(r), s.Fail)
	}
}

// When std holds judging back, the text report names every prelude name and
// module std lacks, not the first few.
func TestReportPrintsEveryMissingStdName(t *testing.T) {
	var missing []string
	for i := range 20 {
		missing = append(missing, fmt.Sprintf("Name%02d", i))
	}
	r := &Result{Std: StdInfo{Through: diag.PhaseParse, State: driver.StdState{Missing: missing}}}
	var b bytes.Buffer
	if err := WriteText(&b, r); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(b.String(), "\n")
	if !strings.HasPrefix(lines[2], "std: units judged through parse; 0 diagnostics; 20 names and modules undeclared (") {
		t.Fatalf("line 3 = %q, want the std line", lines[2])
	}
	for _, m := range missing {
		if !strings.Contains(lines[2], m) {
			t.Errorf("std line lacks %s: %q", m, lines[2])
		}
	}
	if strings.Contains(lines[2], "...") {
		t.Errorf("std line is truncated: %q", lines[2])
	}
}

// TestCorpusJSONShape pins the JSON Lines: one object per unit with exactly
// the keys unit, verdict, codes, status, reason, listed, expected, emitted in
// that order, then one summary object.
func TestCorpusJSONShape(t *testing.T) {
	r := designRun(t)
	var b bytes.Buffer
	if err := WriteJSON(&b, r); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(lines) != len(r.Units)+1 {
		t.Fatalf("%d lines, want %d units and a summary", len(lines), len(r.Units))
	}
	unitKeys := []string{"unit", "verdict", "codes", "status", "reason", "listed", "expected", "emitted"}
	for i, l := range lines[:len(lines)-1] {
		if got := topKeys(t, l); strings.Join(got, ",") != strings.Join(unitKeys, ",") {
			t.Fatalf("line %d keys %v, want %v", i+1, got, unitKeys)
		}
		var o struct {
			Unit, Verdict, Status, Reason string
			Codes                         []string
		}
		if err := json.Unmarshal([]byte(l), &o); err != nil {
			t.Fatal(err)
		}
		u := r.Units[i]
		if o.Unit != u.Unit.Key || o.Verdict != u.Unit.Verdict.String() || o.Status != strings.ToLower(u.Status.String()) || o.Reason != u.Reason {
			t.Errorf("line %d = %+v, want %s", i+1, o, u.Unit.Key)
		}
	}
	if got := topKeys(t, lines[len(lines)-1]); strings.Join(got, ",") != "summary,std" {
		t.Errorf("summary keys %v", got)
	}
	var sum struct {
		Summary map[string]int
		Std     struct {
			Through string
			Missing []string
		}
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Summary["blocks"] != r.Summary.Blocks || sum.Summary["pending"] != r.Summary.Pending || sum.Std.Through != r.Std.Through.String() {
		t.Errorf("summary = %+v", sum)
	}
}

// topKeys lists the keys of a one-line JSON object in order.
func topKeys(t *testing.T, line string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not an object: %q", line)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// Two runs (two sessions, different worker counts) give the same bytes,
// at GOMAXPROCS 1 and 8 with Jobs 1 and 8, with the embedded std.
func TestCorpusJSONStable(t *testing.T) {
	a, b := stableRuns(t, Options{})
	if !bytes.Equal(a, b) {
		t.Errorf("the corpus report differs between GOMAXPROCS 1 -j 1 and GOMAXPROCS 8 -j 8")
	}
}

// The same comparison over doc/ and every conformance unit
// with the near member-less stand-in std, so units are judged through types and
// the shared tables (IDs in scheduling order) are exercised
// by the names and types stages. It fails, never skips, when the stand-in
// stops reaching types.
func TestCorpusJSONStableThroughTypes(t *testing.T) {
	opts := Options{Conformance: true, Driver: driver.Config{StdDir: standInStd(t, repoRoot(t))}}
	a, b := stableRuns(t, opts)
	if !bytes.Equal(a, b) {
		t.Errorf("the corpus report through types differs between GOMAXPROCS 1 -j 1 and GOMAXPROCS 8 -j 8")
	}
}

// stableRuns runs opts twice, at GOMAXPROCS 1 with Jobs 1 and at GOMAXPROCS
// 8 with Jobs 8, and returns each run's JSON Lines followed by its text
// report. When opts names a std directory, both runs must reach types.
func stableRuns(t *testing.T, opts Options) ([]byte, []byte) {
	t.Helper()
	var out [2][]byte
	for i, j := range []int{1, 8} {
		old := runtime.GOMAXPROCS(j)
		o := opts
		o.Jobs = j
		r, err := Run(o)
		runtime.GOMAXPROCS(old)
		if err != nil {
			t.Fatal(err)
		}
		if opts.Driver.StdDir != "" && r.Std.Through < diag.PhaseTypes {
			t.Fatalf("GOMAXPROCS %d: judged through %s, want types or later; the comparison would be vacuous", j, r.Std.Through)
		}
		var b, tx bytes.Buffer
		if err := WriteJSON(&b, r); err != nil {
			t.Fatal(err)
		}
		if err := WriteText(&tx, r); err != nil {
			t.Fatal(err)
		}
		out[i] = append(b.Bytes(), tx.Bytes()...)
		t.Logf("GOMAXPROCS %d, -j %d: through %s, pass %d, fail %d, pending %d", j, j, r.Std.Through, r.Summary.Pass, r.Summary.Fail, r.Summary.Pending)
	}
	return out[0], out[1]
}

// allowlistProblems is TestPendingAllowlistIsCurrent's check: every line of
// a names a unit of r, doc block or conformance unit, that is still
// PENDING.
func allowlistProblems(a Allowlist, r *Result) []string {
	status := map[string]Status{}
	for _, u := range r.Units {
		status[u.Unit.Key] = u.Status
	}
	var out []string
	for _, e := range a.Entries {
		st, ok := status[e.Key]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("pending.txt: %s names no current block or unit", e.Key))
		case st != StatusPending:
			out = append(out, fmt.Sprintf("pending.txt: %s is %s now; remove the line", e.Key, st))
		}
	}
	return out
}

// pending.txt parses, and every line names a current doc block or
// conformance unit that is still pending (stale lines fail; conformance
// units included).
func TestPendingAllowlistIsCurrent(t *testing.T) {
	a, err := LoadPending()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range allowlistProblems(a, fullRun(t)) {
		t.Error(p)
	}
	t.Logf("pending.txt: %d lines", len(a.Entries))
}

// A pending.txt line naming a PENDING conformance unit is current; one
// naming a conformance unit that passes, or no unit, is stale. No real
// conformance unit is PENDING (gen is wired and codes/E0903 passes), so the
// PENDING unit is planted: a copy of the real run's results with
// codes/E0903 set to PENDING "needs StageGen", its judgement before gen was
// wired. The real run must still judge that unit and pass E1206_c_for.
func TestPendingAllowlistCoversConformanceUnits(t *testing.T) {
	full := fullRun(t)
	const gen, passing = "tests/conformance/codes/E0903", "tests/conformance/codes/E1206_c_for.mk"
	r := &Result{Units: slices.Clone(full.Units)}
	var pending string
	for i, u := range r.Units {
		switch u.Unit.Key {
		case gen:
			r.Units[i].Status, r.Units[i].Reason = StatusPending, "needs StageGen"
			pending = r.Units[i].Reason
		case passing:
			if u.Status != StatusPass {
				t.Fatalf("%s is %s %s; the test wants a PASS conformance unit", passing, u.Status, u.Reason)
			}
		}
	}
	if pending == "" {
		t.Fatalf("%s not judged", gen)
	}
	a, err := ParseAllowlist(gen + " " + pending + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if p := allowlistProblems(a, r); len(p) != 0 {
		t.Errorf("a line for the PENDING %s (%s) is reported stale: %v", gen, pending, p)
	}
	a, err = ParseAllowlist(gen + " x\n" + passing + " x\ntests/conformance/codes/E9999.mk x\n")
	if err != nil {
		t.Fatal(err)
	}
	p := allowlistProblems(a, r)
	if len(p) != 2 || !strings.Contains(p[0], passing+" is PASS") || !strings.Contains(p[1], "E9999.mk names no current") {
		t.Errorf("stale conformance lines reported as %q", p)
	}
}

// TestPendingListIsCurrent is TestPendingAllowlistIsCurrent under a second
// name.
func TestPendingListIsCurrent(t *testing.T) { TestPendingAllowlistIsCurrent(t) }

func TestParseAllowlist(t *testing.T) {
	a, err := ParseAllowlist("\na.md:1 needs gen\nb.md:2  std: Cell\n")
	if err != nil || len(a.Entries) != 2 {
		t.Fatalf("ParseAllowlist = %+v, %v", a, err)
	}
	if why, ok := a.Reason("b.md:2"); !ok || why != "std: Cell" {
		t.Errorf("Reason(b.md:2) = %q %v", why, ok)
	}
	if _, ok := a.Reason("c.md:3"); ok {
		t.Errorf("Reason found an unlisted key")
	}
	for _, bad := range []string{"a.md:1\n", "b.md:1 x\na.md:1 y\n", "a.md:1 x\na.md:1 y\n", " a.md:1 x\n"} {
		if _, err := ParseAllowlist(bad); err == nil {
			t.Errorf("ParseAllowlist(%q) accepted", bad)
		}
	}
}

// The adapter's field-for-field mapping.
func TestDriverCompilerMapsUnit(t *testing.T) {
	b := &Block{Document: "doc/x.md", Line: 17, Verdict: VerdictOK, Source: "F() { }\n"}
	u := BlockUnit(b)
	in := ToUnitInput(u, "PRELUDE\n", driver.UnitRun)
	if in.Name != "doc/x.md#17" || in.ParseOnly || in.Prelude != PreludeFile || in.Mode != driver.UnitRun {
		t.Errorf("ok block input = %+v", in)
	}
	if len(in.Files) != 2 || in.Files[0] != (driver.UnitFile{Path: BlockFile, Text: "F() { }\n"}) ||
		in.Files[1] != (driver.UnitFile{Path: PreludeFile, Text: "PRELUDE\n"}) {
		t.Errorf("files = %+v", in.Files)
	}
	b.Verdict = VerdictParse
	in = ToUnitInput(BlockUnit(b), "PRELUDE\n", driver.UnitRun)
	if !in.ParseOnly || in.Prelude != "" || len(in.Files) != 1 || in.Mode != driver.UnitCheck {
		t.Errorf("parse block input = %+v", in)
	}
	b.Verdict, b.Codes, b.Expected = VerdictReject, []diag.Code{diag.E0501}, []Marker{{Line: 1, Code: diag.E0501}}
	ru := BlockUnit(b)
	in = ToUnitInput(ru, "P\n", driver.UnitRun)
	if in.ParseOnly || in.Prelude != PreludeFile || in.Mode != driver.UnitCheck {
		t.Errorf("reject block input = %+v", in)
	}
	if len(ru.Expected) != 1 || ru.Expected[0] != (UnitMarker{File: BlockFile, Line: 1, Code: diag.E0501}) {
		t.Errorf("reject unit markers = %+v", ru.Expected)
	}
	// The outcome comes back unchanged: the session's report, judged on
	// block.mk lines.
	root := repoRoot(t)
	c := DriverCompiler(root, driver.Config{})
	if _, err := c.Start(); err != nil {
		t.Fatal(err)
	}
	pb := &Block{Document: "doc/t.md", Line: 3, Verdict: VerdictParse, Source: "F() {\n    x := \n}\n"}
	out := c.Check(BlockUnit(pb), driver.UnitCheck)
	got := Got(BlockUnit(pb), out)
	if len(got) == 0 || got[0].File != BlockFile || !got[0].Own || got[0].Code != diag.E0101 || got[0].Line < 2 {
		t.Errorf("parse error came back as %+v", got)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
