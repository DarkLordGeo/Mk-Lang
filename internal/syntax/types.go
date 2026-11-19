// Why: types appear in signatures, bindings, fields and generic arguments,
// and the type grammar has its own prefix and fn-type rules apart from
// expressions.
//
// What: the type productions (Type, ErrorType, UnionType, PrefixType,
// FnParams, FnParam, Record): parseType, parseUnionType, parsePrefixType,
// parseArrayType, parseFnType, parseParenType, parsePathType and
// parseTypeArgs.
//
// How: a fn type's flows are greedy; as a signature's result its flows may
// continue on following lines (contFlows). `&&` is read as two `&`. The
// foreign forms `[T; N]` (E1206) and `Name<T>` (E0101 with a bracket
// rewrite) are reported and read as the Mk type.

package syntax

import (
	"strings"

	"mk/internal/ast"
)

// parseType reads Type = UnionType [ "!" Type ].
func (p *parser) parseType() ast.Type {
	if p.tooDeep {
		return p.badTypeFrom(p.p)
	}
	start := p.curStart()
	x := p.parseUnionType()
	if !p.at(ast.TokBang) {
		return x
	}
	n := &ast.ErrorType{}
	p.beginAt(&n.Base, start)
	p.next()
	n.Errors = x
	n.Ok = p.parseType()
	p.finish(n, &n.Base)
	return n
}

// parseUnionType reads PrefixType { "|" PrefixType }.
func (p *parser) parseUnionType() ast.Type {
	start := p.curStart()
	x := p.parsePrefixType()
	if !p.at(ast.TokPipe) {
		return x
	}
	n := &ast.UnionType{Types: []ast.Type{x}}
	p.beginAt(&n.Base, start)
	for p.accept(ast.TokPipe) {
		n.Types = append(n.Types, p.parsePrefixType())
	}
	p.finish(n, &n.Base)
	return n
}

// parsePrefixType reads one PrefixType.
func (p *parser) parsePrefixType() ast.Type {
	if p.tooDeep {
		return p.badTypeFrom(p.p)
	}
	sigResult := p.sigResult
	p.sigResult = false
	switch p.kind() {
	case ast.TokAmp:
		n := &ast.RefType{}
		p.begin(&n.Base)
		p.next()
		if p.at(ast.TokMut) {
			p.ampMutHabit()
			m := &ast.MutType{}
			p.begin(&m.Base)
			p.next()
			m.Elem = p.parsePrefixType()
			p.finish(m, &m.Base)
			n.Elem = m
		} else {
			n.Elem = p.parsePrefixType()
		}
		p.finish(n, &n.Base)
		return n
	case ast.TokAmpAmp:
		// && is two & here; the inner node starts at the second byte.
		n := &ast.RefType{}
		p.begin(&n.Base)
		inner := &ast.RefType{}
		p.beginAt(&inner.Base, p.curStart()+1)
		p.next()
		inner.Elem = p.parsePrefixType()
		p.finish(inner, &inner.Base)
		n.Elem = inner
		p.finish(n, &n.Base)
		return n
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
		n := &ast.MutType{}
		p.begin(&n.Base)
		p.next()
		n.Elem = p.parsePrefixType()
		p.finish(n, &n.Base)
		return n
	case ast.TokQuestion:
		n := &ast.OptionalType{}
		p.begin(&n.Base)
		p.next()
		n.Elem = p.parsePrefixType()
		p.finish(n, &n.Base)
		return n
	case ast.TokStar:
		n := &ast.RawPtrType{}
		p.begin(&n.Base)
		p.next()
		n.Elem = p.parsePrefixType()
		p.finish(n, &n.Base)
		return n
	case ast.TokLBracket:
		return p.parseArrayType()
	case ast.TokFn:
		if p.peekKind(1) == ast.TokLParen {
			return p.parseFnType(sigResult)
		}
	case ast.TokLParen:
		return p.parseParenType()
	case ast.TokIdent:
		return p.parsePathType()
	case ast.TokLifetime:
		// A run of lifetimes is a loop, not a recursion: no node is made for
		// them, so recursion would escape the depth limit.
		for p.at(ast.TokLifetime) {
			p.lifetimeHabit()
			p.next()
		}
		p.sigResult = sigResult
		return p.parsePrefixType()
	case ast.TokBad:
		s0 := p.p
		p.quiet = true
		p.next()
		return p.badTypeFrom(s0)
	}
	if p.kind().IsKeyword() && p.kind() != ast.TokFn {
		p.keywordAsName()
		return p.parsePathType()
	}
	p.expected("a type")
	return p.badTypeFrom(p.p)
}

