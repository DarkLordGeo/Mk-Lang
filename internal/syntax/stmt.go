// Why: statements are where most parse errors happen, so their parsing
// carries the recovery rules that keep one mistake from cascading.
//
// What: the statement productions (Block, Stmt, Bind, Assign, If, Cond,
// For, ForHead, ForSource, Match, Arm, Return, become, Fail, Defer, Scope,
// Unsafe, the labeled For, break and continue): parseBlock, parseStmt and
// one parse function per statement kind.
//
// How: parseStmtInner dispatches on the first token; a statement that
// starts with an expression is read through the RecordPat cover and becomes
// a destructure when ":=" follows. An else at statement start is attached to
// the previous if chain when it can be (attachElse).

package syntax

import (
	"bytes"

	"mk/internal/ast"
)

// parseBlock reads "{" { Stmt Term } "}".
func (p *parser) parseBlock() *ast.Block {
	n := &ast.Block{}
	p.begin(&n.Base)
	savedBracket, savedList, savedHead, savedLit := p.inBracket, p.listCtx, p.inHead, p.litOK
	p.inBracket, p.listCtx, p.inHead, p.litOK = false, false, false, false
	defer func() { p.inBracket, p.listCtx, p.inHead, p.litOK = savedBracket, savedList, savedHead, savedLit }()
	if p.tooDeep {
		p.skipBlock()
		p.finish(n, &n.Base)
		return n
	}
	p.next()
	p.region()
	for !p.at(ast.TokRBrace) && !p.at(ast.TokEOF) && !p.tooDeep {
		if p.at(ast.TokTerm) {
			p.next()
			continue
		}
		before := p.p
		if p.at(ast.TokElse) && len(n.Stmts) > 0 && p.attachElse(n.Stmts[len(n.Stmts)-1]) {
			p.endElem("one statement per line")
			continue
		}
		st := p.parseStmt()
		n.Stmts = append(n.Stmts, st)
		p.endElem("one statement per line")
		if p.p == before && !p.at(ast.TokRBrace) && !p.at(ast.TokEOF) {
			p.next()
		}
	}
	if p.tooDeep {
		p.skipBlock()
	} else {
		p.expect(ast.TokRBrace)
	}
	p.finish(n, &n.Base)
	return n
}

// skipBlock skips to the '}' that closes the current block, for the depth
// error.
func (p *parser) skipBlock() {
	depth := 0
	for !p.at(ast.TokEOF) {
		switch p.kind() {
		case ast.TokLBrace:
			depth++
		case ast.TokRBrace:
			depth--
			if depth <= 0 {
				p.next()
				return
			}
		}
		p.next()
	}
}

// parseStmt reads one statement; it never returns nil.
func (p *parser) parseStmt() ast.Stmt {
	if p.tooDeep {
		return p.badStmtFrom(p.p)
	}
	savedGo := p.goSpans
	p.goSpans = nil
	st := p.parseStmtInner()
	for _, sp := range p.goSpans {
		p.syntaxErr(sp, "`.go` is a statement")
	}
	p.goSpans = savedGo
	return st
}

