// Why: inside a generic, a callee's compile-time requires may name a const
// generic of the caller (std.simd `w.With(0, x)` in a body generic over N,
// clause `i >= 0 && i < N`); with no value for N it cannot be evaluated,
// yet a caller whose own clauses pin N to a few values should not have to
// restate it.
//
// What: finiteRequires, with finiteSet, listedSet and impliedSet (the values
// the caller's clauses allow), ownClauses, foldOwn, ownExcludes, standIns
// and the small helpers below.
//
// How: when the caller's own clauses restrict each such const generic to at
// most 64 values, the clause is evaluated for every combination and the
// call is accepted when it holds for all; otherwise the caller restates it,
// else E0706 with the clause as a fix.

package check

import (
	"math/big"
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// maxFiniteValues bounds one const generic's set;
// maxFiniteCombos bounds the evaluations of one clause at one call.
const (
	maxFiniteValues = 64
	maxFiniteCombos = 4096
)

// finiteRequires judges the clause ct (predicate pred, over const
// value parameters) of a call inside a generic: every value argument folds
// and the clause names a const generic of the callee whose argument is a
// const generic of the caller. True when the clause was judged so, either
// accepted (it holds for every value the caller's clauses allow) or handed
// to requireRestated; false leaves it to the other rules (an argument that
// does not fold, a clause that names no forwarded generic). at is the node
// reported: the call, or with call nil a vector operator or a fn value
// where a clause with a value parameter is left to the other
// rules. Inside a fn literal the caller's clauses are its function's
// (ownerSig).
//
// unj is non-nil when the clause also reads an unspelled closed value (an
// imported callee's enum value, finiteOverValues): the restated text would
// keep the callee's own names for it (`D == Mode.A`), which the caller's
// clause may match by text while meaning something else, so every demand
// to restate is E0706 "cannot be judged here"
// instead, once per call (*unj records it).
func (u *unit) finiteRequires(at ast.Node, call *ast.CallExpr, rest []types.Param, sig *types.Signature, gargs []types.GenericArg, ct types.Contract, pred *types.Item, gsub map[source.Symbol]string, unj *bool) bool {
	if ct.Text == "" || u.sig == nil || u.fx == nil {
		return false
	}
	pd, ok := pred.Decl.(*types.FuncDecl)
	if !ok {
		return false
	}
	if call == nil && len(pd.Sig.Params) > 0 {
		return false
	}
	sub := make(map[source.Symbol]string, len(gsub)+len(pd.Sig.Params))
	for k, v := range gsub {
		sub[k] = v
	}
	// A value argument that does not fold but reads only the caller's const
	// generics (`F(k, t)` in `G[const k: int]`) is folded at each
	// combination with them bound (argGens); valOpens are their names.
	var valOpens []source.Symbol
	_, own := u.ownerSig()
	for _, pp := range pd.Sig.Params {
		arg, isConst := constArgExpr(call, rest, pp.Name)
		if !isConst {
			return false // a run-time contract
		}
		if _, folds := u.requiresArg(call, rest, pp.Name); !folds {
			gs := u.ownConstGenerics(arg)
			if gs == nil || u.namesOwnConst(arg) {
				return false
			}
			for _, g := range gs {
				valOpens = append(valOpens, own.Generics[g].Name)
			}
		}
		text, ok := u.argText(arg)
		if !ok {
			return false
		}
		sub[pp.Name] = text
	}
	// The caller's const generics bound to a callee const generic the
	// clause names, one entry per caller generic with every callee position
	// it is bound to (`S2[M, M]` gives each combination one M), and
	// those a value argument reads (at empty).
	type open struct {
		param uint32 // the caller's const generic
		at    []int  // indexes in sig.Generics and gargs
		vals  []*big.Int
	}
	var opens []open
	for _, name := range valOpens {
		p := -1
		for i, g := range u.x.gens {
			if g.Const && g.Name == name {
				p = i
			}
		}
		if p < 0 {
			return false
		}
		k := 0
		for k < len(opens) && opens[k].param != uint32(p) {
			k++
		}
		if k == len(opens) {
			opens = append(opens, open{param: uint32(p)})
		}
	}
	for i, g := range sig.Generics {
		if !g.Const || i >= len(gargs) || !gargs[i].IsConst || gargs[i].Const.Kind != types.CAParam {
			continue
		}
		if !namesWord(ct.Text, u.c.cfg.Interner.Str(g.Name)) {
			continue
		}
		p := gargs[i].Const.Param
		k := 0
		for k < len(opens) && opens[k].param != p {
			k++
		}
		if k == len(opens) {
			opens = append(opens, open{param: p})
		}
		opens[k].at = append(opens[k].at, i)
	}
	if len(opens) == 0 {
		return false
	}
	clause := u.demandOf(ct, sig, sub)
	demand := func(why string) {
		if unj == nil {
			u.requireRestatedWhy(at, clause, why)
			return
		}
		if !*unj {
			if why == "" {
				why = "the caller's clauses do not limit the const generics it forwards to at most 64 values each, at all of which it holds"
			}
			u.unjudgeableWhy(at, call, ct, "it reads an argument no clause can spell, and "+why)
			*unj = true
		}
	}
	combos := 1
	for k := range opens {
		if int(opens[k].param) >= len(u.x.gens) {
			return false
		}
		vals, ok := u.finiteSet(u.x.gens[opens[k].param].Name)
		if !ok {
			demand("")
			return true
		}
		opens[k].vals = vals
		if combos *= len(vals); combos > maxFiniteCombos {
			// A bound on the evaluations at one call, which the language
			// does not state yet.
			demand("the caller's clauses allow more than 4,096 combinations of its const generics")
			return true
		}
	}
	if combos == 0 {
		return true // the caller's clauses allow no value: it has no instance
	}
	// An imported clause runs in the evaluator, which takes closed
	// generic arguments only; a same-module one is folded and reads only
	// the generics it names.
	same := pred.Module == u.m.ID
	args := append([]types.GenericArg(nil), gargs...)
	if !same && !u.standIns(args, sig, ct, pd) {
		demand("")
		return true
	}
	// A combination at which one of the caller's own clauses folds
	// false is no instance of the caller (that clause is E0706 at its
	// site), so it is skipped; one where none folds false is judged.
	own, cs := u.ownClauses()
	slot := make([]int, len(opens)) // each open's index in own.Generics
	for k := range opens {
		slot[k] = genericIndex(own, u.x.gens[opens[k].param].Name)
	}
	idx := make([]int, len(opens))
	for {
		bind := map[uint32]fval{}
		for k, o := range opens {
			if slot[k] >= 0 {
				bind[uint32(slot[k])] = fval{n: o.vals[idx[k]], t: own.Generics[slot[k]].Type}
			}
		}
		if !u.ownExcludes(cs, bind) {
			for k, o := range opens {
				for _, i := range o.at {
					t := sig.Generics[i].Type
					id, ok := u.c.internFolded(fval{n: o.vals[idx[k]], t: t}, t)
					if !ok {
						demand("")
						return true
					}
					args[i] = types.GenericArg{IsConst: true, Const: types.ConstArg{Kind: types.CAValue, Value: id, Type: t}}
				}
			}
			if !same && !u.closedArgs(args) {
				demand("")
				return true
			}
			// Every combination must be known to hold. One the evaluator
			// refuses (a run-time operation) or that does not fold is
			// unknown, never accepted, so the caller restates the clause:
			// `(N == 0 && Now() > 0) || N == 1` over N in {0, 1, 2}.
			var holds, known bool
			u.x.argGens = bind // a value argument over the caller's generics folds at this combination
			if same {
				holds, known, _ = u.foldedClause(at, call, sig, rest, args, pred)
				u.x.argGens = nil
			} else {
				var d *diag.Diagnostic
				holds, known, d, _ = u.evalClause(at, call, rest, ct, pd, args)
				u.x.argGens = nil
				if d != nil && u.c.cfg.Eval == nil {
					// A build with no evaluator (check's own tests) reports
					// the refusal, as a concrete call does, in place of a
					// restate demand the clause would not get from mk; a
					// caller that states the clause gets neither.
					if unj != nil || !u.restateMet(clause) {
						u.b.Diags = append(u.b.Diags, d)
					}
					return true
				}
			}
			if !known || !holds {
				demand("")
				return true
			}
		}
		k := 0
		for ; k < len(opens); k++ {
			idx[k]++
			if idx[k] < len(opens[k].vals) {
				break
			}
			idx[k] = 0
		}
		if k == len(opens) {
			return true // it holds for every combination left: accepted
		}
	}
}

// genericIndex is the index of the const generic named name in sig, or -1.
func genericIndex(sig *types.Signature, name source.Symbol) int {
	if sig == nil {
		return -1
	}
	for i, g := range sig.Generics {
		if g.Const && g.Name == name {
			return i
		}
	}
	return -1
}

// ownClause is one of the caller's own requires, as finiteSet and
// ownExcludes read it: its expression, the file it is in, its predicate.
type ownClause struct {
	x    ast.Expr
	fx   *fileIndex
	pred *types.Item
}

// ownClauses are the requires of the function the body belongs to
// (ownerSig: a fn literal's or a contract's function) that are in
// this module and have an expression; nil for an invariant.
func (u *unit) ownClauses() (*types.Signature, []ownClause) {
	_, sig := u.ownerSig()
	if sig == nil {
		return nil, nil
	}
	var cs []ownClause
	for _, ct := range sig.Contracts {
		if ct.Kind != types.Requires {
			continue
		}
		pred := u.c.cfg.Items.Get(ct.Body)
		if pred.Module != u.m.ID || u.m.byItem[pred.ID] == nil {
			continue
		}
		n, fx := u.m.nodeOf(pred.ID)
		c, ok := n.(*ast.Contract)
		if !ok || c.X == nil {
			continue
		}
		cs = append(cs, ownClause{x: c.X, fx: fx, pred: pred})
	}
	return sig, cs
}

// foldOwn folds x, from one of the caller's own clauses (predicate pred,
// file fx), with the caller's const generics in gens (by index) bound and
// nothing else: a parameter, another generic or a call does not fold.
// Nothing is reported, a panic included.
func (u *unit) foldOwn(x ast.Expr, fx *fileIndex, pred *types.Item, gens map[uint32]fval) (fval, bool) {
	// gens is keyed by the index of a generic of pred's function, so a
	// generic of another owner (a receiver type's, read by index) must
	// not reach it: such a clause does not fold, as foldedClause's bind
	// refuses it.
	foreign := false
	ast.Inspect(x, func(n ast.Node) bool {
		if n == nil || foreign {
			return false
		}
		if id := n.ID(); int(id) < len(fx.info.Refs) {
			r := fx.info.Refs[id]
			foreign = r.Kind == resolve.RefTypeParam && types.ItemID(r.B) != pred.Owner && types.ItemID(r.B) != pred.ID
		}
		return !foreign
	})
	if foreign {
		return fval{}, false
	}
	f := &folder{m: u.m, fx: fx, rec: u.x.rec, params: map[uint32]fval{}, gens: gens, enums: true}
	v, ok, _ := f.fold(x)
	return v, ok
}

// ownExcludes reports whether one of the caller's own clauses folds false
// with the const generics in gens bound; a clause that reads
// anything else does not fold and excludes nothing.
func (u *unit) ownExcludes(cs []ownClause, gens map[uint32]fval) bool {
	if len(gens) == 0 {
		return false
	}
	for _, c := range cs {
		if v, ok := u.foldOwn(c.x, c.fx, c.pred, gens); ok && v.isBool && !v.b {
			return true
		}
	}
	return false
}

// standIns closes, for the evaluator, the open type arguments of an
// imported callee's generics that clause ct cannot read: a type
// generic the clause does not name, which no const parameter's type
// mentions, gets int in its place, since nothing the clause evaluates
// depends on it. False when an open type argument is one the clause can
// read; the clause then depends on the caller's T and is restated.
func (u *unit) standIns(args []types.GenericArg, sig *types.Signature, ct types.Contract, pd *types.FuncDecl) bool {
	for _, pp := range pd.Sig.Params {
		if !u.c.closed(u.c.subst(pp.Type, args, types.NoType)) {
			return false
		}
	}
	for i, g := range sig.Generics {
		if i >= len(args) || args[i].IsConst != g.Const {
			continue
		}
		if !g.Const && u.c.closed(args[i].Type) || g.Const && (args[i].Const.Kind == types.CAValue && u.c.closedConst(args[i].Const) || args[i].Const.Kind == types.CAParam && namesWord(ct.Text, u.c.cfg.Interner.Str(g.Name))) {
			continue // closed, or one of the opens the loop binds
		}
		// The clause's text (exprText) spells names, literals, operators,
		// fields and calls; any other form, `SizeOf[T]()` among them, is
		// "...", which may read any generic.
		if strings.Contains(ct.Text, "...") || namesWord(ct.Text, u.c.cfg.Interner.Str(g.Name)) {
			return false
		}
		if !g.Const {
			args[i] = types.GenericArg{Type: types.Int}
			continue
		}
		id, ok := u.c.internFolded(fval{n: new(big.Int), t: g.Type}, g.Type)
		if !ok {
			return false
		}
		args[i] = types.GenericArg{IsConst: true, Const: types.ConstArg{Kind: types.CAValue, Value: id, Type: g.Type}}
	}
	return true
}

// closedArgs reports whether every generic argument is closed: a closed
// type, or a closed const value.
func (u *unit) closedArgs(args []types.GenericArg) bool {
	for _, a := range args {
		if !a.IsConst && !u.c.closed(a.Type) || a.IsConst && (a.Const.Kind != types.CAValue || !u.c.closedConst(a.Const)) {
			return false
		}
	}
	return true
}

// finiteSet is the set of values the caller's own requires allow for its
// const generic named name: the intersection of every listed set
// (listedSet) and every implied one (impliedSet). False when
// no clause bounds it.
func (u *unit) finiteSet(name source.Symbol) ([]*big.Int, bool) {
	set, found := u.listedSet(name)
	sig, cs := u.ownClauses()
	for _, c := range cs {
		vals, ok := u.impliedSet(sig, c, name)
		if !ok {
			continue
		}
		set, found = intersect(set, found, vals), true
	}
	return set, found
}

// listedSet is the intersection of every one of the caller's clauses that
// is a disjunction of `name == k` (or `k == name`), k a folded integer.
// False when no clause has that form or a set is larger than
// maxFiniteValues.
func (u *unit) listedSet(name source.Symbol) ([]*big.Int, bool) {
	var set []*big.Int
	found := false
	_, cs := u.ownClauses() // a fn literal or a contract: its function's clauses; none for an invariant
	for _, c := range cs {
		vals, ok := u.disjunction(c.x, c.fx, name, c.pred, 0)
		if !ok || len(vals) > maxFiniteValues {
			continue
		}
		set, found = intersect(set, found, vals), true
	}
	return set, found
}

// impliedSet is the set clause c implies for the const generic named name
// c is, whole, `name == e` or `e == name`, and e reads no generic
// but the caller's const generics that have a listed set, none of them
// name itself; the set is e's values at every combination of theirs that
// fit name's type. False for any other clause, when e does not fold at one
// combination, past maxFiniteCombos combinations or maxFiniteValues
// values. `name * 2 == N` implies nothing for name (it implies N's set when
// name is listed): at a far value of name its fold overflows, which a
// same-module site reports (E0901) but an imported fn value leaves
// unchecked, so it does not bound name.
func (u *unit) impliedSet(sig *types.Signature, c ownClause, name source.Symbol) ([]*big.Int, bool) {
	self := genericIndex(sig, name)
	if self < 0 {
		return nil, false
	}
	x := c.x
	for {
		p, ok := x.(*ast.ParenExpr)
		if !ok {
			break
		}
		x = p.X
	}
	b, ok := x.(*ast.BinaryExpr)
	if !ok || b.Op != ast.TokEqEq {
		return nil, false
	}
	e := b.Y
	if !namesGeneric(b.X, c.fx, name, c.pred) {
		if !namesGeneric(b.Y, c.fx, name, c.pred) {
			return nil, false
		}
		e = b.X
	}
	// The generics e reads, each with its listed set (one step).
	type read struct {
		at   uint32
		vals []*big.Int
	}
	var reads []read
	good := true
	ast.Inspect(e, func(n ast.Node) bool {
		if n == nil || !good {
			return false
		}
		id := n.ID()
		if int(id) >= len(c.fx.info.Refs) {
			return true
		}
		r := c.fx.info.Refs[id]
		if r.Kind != resolve.RefTypeParam {
			return true
		}
		if types.ItemID(r.B) != c.pred.Owner && types.ItemID(r.B) != c.pred.ID || int(r.A) >= len(sig.Generics) || !sig.Generics[r.A].Const || int(r.A) == self {
			good = false
			return false
		}
		for _, rd := range reads {
			if rd.at == r.A {
				return true
			}
		}
		vals, ok := u.listedSet(sig.Generics[r.A].Name)
		if !ok {
			good = false
			return false
		}
		reads = append(reads, read{at: r.A, vals: vals})
		return true
	})
	if !good {
		return nil, false
	}
	combos := 1
	for _, rd := range reads {
		if combos *= len(rd.vals); combos > maxFiniteCombos {
			return nil, false
		}
	}
	var set []*big.Int
	idx := make([]int, len(reads))
	for n := 0; n < combos; n++ {
		gens := map[uint32]fval{}
		for k, rd := range reads {
			gens[rd.at] = fval{n: rd.vals[idx[k]], t: sig.Generics[rd.at].Type}
		}
		v, ok := u.foldOwn(e, c.fx, c.pred, gens)
		if !ok || v.isBool || v.n == nil {
			return nil, false
		}
		if u.c.inRange(v.n, sig.Generics[self].Type) && !containsInt(set, v.n) {
			if set = append(set, v.n); len(set) > maxFiniteValues {
				return nil, false
			}
		}
		for k := range idx {
			if idx[k]++; idx[k] < len(reads[k].vals) {
				break
			}
			idx[k] = 0
		}
	}
	return set, true
}

// intersect is set and vals intersected, or vals when there is no set yet.
func intersect(set []*big.Int, found bool, vals []*big.Int) []*big.Int {
	if !found {
		return vals
	}
	var both []*big.Int
	for _, v := range set {
		if containsInt(vals, v) {
			both = append(both, v)
		}
	}
	return both
}

// disjunction is x's values for the const generic name of pred's function
// when x is `name == k`, `k == name` or an `||` of such, in parentheses or
// not; the values are deduplicated in first-seen order.
func (u *unit) disjunction(x ast.Expr, fx *fileIndex, name source.Symbol, pred *types.Item, depth int) ([]*big.Int, bool) {
	if depth > maxFoldDepth {
		return nil, false
	}
	switch x := x.(type) {
	case *ast.ParenExpr:
		return u.disjunction(x.X, fx, name, pred, depth+1)
	case *ast.BinaryExpr:
		switch x.Op {
		case ast.TokPipePipe:
			a, ok := u.disjunction(x.X, fx, name, pred, depth+1)
			if !ok {
				return nil, false
			}
			b, ok := u.disjunction(x.Y, fx, name, pred, depth+1)
			if !ok {
				return nil, false
			}
			for _, v := range b {
				if !containsInt(a, v) {
					a = append(a, v)
				}
			}
			return a, true
		case ast.TokEqEq:
			lit := x.Y
			if !namesGeneric(x.X, fx, name, pred) {
				if !namesGeneric(x.Y, fx, name, pred) {
					return nil, false
				}
				lit = x.X
			}
			f := &folder{m: u.m, fx: fx, rec: u.x.rec}
			v, ok, _ := f.fold(lit)
			if !ok || v.isBool || v.n == nil {
				return nil, false
			}
			return []*big.Int{v.n}, true
		}
	}
	return nil, false
}

// namesGeneric reports whether x is the generic parameter name of the
// function whose requires pred is (or of pred itself, as foldedClause binds).
func namesGeneric(x ast.Expr, fx *fileIndex, name source.Symbol, pred *types.Item) bool {
	for {
		p, ok := x.(*ast.ParenExpr)
		if !ok {
			break
		}
		x = p.X
	}
	id, ok := x.(*ast.Ident)
	if !ok || id.Name != name || int(id.ID()) >= len(fx.info.Refs) {
		return false
	}
	r := fx.info.Refs[id.ID()]
	return r.Kind == resolve.RefTypeParam && (types.ItemID(r.B) == pred.Owner || types.ItemID(r.B) == pred.ID)
}

func containsInt(xs []*big.Int, v *big.Int) bool {
	for _, x := range xs {
		if x.Cmp(v) == 0 {
			return true
		}
	}
	return false
}

// namesWord reports whether text holds word as a whole identifier that is
// not a field label (labelAt: `P(N: 1)` names no N).
func namesWord(text, word string) bool {
	isWord := func(r byte) bool {
		return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r >= 0x80
	}
	for i := 0; i+len(word) <= len(text); i++ {
		if text[i:i+len(word)] != word {
			continue
		}
		if (i == 0 || !isWord(text[i-1])) && (i+len(word) == len(text) || !isWord(text[i+len(word)])) && !labelAt(text, i+len(word)) {
			return true
		}
	}
	return false
}
