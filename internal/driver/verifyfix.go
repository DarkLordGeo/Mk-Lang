// Why: a fix labelled compiles promises it was checked to compile. A fix
// that only trades one error for others (a `try` that turns one E0802 into
// two E0801) must not be offered as one.
//
// What: Session.CheckVerified and verifiedReport, verifyCompiles and
// verifyFixes, the verifier (split, settle, groupKept), and movedUp, the
// progress rule for clause fixes.
//
// How: each compiles fix is applied in an overlay and the package
// rechecked; one whose recheck is no progress becomes one of the
// diagnostic's alternatives. Rechecks are spent in groups that are halved
// on failure. mk check, mk fix's final report, the compile errors of mk
// build, run and test, and mk lsp's code actions (on request) go through it.

package driver

import (
	"strings"

	"mk/internal/diag"
	"mk/internal/source"
)

// maxVerify bounds the rechecks of one report: a compiles fix not settled
// within the bound is not recompiled, so it is not offered as compiles
// either.
const maxVerify = 16

// Why a compiles fix is downgraded: each alternative says whether the fix
// was recompiled. The common prefix is what tools match on.
const (
	notCheckedPrefix = " (not checked to compile: "
	whyNoProgress    = notCheckedPrefix + "recompiled, it did not improve the report)"
	whyNotOneFile    = notCheckedPrefix + "not recompiled, its edits are not in one file of the package)"
	whyNoSession     = notCheckedPrefix + "not recompiled, the recheck could not start)"
)

// CheckVerified is Check with every compiles fix of the report verified by
// recompiling. The result is the check's, read through an overlay so
// that each fix can be tried in memory; no file is written.
func (s *Session) CheckVerified() (Result, error) {
	ov := &overlay{files: map[string]string{}, base: s.cfg.ReadFile}
	cfg := s.cfg
	cfg.ReadFile = ov.read
	ns, err := s.child(cfg)
	if err != nil {
		return Result{}, err
	}
	res := ns.Check()
	ns.verifyCompiles(res, ov, cfg, nil)
	return res, nil
}

// child is a new session under cfg that checks as s does: mk test's and
// mk test --prop's Main rule (a directory's tests need no Main) and a
// build's single-file bin root. A recheck made without them reports
// E0201 where s reported the fix's diagnostic.
func (s *Session) child(cfg Config) (*Session, error) {
	ns, err := NewSession(cfg)
	if err != nil {
		return nil, err
	}
	ns.testing, ns.prop, ns.forBuild = s.testing, s.prop, s.forBuild
	return ns, nil
}

// verifiedReport is r, the failed check of a build, run or test, with its
// compiles fixes verified: the check is made again through an
// overlay (a cache hit) and verified. When the recheck does not report the
// same number of diagnostics, r is returned as it was.
func (s *Session) verifiedReport(r Result) Result {
	if !hasCompilesFix(r) {
		return r
	}
	ov := &overlay{files: map[string]string{}, base: s.cfg.ReadFile}
	cfg := s.cfg
	cfg.ReadFile = ov.read
	ns, err := s.child(cfg)
	if err != nil {
		return r
	}
	res := ns.Check()
	if len(res.Report.Diags) != len(r.Report.Diags) {
		return r
	}
	ns.verifyCompiles(res, ov, cfg, nil)
	return res
}

// hasCompilesFix reports whether a diagnostic of r offers a compiles fix.
func hasCompilesFix(r Result) bool {
	for _, e := range r.Report.Diags {
		for _, f := range e.Diag.Fixes {
			if f.Class == diag.Compiles {
				return true
			}
		}
	}
	return false
}

// verifyCompiles verifies the compiles fixes of res, whose sources were read
// through ov under cfg. base, when not nil, is res itself as checked under cfg
// with no diagnostic cap and no gen view (mk fix's final report), which spares
// a recheck. It is verifyFixes with the maxVerify bound.
func (s *Session) verifyCompiles(res Result, ov *overlay, cfg Config, base *Result) {
	s.verifyFixes(res, ov, cfg, base, maxVerify)
}

// vcand is one compiles fix under verification: fix number fix of d, and
// the one package file its edits lie in.
type vcand struct {
	d    *diag.Diagnostic
	fix  int
	path string
}