func (p *parser) parseStmtInner() ast.Stmt {
	// A keyword declared as a local, `type := e` or `type: T = e`, is E0102
	// and read as a name, before any keyword's own production
	// or habit: no statement starts with a keyword then ':' or ':='.
	if k := p.kind(); k.IsKeyword() && (p.peekKind(1) == ast.TokColonEq || p.peekKind(1) == ast.TokColon) {
		if p.peekKind(1) == ast.TokColon && p.peekKind(2) == ast.TokFor {
			label := p.parseName("a label")
			p.next()
			return p.parseFor(label)
		}
		return p.parseBind(false)
	}
	if h := p.stmtHabit(); h != nil {
		return h
	}
	switch k := p.kind(); k {
	case ast.TokVar:
		return p.parseVarStmt()
	case ast.TokIf:
		return p.parseIf()
	case ast.TokFor:
		return p.parseFor(nil)
	case ast.TokMatch:
		return p.parseMatch()
	case ast.TokReturn:
		return p.parseReturn()
	case ast.TokFail:
		n := &ast.FailStmt{}
		p.begin(&n.Base)
		p.next()
		n.Value = p.parseExpr()
		p.finish(n, &n.Base)
		return n
	case ast.TokBreak, ast.TokContinue:
		return p.parseBreak()
	case ast.TokDefer, ast.TokOnerr:
		return p.parseDefer()
	case ast.TokScope:
		return p.parseScope(false)
	case ast.TokTry:
		if p.peekKind(1) == ast.TokScope {
			return p.parseScope(true)
		}
	case ast.TokUnsafe:
		return p.parseUnsafe()
	case ast.TokAsm:
		return p.parseAsm()
	case ast.TokBecome:
		return p.parseBecome()
	case ast.TokUnderscore:
		switch p.peekKind(1) {
		case ast.TokAssign:
			n := &ast.DiscardStmt{}
			p.begin(&n.Base)
			p.next()
			p.next()
			n.Value = p.parseValue()
			p.finish(n, &n.Base)
			return n
		case ast.TokColonEq:
			p.errHere("write `_ = e`")
			n := &ast.DiscardStmt{}
			p.begin(&n.Base)
			p.next()
			p.next()
			n.Value = p.parseValue()
			p.finish(n, &n.Base)
			return n
		}
	case ast.TokLBrace:
		s0 := p.p
		p.errHere("a block is not a statement: split into a function")
		p.parseBlock()
		return p.badStmtFrom(s0)
	case ast.TokElse:
		return p.strayElse()
	case ast.TokIdent:
		switch p.peekKind(1) {
		case ast.TokColon:
			if p.peekKind(2) == ast.TokFor {
				label := p.parseName("a label")
				p.next()
				return p.parseFor(label)
			}
			return p.parseBind(false)
		case ast.TokColonEq:
			return p.parseBind(false)
		}
	case ast.TokBad:
		s0 := p.p
		p.quiet = true
		p.skip(stopStmt)
		return p.badStmtFrom(s0)
	}
	return p.parseExprStmt(false)
}

// parseVarStmt reads "var" Name … or "var" RecordPat ":=" Expr.
func (p *parser) parseVarStmt() ast.Stmt {
	if (p.peekKind(1) == ast.TokIdent || p.peekKind(1).IsKeyword()) &&
		(p.peekKind(2) == ast.TokColonEq || p.peekKind(2) == ast.TokColon) {
		return p.parseBind(true)
	}
	return p.parseExprStmt(true)
}

// parseBind reads [ "var" ] Name ( ":=" Expr | ":" Type [ "=" Expr ] ).
func (p *parser) parseBind(isVar bool) ast.Stmt {
	n := &ast.BindStmt{Var: isVar}
	p.begin(&n.Base)
	if isVar {
		p.next()
	}
	n.Name = p.parseDeclName(declLocal)
	if p.accept(ast.TokColonEq) {
		n.Value = p.parseValue()
	} else {
		p.next()
		n.Type = p.parseType()
		if p.accept(ast.TokAssign) {
			n.Value = p.parseValue()
		}
	}
	p.finish(n, &n.Base)
	return n
}

