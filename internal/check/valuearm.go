// Why: an if, a match or an else handler can be a value; its arms must
// agree on one type, untyped literals must adapt to the arm that decides,
// and an arm that gives no value must be reported once, with the right code.
//
// What: valueCtx, valueConstruct and settle, valueArm and armExpr,
// armValue and armWant, elseValue and elseDiscard, the diagnostics E0310,
// E0311 and E0803, and lastValueFix (E0805's fix for a last value construct).
//
// How: with an expected type every arm is checked against it; without one
// the first arm that is not an untyped literal decides, and literal arms met
// before it wait in valueCtx.lits. Arms under `if T is X` see the type
// through their narrowing. Nested last-line constructs share their parent's
// valueCtx.

package check

import (
	"sort"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// valueCtx is one value construct being checked: an
// if, a match or an else handler in value position, with the nested
// last-line constructs that share its arms.
type valueCtx struct {
	want  types.TypeID // the expected type, NoType when none
	got   types.TypeID // the decided type, NoType until the first deciding arm
	first source.Span  // the arm that decided got
	kind  string       // "if", "match" or "else"
	// elseAt is the else keyword of an else handler: E0803's anchor.
	elseAt source.Span
	// lits are the untyped literal arms met before any arm decided got;
	// they adapt to it once it is known, each checked under the
	// narrowing it was met in.
	lits []deferredArm
	// narrow is the narrowing in force where the construct starts: an arm
	// under a further `if T is X` sees want and got through it.
	narrow []types.GenericArg
	// nested are the last-line constructs checked as part of this one.
	nested []ast.Expr
	// bad is set by E0311, E0310 or E0803: the construct is Poison.
	bad bool
	// discard marks the handler of `_ = e else { H }`: H may yield
	// a value of e's success type, or fall through with none (fell), but
	// not both; yielded says some last line gave a value.
	discard bool
	yielded bool
	fell    []fellThrough
}

// fellThrough is a last line of a discarded else handler that gave no
// value: E0803 if another line yields one (elseDiscard).
type fellThrough struct {
	at   source.Span
	last string
}

// deferredArm is an untyped literal arm waiting for the decided type, with
// the narrowing and environment of its arm.
type deferredArm struct {
	x      ast.Expr
	narrow []types.GenericArg
	env    *Env
}

// markValue records node id as a value construct (Body.Values), allocating
// the set on first use; a fields-loop copy marks the same node.
func (b *Body) markValue(id ast.NodeID) {
	b.slot(id)
	if b.Values.w == nil {
		b.Values = newBits(len(b.Types))
	}
	b.Values.set(id, b.Base)
}

// valueConstruct types an if or match in value position: each arm
// through valueArm, then the construct's type: Poison after E0310 or E0311,
// never when no arm falls through, else the expected type or the decided
// one.
func (u *unit) valueConstruct(e ast.Expr, want types.TypeID) types.TypeID {
	v := &valueCtx{want: want, narrow: u.narrow}
	div := false
	switch s := e.(type) {
	case *ast.IfStmt:
		v.kind = "if"
		div = u.ifStmtV(s, v)
	case *ast.MatchStmt:
		v.kind = "match"
		div = u.matchStmtV(s, v)
	}
	t := u.settle(v, div)
	if div {
		u.b.diverge(e.ID())
	}
	u.b.markValue(e.ID())
	for _, n := range v.nested {
		u.setType(n, t)
		u.b.markValue(n.ID())
	}
	return t
}

// settle checks the deferred literal arms and gives the construct's type.
func (u *unit) settle(v *valueCtx, div bool) types.TypeID {
	// In source order: isBranch may check an else arm before its then arm.
	sort.SliceStable(v.lits, func(i, j int) bool { return v.lits[i].x.Span().Start < v.lits[j].x.Span().Start })
	savedEnv, savedNarrow := u.env, u.narrow
	for _, l := range v.lits {
		u.env, u.narrow = l.env, l.narrow
		if v.got == types.NoType {
			// Every yielding arm is an untyped literal: the first takes the
			// literal default, and the others adapt to it.
			if t := u.expr(l.x, types.NoType); t != types.Poison {
				v.got, v.first = t, l.x.Span()
			}
			continue
		}
		u.armValue(v, l.x)
	}
	u.env, u.narrow = savedEnv, savedNarrow
	switch {
	case v.bad:
		return types.Poison
	case div:
		return types.Never
	case v.want != types.NoType:
		return v.want
	case v.got != types.NoType:
		return v.got
	}
	return types.Poison
}

// armBlock checks an arm's block: as a statement block when v is nil, as a
// value arm otherwise. It reports whether the arm does not fall through.
func (u *unit) armBlock(b *ast.Block, v *valueCtx) bool {
	if v == nil {
		return u.block(b)
	}
	return u.valueArm(b, v)
}

// valueArm checks one arm: the statements before the last line as in any block,
// then the last line, which is the arm's value or does not fall through.
func (u *unit) valueArm(b *ast.Block, v *valueCtx) bool {
	if b == nil {
		return false
	}
	n := len(b.Stmts)
	div := false
	for i := 0; i < n-1; i++ {
		if s := b.Stmts[i]; s != nil && u.stmt(s) {
			div = true
		}
	}
	var l ast.Stmt
	if n > 0 {
		l = b.Stmts[n-1]
	}
	switch s := l.(type) {
	case nil:
		if !div {
			sp := b.Span()
			end := sp
			if sp.End > sp.Start {
				end = source.Span{File: sp.File, Start: sp.End - 1, End: sp.End}
			}
			u.noValue(v, end, "nothing")
		}
	case *ast.ExprStmt:
		if u.armExpr(s.X, v, div) {
			u.b.diverge(s.ID())
			div = true
		}
	case *ast.IfStmt:
		if s.Else == nil {
			if !u.stmt(s) && !div {
				u.noValue(v, s.Span(), "if")
			} else {
				div = true
			}
			break
		}
		v.nested = append(v.nested, s)
		if u.ifStmtV(s, v) {
			u.b.diverge(s.ID())
			div = true
		}
	case *ast.MatchStmt:
		v.nested = append(v.nested, s)
		if u.matchStmtV(s, v) {
			u.b.diverge(s.ID())
			div = true
		}
	default:
		if u.stmt(l) {
			div = true
		} else if !div {
			u.noValue(v, l.Span(), lastKind(l))
		}
	}
	if div {
		u.b.diverge(b.ID())
	}
	return div
}

// armExpr checks an arm's last-line expression and reports whether it has
// type never. With no expected type the first arm that is not an untyped
// literal decides; a literal met before it waits. dead says an
// earlier statement of the arm does not fall through, so the line decides
// nothing.
func (u *unit) armExpr(x ast.Expr, v *valueCtx, dead bool) bool {
	if v.want == types.NoType && v.got == types.NoType && !dead && u.untypedLit(x) {
		// `none` and `[]` are literals too: they wait as 0 does.
		v.lits = append(v.lits, deferredArm{x: x, narrow: u.narrow, env: u.env})
		return false
	}
	if dead || v.want != types.NoType || v.got != types.NoType {
		want := u.armWant(v, v.want)
		if want == types.NoType {
			want = u.armWant(v, v.got)
		}
		if dead {
			return u.expr(x, want) == types.Never
		}
		if v.want == types.NoType {
			return u.armValue(v, x)
		}
		before := len(u.b.Diags)
		t := u.expr(x, want)
		if t == types.Void && v.kind == "else" {
			u.voidHandler(v, x, before)
		} else if v.discard && u.tab.Kind(t) == types.KErrorUnion {
			// A discarded handler's last line that is an unhandled call is
			// a statement there: E0802, as the statement form reports it.
			u.voidHandler(v, x, before)
			u.unhandled(x, t)
		} else if t != types.Never && t != types.Poison {
			v.yielded = true
		}
		return t == types.Never
	}
	t := u.expr(x, types.NoType)
	if t != types.Poison && t != types.Never {
		v.got, v.first = t, x.Span()
	}
	return t == types.Never
}

// armValue checks a later arm against the deciding arm's type, as the arm
// sees it under its narrowing; a mismatch is E0301 naming both arms.
func (u *unit) armValue(v *valueCtx, x ast.Expr) bool {
	before := len(u.b.Diags)
	got := u.armWant(v, v.got)
	t := u.expr(x, got)
	if t == types.Never || u.assignable(got, t) {
		return t == types.Never
	}
	for _, d := range u.b.Diags[before:] {
		if d.Code == diag.E0301 && d.At == x.Span() {
			d.Message = "this arm gives `" + u.str(t) + "`, the arm that decides the type gives `" + u.str(got) + "`"
			d.Loc("arm", v.first)
		}
	}
	return false
}

// armWant is t, the construct's expected or decided type, as an arm under
// an `if T is X` of the construct sees it: with T read as X, as the
// statement form's `return` and assignments see the result and the local.
// Outside such an arm it is t.
func (u *unit) armWant(v *valueCtx, t types.TypeID) types.TypeID {
	if t == types.NoType || !u.narrowedSince(v) {
		return t
	}
	return u.nt(t)
}

// narrowedSince reports whether an `if T is X` inside construct v narrows
// the current arm: isBranch installs a fresh copy of the narrowing for its
// then-branch, so a narrowing other than the construct's own is one.
func (u *unit) narrowedSince(v *valueCtx) bool {
	if len(u.narrow) == 0 {
		return false
	}
	return len(v.narrow) == 0 || &u.narrow[0] != &v.narrow[0]
}

// voidHandler turns an else handler whose last line is a void call into
// E0803, the handler falling through with no value, instead of E0301.
func (u *unit) voidHandler(v *valueCtx, x ast.Expr, before int) {
	kept := u.b.Diags[:before]
	for _, d := range u.b.Diags[before:] {
		if d.Code == diag.E0301 && d.At == x.Span() {
			continue
		}
		kept = append(kept, d)
	}
	u.b.Diags = kept
	u.noValue(v, x.Span(), "void")
}

// noValue is E0310 at an arm's last line that gives no value and falls
// through, or E0803 for an else handler (one code per construct).
func (u *unit) noValue(v *valueCtx, at source.Span, last string) {
	if v.discard {
		// Under `_ =` a handler may fall through with no value;
		// elseDiscard reports it only when another line yields one.
		v.fell = append(v.fell, fellThrough{at: at, last: last})
		return
	}
	v.bad = true
	if v.kind == "else" {
		u.err(diag.E0803, v.elseAt, "the handler falls through where a value is needed").
			Alt("the value on its last line").Alt("end the handler with `return`, `fail`, `break` or `continue`")
		return
	}
	u.err(diag.E0310, at, "this arm of a value `"+v.kind+"` ends without a value").
		Text("construct", v.kind).Text("last", last).
		Alt("the value on the arm's last line").Alt("the statement form")
}

// lastKind names the statement form of an arm's last line for E0310's fact.
func lastKind(s ast.Stmt) string {
	switch s.(type) {
	case *ast.BindStmt, *ast.DestructureStmt:
		return "binding"
	case *ast.AssignStmt:
		return "assignment"
	case *ast.DiscardStmt:
		return "discard"
	case *ast.DeferStmt:
		if d := s.(*ast.DeferStmt); d.Onerr {
			return "onerr"
		}
		return "defer"
	case *ast.ForStmt:
		return "loop"
	case *ast.ScopeStmt:
		return "scope"
	case *ast.UnsafeStmt:
		return "unsafe"
	case *ast.AsmStmt:
		return "asm"
	}
	return "statement"
}

// valueIfWithoutElse is E0311 at the if keyword.
func (u *unit) valueIfWithoutElse(s *ast.IfStmt, v *valueCtx) {
	v.bad = true
	sp := s.Span()
	sp.End = min(sp.End, sp.Start+2)
	u.err(diag.E0311, sp, "a value `if` needs an `else`").Alt("add the `else`").Alt("the statement form")
}

// elseValue checks an else handler where a value is needed: the
// handler is a value arm, except that falling through with no value is
// E0803. It reports whether the handler does not fall through.
func (u *unit) elseValue(e *ast.ElseExpr, elem types.TypeID) bool {
	v := &valueCtx{want: elem, narrow: u.narrow, kind: "else", elseAt: keywordSpan(u.fx, e.X.Span().End, ast.TokElse)}
	div := u.valueArm(e.Handler, v)
	if !div && !v.bad {
		u.b.markValue(e.ID())
	}
	// A last-line if or match of the handler yields into the same value.
	t := elem
	if v.bad {
		t = types.Poison
	}
	for _, n := range v.nested {
		u.setType(n, t)
		u.b.markValue(n.ID())
	}
	return div
}

// elseDiscard checks the handler of `_ = e else { H }` with e fallible over
// a success type elem: `_ =` is a value position, so H's last line
// may be a value of elem, yielded and then discarded (the else is a value
// construct, as elseValue makes it); or H falls through with no value, the
// ignore form (the statement form); or it does not fall through. A handler
// that does both, a value on one path and none on another, is E0803 as in
// any value position.
func (u *unit) elseDiscard(e *ast.ElseExpr, elem types.TypeID) {
	v := &valueCtx{want: elem, narrow: u.narrow, kind: "else", discard: true, elseAt: keywordSpan(u.fx, e.X.Span().End, ast.TokElse)}
	div := u.valueArm(e.Handler, v)
	if !v.yielded {
		return
	}
	if len(v.fell) > 0 {
		v.discard = false
		u.noValue(v, v.fell[0].at, v.fell[0].last)
	}
	if !div && !v.bad {
		u.b.markValue(e.ID())
	}
	t := elem
	if v.bad {
		t = types.Poison
	}
	for _, n := range v.nested {
		u.setType(n, t)
		u.b.markValue(n.ID())
	}
}

// lastValueFix is E0805's Compiles fix: a body whose last statement
// is an if with an else, or a match, whose arms each end in a value of the
// result type or do not fall through, compiles with `return` before it.
func (u *unit) lastValueFix(d *diag.Diagnostic, s ast.Stmt) {
	if !u.yieldsResult(s) {
		return
	}
	at := s.Span()
	d.Fix(diag.Fix{Class: diag.Compiles, Edits: []diag.Edit{{Span: source.Span{File: at.File, Start: at.Start, End: at.Start}, Text: "return "}},
		Why: "every arm of the last `if` or `match` ends in a value of the result type"})
}

// yieldsResult reports whether s is an if with an else or a match each of
// whose arms ends in a value of the result type or does not fall through,
// with at least one value.
func (u *unit) yieldsResult(s ast.Stmt) bool {
	value := false
	var arm func(b *ast.Block) bool
	var construct func(s ast.Stmt) bool
	arm = func(b *ast.Block) bool {
		if b == nil || len(b.Stmts) == 0 {
			return false
		}
		if u.b.Diverges.Has(b.ID(), u.b.Base) {
			return true
		}
		switch l := b.Stmts[len(b.Stmts)-1].(type) {
		case *ast.ExprStmt:
			if t := u.b.TypeOf(l.X.ID()); t == u.result || isUntyped(l.X) && u.isNum(u.result) {
				value = true
				return true
			}
		case *ast.IfStmt, *ast.MatchStmt:
			return construct(l)
		}
		return false
	}
	construct = func(s ast.Stmt) bool {
		switch s := s.(type) {
		case *ast.IfStmt:
			if s.Else == nil || !arm(s.Then) {
				return false
			}
			switch e := s.Else.(type) {
			case *ast.IfStmt:
				return construct(e)
			case *ast.Block:
				return arm(e)
			}
		case *ast.MatchStmt:
			for _, a := range s.Arms {
				if a == nil || !arm(a.Body) {
					return false
				}
			}
			return len(s.Arms) > 0
		}
		return false
	}
	return construct(s) && value
}
