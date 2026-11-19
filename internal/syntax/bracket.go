// Why: inside "[" … "]" (type arguments, gen arguments, an index suffix) a
// token run may spell a type or an expression, and the parser cannot tell
// which until it has read it; `[N]T` against `[N]` has the same problem.
//
// What: bracket mode, the TypeOrExpr cover: parseBracketList, parseBArg and
// its ladder (parseBBin, parseBUnary, parseBParen, parseBRecord), and
// parseBGroups for a run of "[" … "]" groups; toType and toExpr turn the
// result into the node the caller needs.
//
// How: each level returns a type node when an operand is type-only (a "|"
// over a type, "&" over a type, a record with a type value) and an
// expression otherwise. A run of groups is decided at the token after the
// last one: a type follows, so the groups are slice or array prefixes; else
// the first is an array literal and the rest are index suffixes.

package syntax

import (
	"mk/internal/ast"
	"mk/internal/source"
)

// parseBracketList reads TypeOrExpr { "," TypeOrExpr } [ "," ] up to closer,
// which it leaves for the caller.
func (p *parser) parseBracketList(closer ast.TokenKind) []ast.TypeOrExpr {
	savedList, savedCover := p.listCtx, p.coverOK
	p.listCtx, p.coverOK = true, false
	var out []ast.TypeOrExpr
	for !p.at(closer) && !p.at(ast.TokEOF) {
		if p.at(ast.TokSemi) {
			p.semiInBrackets()
			continue
		}
		out = append(out, p.parseBArg())
		if p.at(ast.TokSemi) {
			p.semiInBrackets()
			continue
		}
		if !p.at(ast.TokComma) {
			if !p.at(closer) && !p.at(ast.TokEOF) {
				p.expected("`,`", "`"+closer.FixedText()+"`")
				p.skip(stopList)
				if p.at(ast.TokComma) {
					p.next()
					p.region()
					continue
				}
			}
			break
		}
		p.next()
		p.region()
	}
	p.listCtx, p.coverOK = savedList, savedCover
	return out
}

// isTypeOnly reports whether n is a type node that is not an expression.
func isTypeOnly(n ast.Node) bool {
	_, t := n.(ast.Type)
	return t
}

// parseBArg reads one bracket argument: the expression ladder with the
// type-only forms, and "!" at the lowest level, right-associative.
func (p *parser) parseBArg() ast.TypeOrExpr {
	if p.tooDeep {
		return p.badExprFrom(p.p)
	}
	saved := p.inBracket
	p.inBracket = true
	defer func() { p.inBracket = saved }()
	start := p.curStart()
	x := p.parseBFallback()
	if !p.at(ast.TokBang) {
		return x
	}
	var b ast.Base
	p.beginAt(&b, start)
	p.next()
	y := p.parseBArg()
	if !isTypeOnly(x) && !isTypeOnly(y) {
		n := &ast.BinaryExpr{Base: b, X: x.(ast.Expr), Op: ast.TokBang, Y: y.(ast.Expr)}
		p.finish(n, &n.Base)
		return n
	}
	n := &ast.ErrorType{Base: b, Errors: p.toType(x), Ok: p.toType(y)}
	p.finish(n, &n.Base)
	return n
}

func (p *parser) parseBFallback() ast.TypeOrExpr {
	start := p.curStart()
	x := p.parseBBin(precOr, start)
	if e, ok := x.(ast.Expr); ok && p.at(ast.TokElse) {
		return p.parseElse(e, start)
	}
	return x
}