// parseExprStmt reads an expression statement, an assignment, or a
// destructure through the RecordPat cover.
func (p *parser) parseExprStmt(isVar bool) ast.Stmt {
	start := p.curStart()
	s0 := p.p
	if isVar {
		p.next()
	}
	savedMarks := p.coverMarks
	p.coverMarks = nil
	p.coverOK = true
	x := p.parseExpr()
	p.coverOK = false
	marks := p.coverMarks
	p.coverMarks = savedMarks
	if p.at(ast.TokColonEq) {
		return p.parseDestructure(x, start, isVar)
	}
	for _, m := range marks {
		p.syntaxErr(m.span, m.msg)
	}
	if isVar {
		p.syntaxErr(p.toks[p.sig[s0]].Span(p.file), "`var` declares a name: `var x := e`")
	}
	if p.kind().IsAssignOp() {
		n := &ast.AssignStmt{Target: x, Op: p.kind()}
		p.beginAt(&n.Base, start)
		p.next()
		n.Value = p.parseValue()
		if p.kind().IsAssignOp() {
			p.errHere("one assignment per statement")
			p.next()
			p.parseExpr()
		}
		p.finish(n, &n.Base)
		return n
	}
	n := &ast.ExprStmt{X: x}
	p.beginAt(&n.Base, start)
	p.finish(n, &n.Base)
	if g, ok := x.(*ast.GoExpr); ok {
		for i, sp := range p.goSpans {
			if sp == g.Sp {
				p.goSpans = append(p.goSpans[:i:i], p.goSpans[i+1:]...)
				break
			}
		}
	}
	return n
}

// strayElse reports an else at statement start and parses
// its part; the block parser attaches it to the previous if.
func (p *parser) strayElse() ast.Stmt {
	s0 := p.p
	p.errHere("put `else` on the line of the closing brace")
	p.next()
	if p.at(ast.TokIf) {
		p.parseIf()
	} else if p.at(ast.TokLBrace) {
		p.parseBlock()
	}
	return p.badStmtFrom(s0)
}

// parseIf reads "if" Cond Block [ "else" ( If | Block ) ].
func (p *parser) parseIf() ast.Stmt {
	n := &ast.IfStmt{}
	p.begin(&n.Base)
	p.next()
	if p.atWord("let") && p.peekKind(1) == ast.TokIdent {
		n.Cond = p.ifLetHabit()
	} else {
		done := p.head()
		n.Cond = p.parseCond(false)
		done()
	}
	n.Then = p.parseBlockOrMissing()
	if p.at(ast.TokElse) {
		p.next()
		if p.at(ast.TokIf) {
			n.Else = p.parseIf()
		} else {
			n.Else = p.parseBlockOrMissing()
		}
	}
	p.finish(n, &n.Base)
	return n
}

// parseCond reads Cond = Name ":=" Expr | Name "is" Type | Expr.
func (p *parser) parseCond(forHead bool) ast.Node {
	if p.at(ast.TokLParen) && p.condDestructure() {
		s0 := p.p
		p.errHere("`if` binds one name")
		p.skipToBrace()
		return p.badExprFrom(s0)
	}
	if p.at(ast.TokIdent) || p.kind().IsKeyword() && p.peekKind(1) == ast.TokColonEq {
		switch p.peekKind(1) {
		case ast.TokColonEq:
			n := &ast.BindCond{}
			p.begin(&n.Base)
			n.Name = p.parseDeclName(declLocal)
			p.next()
			n.Value = p.parseExpr()
			p.finish(n, &n.Base)
			return n
		case ast.TokIs:
			if forHead {
				p.errHere("a type test cannot change between iterations")
			}
			n := &ast.IsCond{}
			p.begin(&n.Base)
			n.Name = p.ident()
			p.next()
			n.Type = p.parseType()
			p.finish(n, &n.Base)
			return n
		}
	}
	return p.parseExpr()
}

// condDestructure is true at "(" … ")" ":=" in a condition.
func (p *parser) condDestructure() bool {
	depth := 0
	for i := 0; ; i++ {
		switch p.peekKind(i) {
		case ast.TokLParen:
			depth++
		case ast.TokRParen:
			depth--
			if depth == 0 {
				return p.peekKind(i+1) == ast.TokColonEq
			}
		case ast.TokEOF, ast.TokLBrace, ast.TokTerm:
			return false
		}
	}
}

// skipToBrace skips to the '{' that opens the block of a head.
func (p *parser) skipToBrace() {
	for !p.at(ast.TokLBrace) && !p.at(ast.TokEOF) && !p.at(ast.TokTerm) {
		p.next()
	}
}

