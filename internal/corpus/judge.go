// Why: a corpus unit passes, fails or is pending for reasons that must be the
// same on every run and stated in words, so the judge is one pure function
// over the driver's report.
//
// What: Status, GotDiag, Judgement, Judge (one design block), JudgeUnit (one
// unit) and Got, which turns a report into the judge's view.
//
// How: JudgeUnit tries numbered rules in order and the first that matches
// decides: internal errors and stray diagnostics fail first, then the
// verdict's own test, then the pending cases (a broken std, a missing name,
// a stubbed or unwired stage). The judge never re-filters: E is the report
// the driver returns.

package corpus

import (
	"fmt"
	"slices"
	"strings"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
)

// Status is a unit's judgement.
type Status uint8

const (
	StatusPass    Status = iota + 1 // the unit is what its verdict says
	StatusFail                      // it is not, or the compiler failed
	StatusPending                   // it cannot be judged yet; never a pass
	StatusSkip                      // its // host: names another target; never a pass
)

var statusNames = [...]string{"?", "PASS", "FAIL", "PENDING", "SKIP"}

// String is PASS, FAIL, PENDING or SKIP.
func (s Status) String() string {
	if int(s) < len(statusNames) {
		return statusNames[s]
	}
	return "?"
}

// GotDiag is one diagnostic of a unit's report, as the judge sees it.
type GotDiag struct {
	File    string // unit-relative path for a unit file, else the source map's path (std/..., or "" with no span)
	Line    int    // 1-based start line of At in File; 0 with no span
	Code    diag.Code
	Phase   diag.Phase
	Message string
	Own     bool   // File is one of the unit's own files (Unit.Files)
	Text    string // the source text At covers
	LineTxt string // the whole source line At starts on
}

// Judgement is the outcome of judging one unit.
type Judgement struct {
	Status Status
	// Reason says why: every PENDING and FAIL has one;
	// a PASS has none. A pending reason starts with "needs " or "std: ".
	Reason   string
	Expected []UnitMarker // the unit's markers
	Emitted  []GotDiag    // the unit's report, in report order
}

// Judge judges one design block: the
// block's unit is BlockUnit(b).
func Judge(b *Block, r driver.BlockResult, std driver.StdState, avail driver.StageSet) Judgement {
	return JudgeUnit(BlockUnit(b), r, std, avail)
}

// Got converts a unit's report to GotDiags, in report order.
func Got(u *Unit, r driver.BlockResult) []GotDiag {
	out := make([]GotDiag, 0, len(r.Report.Diags))
	for _, e := range r.Report.Diags {
		out = append(out, gotDiag(u, r, e.Diag))
	}
	return out
}

func gotDiag(u *Unit, r driver.BlockResult, d *diag.Diagnostic) GotDiag {
	g := GotDiag{Code: d.Code, Phase: d.Phase, Message: d.Message}
	if !d.At.IsValid() || r.Sources == nil {
		return g
	}
	f := r.Sources.File(d.At.File)
	if p, ok := r.Paths[d.At.File]; ok {
		g.File, g.Own = p, u.Owns(p)
	} else {
		g.File = f.Path
	}
	g.Line = int(r.Sources.LineCol(d.At.StartPos()).Line)
	g.Text = r.Sources.Text(d.At)
	g.LineTxt = f.Line(g.Line)
	return g
}