// parseBBin is the binary ladder in bracket mode: "|" makes a UnionType when
// either side is type-only.
func (p *parser) parseBBin(min int, start uint32) ast.TypeOrExpr {
	left := p.parseBUnary()
	for !p.tooDeep {
		op := p.kind()
		prec := binPrec(op)
		if prec == 0 || prec < min {
			break
		}
		var b ast.Base
		p.beginAt(&b, start)
		p.next()
		right := p.parseBBin(prec+1, p.curStart())
		if op == ast.TokPipe && (isTypeOnly(left) || isTypeOnly(right)) {
			n := &ast.UnionType{Base: b}
			lt := p.toType(left)
			if u, ok := lt.(*ast.UnionType); ok {
				n.Types = append(n.Types, u.Types...)
			} else {
				n.Types = append(n.Types, lt)
			}
			n.Types = append(n.Types, p.toType(right))
			p.finish(n, &n.Base)
			left = n
			continue
		}
		n := &ast.BinaryExpr{Base: b, X: p.toExpr(left), Op: op, Y: p.toExpr(right)}
		p.finish(n, &n.Base)
		left = n
		if prec == precCompare && binPrec(p.kind()) == precCompare {
			p.errHere("comparisons do not chain; use `&&`")
		}
	}
	return left
}

// parseBUnary reads the prefix and operand level of bracket mode.
func (p *parser) parseBUnary() ast.TypeOrExpr {
	if p.tooDeep {
		return p.badExprFrom(p.p)
	}
	switch p.kind() {
	case ast.TokQuestion:
		n := &ast.OptionalType{}
		p.begin(&n.Base)
		p.next()
		n.Elem = p.parsePrefixType()
		p.finish(n, &n.Base)
		return n
	case ast.TokLBracket:
		return p.parseBGroups()
	case ast.TokFn:
		if p.peekKind(1) == ast.TokLParen {
			return p.parseFnType(false)
		}
	case ast.TokMut:
		if p.peekKind(1) == ast.TokLBracket && p.peekKind(2) == ast.TokRBracket {
			n := &ast.MutSliceType{}
			p.begin(&n.Base)
			p.next()
			p.next()
			p.next()
			n.Elem = p.parsePrefixType()
			p.finish(n, &n.Base)
			return n
		}
		return p.parseBPrefix(ast.TokMut)
	case ast.TokAmp:
		return p.parseBPrefix(ast.TokAmp)
	case ast.TokStar:
		return p.parseBPrefix(ast.TokStar)
	case ast.TokAmpAmp:
		var outer, inner ast.Base
		p.begin(&outer)
		p.beginAt(&inner, p.curStart()+1)
		p.next()
		x := p.parseBUnary()
		in := p.prefixNode(inner, ast.TokAmp, x)
		return p.prefixNode(outer, ast.TokAmp, in)
	case ast.TokLParen:
		return p.parseBParen()
	}
	return p.parseUnary()
}

// parseBPrefix reads & * mut over a bracket operand: a type node when the
// operand is type-only, else a UnaryExpr.
func (p *parser) parseBPrefix(op ast.TokenKind) ast.TypeOrExpr {
	var b ast.Base
	p.begin(&b)
	p.next()
	if op == ast.TokAmp && p.at(ast.TokMut) {
		p.ampMutHabit()
		var m ast.Base
		p.begin(&m)
		p.next()
		x := p.parseBUnary()
		return p.prefixNode(b, ast.TokMut, p.prefixNode(m, ast.TokMut, x))
	}
	x := p.parseBUnary()
	return p.prefixNode(b, op, x)
}

// prefixNode finishes a prefix node whose Base was begun.
func (p *parser) prefixNode(b ast.Base, op ast.TokenKind, x ast.TypeOrExpr) ast.TypeOrExpr {
	if isTypeOnly(x) {
		t := x.(ast.Type)
		var n ast.Type
		switch op {
		case ast.TokAmp:
			r := &ast.RefType{Base: b, Elem: t}
			p.finish(r, &r.Base)
			n = r
		case ast.TokStar:
			r := &ast.RawPtrType{Base: b, Elem: t}
			p.finish(r, &r.Base)
			n = r
		default:
			r := &ast.MutType{Base: b, Elem: t}
			p.finish(r, &r.Base)
			n = r
		}
		return n
	}
	n := &ast.UnaryExpr{Base: b, Op: op, X: x.(ast.Expr)}
	p.finish(n, &n.Base)
	return n
}

