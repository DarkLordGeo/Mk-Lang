// Why: E0601 (a result whose loan source is ambiguous) and E0501 are easiest
// to fix with the `from` clause the body already implies, so the compiler
// offers it.
//
// What: fromClauseFix, the minimal `from` clause for E0601 read from the
// body; FieldClause and recordFieldClause, the `from p.f` fix data for E0501.
//
// How: the clause names, in parameter order, the candidates the body's
// returned values are rooted at (returnRoots, rootParam); when a returned
// value is not a path from a parameter, or the body is absent, it names every
// candidate. The fix is proven for a private function and compiles for an
// exported one; mk fix checks each by recompiling, so a clause the body does
// not satisfy is never applied. It is a suggestion, never a public fact.

package check

import (
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// fromFix is where a `from` clause would be written for one signature.
type fromFix struct {
	// ok is false when no clause is offered (no result, other clauses).
	ok bool
	// at is the zero-length span right after the result type.
	at   source.Span
	body *ast.Block // nil when bodiless
	item types.ItemID
	x    *tctx
}

// fromClauseFix attaches the clause fix to an E0601 diagnostic d whose
// candidates are cands (in parameter order).
func (c *Checker) fromClauseFix(fn flowFn, d *diag.Diagnostic, cands []supplier) {
	f := fn.fix
	if f == nil || !f.ok || len(cands) == 0 {
		return
	}
	pick := cands
	why := "every parameter that could supply the result, as callers see it now"
	if roots, ok := f.returnRoots(); ok {
		var narrow []supplier
		for _, s := range cands {
			if roots[s.param] {
				narrow = append(narrow, s)
			}
		}
		if len(narrow) > 0 && len(narrow) == len(roots) {
			pick = narrow
			why = "the parameters the body's results come from"
		}
	}
	class := diag.Proven
	if fn.sig.Exported {
		// A new clause on an exported function can move errors to its
		// callers.
		class = diag.Compiles
	}
	clause := " from " + strings.Join(c.paramNames(fn.sig, pick), ", ")
	d.Fix(diag.Fix{Class: class, Edits: []diag.Edit{{Span: f.at, Text: clause}}, Why: "write `" + clause[1:] + "`: " + why})
}

// returnRoots is the set of parameters of f's item the body's `return`
// values are rooted at, through fields, indexing, slicing, `&`, `*`, `mut`
// and parentheses. false when the body is absent, returns no value, or
// returns one that is not such a path (a literal's own returns are not the
// body's).
func (f *fromFix) returnRoots() (map[types.ParamIndex]bool, bool) {
	if f.body == nil {
		return nil, false
	}
	roots := map[types.ParamIndex]bool{}
	ok := true
	ast.Inspect(f.body, func(n ast.Node) bool {
		if !ok {
			return false
		}
		switch n := n.(type) {
		case *ast.FnLit:
			return false
		case *ast.ReturnStmt:
			if n.Value == nil {
				return false
			}
			p, rooted := f.rootParam(n.Value)
			if !rooted {
				ok = false
				return false
			}
			roots[p] = true
			return false
		}
		return true
	})
	if !ok || len(roots) == 0 {
		return nil, false
	}
	return roots, true
}

// rootParam is the parameter a place path e starts at.
func (f *fromFix) rootParam(e ast.Expr) (types.ParamIndex, bool) {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.FieldExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.SliceExpr:
			e = x.X
		case *ast.UnaryExpr:
			if x.Op != ast.TokAmp && x.Op != ast.TokStar && x.Op != ast.TokMut {
				return 0, false
			}
			e = x.X
		case *ast.Ident:
			r := f.x.ref(x)
			if r.Kind != resolve.RefParam || types.ItemID(r.B) != f.item {
				return 0, false
			}
			return types.ParamIndex(r.A), true
		default:
			return 0, false
		}
	}
}

