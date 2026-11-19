// Why: the manifest step supplies a field whose value is "", [] or false,
// so writing one out is noise; `mk fix --canonical` removes it.
//
// What: the canonical round for pkg.mk: manifestEmpties finds the named
// arguments of a `pkg.<T>(...)` call (T a struct of std.pkg) whose value is
// the field's empty value (isEmptyValue), and argRemovals and mergeEdits
// turn them into edits.
//
// How: the round runs with the other canonical rounds, so the package must
// still check clean after it (canonical.go applyProven). mk fmt never
// inserts an empty argument.

package driver

import (
	"path/filepath"
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// manifestEmpties proposes the removal of every empty field of the
// package's pkg.mk, one candidate per call (its edits together).
func (s *Session) manifestEmpties(res Result, ov *overlay) []fixCand {
	if s.root.kind != rootPackage || s.ms == nil || !s.ms.pkgOK() || s.w == nil {
		return nil
	}
	p := filepath.Clean(filepath.Join(s.root.dir, "pkg.mk"))
	if !ov.wasRead(p) {
		return nil
	}
	ov.mu.Lock()
	text := ov.files[p]
	ov.mu.Unlock()
	sm := source.NewSourceMap()
	f := sm.Add("pkg.mk", []byte(text))
	if f.Text != text {
		return nil // not normalized as the overlay holds it: leave it
	}
	file, ds := syntax.ParseFile(f, s.w.in, syntax.Options{})
	if len(ds) > 0 || file == nil {
		return nil
	}
	var out []fixCand
	ast.Inspect(file, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return n != nil
		}
		fe, ok := c.Fun.(*ast.FieldExpr)
		if !ok || fe.Name == nil {
			return true
		}
		if x, ok := fe.X.(*ast.Ident); !ok || s.w.in.Str(x.Name) != "pkg" {
			return true
		}
		sd := s.ms.pkgStruct(s.w.in.Str(fe.Name.Name))
		if sd == nil {
			return true
		}
		ftypes := map[string]types.TypeID{}
		for _, fd := range sd.Fields {
			ftypes[s.w.in.Str(fd.Name)] = fd.Type
		}
		var drop []int
		for i, a := range c.Args {
			if a == nil || a.Name == nil {
				return true // positional: not a struct construction
			}
			t, ok := ftypes[s.w.in.Str(a.Name.Name)]
			if ok && s.isEmptyValue(a.Value, t) {
				drop = append(drop, i)
			}
		}
		if len(drop) == 0 || strings.Contains(text[c.Span().Start:c.Span().End], "//") {
			return true // a comment in the call would be lost or misplaced
		}
		edits := argRemovals(text, c, drop)
		l, col := lineColOf(text, c.Span().Start)
		out = append(out, fixCand{path: p, edits: edits,
			applied: AppliedFix{At: s.root.prefix + "pkg.mk:" + itoaQ(l) + ":" + itoaQ(col),
				Why: "a std.pkg field holding its empty value is left out; the manifest step supplies it"}})
		return true
	})
	return out
}

// isEmptyValue reports whether e is the literal empty value of type t.
func (s *Session) isEmptyValue(e ast.Expr, t types.TypeID) bool {
	switch s.ms.mw.tab.Get(t).Kind {
	case types.KStr:
		lit, ok := e.(*ast.BasicLit)
		return ok && lit.Kind == ast.TokStr && lit.Raw == `""`
	case types.KSlice:
		al, ok := e.(*ast.ArrayLit)
		return ok && len(al.Elems) == 0
	case types.KBool:
		id, ok := e.(*ast.Ident)
		return ok && s.w.in.Str(id.Name) == "false"
	}
	return false
}

// argRemovals are the edits that remove the arguments of c at the indexes
// drop (ascending). An argument alone on its line goes with its line; in a
// one-line call it goes with the comma before it, or after it when it is
// the first argument left.
func argRemovals(text string, c *ast.CallExpr, drop []int) []diag.Edit {
	gone := map[int]bool{}
	for _, i := range drop {
		gone[i] = true
	}
	var edits []diag.Edit
	file := c.Span().File
	for _, i := range drop {
		a := c.Args[i].Span()
		ls := int(a.Start)
		for ls > 0 && (text[ls-1] == ' ' || text[ls-1] == '\t') {
			ls--
		}
		le := int(a.End)
		if le < len(text) && text[le] == ',' {
			le++
		}
		for le < len(text) && (text[le] == ' ' || text[le] == '\t') {
			le++
		}
		if (ls == 0 || text[ls-1] == '\n') && le < len(text) && text[le] == '\n' {
			edits = append(edits, diag.Edit{Span: source.Span{File: file, Start: uint32(ls), End: uint32(le + 1)}})
			continue
		}
		// One line: take the separator before it from the previous kept
		// argument, else the one after it.
		prev := -1
		for j := i - 1; j >= 0; j-- {
			if !gone[j] {
				prev = j
				break
			}
		}
		if prev >= 0 {
			edits = append(edits, diag.Edit{Span: source.Span{File: file, Start: c.Args[prev].Span().End, End: a.End}})
			continue
		}
		next := -1
		for j := i + 1; j < len(c.Args); j++ {
			if !gone[j] {
				next = j
				break
			}
		}
		end := a.End
		if next >= 0 {
			end = c.Args[next].Span().Start
		} else if int(end) < len(text) && text[end] == ',' {
			end++
		}
		edits = append(edits, diag.Edit{Span: source.Span{File: file, Start: a.Start, End: end}})
	}
	return mergeEdits(edits)
}

// mergeEdits joins overlapping or touching removals.
func mergeEdits(es []diag.Edit) []diag.Edit {
	var out []diag.Edit
	for _, e := range es {
		if n := len(out); n > 0 && e.Span.Start <= out[n-1].Span.End {
			out[n-1].Span.End = max(out[n-1].Span.End, e.Span.End)
			continue
		}
		out = append(out, e)
	}
	return out
}