// verifyFixes verifies fixes: each compiles fix whose edits all lie in one
// package file is recompiled before it is offered, and one whose recompile is
// no progress over the uncapped report becomes an alternative naming what it
// would have written. A fix it cannot recompile (an edit in std or in two
// files, a session that does not start, one past the bound) is downgraded too,
// and its alternative says it was not recompiled: compiles means checked.
//
// Rechecks are spent in groups, since a one-shot mk check pays for each: one
// fix per diagnostic and per line, edits apart and adding no line, all
// applied at once. A group is kept whole when its recheck is base less the
// group's diagnostics and nothing else (groupKept); otherwise it is halved,
// and a fix alone is judged as the fix loop judges one (better, or a clause
// whose error moved up). A report whose fixes all hold costs one recheck
// after the base. budget bounds the rechecks of a fix alone (the first
// budget fixes are always rechecked, alone if their group fails) and half of
// it the group rechecks; the base is not counted. It returns the rechecks
// made.
func (s *Session) verifyFixes(res Result, ov *overlay, cfg Config, base *Result, budget int) int {
	var cands []vcand
	bad := map[*diag.Diagnostic]map[int]string{}
	drop := func(c vcand, why string) {
		if bad[c.d] == nil {
			bad[c.d] = map[int]string{}
		}
		bad[c.d][c.fix] = why
	}
	for _, e := range res.Report.Diags {
		for i, f := range e.Diag.Fixes {
			if f.Class != diag.Compiles {
				continue
			}
			c := vcand{d: e.Diag, fix: i, path: s.fixPath(res, f, ov)}
			if c.path == "" {
				drop(c, whyNotOneFile)
				continue
			}
			cands = append(cands, c)
		}
	}
	rechecks := 0
	if len(cands) > 0 {
		cfg.GenView = false
		cfg.MaxDiags = diag.NoCap
		check := func() (Result, bool) {
			ns, err := s.child(cfg)
			if err != nil {
				return Result{}, false
			}
			return ns.Check(), true
		}
		b, ok := Result{}, true
		if base != nil {
			b = *base
		} else {
			b, ok = check()
		}
		if !ok {
			for _, c := range cands {
				drop(c, whyNoSession)
			}
		} else {
			v := &verifier{ov: ov, check: check, res: res, base: b, budget: budget, drop: drop}
			for _, g := range v.split(cands) {
				v.settle(g, false)
			}
			rechecks = v.used + v.groups
		}
	}
	for _, e := range res.Report.Diags {
		d := e.Diag
		why := bad[d]
		if len(why) == 0 {
			continue
		}
		var keep []diag.Fix
		for i, f := range d.Fixes {
			if w, ok := why[i]; ok {
				d.Alt(f.Why + w)
				continue
			}
			keep = append(keep, f)
		}
		d.Fixes = keep
	}
	return rechecks
}

// verifier is one report's verification run: the overlay the fixes are tried
// in, the recheck, the report the fixes came from, the uncapped base report,
// the rechecks of a fix alone allowed (budget) and made (used), the group
// rechecks made (at most budget/2), and where a downgraded fix goes.
type verifier struct {
	ov     *overlay
	check  func() (Result, bool)
	res    Result
	base   Result
	budget int
	used   int
	groups int
	drop   func(vcand, string)
}

// split divides cands, in report order, into groups that can be rechecked
// together: at most one fix per diagnostic and per diagnostic line
// (groupKept finds a diagnostic by its line), edits that neither overlap
// nor add or remove a line (a shift would move the diagnostics after it).
// A fix that changes lines is a group of its own, after the others.
func (v *verifier) split(cands []vcand) [][]vcand {
	type slot struct {
		ds    map[*diag.Diagnostic]bool
		lines map[diagLine]bool
		spans map[string][][2]uint32
	}
	var out, solo [][]vcand
	var slots []*slot // slots[i] describes out[i]
	for _, c := range cands {
		eds := c.d.Fixes[c.fix].Edits
		v.ov.mu.Lock()
		text := v.ov.files[c.path]
		v.ov.mu.Unlock()
		lineFree := c.d.At.File != source.NoFile
		for _, e := range eds {
			if strings.Contains(e.Text, "\n") || int(e.Span.End) > len(text) || e.Span.Start > e.Span.End ||
				strings.Contains(text[e.Span.Start:e.Span.End], "\n") {
				lineFree = false
			}
		}
		if !lineFree {
			solo = append(solo, []vcand{c})
			continue
		}
		line := lineOf(v.res, c.d)
		i := 0
		for ; i < len(slots); i++ {
			sl := slots[i]
			if !sl.ds[c.d] && !sl.lines[line] && !overlaps(sl.spans[c.path], eds) {
				break
			}
		}
		if i == len(slots) {
			slots = append(slots, &slot{ds: map[*diag.Diagnostic]bool{}, lines: map[diagLine]bool{}, spans: map[string][][2]uint32{}})
			out = append(out, nil)
		}
		sl := slots[i]
		sl.ds[c.d], sl.lines[line] = true, true
		for _, e := range eds {
			sl.spans[c.path] = append(sl.spans[c.path], [2]uint32{e.Span.Start, e.Span.End})
		}
		out[i] = append(out[i], c)
	}
	return append(out, solo...)
}

// overlaps reports whether an edit of eds touches one of the spans taken
// (fix.go's nonOverlapping rule).
func overlaps(taken [][2]uint32, eds []diag.Edit) bool {
	for _, e := range eds {
		for _, r := range taken {
			if e.Span.Start < r[1] && r[0] < e.Span.End || e.Span.Start == r[0] {
				return true
			}
		}
	}
	return false
}