// parseFor reads [ Name ":" ] "for" [ ForHead ] Block.
func (p *parser) parseFor(label *ast.Ident) ast.Stmt {
	n := &ast.ForStmt{Label: label}
	if label != nil {
		p.beginAt(&n.Base, label.Sp.Start)
	} else {
		p.begin(&n.Base)
	}
	p.next()
	done := p.head()
	switch {
	case p.at(ast.TokLBrace):
	case p.cStyleFor():
		// Before the other heads: `for i := 0; …` starts like a BindCond
		// (E1206).
		n.Head = p.cStyleForHabit()
	case p.goRangeAhead():
		// Go's `for i, x := range xs {`, a foreign form.
		s0 := p.p
		for !p.atWord("range") {
			p.next()
		}
		p.errHere("write `for i, x in xs {`")
		p.skipToBrace()
		n.Head = p.badExprFrom(s0)
	case (p.at(ast.TokIdent) || p.at(ast.TokUnderscore) || p.kind().IsKeyword()) && (p.peekKind(1) == ast.TokComma || p.peekKind(1) == ast.TokIn),
		p.loopPatAhead(0):
		// A keyword loop name is E0102 and read as a name. A
		// destructured loop variable starts at "(".
		n.Head = p.parseInHead()
	default:
		n.Head = p.parseCond(true)
	}
	done()
	if p.fn != nil {
		p.fn.loops++
	}
	n.Body = p.parseBlockOrMissing()
	if p.fn != nil {
		p.fn.loops--
	}
	p.finish(n, &n.Base)
	return n
}

// goRangeAhead is true at a for head `i := range` or `i, x := range`, each
// name an Ident or `_`.
func (p *parser) goRangeAhead() bool {
	name := func(k int) bool { return p.peekKind(k) == ast.TokIdent || p.peekKind(k) == ast.TokUnderscore }
	if !name(0) {
		return false
	}
	if p.peekKind(1) == ast.TokColonEq {
		return p.peekWord(2, "range")
	}
	return p.peekKind(1) == ast.TokComma && name(2) && p.peekKind(3) == ast.TokColonEq && p.peekWord(4, "range")
}

func (p *parser) loopName() *ast.Ident {
	if p.at(ast.TokUnderscore) {
		return p.identUnderscore()
	}
	return p.parseDeclName(declLocal)
}

// parseInHead reads Name [ "," Name ] "in" ForSource.
func (p *parser) parseInHead() *ast.InHead {
	n := &ast.InHead{}
	p.begin(&n.Base)
	if p.loopPatAhead(0) {
		n.Pat = p.parseLoopPat()
	} else {
		n.Key = p.loopName()
		if p.accept(ast.TokComma) {
			if p.loopPatAhead(0) {
				n.Pat = p.parseLoopPat()
			} else {
				n.Value = p.loopName()
			}
		}
	}
	if n.Pat != nil && p.at(ast.TokComma) {
		// A RecordPat is only the last loop name.
		s0 := p.p
		p.errHere("a destructured loop variable is the last name: `for i, (a, b) in xs`")
		for !p.at(ast.TokIn) && !p.at(ast.TokLBrace) && !p.at(ast.TokEOF) && !p.at(ast.TokTerm) {
			p.next()
		}
		_ = s0
	}
	p.expect(ast.TokIn)
	switch {
	case p.atWord("fields") && p.peekKind(1) == ast.TokLParen:
		if n.Value == nil {
			p.errHere("a fields loop names the field and its value: `for f, v in fields(x)`")
		}
		fs := &ast.FieldsSource{}
		p.begin(&fs.Base)
		p.toks[p.sig[p.p]].Kind = ast.TokFields // the contextual word fields
		p.next()
		p.next()
		fs.X = p.parseExpr()
		p.expect(ast.TokRParen)
		p.finish(fs, &fs.Base)
		n.Source = fs
	default:
		start := p.curStart()
		x := p.parseExpr()
		if p.at(ast.TokDotDot) {
			rs := &ast.RangeSource{Lo: x}
			p.beginAt(&rs.Base, start)
			if n.Value != nil {
				p.syntaxErr(n.Value.Sp, "a range has no index")
			}
			p.next()
			eq := p.inclusiveEq()
			rs.Hi = p.parseExpr()
			if eq.File != 0 {
				p.inclusiveRangeHabit(eq, rs.Hi)
			}
			p.finish(rs, &rs.Base)
			n.Source = rs
			break
		}
		rs := &ast.RefSource{X: x}
		if u, ok := x.(*ast.UnaryExpr); ok && (u.Op == ast.TokAmp || u.Op == ast.TokMut) {
			rs.Mode = ast.ByRef
			if u.Op == ast.TokMut {
				rs.Mode = ast.ByMutRef
			}
			rs.X = u.X
		}
		p.beginAt(&rs.Base, start)
		p.finish(rs, &rs.Base)
		n.Source = rs
	}
	p.loopPatSource(n)
	p.finish(n, &n.Base)
	return n
}