// JudgeUnit applies the judge's rules to one unit; the first that
// matches decides. It is a pure function of its inputs. E is the unit's
// report; "wired" means the unit's need and r.Skipped are in avail.
//
//  1. E holds an E0000: FAIL.
//     1b. E holds an E0001 and the unit is not a reject unit whose verdict
//     names E0001: FAIL, with its own reason. Only --build (a tool or file
//     failure of stages 8 to 10) produces one, and it is usually spanless,
//     so rule 2 would misname it "no position". A reject unit whose
//     verdict names E0001 (the driver's E0001 with `reason: no-body` has a
//     span) is judged by rules 2 and 4 like any other code.
//  2. E holds a diagnostic outside the unit's own files, the prelude
//     included, or one with no position: FAIL.
//  3. parse: PASS iff E holds no parse-group diagnostic, else FAIL.
//  4. reject, E's (file, line, code) set equals the markers and its code
//     set equals the verdict's, and r.Skipped holds no stage avail lacks:
//     PASS. With a stubbed stage the match may be the stub's doing (a gen
//     line not run leaves its type undeclared), so rule 7b decides instead:
//     a missing stage is PENDING, never PASS.
//  5. ok, E empty, wired, judged through flow: PASS.
//  6. std's own report holds a diagnostic of a group the unit needs:
//     PENDING "std: <it>".
//  7. std lacks a name and E holds an E0201 or E0202 on it: PENDING
//     "std: <name> undeclared".
//     7b. E non-empty and r.Skipped holds a stage avail lacks: PENDING
//     "needs <first such stage>". The driver stubbed a stage this unit
//     entered (a gen line before gen is wired), so the report is not a
//     whole compile and any diagnostic in it may be the stub's (the type a
//     gen line would declare is unknown): a missing stage is PENDING, never
//     FAIL or PASS. Rules 1 and 2 still fail first, and once every stage
//     is wired the masking ends.
//  8. E empty, not wired: PENDING "needs <first missing stage>".
//     8b. E empty, the need wired but r.Through earlier than the unit's
//     group: PENDING "std: ..." (the driver ran nothing past Through
//     because std is missing or broken).
//  9. Anything else: FAIL.
func JudgeUnit(u *Unit, r driver.BlockResult, std driver.StdState, avail driver.StageSet) Judgement {
	got := Got(u, r)
	j := Judgement{Expected: slices.Clone(u.Expected), Emitted: got}
	decide := func(s Status, format string, args ...any) Judgement {
		j.Status, j.Reason = s, fmt.Sprintf(format, args...)
		return j
	}
	for _, g := range got { // rule 1
		if g.Code == diag.E0000 {
			return decide(StatusFail, "internal error: %s", g.Message)
		}
	}
	markedE0001 := u.Verdict == VerdictReject && slices.Contains(u.Codes, diag.E0001)
	for _, g := range got { // rule 1b
		if g.Code == diag.E0001 && !markedE0001 {
			return decide(StatusFail, "E0001 (toolchain or file system, from the build): %s", g.Message)
		}
	}
	for _, g := range got { // rule 2
		if g.Line == 0 {
			return decide(StatusFail, "diagnostic with no position: %s %s", g.Code, g.Message)
		}
		if !g.Own {
			return decide(StatusFail, "diagnostic outside the unit: %s", describe(g))
		}
	}
	if u.Verdict == VerdictParse { // rule 3
		for _, g := range got {
			if g.Phase == diag.PhaseParse {
				return decide(StatusFail, "does not parse: %s", describe(g))
			}
		}
		j.Status = StatusPass
		return j
	}
	stubbed := r.Skipped &^ avail
	if u.Verdict == VerdictReject && stubbed == 0 && matches(u, got) { // rule 4
		j.Status = StatusPass
		return j
	}
	need, group := unitNeed(u)
	need |= r.Skipped
	stagesWired := need&avail == need
	judged := group <= r.Through
	if u.Verdict == VerdictOK && len(got) == 0 && stagesWired && judged { // rule 5
		j.Status = StatusPass
		return j
	}
	if why, ok := stdBroken(std, r.Sources, group); ok { // rule 6
		return decide(StatusPending, "std: %s", why)
	}
	if name, ok := stdGap(std, got); ok { // rule 7
		return decide(StatusPending, "std: %s undeclared", name)
	}
	if len(got) != 0 && stubbed != 0 { // rule 7b
		return decide(StatusPending, "needs %s", stageName(firstMissing(stubbed, avail)))
	}
	if len(got) == 0 && !stagesWired { // rule 8
		return decide(StatusPending, "needs %s", stageName(firstMissing(need, avail)))
	}
	if len(got) == 0 && !judged { // rule 8b
		return decide(StatusPending, "std: judged through %s only%s", r.Through, missingNote(std))
	}
	switch { // rule 9
	case u.Verdict == VerdictOK:
		return decide(StatusFail, "ok block has diagnostics: %s", describeAll(got))
	case len(got) == 0:
		return decide(StatusFail, "expected %s, emitted nothing", markersText(u.Expected))
	}
	return decide(StatusFail, "expected %s, emitted %s", markersText(u.Expected), describeAll(got))
}

