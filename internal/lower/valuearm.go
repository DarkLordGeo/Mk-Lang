// Why: match, if and else may yield a value (value arms, else values), but
// borrow, eval and codegen understand only the statement form; lower
// rewrites one into the other.
//
// What: isValueConstruct, valueExpr and valueInto lower a value construct;
// armBlock, armStmts and armAssign lower its arms; elseValueJoin joins an
// else handler that yields a value.
//
// How: a fresh local (a binding's own local for a binding) is declared
// before the construct, assigned in each yielding arm, and read after it.

package lower

import (
	"mk/internal/ast"
	"mk/internal/mir"
	"mk/internal/types"
)

// isValueConstruct reports whether e is an if or match checked as a value.
func (bl *builder) isValueConstruct(e ast.Expr) bool {
	switch e.(type) {
	case *ast.IfStmt, *ast.MatchStmt:
		return bl.in.IsValue(e.ID())
	}
	return false
}

// valueExpr lowers a value construct anywhere but a binding's right side
// into a temporary of the enclosing statement's frame, created
// before the construct, then read once.
func (bl *builder) valueExpr(e ast.Expr) mir.Rvalue {
	t := bl.in.Type(e.ID())
	if t == types.Never {
		// Every arm does not fall through: the statement form, no local.
		bl.constructStmt(e)
		return use(mir.Imm(types.Never, 0))
	}
	r := bl.newTemp(t, e.Span())
	bl.valueInto(e, bl.b.Place(r))
	if !bl.open() {
		return use(mir.Imm(types.Never, 0))
	}
	return use(bl.valueOf(bl.b.Place(r), true, e.Span()))
}

// constructStmt lowers an if or match as the statement it is.
func (bl *builder) constructStmt(e ast.Expr) {
	switch s := e.(type) {
	case *ast.IfStmt:
		bl.ifStmt(s)
	case *ast.MatchStmt:
		bl.matchStmt(s)
	}
}

// valueInto lowers construct e with each yielding arm assigning dest
// the statement form's shape, with armStmts at each arm.
func (bl *builder) valueInto(e ast.Expr, dest mir.Place) {
	if bl.armDest == nil {
		bl.armDest = map[*ast.Block]mir.Place{}
	}
	if bl.valueDests == nil {
		bl.valueDests = map[mir.LocalID]int{}
	}
	bl.valueDests[dest.Local]++
	defer func() {
		if bl.valueDests[dest.Local]--; bl.valueDests[dest.Local] == 0 {
			delete(bl.valueDests, dest.Local)
		}
	}()
	var arms []*ast.Block
	eachArm(e, func(b *ast.Block) {
		if b != nil {
			bl.armDest[b] = dest
			arms = append(arms, b)
		}
	})
	bl.constructStmt(e)
	for _, b := range arms {
		delete(bl.armDest, b)
	}
}

// eachArm calls f with each arm block of an if chain or a match.
func eachArm(e ast.Node, f func(*ast.Block)) {
	switch s := e.(type) {
	case *ast.IfStmt:
		f(s.Then)
		switch el := s.Else.(type) {
		case *ast.IfStmt:
			eachArm(el, f)
		case *ast.Block:
			f(el)
		}
	case *ast.MatchStmt:
		for _, a := range s.Arms {
			if a != nil {
				f(a.Body)
			}
		}
	}
}

// armBlock lowers an arm block in a scope of its own.
func (bl *builder) armBlock(b *ast.Block, k scopeKind) {
	bl.openScope(k)
	bl.armStmts(b)
	bl.closeScope(closeBrace(b))
}

// armStmts lowers an arm's statements in the current scope. In a value
// arm (armDest) the last line's value is assigned to the destination as
// `dest = v` would be, in a frame of its own, and a last-line value
// construct lowers into the same destination.
func (bl *builder) armStmts(b *ast.Block) {
	dest, ok := bl.armDest[b]
	if !ok {
		bl.blockStmts(b)
		return
	}
	n := len(b.Stmts)
	for i := 0; i < n-1; i++ {
		if !bl.open() {
			return
		}
		bl.stmt(b.Stmts[i])
	}
	if n == 0 || !bl.open() {
		return
	}
	switch l := b.Stmts[n-1].(type) {
	case *ast.ExprStmt:
		bl.pushFrame(l.Span())
		rv := bl.exprConv(l.X)
		if bl.open() && bl.rvType(rv, l.X) != types.Never {
			bl.armAssign(dest, rv, l)
		}
		bl.popFrame()
	case *ast.IfStmt:
		if bl.in.IsValue(l.ID()) {
			bl.pushFrame(l.Span())
			bl.valueInto(l, dest)
			bl.popFrame()
			return
		}
		bl.stmt(l)
	case *ast.MatchStmt:
		if bl.in.IsValue(l.ID()) {
			bl.pushFrame(l.Span())
			bl.valueInto(l, dest)
			bl.popFrame()
			return
		}
		bl.stmt(l)
	default:
		bl.stmt(l)
	}
}

// armAssign is `dest = v` for an arm's last line l. Under an `if T is X`
// of the construct check types the value with T read as X while dest keeps
// T (L7R-4): the value is retyped by narrowBack's CastNarrow, as `return`'s
// is.
func (bl *builder) armAssign(dest mir.Place, rv mir.Rvalue, l *ast.ExprStmt) {
	at := bl.trapSpan(rv, l.Span(), l.X)
	rt, dt := bl.rvType(rv, l.X), bl.b.PlaceType(dest)
	if rt == dt || bl.narrowedType(dt) != rt {
		bl.assign(dest, rv, at)
		return
	}
	tmp := bl.newTemp(rt, l.Span())
	bl.assign(bl.b.Place(tmp), rv, at)
	if !bl.open() {
		return
	}
	bl.assign(dest, use(bl.narrowBack(bl.valueOfTemp(tmp, l.Span()), dt, l.Span())), l.Span())
}

// elseValueJoin is elseJoin for an else handler that yields a value: the
// handler assigned r on its edge, the success edge assigns the payload, and
// both join with r read once.
func (bl *builder) elseValueJoin(x *ast.ElseExpr, succ mir.BlockID, r mir.LocalID, value func() mir.Rvalue) mir.Rvalue {
	join := bl.newBlock()
	reached := bl.joinTo(join, x.Span())
	bl.cur = succ
	if bl.open() {
		bl.assign(bl.b.Place(r), value(), x.Span())
		reached = bl.joinTo(join, x.Span()) || reached
	}
	bl.continueAt(join, reached)
	if !bl.open() {
		return use(mir.Imm(types.Never, 0))
	}
	return use(bl.valueOf(bl.b.Place(r), true, x.Span()))
}
