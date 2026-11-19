// Why: doc/style.md's mechanical rules are meant to be applied by a tool,
// and a rewrite is only safe when the compiler proves it keeps the
// program's meaning.
//
// What: the round table canonicalRounds and the loop runCanonical that
// `mk fix --canonical` runs after the checking rounds, on a package that
// checks clean, plus the helpers applyProven, disjointRanges, unitBodies
// and IsRewriteRound.
//
// How: canonicalRounds runs value-arm, for-in, error-format and
// manifest-empty, in that order; short form runs after them in Fix, and the
// release round (canonical_release.go) runs on E0701 with the checking
// rounds. Each round proposes text edits; the whole round is applied and
// the package rechecked, and when the result is not clean each edit is
// tried alone and kept only if the package still checks clean (one bad
// candidate never disables a round). A round rewrites only what it can
// prove the same, so each round's matcher is narrow: what it cannot prove
// it leaves. `Or` is E0207, so its rewrite is that diagnostic's fix, which
// the checking rounds apply.

package driver

import (
	"sort"

	"mk/internal/ast"
	checker "mk/internal/check"
	"mk/internal/source"
)

// canonicalRound is one canonical round of doc/style.md: its name and the
// edits it proposes over the clean package of session s.
type canonicalRound struct {
	name    string
	propose func(s *Session, res Result, ov *overlay) []fixCand
}

// canonicalRounds are the built rounds, in the order they run (short form
// runs after them, in Fix).
var canonicalRounds = []canonicalRound{
	{"value-arm", (*Session).valueArms},
	{"for-in", (*Session).forIns},
	{"error-format", (*Session).errorFormats},
	// A std.pkg field holding its empty value leaves pkg.mk
	// (manifest_canonical.go).
	{"manifest-empty", (*Session).manifestEmpties},
}

// runCanonical runs the canonical rounds over a clean package: s is the
// session of the clean check res, check rechecks through ov. It returns the
// rounds that changed something and the result after them.
func runCanonical(s *Session, res Result, ov *overlay, check func() (Result, *Session, error), changed map[string]bool) ([]FixRound, Result, *Session, error) {
	var rounds []FixRound
	cur := s
	for _, rd := range canonicalRounds {
		var fr FixRound
		// A pass takes candidates whose ranges are disjoint; one nested in
		// another (an Or inside an Or's receiver, an arm inside an arm) is
		// proposed again by the next pass over the rewritten text.
		for pass := 0; pass < maxCanonicalPasses; pass++ {
			cands := disjointRanges(rd.propose(cur, res, ov))
			if len(cands) == 0 {
				break
			}
			kept, nres, ns, err := applyProven(cands, ov, check)
			if err != nil {
				return rounds, res, cur, err
			}
			if len(kept) == 0 {
				break
			}
			fr.Phase = rd.name
			sortCands(kept)
			for _, c := range kept {
				fr.Applied = append(fr.Applied, c.applied)
				changed[c.path] = true
			}
			res, cur = nres, ns
		}
		if len(fr.Applied) > 0 {
			rounds = append(rounds, fr)
		}
	}
	return rounds, res, cur, nil
}

// maxCanonicalPasses bounds a round's passes over nested candidates.
const maxCanonicalPasses = 8

// disjointRanges keeps, in source order, the candidates whose edit range
// (first edit start to last edit end) overlaps no candidate kept before it
// in the same file. cands are in sortCands order.
func disjointRanges(cands []fixCand) []fixCand {
	var out []fixCand
	end := map[string]uint32{}
	for _, c := range cands {
		lo, hi := c.edits[0].Span.Start, c.edits[0].Span.End
		for _, e := range c.edits {
			lo, hi = min(lo, e.Span.Start), max(hi, e.Span.End)
		}
		if e, ok := end[c.path]; ok && lo < e {
			continue
		}
		end[c.path] = max(hi, lo+1)
		out = append(out, c)
	}
	return out
}

// applyProven applies cands together and keeps them when the package still
// checks clean; otherwise each alone, in order, keeping those after which
// it still checks clean. It returns the kept candidates and the last clean
// result.
func applyProven(cands []fixCand, ov *overlay, check func() (Result, *Session, error)) ([]fixCand, Result, *Session, error) {
	saved := ov.snapshot()
	ov.apply(nonOverlapping(cands))
	nres, ns, err := check()
	if err != nil {
		ov.restore(saved)
		return nil, Result{}, nil, err
	}
	if len(nres.Report.Diags) == 0 && len(nonOverlapping(cands)) == len(cands) {
		return cands, nres, ns, nil
	}
	ov.restore(saved)
	var kept []fixCand
	var last Result
	var lastS *Session
	// Last offset first: a kept edit then never moves the text a later
	// candidate (earlier in its file) points at.
	for k := len(cands) - 1; k >= 0; k-- {
		c := cands[k]
		before := ov.snapshot()
		ov.apply([]fixCand{c})
		r, s2, err := check()
		if err != nil {
			ov.restore(before)
			return kept, last, lastS, err
		}
		if len(r.Report.Diags) == 0 {
			kept, last, lastS = append(kept, c), r, s2
			continue
		}
		ov.restore(before)
	}
	return kept, last, lastS, nil
}

// unitBodies calls f with every typed body of the package's own modules
// (lspapi.go's re-check, memoized) and the file text of that body.
func (s *Session) unitBodies(ov *overlay, res Result, f func(b *checker.Body, path, text string)) {
	if s.memo() == nil {
		return
	}
	for _, m := range s.subjectMods() {
		if m.cmod == nil || m.info == nil {
			continue
		}
		for _, id := range s.unitsOf(m) {
			b := s.body(m, id)
			if b == nil || b.File == nil || b.Node == nil {
				continue
			}
			path := s.osPath(res.Sources, b.Node.Span().File, ov)
			if path == "" {
				continue
			}
			ov.mu.Lock()
			text := ov.files[path]
			ov.mu.Unlock()
			f(b, path, text)
		}
	}
}

// quietValue reports whether evaluating e early or late cannot be told
// apart: a literal, a name, a field path from one, or a negated literal.
func quietValue(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.BasicLit, *ast.Ident:
		return true
	case *ast.FieldExpr:
		return e.X != nil && quietValue(e.X)
	case *ast.ParenExpr:
		return quietValue(e.X)
	case *ast.UnaryExpr:
		_, lit := e.X.(*ast.BasicLit)
		return lit && e.Op == ast.TokMinus
	}
	return false
}

// posText is a span's file:line:col for an AppliedFix.
func posText(res Result, sp source.Span) string {
	sf := res.Sources.File(sp.File)
	if sf == nil {
		return "?"
	}
	l, c := lineColOf(sf.Text, sp.Start)
	return sf.Path + ":" + itoaQ(l) + ":" + itoaQ(c)
}

// sortCands puts candidates in source order, file by file.
func sortCands(out []fixCand) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].edits[0].Span.Start < out[j].edits[0].Span.Start
	})
}

// IsRewriteRound reports whether a FixRound phase is a rewrite of a clean
// package (short form or a canonical round): no diagnostic, no code.
func IsRewriteRound(phase string) bool {
	if phase == "short form" || phase == fsInfoPhase || phase == "pkg-sum" {
		return true
	}
	for _, r := range canonicalRounds {
		if r.name == phase {
			return true
		}
	}
	return false
}