// parseMatch reads "match" Expr "{" { Arm Term } "}".
func (p *parser) parseMatch() ast.Stmt {
	n := &ast.MatchStmt{}
	p.begin(&n.Base)
	p.next()
	done := p.head()
	n.X = p.parseExpr()
	done()
	if p.expect(ast.TokLBrace) {
		p.region()
		for !p.at(ast.TokRBrace) && !p.at(ast.TokEOF) && !p.tooDeep {
			if p.at(ast.TokTerm) {
				p.next()
				continue
			}
			before := p.p
			n.Arms = append(n.Arms, p.parseArm())
			p.endElem("one arm per line")
			if p.p == before && !p.at(ast.TokRBrace) && !p.at(ast.TokEOF) {
				p.next()
			}
		}
		if !p.tooDeep {
			p.expect(ast.TokRBrace)
		}
	}
	p.finish(n, &n.Base)
	return n
}

// parseArm reads Pattern Block; => is E1214.
func (p *parser) parseArm() *ast.Arm {
	n := &ast.Arm{}
	p.begin(&n.Base)
	n.Pat = p.parsePattern()
	switch {
	case p.at(ast.TokLBrace):
		n.Body = p.parseBlock()
	case p.at(ast.TokFatArrow):
		n.Body = p.fatArrowHabit()
	case p.at(ast.TokTerm):
		p.errHere("the arm's block opens on the pattern's line")
		n.Body = p.parseBlockOrMissing()
	default:
		n.Body = p.parseBlockOrMissing()
	}
	p.finish(n, &n.Base)
	return n
}

func (p *parser) parseReturn() ast.Stmt {
	n := &ast.ReturnStmt{}
	p.begin(&n.Base)
	p.next()
	if !p.at(ast.TokTerm) && !p.at(ast.TokRBrace) && !p.at(ast.TokEOF) {
		n.Value = p.parseValue()
		if p.at(ast.TokComma) {
			p.errHere("return a record: `return (a, b)`")
			for p.accept(ast.TokComma) {
				p.parseExpr()
			}
		}
	}
	p.finish(n, &n.Base)
	return n
}

// parseBecome reads "become" Postfix, a guaranteed tail call. The Postfix
// must end in a call suffix; any other operand is E0101 and the statement
// a BadStmt, so a BecomeStmt's Call is never nil.
func (p *parser) parseBecome() ast.Stmt {
	s0 := p.p
	at := p.curStart()
	p.next()
	start := p.curStart()
	x := p.parsePostfix(p.parseOperand(false), start, false)
	if call, ok := x.(*ast.CallExpr); ok && (p.at(ast.TokTerm) || p.at(ast.TokRBrace) || p.at(ast.TokEOF)) {
		n := &ast.BecomeStmt{Call: call}
		p.beginAt(&n.Base, at)
		p.finish(n, &n.Base)
		return n
	}
	p.syntaxErr(p.span(start, p.lastEnd), "`become` takes a call: `become f(a)`", "return the value: `return e`")
	p.skip(stopStmt)
	return p.badStmtFrom(s0)
}