// FieldClause is E0501's fix data for function item (`from self.f` when the
// callee body shows it, proven for a private function and compiles for an
// exported one): the clause
// `from p.f` that every `return` of its body shows, the zero-length span
// right after its result type where the clause goes, and whether the
// function is exported (an impl method counts as exported: the interface
// method's flows bound it, E0608). ok is false when the function has a clause
// written, no body, a result no return is a field path of, or a field that
// carries no loan of its own (an inline or `mut` field is p's own bytes).
// borrow reads it through its Env when a loan made by a
// call of item conflicts (E0501); it is a suggestion, never a public fact,
// and mk fix applies it only after a recheck.
func (c *Checker) FieldClause(item types.ItemID) (clause string, at source.Span, exported bool, ok bool) {
	v, found := c.fieldClauses.Load(item)
	if !found {
		return "", source.Span{}, false, false
	}
	fc := v.(fieldClause)
	return fc.clause, fc.at, fc.exported, true
}

type fieldClause struct {
	clause   string
	at       source.Span
	exported bool
}

// recordFieldClause stores j's FieldClause after its flows are settled:
// automatic (no clause written, so j.fix is set), the result holding a
// loan, and every return of the body `p.f` (through indexing, slicing and
// parentheses) for one parameter p and one field f of p's struct type
// whose declared type is a shared reference, a slice or str.
func (c *Checker) recordFieldClause(j *flowJob) {
	f := &j.fix
	if !f.ok || f.body == nil || j.sig.Flows.Declared || len(j.sig.Flows.Sources) == 0 {
		return
	}
	var param types.ParamIndex
	var field source.Symbol
	ok := true
	ast.Inspect(f.body, func(n ast.Node) bool {
		if !ok {
			return false
		}
		switch n := n.(type) {
		case *ast.FnLit:
			return false
		case *ast.ReturnStmt:
			p, fl, rooted := f.fieldPath(n.Value)
			if !rooted || param != 0 && (p != param || fl != field) {
				ok = false
				return false
			}
			param, field = p, fl
			return false
		}
		return true
	})
	if !ok || param == 0 || int(param) > len(j.sig.Params) {
		return
	}
	pt := j.sig.Params[param-1].Type
	for {
		k := c.cfg.Types.Kind(pt)
		if k != types.KRef && k != types.KMut {
			break
		}
		pt = c.cfg.Types.Get(pt).Elem
	}
	if k := c.cfg.Types.Kind(pt); k == types.KSelf {
		pt = selfTypeOf(c, j.it)
	}
	ty := c.cfg.Types.Get(pt)
	if ty.Kind != types.KNamed {
		return
	}
	sd, isStruct := c.cfg.Items.Get(ty.Item).Decl.(*types.StructDecl)
	if !isStruct {
		return
	}
	for _, fd := range sd.Fields {
		if fd.Name != field {
			continue
		}
		switch c.cfg.Types.Kind(fd.Type) {
		case types.KRef, types.KSlice, types.KStr:
			name := c.cfg.Interner.Str(j.sig.Params[param-1].Name)
			// An impl method's flows answer to the interface method's
			// (E0608), so its clause is not proven by the package's
			// callers recompiling: it is offered as an exported one's.
			impl := j.it.Owner != types.NoItem && c.cfg.Items.Get(j.it.Owner).Kind == types.KindImpl
			c.fieldClauses.Store(j.it.ID, fieldClause{clause: "from " + name + "." + c.cfg.Interner.Str(field), at: f.at, exported: j.sig.Exported || impl})
		}
		return
	}
}

// selfTypeOf is the named type a method's Self stands for: its owner's
// type for a `Type.Name` method, NoType otherwise.
func selfTypeOf(c *Checker, it *types.Item) types.TypeID {
	if it.Owner == types.NoItem {
		return types.NoType
	}
	own := c.cfg.Items.Get(it.Owner)
	if _, ok := own.Decl.(*types.StructDecl); ok {
		return c.cfg.Types.Named(it.Owner, nil)
	}
	return types.NoType
}

// fieldPath is the parameter and the one field a returned place path
// `p.f` starts with, under indexing, slicing and parentheses.
func (f *fromFix) fieldPath(e ast.Expr) (types.ParamIndex, source.Symbol, bool) {
	for e != nil {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.SliceExpr:
			e = x.X
		case *ast.FieldExpr:
			id, ok := x.X.(*ast.Ident)
			if !ok {
				return 0, 0, false
			}
			r := f.x.ref(id)
			if r.Kind != resolve.RefParam || types.ItemID(r.B) != f.item {
				return 0, 0, false
			}
			return types.ParamIndex(r.A), x.Name.Name, true
		default:
			return 0, 0, false
		}
	}
	return 0, 0, false
}