// parseBParen reads a parenthesized bracket argument: a record type or
// literal by its fields' values, a one-name record literal, or a ParenType or
// ParenExpr by its content.
func (p *parser) parseBParen() ast.TypeOrExpr {
	k1, k2 := p.peekKind(1), p.peekKind(2)
	nameNext := k1 == ast.TokIdent || k1.IsKeyword()
	if k1 == ast.TokRParen || (nameNext && (k2 == ast.TokRParen || k2 == ast.TokComma)) {
		return p.parseUnary()
	}
	if nameNext && k2 == ast.TokColon {
		return p.parseBRecord()
	}
	var b ast.Base
	p.begin(&b)
	p.next()
	saved := p.listCtx
	p.listCtx = false
	x := p.parseBArg()
	p.listCtx = saved
	if p.at(ast.TokComma) {
		p.errHere("record fields need names: `(a: 1, b: 2)`")
		p.skip(stopList)
		for p.accept(ast.TokComma) {
			p.skip(stopList)
		}
	}
	p.expect(ast.TokRParen)
	if t, ok := x.(ast.Type); ok {
		n := &ast.ParenType{Base: b, Type: t}
		p.finish(n, &n.Base)
		return n
	}
	n := &ast.ParenExpr{Base: b, X: x.(ast.Expr)}
	p.finish(n, &n.Base)
	if e, ok := ast.Node(n).(ast.Expr); ok {
		return p.parsePostfix(e, b.Sp.Start, false)
	}
	return n
}

// parseBRecord reads "(" Name ":" … ")" in bracket mode: a RecordType when
// any value is type-only, else a RecordLit.
func (p *parser) parseBRecord() ast.TypeOrExpr {
	var b ast.Base
	p.begin(&b)
	p.next()
	type fld struct {
		name  *ast.Ident
		value ast.TypeOrExpr
		start uint32
		end   uint32
	}
	var fields []fld
	anyType := false
	for !p.at(ast.TokRParen) && !p.at(ast.TokEOF) {
		start := p.curStart()
		name := p.parseName("a field name")
		var v ast.TypeOrExpr
		if p.accept(ast.TokColon) {
			v = p.parseBArg()
			anyType = anyType || isTypeOnly(v)
		}
		fields = append(fields, fld{name, v, start, p.lastEnd})
		if !p.accept(ast.TokComma) {
			break
		}
		p.region()
	}
	p.expect(ast.TokRParen)
	if anyType {
		n := &ast.RecordType{Base: b}
		for _, f := range fields {
			fn := &ast.Field{Name: f.name}
			if f.value == nil {
				p.syntaxErr(f.name.Sp, "a record type's fields need types")
				fn.Type = p.badTypeFrom(p.p)
			} else {
				fn.Type = p.toType(f.value)
			}
			p.adopt(fn, &fn.Base, p.span(f.start, f.end))
			n.Fields = append(n.Fields, fn)
		}
		p.finish(n, &n.Base)
		return n
	}
	n := &ast.RecordLit{Base: b}
	for _, f := range fields {
		rf := &ast.RecField{Name: f.name}
		if f.value != nil {
			rf.Value = f.value.(ast.Expr)
		}
		p.adopt(rf, &rf.Base, p.span(f.start, f.end))
		n.Fields = append(n.Fields, rf)
	}
	p.finish(n, &n.Base)
	return p.parsePostfix(n, b.Sp.Start, false)
}

// group is one "[" … "]" read in bracket mode before it is known to be a
// type prefix or an array literal.
type group struct {
	start, end uint32
	elems      []ast.Expr
	repeat     bool
	count      ast.Expr
}

