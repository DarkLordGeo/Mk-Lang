// Why: a programmer coming from another language writes `null`, `nil`,
// `Some`, `Ok` or a synonym of a real name; one line can hold several such
// habits, and `mk fix` should repair the whole line in one pass.
//
// What: habit and mergeHabits (the name-shaped habits resolve detects:
// E1209 `null` `nil`, E1221 synonym names, E0207 for `Some Ok Err None`),
// lineOf, lineTarget and dropOverlapping.
//
// How: a line gets one diagnostic, with the leftmost habit's code, every
// habit code of the line in facts.habits and the line rewritten by the
// proven edits in facts.target. The format matches the parser's
// (internal/syntax/habit.go), so `mk fix` merges the rounds of both phases.

package resolve

import (
	"sort"
	"strings"

	"mk/internal/diag"
	"mk/internal/source"
)

// habit marks a diagnostic of the current task as a habit, merged per line
// when the task ends.
func (w *walker) habit(d *diag.Diagnostic) { w.t.habits = append(w.t.habits, d) }

// mergeHabits merges the habit diagnostics among ds per (file, line): the
// leftmost stays, with facts habits and target, the proven edits and the
// alternatives of the whole line; the others are dropped, and a cause that
// named a dropped one names the kept one. It returns ds without the dropped.
func (m *Module) mergeHabits(ds, habits []*diag.Diagnostic) []*diag.Diagnostic {
	if len(habits) == 0 {
		return ds
	}
	type lineKey struct {
		file source.FileID
		line uint32
		// solo keeps a diagnostic whose line is not known (no
		// Config.Sources) in a group of its own: nothing is merged then.
		solo int
	}
	groups := map[lineKey][]*diag.Diagnostic{}
	var order []lineKey
	for i, d := range habits {
		k := lineKey{file: d.At.File, line: m.lineOf(d.At)}
		if k.line == 0 {
			k.solo = i + 1
		}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], d)
	}
	dropped := map[*diag.Diagnostic]*diag.Diagnostic{}
	for _, k := range order {
		g := groups[k]
		sort.SliceStable(g, func(i, j int) bool {
			if g[i].At.Start != g[j].At.Start {
				return g[i].At.Start < g[j].At.Start
			}
			return g[i].Code < g[j].Code
		})
		keep := g[0]
		var codes []string
		seen := map[diag.Code]bool{}
		var edits []diag.Edit
		for _, d := range g {
			if !seen[d.Code] {
				seen[d.Code] = true
				codes = append(codes, d.Code.String())
			}
			for _, f := range d.Fixes {
				if f.Class == diag.Proven {
					edits = append(edits, f.Edits...)
				}
			}
		}
		keep.Fact("habits", diag.TextList(codes...))
		if t, ok := m.lineTarget(keep.At, k.line, edits); ok {
			keep.Text("target", t)
		}
		if len(g) == 1 {
			continue
		}
		keep.Fixes = nil
		if len(edits) > 0 {
			keep.Fix(diag.Fix{Class: diag.Proven, Edits: dropOverlapping(edits), Why: "rewrite the habits of the line as Mk"})
		}
		alts := map[string]bool{}
		for _, a := range keep.Alternatives {
			alts[a] = true
		}
		for _, d := range g[1:] {
			for _, a := range d.Alternatives {
				if !alts[a] {
					alts[a] = true
					keep.Alt(a)
				}
			}
			dropped[d] = keep
		}
	}
	if len(dropped) == 0 {
		return ds
	}
	remap := map[diag.Cause]diag.Cause{}
	for d, k := range dropped {
		remap[diag.Cause{Code: d.Code, At: d.At}] = diag.Cause{Code: k.Code, At: k.At}
	}
	out := ds[:0]
	for _, d := range ds {
		if dropped[d] != nil {
			continue
		}
		if d.Cause != nil {
			if c, ok := remap[*d.Cause]; ok {
				if c.Code == d.Code && c.At == d.At {
					d.Cause = nil
				} else {
					d.ConsequenceOf(c.Code, c.At)
				}
			}
		}
		out = append(out, d)
	}
	return out
}

// lineOf is the 1-based line of a span's start, 0 when the file's text is
// not known (no Config.Sources).
func (m *Module) lineOf(s source.Span) uint32 {
	if f := m.sourceFile(s.File); f != nil && s.Start <= f.Len() {
		return f.LineCol(s.Start).Line
	}
	return 0
}

func (m *Module) sourceFile(id source.FileID) *source.File {
	if m.s.cfg.Sources == nil || id == source.NoFile {
		return nil
	}
	return m.s.cfg.Sources.File(id)
}

// lineTarget rewrites one line by the edits that lie on it; an
// import line added elsewhere stays in the fix only. ok is false when no
// edit touches the line or the text is not known.
func (m *Module) lineTarget(at source.Span, line uint32, edits []diag.Edit) (string, bool) {
	f := m.sourceFile(at.File)
	if f == nil || line == 0 {
		return "", false
	}
	start := f.LineStart(int(line))
	text := f.Line(int(line))
	end := start + uint32(len(text))
	var on []diag.Edit
	for _, e := range dropOverlapping(edits) {
		if e.Span.File == at.File && e.Span.Start >= start && e.Span.End <= end {
			on = append(on, e)
		}
	}
	if len(on) == 0 {
		return "", false
	}
	var b strings.Builder
	pos := start
	for _, e := range on {
		b.WriteString(text[pos-start : e.Span.Start-start])
		b.WriteString(e.Text)
		pos = e.Span.End
	}
	b.WriteString(text[pos-start:])
	return b.String(), true
}

// dropOverlapping sorts edits by (file, offset) and drops any that overlaps
// an earlier one or repeats it (two `Rc` on a line add one import).
func dropOverlapping(es []diag.Edit) []diag.Edit {
	sorted := append([]diag.Edit(nil), es...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i].Span, sorted[j].Span
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return a.End < b.End
	})
	var out []diag.Edit
	for _, e := range sorted {
		if n := len(out); n > 0 && (out[n-1] == e || out[n-1].Span.File == e.Span.File && e.Span.Start < out[n-1].Span.End) {
			continue
		}
		out = append(out, e)
	}
	return out
}