// matches is rule 4: E as a set of (file, line, code) equals the markers,
// and E's codes are the verdict's.
func matches(u *Unit, got []GotDiag) bool {
	if len(got) == 0 {
		return false
	}
	var e []UnitMarker
	var codes []diag.Code
	for _, g := range got {
		e = append(e, UnitMarker{File: g.File, Line: g.Line, Code: g.Code})
		codes = append(codes, g.Code)
	}
	e = sortMarkers(e)
	slices.Sort(codes)
	codes = slices.Compact(codes)
	return slices.Equal(e, sortMarkers(slices.Clone(u.Expected))) && slices.Equal(codes, u.Codes)
}

// stdBroken is rule 6: the first diagnostic of std's report whose group the
// unit needs (every group up to the unit's, and any E0000).
func stdBroken(std driver.StdState, sm *source.SourceMap, group diag.Phase) (string, bool) {
	for _, e := range std.Report.Diags {
		d := e.Diag
		if d.Phase == diag.PhaseInternal || d.Phase <= group {
			loc := "-"
			if sm != nil && d.At.IsValid() {
				loc = sm.Position(d.At.StartPos()).String()
			}
			return fmt.Sprintf("%s %s %s", d.Code, loc, d.Message), true
		}
	}
	return "", false
}

// stdGap is rule 7: an E0201 on a name std lacks, or an E0202 on a std
// module path std lacks.
func stdGap(std driver.StdState, got []GotDiag) (string, bool) {
	if len(std.Missing) == 0 {
		return "", false
	}
	for _, g := range got {
		switch g.Code {
		case diag.E0201:
			if slices.Contains(std.Missing, g.Text) {
				return g.Text, true
			}
		case diag.E0202:
			if p := importPathAt(g); p != "" && slices.Contains(std.Missing, p) {
				return p, true
			}
		}
	}
	return "", false
}

// importPathAt is the dotted path an E0202 points into: the word of its
// source line that starts with the anchor's text ("std" or "std.fs").
func importPathAt(g GotDiag) string {
	if g.Text == "" {
		return ""
	}
	for _, w := range strings.Fields(g.LineTxt) {
		if strings.HasPrefix(w, g.Text) {
			return w
		}
	}
	return ""
}

// missingNote names the first missing prelude name (an upper-case name sorts
// before every "std." path) for rule 8b's reason.
func missingNote(std driver.StdState) string {
	if len(std.Missing) == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d std names undeclared, first %s)", len(std.Missing), std.Missing[0])
}

// firstMissing is the first stage of need not in avail, in Stage order.
func firstMissing(need, avail driver.StageSet) driver.Stage {
	for _, st := range need.Stages() {
		if !avail.Has(st) {
			return st
		}
	}
	return driver.StageNone
}

// stageName spells a stage as the judge's reasons do: StageFlow.
func stageName(st driver.Stage) string {
	s := st.String()
	if s == "" {
		return "Stage"
	}
	return "Stage" + strings.ToUpper(s[:1]) + s[1:]
}

// describe renders one emitted diagnostic for a reason.
func describe(g GotDiag) string {
	return fmt.Sprintf("%s:%d %s %s", g.File, g.Line, g.Code, g.Message)
}

func describeAll(got []GotDiag) string {
	parts := make([]string, len(got))
	for i, g := range got {
		parts[i] = describe(g)
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func markersText(ms []UnitMarker) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		parts[i] = fmt.Sprintf("%s:%d %s", m.File, m.Line, m.Code)
	}
	return "[" + strings.Join(parts, "; ") + "]"
}