// parseBGroups reads consecutive "[" … "]" groups and decides at the token
// after the last one: a type follows, so every group is a slice
// or array prefix; otherwise the first is an array literal and the rest are
// index suffixes on it.
func (p *parser) parseBGroups() ast.TypeOrExpr {
	var gs []group
	for p.at(ast.TokLBracket) {
		gs = append(gs, p.parseGroup())
		if len(gs) > p.maxDepth {
			p.depthError()
			return p.badExprFrom(p.p)
		}
	}
	if p.typeStartsHere() {
		allType := true
		for _, g := range gs {
			if g.repeat || len(g.elems) > 1 {
				allType = false
			}
		}
		if allType {
			elem := p.parsePrefixType()
			for i := len(gs) - 1; i >= 0; i-- {
				g := gs[i]
				sp := p.span(g.start, elem.Span().End)
				if len(g.elems) == 0 {
					n := &ast.SliceType{Elem: elem}
					p.adopt(n, &n.Base, sp)
					elem = n
				} else {
					n := &ast.ArrayType{Len: g.elems[0], Elem: elem}
					p.adopt(n, &n.Base, sp)
					elem = n
				}
			}
			return elem
		}
	}
	var x ast.Expr = p.groupLit(gs[0])
	for _, g := range gs[1:] {
		n := &ast.IndexExpr{X: x}
		if g.repeat || len(g.elems) == 0 {
			p.syntaxErr(p.span(g.start, g.end), "an index needs one value")
		}
		for _, e := range g.elems {
			n.Args = append(n.Args, e)
		}
		p.adopt(n, &n.Base, p.span(gs[0].start, g.end))
		x = n
	}
	return p.parsePostfix(x, gs[0].start, false)
}

// typeStartsHere reports whether the token after a group starts a type.
func (p *parser) typeStartsHere() bool {
	switch p.kind() {
	case ast.TokIdent, ast.TokQuestion, ast.TokAmp, ast.TokAmpAmp, ast.TokStar, ast.TokMut, ast.TokFn, ast.TokLParen:
		return true
	}
	return false
}

func (p *parser) parseGroup() group {
	g := group{start: p.curStart()}
	p.next()
	saved, savedCover := p.listCtx, p.coverOK
	p.listCtx, p.coverOK = false, false
	for !p.at(ast.TokRBracket) && !p.at(ast.TokEOF) {
		g.elems = append(g.elems, p.parseExpr())
		if len(g.elems) == 1 && p.at(ast.TokSemi) {
			p.next()
			g.repeat = true
			g.count = p.parseExpr()
			break
		}
		if !p.accept(ast.TokComma) {
			break
		}
		p.region()
	}
	p.listCtx, p.coverOK = saved, savedCover
	if !p.expect(ast.TokRBracket) {
		p.skip(stopList)
		p.accept(ast.TokRBracket)
	}
	g.end = p.lastEnd
	return g
}

// groupLit turns a group into an ArrayLit or RepeatLit.
func (p *parser) groupLit(g group) ast.Expr {
	sp := p.span(g.start, g.end)
	if g.repeat {
		n := &ast.RepeatLit{Value: g.elems[0], Count: g.count}
		p.adopt(n, &n.Base, sp)
		return n
	}
	n := &ast.ArrayLit{Elems: g.elems}
	p.adopt(n, &n.Base, sp)
	return n
}