// parseBreak reads "break" [ Name ] or "continue" [ Name ].
func (p *parser) parseBreak() ast.Stmt {
	isBreak := p.at(ast.TokBreak)
	word := p.curText()
	start := p.curSpan()
	var b ast.Base
	p.begin(&b)
	p.next()
	var label *ast.Ident
	if p.at(ast.TokIdent) {
		label = p.ident()
	}
	if p.fn == nil || p.fn.loops == 0 {
		p.syntaxErr(start, "`"+word+"` outside a loop")
	}
	if isBreak {
		n := &ast.BreakStmt{Base: b, Label: label}
		p.finish(n, &n.Base)
		return n
	}
	n := &ast.ContinueStmt{Base: b, Label: label}
	p.finish(n, &n.Base)
	return n
}

// parseDefer reads ( "defer" | "onerr" ) ( Block | Expr ).
func (p *parser) parseDefer() ast.Stmt {
	n := &ast.DeferStmt{Onerr: p.at(ast.TokOnerr)}
	p.begin(&n.Base)
	p.next()
	if p.at(ast.TokLBrace) {
		n.Body = p.parseBlock()
	} else {
		n.X = p.parseExpr()
		if p.kind().IsAssignOp() {
			p.errHere("an assignment needs `defer { }`")
			p.next()
			p.parseExpr()
		}
	}
	p.finish(n, &n.Base)
	return n
}

// parseScope reads [ "try" ] "scope" Name ":=" Expr Block.
func (p *parser) parseScope(try bool) ast.Stmt {
	n := &ast.ScopeStmt{Try: try}
	p.begin(&n.Base)
	if try {
		p.next()
	}
	p.next()
	n.Name = p.parseDeclName(declLocal)
	p.expect(ast.TokColonEq)
	done := p.head()
	n.Value = p.parseExpr()
	done()
	n.Body = p.parseBlockOrMissing()
	p.finish(n, &n.Base)
	return n
}

// parseUnsafe reads "unsafe" String Block.
func (p *parser) parseUnsafe() ast.Stmt {
	n := &ast.UnsafeStmt{}
	p.begin(&n.Base)
	p.next()
	switch p.kind() {
	case ast.TokStr, ast.TokRawStr, ast.TokMultiStr:
		n.Reason = p.basicLit()
		if v, ok := StringValue(n.Reason.Kind, n.Reason.Raw); ok && len(bytes.TrimSpace(v)) == 0 {
			p.syntaxErr(n.Reason.Sp, "say why: `unsafe \"reason\" { }`")
		}
	default:
		p.errHere("say why: `unsafe \"reason\" { }`")
		lit := &ast.BasicLit{Kind: ast.TokStr}
		p.begin(&lit.Base)
		p.finishAt(lit, &lit.Base, lit.Sp.Start)
		n.Reason = lit
	}
	n.Body = p.parseBlockOrMissing()
	p.finish(n, &n.Base)
	return n
}

// attachElse reports an else at statement start and, when the
// previous statement is an if chain with no final else, parses the else part
// and attaches it there, so the next statements see no cascade.
func (p *parser) attachElse(prev ast.Stmt) bool {
	ifs, ok := prev.(*ast.IfStmt)
	if !ok {
		return false
	}
	chain := []*ast.IfStmt{ifs}
	for {
		next, ok := chain[len(chain)-1].Else.(*ast.IfStmt)
		if !ok {
			break
		}
		chain = append(chain, next)
	}
	last := chain[len(chain)-1]
	if last.Else != nil {
		return false
	}
	p.errHere("put `else` on the line of the closing brace")
	p.next()
	var part ast.Node
	if p.at(ast.TokIf) {
		part = p.parseIf()
	} else {
		part = p.parseBlockOrMissing()
	}
	last.Else = part
	h := p.height[part.ID()]
	for i := len(chain) - 1; i >= 0; i-- {
		chain[i].Sp.End = p.lastEnd
		h++
		if id := chain[i].NID; p.height[id] < h {
			p.height[id] = h
		}
	}
	return true
}