// parseArrayType reads "[" "]" PrefixType or "[" Expr "]" PrefixType; the
// Rust form "[" Type ";" Expr "]" is E1206 and read as an ArrayType.
func (p *parser) parseArrayType() ast.Type {
	start := p.curStart()
	if p.peekKind(1) == ast.TokRBracket {
		n := &ast.SliceType{}
		p.begin(&n.Base)
		p.next()
		p.next()
		n.Elem = p.parsePrefixType()
		p.finish(n, &n.Base)
		return n
	}
	if t := p.rustArrayType(); t != nil {
		return t
	}
	n := &ast.ArrayType{}
	p.beginAt(&n.Base, start)
	p.next()
	saved := p.listCtx
	p.listCtx = false
	n.Len = p.parseExpr()
	p.listCtx = saved
	p.expect(ast.TokRBracket)
	n.Elem = p.parsePrefixType()
	p.finish(n, &n.Base)
	return n
}

// parseFnType reads "fn" "(" [ FnParams ] ")" [ ":" Type ] { [ "," ] Flow }.
// sigResult says the fn type is a signature's result: its flows may
// continue on following lines and belong to it.
func (p *parser) parseFnType(sigResult bool) ast.Type {
	n := &ast.FnType{}
	p.begin(&n.Base)
	inList := p.listCtx
	p.next()
	p.next()
	p.listCtx = true
	for !p.at(ast.TokRParen) && !p.at(ast.TokEOF) {
		if p.at(ast.TokEllipsis) || p.at(ast.TokConst) || p.at(ast.TokVar) || p.atWord("self") && (p.peekKind(1) == ast.TokComma || p.peekKind(1) == ast.TokRParen) {
			p.errHere("a fn type's parameters take no `self`, `const`, `var` or `...`")
			p.next()
		} else {
			n.Params = append(n.Params, p.parseFnParam())
		}
		if !p.at(ast.TokComma) {
			break
		}
		p.next()
		p.region()
	}
	p.listCtx = false
	p.expect(ast.TokRParen)
	if sigResult {
		p.cont(true)
	}
	if p.at(ast.TokColon) || p.at(ast.TokArrow) {
		if p.at(ast.TokArrow) {
			p.arrowHabit()
		}
		p.next()
		p.listCtx = inList
		p.sigResult = sigResult
		n.Result = p.parseType()
		p.sigResult = false
		p.listCtx = false
		if sigResult {
			p.contFlows()
		}
	}
	for p.flowStart(p.tok()) && p.flowAhead() {
		n.Flows = append(n.Flows, p.parseFlow(inList))
		if sigResult {
			p.contFlows()
		}
		if p.at(ast.TokComma) && !inList && p.flowStart(p.peek(1)) && p.flowAheadAt(1) {
			p.next()
		}
	}
	p.listCtx = inList
	p.finish(n, &n.Base)
	return n
}

// contFlows skips a real Term before a line beginning with from, ends or *
// inside a signature's fn result type.
func (p *parser) contFlows() {
	if p.isLineTerm() && p.flowStart(p.peek(1)) {
		p.next()
		p.sigContinued = true
	}
}

// flowAhead is true when the current from, ends or * starts a flow: a name
// follows it.
func (p *parser) flowAhead() bool { return p.flowAheadAt(0) }

func (p *parser) flowAheadAt(k int) bool {
	t := p.peek(k + 1)
	if t.Kind == ast.TokLParen && p.tokText(p.peek(k)) == "from" {
		// The per-field clause: `from (` Name `:`.
		n := p.peek(k + 2)
		return (n.Kind == ast.TokIdent || n.Kind.IsKeyword()) && p.peekKind(k+3) == ast.TokColon
	}
	return t.Kind == ast.TokIdent || t.Kind.IsKeyword()
}

