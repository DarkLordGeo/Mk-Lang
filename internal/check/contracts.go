// Why: contract predicates read the function's parameters and result
// through borrowed references and must stay pure expressions; onerr bodies
// may consume at most one linear value. Both need rules of their own in the
// type phase.
//
// What: contractForm (E0101 for `try` or a call taking Io in a requires,
// ensures or invariant predicate), predicateLocals, predicateParam,
// predicateLocal, predDeref and invariantField (how a predicate reads its
// owner's names), onerrValues (E0807).
//
// How: a requires or ensures predicate gets one &P local per owner
// parameter it names, then &R for result; a read of the name goes through
// one AdjDeref, so the expression sees the owner's P. An assignment cannot
// occur, since it is a statement.

package check

import (
	"sort"
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// contractForm is E0101 for a form a predicate may not hold.
func (u *unit) contractForm(at source.Span, what string) {
	u.err(diag.E0101, at, "a contract predicate cannot contain "+what+": it must be a side-effect-free bool expression").In(diag.PhaseTypes).
		Text("reason", "contract")
}

// takesIo reports whether a signature has a parameter (the receiver
// included) whose type is Io, or a reference to one.
func (u *unit) takesIo(s *types.Signature) bool {
	io := u.c.known.Io
	if io == types.NoItem {
		return false
	}
	for _, p := range s.Params {
		t := u.tab.Get(p.Type)
		if t.Kind == types.KRef || t.Kind == types.KMut {
			t = u.tab.Get(t.Elem)
		}
		if t.Kind == types.KNamed && t.Item == io {
			return true
		}
	}
	return false
}

// predicateLocals gives a requires or ensures unit one local per parameter
// of its signature (&P for each owner parameter it names, then
// result: &R), numbered as the signature's ParamIndex.
func (u *unit) predicateLocals() {
	n := 1 + len(u.sig.Params)
	for len(u.b.Locals) < n {
		u.b.Locals = append(u.b.Locals, Local{Type: types.NoType})
	}
	for i, p := range u.sig.Params {
		u.b.Locals[i+1] = Local{Name: p.Name, Type: p.Type, Param: types.ParamIndex(i + 1), Span: p.Span}
	}
}

// predicateParam reads, inside a requires or ensures predicate, a name that
// resolve bound to the owner function's parameter or to `result` (RefParam
// or RefResult with B the owner): the predicate's own &P parameter, read
// through one AdjDeref, so the expression sees the owner's P
// (`requires b != 0`).
func (u *unit) predicateParam(e ast.Expr, r resolve.Ref) (types.TypeID, bool) {
	t, id, ok := u.predicateLocal(r)
	if !ok || id == 0 {
		return t, ok
	}
	*u.b.use(e.ID()) = Use{Kind: UseLocal, Local: id}
	*u.b.adjust(e.ID()) = Adjust{Kind: AdjDeref, Deref: 1}
	return u.nt(t), true
}

// predDeref is 1 when x reads a predicate's own &P parameter
// (predicateParam records that one AdjDeref on x), else 0. A member access
// or a method receiver on x records its own Adjust on x, which replaces
// that one, so it counts both layers: `requires self.hi < 1000` on a
// `&self` or `mut self` method derefs &P, then P.
func (u *unit) predDeref(x ast.Expr) int {
	if _, ok := u.b.Node.(*ast.Contract); !ok || u.sig == nil {
		return 0
	}
	if us := u.b.use(x.ID()); us.Kind == UseLocal && us.Local >= 1 && int(us.Local) <= len(u.sig.Params) {
		return 1
	}
	return 0
}

// predicateLocal maps r, the owner function's parameter or result inside a
// requires or ensures predicate, to the predicate's own &P parameter: its
// local and P. ok is false outside a predicate or for another item's Ref;
// local 0 (and Poison) when the predicate has no such parameter.
func (u *unit) predicateLocal(r resolve.Ref) (types.TypeID, LocalID, bool) {
	if _, ok := u.b.Node.(*ast.Contract); !ok || u.sig == nil || types.ItemID(r.B) != u.it.Owner {
		return types.NoType, 0, false
	}
	name := source.SymResult
	if r.Kind == resolve.RefParam {
		od, ok := u.c.cfg.Items.Get(u.it.Owner).Decl.(*types.FuncDecl)
		if !ok || r.A == 0 || int(r.A) > len(od.Sig.Params) {
			return types.Poison, 0, true
		}
		name = od.Sig.Params[r.A-1].Name
	}
	for i, p := range u.sig.Params {
		if p.Name != name {
			continue
		}
		if t := u.tab.Get(p.Type); t.Kind == types.KRef {
			return t.Elem, LocalID(i + 1), true
		}
		return p.Type, LocalID(i + 1), true
	}
	return types.Poison, 0, true
}

// invariantField reads a bare field name inside an invariant:
// field A of struct B through the predicate's self: &Self, recorded as a
// UseField with one AdjDeref (self.f).
func (u *unit) invariantField(e ast.Expr, r resolve.Ref) types.TypeID {
	d, ok := u.c.cfg.Items.Get(types.ItemID(r.B)).Decl.(*types.StructDecl)
	if !ok || int(r.A) >= len(d.Fields) || u.sig == nil {
		return types.Poison
	}
	selfRef, via := types.NoType, LocalID(0)
	switch u.b.Node.(type) {
	case *ast.InvariantMember:
		if len(u.sig.Params) == 0 {
			return types.Poison
		}
		selfRef = u.sig.Params[0].Type
	case *ast.FnLit:
		// A literal of the invariant reads the field through its capture
		// of the invariant's self (resolve's useField); never
		// through its own first parameter.
		for i, c := range u.b.Captures {
			if c.Name == source.SymSelf {
				selfRef, via = c.Type, u.capBase+LocalID(i)
				break
			}
		}
	}
	if selfRef == types.NoType || selfRef == types.Poison || u.tab.Kind(selfRef) != types.KRef {
		return types.Poison // no environment: the enclosing unit's error, or the parser's
	}
	*u.b.use(e.ID()) = Use{Kind: UseField, Item: types.ItemID(r.B), Index: r.A, Local: via}
	*u.b.adjust(e.ID()) = Adjust{Kind: AdjDeref, Deref: 1}
	u.x.rec.fields(u.c.cfg.Items.Get(types.ItemID(r.B)).Key, true)
	self := u.tab.Get(selfRef).Elem
	return u.nt(u.c.subst(d.Fields[r.A].Type, u.tab.Get(self).Args, types.NoType))
}

// onerrValues is E0807 (an onerr body may consume one linear value at
// most): the distinct locals of closed Linear type that the onerr body
// uses by value, at the onerr keyword, fact values. A use is by value
// unless it is the operand of & or mut, the
// value of a field access, or a method receiver borrowed as &self or mut
// self; fn literals are excluded.
func (u *unit) onerrValues(s *ast.DeferStmt) {
	var root ast.Node = s.Body
	if s.Body == nil {
		root = s.X
	}
	if root == nil || u.b.Uses == nil {
		return
	}
	borrowed := map[ast.NodeID]bool{}
	var ids []*ast.Ident
	ast.Inspect(root, func(n ast.Node) bool {
		switch n := n.(type) {
		case nil:
			return false
		case *ast.FnLit:
			return false
		case *ast.UnaryExpr:
			if n.Op == ast.TokAmp || n.Op == ast.TokMut {
				if id, ok := n.X.(*ast.Ident); ok {
					borrowed[id.ID()] = true
				}
			}
		case *ast.FieldExpr:
			if id, ok := n.X.(*ast.Ident); ok {
				use := u.b.Uses[u.b.slot(n.ID())]
				adj := Adjust{}
				if u.b.Adjust != nil {
					adj = u.b.Adjust[u.b.slot(id.ID())]
				}
				if use.Kind == UseField || adj.Kind == AdjRefShared || adj.Kind == AdjRefMut {
					borrowed[id.ID()] = true
				}
			}
		case *ast.Ident:
			ids = append(ids, n)
		}
		return true
	})
	// A local the body declares itself (`onerr { g := f; g.Abandon() }`) is
	// not a value the onerr consumes from outside: f is.
	lo, hi := nodeRange(root)
	seen := map[LocalID]bool{}
	var names []string
	for _, id := range ids {
		if borrowed[id.ID()] {
			continue
		}
		use := u.b.Uses[u.b.slot(id.ID())]
		if use.Kind != UseLocal || seen[use.Local] || int(use.Local) >= len(u.b.Locals) {
			continue
		}
		if n := u.b.Locals[use.Local].Node; n >= lo && n <= hi {
			continue
		}
		t := u.b.Locals[use.Local].Type
		if t == types.NoType || t == types.Poison || !u.c.closed(t) || u.c.FactsIn(u.env, t).Kind != types.Linear {
			continue
		}
		seen[use.Local] = true
		names = append(names, u.c.cfg.Interner.Str(u.b.Locals[use.Local].Name))
	}
	if len(names) < 2 {
		return
	}
	sort.Strings(names)
	u.err(diag.E0807, keywordSpan(u.fx, s.Span().Start, ast.TokOnerr), "one `onerr` may consume one linear value; this one consumes "+strings.Join(names, ", ")).
		Fact("values", diag.TextList(names...)).Alt("write one `onerr` per value")
}
