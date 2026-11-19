// Why: the release protocol (a container of linear elements must release
// them, never drop them) makes some older code E0701; where a rewrite
// keeps the code's behaviour, mk fix --canonical should make it.
//
// What: the release round of mk fix --canonical: releaseFixes, which
// proposes the edits, releaseKept, which judges each one, and the helpers
// freshPair, namesSym, lineIndent and releaseOnly.
//
// How: each candidate is kept only when recompiling is progress (the fix
// loop's `better`):
//   - `c.Free(a)` on a container of linear elements that are not Free
//     becomes `c.FreeEmpty(a)` (check's E0701 fix, ReleaseFreeEmptyWhy):
//     the older Free panicked on such contents as FreeEmpty does;
//   - `try m.Set(a, k, v)` that is E0701 becomes
//     `old := try m.Replace(a, k, v)` then `if o := old { o.Free(a) }`,
//     which compiles only when the replaced value has a Free to call;
//     a body that already names old or o gets old2 and o2, and so on.
// Anything else keeps its E0701, whose alternatives name the protocol.

package driver

import (
	"strconv"
	"strings"

	"mk/internal/ast"
	checker "mk/internal/check"
	"mk/internal/diag"
	"mk/internal/source"
)

// releasePhase is the round's name in FixRound.Phase.
const releasePhase = "release"

// releaseFixes are the release round's candidates over a report that is
// not clean: one per E0701 the round can rewrite.
func (s *Session) releaseFixes(res Result, ov *overlay) []fixCand {
	var out []fixCand
	sets := map[source.Span]*diag.Diagnostic{} // E0701 at a Set's name
	for _, e := range res.Report.Diags {
		d := e.Diag
		if d.Code != diag.E0701 {
			continue
		}
		sets[d.At] = d
		for _, f := range d.Fixes {
			if f.Class != diag.Compiles || f.Why != checker.ReleaseFreeEmptyWhy || len(f.Edits) != 1 {
				continue
			}
			path := s.osPath(res.Sources, f.Edits[0].Span.File, ov)
			if path == "" {
				continue
			}
			out = append(out, fixCand{path: path, edits: f.Edits, d: d,
				applied: AppliedFix{Code: d.Code, At: posText(res, d.At), Why: f.Why}})
		}
	}
	if len(sets) == 0 || s.w == nil || s.w.in == nil {
		return out
	}
	set, ok := s.w.in.Lookup("Set")
	if !ok {
		return out
	}
	s.unitBodies(ov, res, func(b *checker.Body, path, text string) {
		// The rewrite binds two names the body names nowhere: old and o,
		// else old2 and o2, and so on. A body rewritten once names old,
		// so its next Set (the next round's, one release fix per round)
		// takes old2.
		old, o := s.freshPair(b.Node)
		if old == "" {
			return
		}
		ast.Inspect(b.Node, func(n ast.Node) bool {
			blk, ok := n.(*ast.Block)
			if !ok || blk == nil {
				return n != nil
			}
			for _, st := range blk.Stmts {
				es, ok := st.(*ast.ExprStmt)
				if !ok {
					continue
				}
				try, ok := es.X.(*ast.UnaryExpr)
				if !ok || try.Op != ast.TokTry {
					continue
				}
				call, ok := try.X.(*ast.CallExpr)
				// The allocator is written twice: only a name or a path
				// from one, whose second evaluation cannot be seen.
				if !ok || len(call.Args) != 3 || call.Args[0].Name != nil || !quietValue(call.Args[0].Value) {
					continue
				}
				fe, ok := call.Fun.(*ast.FieldExpr)
				if !ok || fe.Name == nil || fe.Name.Name != set {
					continue
				}
				d := sets[fe.Name.Span()]
				if d == nil {
					continue
				}
				sp := es.Span()
				if int(sp.End) > len(text) || strings.Contains(text[sp.Start:sp.End], "//") {
					continue // a comment inside would be lost
				}
				nsp, csp := fe.Name.Span(), call.Span()
				a := call.Args[0].Span()
				indent := lineIndent(text, sp.Start)
				repl := old + " := " + text[sp.Start:nsp.Start] + "Replace" + text[nsp.End:csp.End] +
					text[csp.End:sp.End] + "\n" + indent + "if " + o + " := " + old + " {\n" + indent + "    " + o + ".Free(" + text[a.Start:a.End] + ")\n" + indent + "}"
				out = append(out, fixCand{path: path, d: d, edits: []diag.Edit{{Span: sp, Text: repl}},
					applied: AppliedFix{Code: d.Code, At: posText(res, d.At), Why: "a Set that would drop a value is Replace, and the old value is freed"}})
			}
			return true
		})
	})
	sortCands(out)
	return out
}

// lineIndent is the run of blanks that starts the line holding off.
func lineIndent(text string, off uint32) string {
	start := strings.LastIndexByte(text[:off], '\n') + 1
	end := start
	for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
		end++
	}
	return text[start:end]
}

// releaseOnly reports whether every candidate of set is the release
// round's (it names the fix loop's round).
func releaseOnly(set []fixCand) bool {
	for _, c := range set {
		if c.d == nil || c.d.Code != diag.E0701 {
			return false
		}
	}
	return len(set) > 0
}

// freshPair is the release rewrite's two names for body n: old and o, or
// the first oldK and oK (K from 2) that no identifier under n spells, so
// the rewrite shadows and clashes with nothing the body names. Empty past
// a bound no body reaches.
func (s *Session) freshPair(n ast.Node) (string, string) {
	free := func(x string) bool {
		sym, ok := s.w.in.Lookup(x)
		return !ok || !namesSym(n, sym)
	}
	for k := 1; k <= 64; k++ {
		suf := ""
		if k > 1 {
			suf = strconv.Itoa(k)
		}
		if old, o := "old"+suf, "o"+suf; free(old) && free(o) {
			return old, o
		}
	}
	return "", ""
}

// namesSym reports whether an identifier under n is sym.
func namesSym(n ast.Node, sym source.Symbol) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if id, ok := m.(*ast.Ident); ok && id != nil && id.Name == sym {
			found = true
		}
		return !found && m != nil
	})
	return found
}

// releaseKept is the release round's acceptance of one candidate c applied
// alone (the fix loop tries each alone): progress, and no diagnostic of
// the result on the lines the rewrite wrote, so a rewrite that trades its
// E0701 for another error there is never kept.
func (s *Session) releaseKept(c fixCand, after Result, ov *overlay) bool {
	ov.mu.Lock()
	text := ov.files[c.path]
	ov.mu.Unlock()
	e := c.edits[0]
	lo := uint32(strings.LastIndexByte(text[:min(int(e.Span.Start), len(text))], nl) + 1)
	end := min(int(e.Span.Start)+len(e.Text), len(text))
	hi := uint32(len(text))
	if k := strings.IndexByte(text[end:], nl); k >= 0 {
		hi = uint32(end + k)
	}
	for _, x := range after.Report.Diags {
		at := x.Diag.At
		if at.Start >= lo && at.Start <= hi && s.osPath(after.Sources, at.File, ov) == c.path {
			return false
		}
	}
	return true
}

// nl is a line end.
const nl = '\n'
