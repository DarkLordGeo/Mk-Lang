// Why: 1.2's Method.Flows() was a bool; 1.3 returns the clauses as []Flow
// and the bool is Method.HasFlows(). Code that used the bool can be moved
// mechanically.
//
// What: the round hasFlowsFixes, with flowsCalls, boundFlows and the
// identifier helpers.
//
// How: a call of Flows() that a package uses as a bool is a diagnostic
// that names []Flow: E0301 where a bool is expected (`if m.Flows()`, a bool
// binding or result) or E0308 for an operator a list lacks (`a ||
// m.Flows()`, `!m.Flows()`). The round renames each `.Flows()` call on the
// lines such a diagnostic covers to `.HasFlows()`, except a
// call whose result is indexed or has a member read there. A call used as
// a list checks clean and is left as it is. Like the other rewrite
// rounds (fix_become.go), its candidates join the proven fixes while the
// package has diagnostics, and the fix loop keeps them only when its
// recompile shows progress.

package driver

import (
	"strings"

	"mk/internal/diag"
	"mk/internal/source"
)

// hasFlowsWhy is the AppliedFix reason of the rename.
const hasFlowsWhy = "Method.Flows() returns the clauses from 1.3: its bool form is Method.HasFlows()"

// hasFlowsFixes are the round's candidates, one per diagnostic that names
// []Flow where a bool belongs.
func (s *Session) hasFlowsFixes(res Result, ov *overlay) []fixCand {
	if res.Sources == nil {
		return nil
	}
	var out []fixCand
	seen := map[source.Span]bool{}
	for _, e := range res.Report.Diags {
		d := e.Diag
		if d == nil || (d.Code != diag.E0301 && d.Code != diag.E0308) || !strings.Contains(d.Message, "[]Flow") {
			continue
		}
		f := res.Sources.File(d.At.File)
		if f == nil || int(d.At.End) > len(f.Text) || d.At.Start > d.At.End {
			continue
		}
		path := s.osPath(res.Sources, f.ID, ov)
		if path == "" {
			continue
		}
		lo := strings.LastIndexByte(f.Text[:d.At.Start], '\n') + 1
		hi := len(f.Text)
		if i := strings.IndexByte(f.Text[d.At.End:], '\n'); i >= 0 {
			hi = int(d.At.End) + i
		}
		var edits []diag.Edit
		calls := flowsCalls(f.Text[lo:hi])
		if len(calls) == 0 {
			// A local bound to the call and used as a bool (`b :=
			// m.Flows()` then `if b`): the diagnostic is at the name, the
			// call on the binding's line above it.
			if blo, at, ok := boundFlows(f.Text[:d.At.Start], f.Text[d.At.Start:d.At.End]); ok {
				lo, calls = blo, []int{at}
			}
		}
		for _, at := range calls {
			sp := source.Span{File: f.ID, Start: uint32(lo + at), End: uint32(lo + at + len("Flows"))}
			if !seen[sp] {
				seen[sp] = true
				edits = append(edits, diag.Edit{Span: sp, Text: "HasFlows"})
			}
		}
		if len(edits) > 0 {
			out = append(out, fixCand{path: path, edits: edits, d: d, applied: AppliedFix{At: posText(res, edits[0].Span), Why: hasFlowsWhy}})
		}
	}
	return out
}

// flowsCalls are the offsets of Flows in each `.Flows()` call of text (no
// arguments, blanks allowed inside the parentheses).
func flowsCalls(text string) []int {
	var out []int
	for i := 0; ; {
		j := strings.Index(text[i:], ".Flows")
		if j < 0 {
			return out
		}
		at := i + j + 1
		k := at + len("Flows")
		i = k
		if k < len(text) && (isIdentByte(text[k])) {
			continue // .FlowsOf, not Flows
		}
		rest := strings.TrimLeft(text[k:], " \t")
		if !strings.HasPrefix(rest, "(") {
			continue
		}
		if after, ok := strings.CutPrefix(strings.TrimLeft(rest[1:], " \t"), ")"); ok {
			// A call whose result is indexed or has a member read
			// (`m.Flows().Len()`, `m.Flows()[0]`) is a list use: a bool
			// has neither, so it stays as it is even on the line of a
			// bool use.
			if next := strings.TrimLeft(after, " \t"); !strings.HasPrefix(next, ".") && !strings.HasPrefix(next, "[") {
				out = append(out, at)
			}
		}
	}
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// boundFlows finds, for a diagnostic whose text is a local's name, the
// nearest line above it that binds that name to a `.Flows()` call
// (`name := x.Flows()`): the line's start and the call's offset in it.
func boundFlows(before, name string) (int, int, bool) {
	name = strings.TrimSpace(name)
	if name == "" || !isIdentName(name) {
		return 0, 0, false
	}
	for end := len(before); end > 0; {
		lo := strings.LastIndexByte(before[:end], '\n') + 1
		line := before[lo:end]
		end = max(lo-1, 0)
		t := strings.TrimLeft(line, " \t")
		t = strings.TrimPrefix(t, "var ")
		rest, ok := strings.CutPrefix(t, name)
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, ":=") {
			continue
		}
		if calls := flowsCalls(line); len(calls) == 1 {
			return lo, calls[0], true
		}
		return 0, 0, false // the nearest binding of the name is something else
	}
	return 0, 0, false
}

func isIdentName(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isIdentByte(s[i]) {
			return false
		}
	}
	return !(s[0] >= '0' && s[0] <= '9')
}