func (p *parser) parseFnParam() *ast.FnParam {
	n := &ast.FnParam{}
	p.begin(&n.Base)
	if (p.at(ast.TokIdent) || p.kind().IsKeyword() && p.kind() != ast.TokFn && p.kind() != ast.TokMut) && p.peekKind(1) == ast.TokColon {
		n.Name = p.parseDeclName(declParam)
		p.next()
	}
	n.Type = p.parseType()
	if p.atWord("into") {
		p.next()
		n.Into = p.parseName("a parameter name")
	}
	p.finish(n, &n.Base)
	return n
}

// parseParenType reads Record = "(" Fields ")" or "(" Type ")".
func (p *parser) parseParenType() ast.Type {
	if p.peekKind(1) == ast.TokRParen {
		s0 := p.p
		p.errHere("the empty record type cannot be written")
		p.next()
		p.next()
		return p.badTypeFrom(s0)
	}
	if (p.peekKind(1) == ast.TokIdent || p.peekKind(1).IsKeyword()) && p.peekKind(2) == ast.TokColon {
		n := &ast.RecordType{}
		p.begin(&n.Base)
		n.Fields = p.parseFieldsParen()
		p.finish(n, &n.Base)
		return n
	}
	n := &ast.ParenType{}
	p.begin(&n.Base)
	p.next()
	saved := p.listCtx
	p.listCtx = false
	n.Type = p.parseType()
	p.listCtx = saved
	p.expect(ast.TokRParen)
	p.finish(n, &n.Base)
	return n
}

// parsePathType reads Path [ TypeArgs ]; '<' after a type name is E0101
// with facts.target.
func (p *parser) parsePathType() ast.Type {
	n := &ast.PathType{}
	p.begin(&n.Base)
	n.Path = p.parsePath()
	if p.at(ast.TokLBracket) {
		n.Args = p.parseTypeArgs()
	} else if p.at(ast.TokLt) && p.tok().Start == p.lastEnd {
		p.angleArgs(n)
	}
	p.finish(n, &n.Base)
	return n
}

// parseTypeArgs reads "[" TypeOrExpr { "," TypeOrExpr } [ "," ] "]".
func (p *parser) parseTypeArgs() *ast.TypeArgs {
	n := &ast.TypeArgs{}
	p.begin(&n.Base)
	p.next()
	n.Args = p.parseBracketList(ast.TokRBracket)
	p.expect(ast.TokRBracket)
	p.finish(n, &n.Base)
	return n
}

// angleArgs reports Name<…> with the line rewritten to brackets as
// facts.target, reads the arguments up to the matching '>' and keeps them.
func (p *parser) angleArgs(n *ast.PathType) {
	lt := p.curSpan()
	args := &ast.TypeArgs{}
	p.begin(&args.Base)
	p.next()
	saved := p.listCtx
	p.listCtx = true
	for !p.at(ast.TokGt) && !p.at(ast.TokShr) && !p.at(ast.TokEOF) && !p.at(ast.TokTerm) {
		args.Args = append(args.Args, p.parseType())
		if !p.accept(ast.TokComma) {
			break
		}
	}
	p.listCtx = saved
	gt := p.curSpan()
	if p.at(ast.TokGt) || p.at(ast.TokShr) {
		p.next()
	}
	p.finish(args, &args.Base)
	n.Args = args
	d := p.syntaxErr(lt, "type arguments use brackets: `[`…`]`")
	if d == nil {
		return
	}
	line := int(p.f.LineCol(lt.Start).Line)
	ls := p.f.LineStart(line)
	text := p.f.Line(line)
	off := int(lt.Start - ls)
	gtOff := int(gt.Start - ls)
	if gtOff < len(text) && off < gtOff {
		target := text[:off] + "[" + text[off+1:gtOff] + "]" + text[gtOff+1:]
		d.Text("target", strings.TrimRight(target, " \t"))
	}
}