// toType converts an expression-shaped bracket argument to the type it
// spells; anything else is E0101 "expected a type".
func (p *parser) toType(n ast.TypeOrExpr) ast.Type {
	switch x := n.(type) {
	case ast.Type:
		return x
	case *ast.Ident:
		return p.pathTypeOf([]*ast.Ident{x}, x.Sp, x.Sp, nil)
	case *ast.FieldExpr:
		if names, ok := fieldChain(x); ok {
			return p.pathTypeOf(names, x.Sp, x.Sp, nil)
		}
	case *ast.IndexExpr:
		var names []*ast.Ident
		ok := false
		switch y := x.X.(type) {
		case *ast.Ident:
			names, ok = []*ast.Ident{y}, true
		case *ast.FieldExpr:
			names, ok = fieldChain(y)
		}
		if ok {
			args := &ast.TypeArgs{Args: x.Args}
			p.adopt(args, &args.Base, p.span(x.X.Span().End, x.Sp.End))
			return p.pathTypeOf(names, x.X.Span(), x.Sp, args)
		}
	case *ast.UnaryExpr:
		switch x.Op {
		case ast.TokAmp:
			t := &ast.RefType{Elem: p.toType(x.X)}
			p.adopt(t, &t.Base, x.Sp)
			return t
		case ast.TokStar:
			t := &ast.RawPtrType{Elem: p.toType(x.X)}
			p.adopt(t, &t.Base, x.Sp)
			return t
		case ast.TokMut:
			t := &ast.MutType{Elem: p.toType(x.X)}
			p.adopt(t, &t.Base, x.Sp)
			return t
		}
	case *ast.BinaryExpr:
		switch x.Op {
		case ast.TokPipe:
			// The left operand is converted once: converting it again in the
			// else branch doubled the nodes at each nested level (the
			// conversion must stay linear).
			t := &ast.UnionType{}
			lt := p.toType(x.X)
			if u, ok := lt.(*ast.UnionType); ok {
				t.Types = append(t.Types, u.Types...)
			} else {
				t.Types = append(t.Types, lt)
			}
			t.Types = append(t.Types, p.toType(x.Y))
			p.adopt(t, &t.Base, x.Sp)
			return t
		case ast.TokBang:
			t := &ast.ErrorType{Errors: p.toType(x.X), Ok: p.toType(x.Y)}
			p.adopt(t, &t.Base, x.Sp)
			return t
		}
	case *ast.RecordLit:
		if len(x.Fields) == 1 && x.Fields[0].Value == nil {
			f := x.Fields[0]
			t := &ast.ParenType{Type: p.pathTypeOf([]*ast.Ident{f.Name}, f.Name.Sp, f.Name.Sp, nil)}
			p.adopt(t, &t.Base, x.Sp)
			return t
		}
	case *ast.ParenExpr:
		t := &ast.ParenType{Type: p.toType(x.X)}
		p.adopt(t, &t.Base, x.Sp)
		return t
	case *ast.BadExpr:
		t := &ast.BadType{Toks: x.Toks}
		p.adopt(t, &t.Base, x.Sp)
		return t
	}
	p.syntaxErr(n.Span(), "expected a type")
	t := &ast.BadType{}
	p.adopt(t, &t.Base, n.Span())
	return t
}

// toExpr returns n as an expression; a type-only node there is E0101.
func (p *parser) toExpr(n ast.TypeOrExpr) ast.Expr {
	if e, ok := n.(ast.Expr); ok {
		return e
	}
	p.syntaxErr(n.Span(), "expected an expression, found a type")
	b := &ast.BadExpr{}
	p.adopt(b, &b.Base, n.Span())
	return b
}

// fieldChain flattens a.b.c into its names when every step is a name.
func fieldChain(x *ast.FieldExpr) ([]*ast.Ident, bool) {
	var rev []*ast.Ident
	var e ast.Expr = x
	for {
		switch y := e.(type) {
		case *ast.FieldExpr:
			rev = append(rev, y.Name)
			e = y.X
			continue
		case *ast.Ident:
			rev = append(rev, y)
			out := make([]*ast.Ident, len(rev))
			for i, id := range rev {
				out[len(rev)-1-i] = id
			}
			return out, true
		}
		return nil, false
	}
}

// pathTypeOf builds the PathType that names spell. pathSp is the span of the
// expression the names came from (an Ident or a FieldExpr chain), not one
// computed from the names: a missing last name (`A.` then a space or the end)
// is an empty Ident at the next token, past the end of the expression, and a
// span ending there would leave the PathType (ast.Validate).
func (p *parser) pathTypeOf(names []*ast.Ident, pathSp, sp source.Span, args *ast.TypeArgs) *ast.PathType {
	path := &ast.Path{Names: names}
	p.adopt(path, &path.Base, pathSp)
	t := &ast.PathType{Path: path, Args: args}
	p.adopt(t, &t.Base, sp)
	return t
}