// settle verifies group g: kept whole, else halved; a fix alone is judged
// by the fix loop's rule. Past the group cap a group is settled fix by
// fix; past the budget a fix is not recompiled alone, and its alternative
// says whether a failed group held it (tried).
func (v *verifier) settle(g []vcand, tried bool) {
	if len(g) > 1 && v.groups >= v.budget/2 {
		// Group rechecks have their own cap, so they never take a fix
		// alone's recheck: at least the first budget fixes are recompiled
		// alone.
		for _, c := range g {
			v.settle([]vcand{c}, tried)
		}
		return
	}
	if len(g) == 1 && v.used >= v.budget {
		why := notCheckedPrefix + "not recompiled, over the bound of " + itoaQ(v.budget) + " rechecks per report)"
		if tried {
			why = notCheckedPrefix + "recompiled only with other fixes, which did not hold; over the bound of " + itoaQ(v.budget) + " rechecks per report)"
		}
		v.drop(g[0], why)
		return
	}
	if len(g) == 1 {
		v.used++
	} else {
		v.groups++
	}
	saved := v.ov.snapshot()
	set := make([]fixCand, 0, len(g))
	for _, c := range g {
		set = append(set, fixCand{path: c.path, edits: c.d.Fixes[c.fix].Edits})
	}
	v.ov.apply(set)
	nres, ok := v.check()
	v.ov.restore(saved)
	if !ok {
		for _, c := range g {
			v.drop(c, whyNoSession)
		}
		return
	}
	if len(g) == 1 {
		if !better(nres.Report, v.base.Report) && !movedUp(nres, v.base, g[0].d) {
			v.drop(g[0], whyNoProgress)
		}
		return
	}
	if v.groupKept(nres, g) {
		return
	}
	h := len(g) / 2
	v.settle(g[:h], true)
	v.settle(g[h:], true)
}

// diagLine is where a diagnostic is, to the line, and its code.
type diagLine struct {
	path string
	line uint32
	code diag.Code
}

func lineOf(r Result, d *diag.Diagnostic) diagLine {
	p := r.Sources.Position(d.At.StartPos())
	return diagLine{p.Path, p.Line, d.Code}
}

// groupKept reports whether the recheck after group g is exactly what each
// fix promises alone: no internal error added, and the report clean, or of
// base's phase with every diagnostic one of base's (path, line, code and
// message) and no diagnostic of g's code left on a line of g's. A fix that
// adds any error, even beside others that remove theirs, fails the group.
func (v *verifier) groupKept(after Result, g []vcand) bool {
	base := v.base
	if internalCount(after.Report) > internalCount(base.Report) {
		return false
	}
	if len(after.Report.Diags) == 0 {
		return true
	}
	if after.Report.Phase != base.Report.Phase {
		return false
	}
	type key struct {
		at  diagLine
		msg string
	}
	left := map[key]int{}
	for _, e := range base.Report.Diags {
		left[key{lineOf(base, e.Diag), e.Diag.Message}]++
	}
	gone := map[diagLine]bool{}
	for _, c := range g {
		gone[lineOf(v.res, c.d)] = true
	}
	for _, e := range after.Report.Diags {
		k := key{lineOf(after, e.Diag), e.Diag.Message}
		if gone[k.at] || left[k] == 0 {
			return false
		}
		left[k]--
	}
	return true
}

// fixPath is the one package file all of f's edits lie in, or "".
func (s *Session) fixPath(res Result, f diag.Fix, ov *overlay) string {
	path := ""
	for _, ed := range f.Edits {
		p := s.osPath(res.Sources, ed.Span.File, ov)
		if p == "" || (path != "" && p != path) {
			return ""
		}
		path = p
	}
	return path
}

// movedUp counts "a clause fix that moves an error up one call level" as
// progress: d is in before (a recheck that never reached d's error, a session
// stopped at E0201, has nothing to move), after the fix no diagnostic of d's
// code is on d's line (an edit in a signature adds no line), and the report has
// no more diagnostics of another code, none of an earlier phase. Writing `*self
// into self` on a method moves its E0604 to every caller that relies on the old
// signature: one diagnostic may become one per caller, and that is still
// progress. A clause is only ever added, so the loop cannot cycle.
func movedUp(after, before Result, d *diag.Diagnostic) bool {
	if after.Report.Phase < before.Report.Phase {
		return false
	}
	line := func(r Result, e *diag.Diagnostic) (string, uint32) {
		p := r.Sources.Position(e.At.StartPos())
		return p.Path, p.Line
	}
	other := func(r Result) int {
		n := 0
		for _, e := range r.Report.Diags {
			if e.Diag.Code != d.Code {
				n++
			}
		}
		return n
	}
	if other(after) > other(before) {
		return false // another kind of error appeared: not the same error moved
	}
	dp, dl := line(before, d)
	present := false
	for _, e := range before.Report.Diags {
		if p, l := line(before, e.Diag); e.Diag.Code == d.Code && p == dp && l == dl {
			present = true
		}
	}
	if !present {
		return false
	}
	for _, e := range after.Report.Diags {
		if e.Diag.Code == d.Code {
			if p, l := line(after, e.Diag); p == dp && l == dl {
				return false
			}
		}
	}
	return true
}
