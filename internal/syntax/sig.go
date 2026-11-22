// Why: a Mk signature carries more than a name and parameters: flows
// (`from`, `ends`, `*`), contracts, cpu and section clauses, and it may span
// several lines, so its parsing needs its own continuation and comma rules.
//
// What: the signature productions (Signature, FuncName, Params, SelfParam,
// Param, ParamFlow, Flow, Sources, Source, Contract, TypeParams, TypeParam,
// Bounds): parseSignature, parseFuncName, parseParams, parseSigFlows,
// parseFlow, parseFieldSrcs and parseTypeParams.
//
// How: a signature continues on a line that starts with from, ends, *,
// requires or ensures (cont). A receiver is `self`, `&self` or `mut self`,
// and takes `into Name` as a parameter does.
// At signature level a ',' between flows is optional; inside a comma list a
// ',' after a fn type's flow continues it only as more Sources.

package syntax

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
)

// sigCtx is where a signature stands.
type sigCtx uint8

const (
	sigFunc sigCtx = iota
	sigExtern
	sigInterface
	sigImpl
)

// contStart reports whether a token begins a continuation line of a
// signature: from, ends, *, requires or ensures.
func (p *parser) contStart(t ast.Token) bool {
	switch t.Kind {
	case ast.TokStar, ast.TokRequires, ast.TokEnsures:
		return true
	case ast.TokIdent:
		s := p.tokText(t)
		return s == "from" || s == "ends"
	}
	return false
}

// flowStart reports whether t begins a flow: from, ends or *.
func (p *parser) flowStart(t ast.Token) bool {
	switch t.Kind {
	case ast.TokStar:
		return true
	case ast.TokIdent:
		s := p.tokText(t)
		return s == "from" || s == "ends"
	}
	return false
}

// cont skips a real Term before a continuation line and records it;
// a line that begins with the identifier cpu or section and a string
// continues it too (the two-token form, so `cpu := ...` stays a
// statement).
func (p *parser) cont(flowsToo bool) {
	if !p.isLineTerm() {
		return
	}
	t := p.peek(1)
	if p.contStart(t) && (flowsToo || !p.flowStart(t)) || p.clauseAt(1) != "" {
		p.next()
		p.sigContinued = true
	}
}

// clauseAt is "cpu" or "section" when the token k ahead is that identifier
// and the one after it a string literal, else "".
func (p *parser) clauseAt(k int) string {
	t := p.peek(k)
	if t.Kind != ast.TokIdent {
		return ""
	}
	switch s := p.tokText(t); s {
	case "cpu", "section":
		switch p.peekKind(k + 1) {
		case ast.TokStr, ast.TokRawStr:
			return s
		}
	}
	return ""
}

// sigClauses reads the Cpu and Section clauses after the contracts
// (Signature = ... { Contract } [ Cpu ] [ Section ]).
func (p *parser) sigClauses(n *ast.Signature) {
	if p.clauseAt(0) == "cpu" {
		p.next()
		n.Cpu = p.basicLit()
		p.cont(true)
	}
	if p.clauseAt(0) == "section" {
		p.next()
		n.Section = p.basicLit()
		p.cont(true)
	}
	if c := p.clauseAt(0); c != "" {
		p.errHere("the cpu clause comes before the section clause, each at most once")
		p.next()
		p.basicLit()
	}
	// A contract after a cpu or section clause would otherwise break the
	// signature with "function needs a body"; name the order instead, and
	// read the contracts for recovery.
	if (n.Cpu != nil || n.Section != nil) && (p.at(ast.TokRequires) || p.at(ast.TokEnsures)) {
		p.errHere("requires and ensures lines come before the cpu and section clauses")
		for p.at(ast.TokRequires) || p.at(ast.TokEnsures) {
			p.next()
			p.parseExpr()
			p.cont(true)
		}
	}
}

// parseSignature reads a Signature; continued reports whether it went on to
// a following line, so a body '{' may start a line of its own.
func (p *parser) parseSignature(ctx sigCtx) (*ast.Signature, bool) {
	n := &ast.Signature{}
	p.begin(&n.Base)
	savedCont := p.sigContinued
	p.sigContinued = false
	savedList := p.listCtx
	p.listCtx = false
	var tp *ast.TypeParams
	n.Name, tp = p.parseFuncName(ctx)
	n.TypeParams = tp
	if p.at(ast.TokLt) && p.tok().Start == p.lastEnd && p.peekKind(1) == ast.TokLifetime {
		p.lifetimeList()
	}
	if n.TypeParams == nil && p.at(ast.TokLBracket) {
		n.TypeParams = p.parseTypeParams()
	}
	recvOK := n.Name.Owner != nil || ctx == sigInterface || ctx == sigImpl
	if p.expect(ast.TokLParen) {
		p.parseParams(n, recvOK, false)
		p.expect(ast.TokRParen)
	}
	p.cont(true)
	if p.at(ast.TokColon) || p.at(ast.TokArrow) {
		if p.at(ast.TokArrow) {
			p.arrowHabit()
		}
		p.next()
		p.sigResult = true
		n.Result = p.parseType()
		p.sigResult = false
		p.cont(true)
	} else if p.bareResultAhead() {
		n.Result = p.bareResultHabit()
		p.cont(true)
	}
	p.parseSigFlows(n)
	for p.at(ast.TokRequires) || p.at(ast.TokEnsures) {
		c := &ast.Contract{}
		p.begin(&c.Base)
		if p.at(ast.TokEnsures) {
			c.Kind = ast.Ensures
		}
		p.next()
		c.X = p.parseExpr()
		p.finish(c, &c.Base)
		n.Contracts = append(n.Contracts, c)
		at := p.p
		p.cont(true)
		// A flow after a contract breaks the grammar's order (flows, then
		// contracts). It is read for recovery into a scratch Signature and
		// dropped: appended to n.Flows it would come before the contracts in
		// EachChild's order and break the tree's source order (FuzzParse
		// 1c7326dab702c699). A `*` on the contract's own line is the
		// expression's multiplication; on a continuation line (cont skipped
		// the Term) it can only start a flow.
		if p.flowStart(p.tok()) && (!p.at(ast.TokStar) || p.p != at) {
			p.errHere("flows come before contracts")
			p.parseSigFlows(&ast.Signature{})
		}
	}
	p.sigClauses(n)
	p.finish(n, &n.Base)
	cont := p.sigContinued
	p.sigContinued = savedCont
	p.listCtx = savedList
	return n, cont
}

// lifetimeList reads a "<" list after a function name that holds only
// lifetimes, `F<'a, 'b>(…)`: one E1203 whose proven edit deletes the list. A
// list holding anything else is left to the caller's "expected `(`".
func (p *parser) lifetimeList() {
	i := 1
	for {
		if p.peekKind(i) != ast.TokLifetime {
			return
		}
		i++
		switch p.peekKind(i) {
		case ast.TokComma:
			i++
			continue
		case ast.TokGt:
		default:
			return
		}
		break
	}
	lt := p.tok()
	gt := p.peek(i)
	p.hit(habitHit{code: diag.E1203, at: p.span(lt.Start, gt.End()), msg: "Mk has no lifetimes",
		edits: []diag.Edit{p.edit(lt.Start, gt.End(), "")}})
	for j := 0; j <= i; j++ {
		p.next()
	}
}

// parseSigFlows reads [ Flow { [ "," ] Flow } ] at signature level.
func (p *parser) parseSigFlows(n *ast.Signature) {
	seenFrom, seenEnds := -1, false
	for p.flowStart(p.tok()) {
		// The separator before a flow is the previous significant token when
		// it is a ',' (secondFrom's merge fix deletes it with a second from).
		sepEnd := p.lastEnd
		if p.p > 0 {
			if prev := p.toks[p.sig[p.p-1]]; prev.Kind == ast.TokComma {
				sepEnd = prev.Start
				if p.p > 1 {
					sepEnd = p.toks[p.sig[p.p-2]].End()
				}
			}
		}
		f := p.parseFlow(false)
		switch f := f.(type) {
		case *ast.FromFlow:
			if seenFrom >= 0 {
				p.secondFrom(n.Flows[seenFrom].(*ast.FromFlow), f, sepEnd)
			} else {
				seenFrom = len(n.Flows)
			}
		case *ast.EndsFlow:
			if seenEnds {
				p.syntaxErr(f.Sp, "a signature has one `ends`")
			}
			seenEnds = true
		}
		n.Flows = append(n.Flows, f)
		p.cont(true)
		if p.at(ast.TokComma) {
			if p.flowStart(p.peek(1)) {
				p.next()
				continue
			}
			p.expected("a flow")
			return
		}
	}
	if p.at(ast.TokColon) && len(n.Flows) > 0 {
		p.errHere("flows come after the result type")
	}
}

// secondFrom reports a second from with the proven merge fix: the
// edits delete the second from and the separator before it, [sepEnd, end of
// the second from), and insert its sources after the first list's last
// source. A flow written between the two is kept.
func (p *parser) secondFrom(first, second *ast.FromFlow, sepEnd uint32) {
	d := p.syntaxErr(second.Sp, "one `from` per signature: merge into one `from` list")
	if d == nil || len(first.Sources) == 0 || len(second.Sources) == 0 {
		// A per-field clause has no mechanical merge.
		return
	}
	var srcs string
	for i, s := range second.Sources {
		if i > 0 {
			srcs += ", "
		}
		srcs += p.f.Slice(s.Sp)
	}
	last := first.Sources[len(first.Sources)-1].Sp
	del := second.Sp
	if sepEnd < del.Start && sepEnd >= first.Sp.End {
		del.Start = sepEnd
	}
	d.Fix(diag.Fix{Class: diag.Proven, Why: "merge into one `from a, b`", Edits: []diag.Edit{
		{Span: p.span(last.End, last.End), Text: ", " + srcs},
		{Span: del, Text: ""},
	}})
}

// parseFlow reads one Flow. inList selects the comma rule for a fn type in
// a comma list (a ',' continues only more Sources); otherwise the
// signature rule applies (a ',' may separate flows).
func (p *parser) parseFlow(inList bool) ast.Flow {
	switch {
	case p.at(ast.TokStar):
		n := &ast.StoreFlow{}
		p.begin(&n.Base)
		p.next()
		n.From = p.parseName("a parameter name")
		if p.atWord("into") {
			p.next()
		} else {
			p.expected("`into`")
		}
		n.Into = p.parseName("a parameter name")
		p.finish(n, &n.Base)
		return n
	case p.atWord("ends"):
		n := &ast.EndsFlow{}
		p.begin(&n.Base)
		p.next()
		n.Name = p.parseName("a parameter name")
		p.finish(n, &n.Base)
		return n
	}
	n := &ast.FromFlow{}
	p.begin(&n.Base)
	p.next()
	if p.at(ast.TokLParen) {
		// A per-field clause: `from (f: a, g: b, c)`; a `(` cannot start a
		// Source.
		p.parseFieldSrcs(n)
		p.finish(n, &n.Base)
		return n
	}
	n.Sources = append(n.Sources, p.parseSource())
	for p.at(ast.TokComma) {
		t1, t2 := p.peek(1), p.peek(2)
		if t1.Kind != ast.TokIdent {
			break
		}
		flowWord := p.flowStart(t1) && t2.Kind == ast.TokIdent
		if flowWord {
			break
		}
		if inList {
			switch t2.Kind {
			case ast.TokColon, ast.TokLBracket, ast.TokLParen, ast.TokPipe, ast.TokBang:
				p.finish(n, &n.Base)
				return n
			}
		}
		p.next()
		n.Sources = append(n.Sources, p.parseSource())
	}
	p.finish(n, &n.Base)
	return n
}

// parseFieldSrcs reads "(" FieldSrc { "," FieldSrc } [ "," ] ")" with
// FieldSrc = Name ":" Sources. Inside the parentheses a ","
// followed by Name ":" starts the next FieldSrc, a "," followed by ")" is
// the trailing comma, and any other "," continues the Sources (LL(2)); the
// ")" closes the clause, so the fn type comma rule gains no case.
func (p *parser) parseFieldSrcs(n *ast.FromFlow) {
	p.next() // (
	for {
		fs := &ast.FieldSrc{}
		p.begin(&fs.Base)
		fs.Name = p.parseName("a field name")
		p.expect(ast.TokColon)
		fs.Sources = append(fs.Sources, p.parseSource())
		for p.at(ast.TokComma) && p.peekKind(1) != ast.TokRParen &&
			!(p.peekKind(1) == ast.TokIdent && p.peekKind(2) == ast.TokColon) {
			p.next()
			fs.Sources = append(fs.Sources, p.parseSource())
		}
		p.finish(fs, &fs.Base)
		n.Fields = append(n.Fields, fs)
		if !p.at(ast.TokComma) {
			break
		}
		p.next()
		if p.at(ast.TokRParen) {
			break
		}
	}
	p.expect(ast.TokRParen)
}

// parseSource reads Name { "." Name }.
func (p *parser) parseSource() *ast.Source {
	n := &ast.Source{}
	p.begin(&n.Base)
	n.Names = append(n.Names, p.parseName("a parameter name"))
	for p.at(ast.TokDot) && (p.peekKind(1) == ast.TokIdent || p.peekKind(1).IsKeyword()) {
		p.next()
		n.Names = append(n.Names, p.parseName("a field name"))
	}
	p.finish(n, &n.Base)
	return n
}

// parseFuncName reads the FuncName cover: Name, then a bracket list
// read as TypeParams, decided at '.' (owner arguments) or '(' (the
// function's own type parameters).
//
// A name with no owner is a declared name (E0102 for a universe name) except
// for an interface or impl member, which lives in a type's namespace.
func (p *parser) parseFuncName(ctx sigCtx) (*ast.FuncName, *ast.TypeParams) {
	n := &ast.FuncName{}
	p.begin(&n.Base)
	first := p.parseName("a function name")
	var tp *ast.TypeParams
	if p.at(ast.TokLBracket) {
		tp = p.parseTypeParams()
	}
	if p.at(ast.TokDot) || p.at(ast.TokColonColon) {
		if p.at(ast.TokColonColon) {
			p.colonColonHabit()
		}
		p.next()
		n.Owner = first
		if tp != nil {
			args := &ast.TypeArgs{}
			for _, x := range tp.Params {
				if x.Const || len(x.Bounds) > 0 {
					p.syntaxErr(x.Sp, "bounds come from the type")
				}
				args.Args = append(args.Args, x.Name)
			}
			p.adoptTypeArgs(args, tp.Sp)
			n.OwnerArgs = args
			tp = nil
		}
		n.Name = p.parseName("a method name")
		p.finish(n, &n.Base)
		if p.at(ast.TokLBracket) {
			tp = p.parseTypeParams()
		}
		return n, tp
	}
	n.Name = first
	if ctx != sigInterface && ctx != sigImpl {
		p.checkDeclared(first, declItem)
	}
	p.finishAt(n, &n.Base, first.Sp.End)
	return n, tp
}

// adoptTypeArgs numbers a TypeArgs built from a discarded TypeParams list.
func (p *parser) adoptTypeArgs(args *ast.TypeArgs, sp source.Span) {
	p.adopt(args, &args.Base, sp)
}

// parseTypeParams reads "[" TypeParam { "," TypeParam } [ "," ] "]".
func (p *parser) parseTypeParams() *ast.TypeParams {
	n := &ast.TypeParams{}
	p.begin(&n.Base)
	p.next()
	saved := p.listCtx
	p.listCtx = true
	for !p.at(ast.TokRBracket) && !p.at(ast.TokEOF) {
		if p.at(ast.TokLifetime) {
			p.lifetimeHabit()
			p.next()
			if !p.accept(ast.TokComma) {
				break
			}
			continue
		}
		n.Params = append(n.Params, p.parseTypeParam())
		if !p.at(ast.TokComma) {
			break
		}
		p.next()
		p.region()
	}
	p.listCtx = saved
	if !p.expect(ast.TokRBracket) {
		p.skip(stopList)
		p.accept(ast.TokRBracket)
	}
	p.finish(n, &n.Base)
	return n
}

func (p *parser) parseTypeParam() *ast.TypeParam {
	n := &ast.TypeParam{}
	p.begin(&n.Base)
	if p.accept(ast.TokConst) {
		n.Const = true
		n.Name = p.parseDeclName(declTypeParam)
		if p.expect(ast.TokColon) {
			n.Type = p.parseType()
		} else {
			n.Type = p.badTypeFrom(p.p)
		}
		p.finish(n, &n.Base)
		return n
	}
	n.Name = p.parseDeclName(declTypeParam)
	if p.accept(ast.TokColon) {
		n.Bounds = append(n.Bounds, p.parseType())
		for p.accept(ast.TokPlus) {
			n.Bounds = append(n.Bounds, p.parseType())
		}
	}
	p.finish(n, &n.Base)
	return n
}

// parseParams reads Params into n. recvOK says a receiver is
// legal; fnLit applies the fn literal's rules.
func (p *parser) parseParams(n *ast.Signature, recvOK, fnLit bool) {
	saved := p.listCtx
	p.listCtx = true
	defer func() { p.listCtx = saved }()
	first := true
	for !p.at(ast.TokRParen) && !p.at(ast.TokEOF) {
		switch {
		case p.selfParamAhead():
			sp := p.parseSelfParam()
			switch {
			case !first:
				p.syntaxErr(sp.Sp, "the receiver comes first")
			case fnLit:
				p.syntaxErr(sp.Sp, "a fn literal has no receiver")
			case !recvOK:
				p.syntaxErr(sp.Sp, "a receiver needs a `Type.` prefix")
			}
			if n.Self == nil && first {
				n.Self = sp
			}
		case p.at(ast.TokEllipsis):
			el := p.curSpan()
			p.next()
			if len(n.Params) == 0 || fnLit {
				p.syntaxErr(el, "`...` is only for declaring a C variadic")
			}
			n.Ellipsis = el
			p.accept(ast.TokComma)
			if !p.at(ast.TokRParen) {
				p.syntaxErr(el, "`...` comes last")
			}
			return
		default:
			if p.at(ast.TokIdent) || p.at(ast.TokConst) || p.at(ast.TokVar) || p.kind().IsKeyword() || p.at(ast.TokUnderscore) {
				prm := p.parseParam(fnLit)
				n.Params = append(n.Params, prm)
			} else {
				p.expected("a parameter")
				p.skip(stopList)
			}
		}
		first = false
		if !p.at(ast.TokComma) {
			return
		}
		p.next()
		p.region()
	}
}

// selfParamAhead is true at self, &self or mut self followed by ',', ')'
// or `into`.
func (p *parser) selfParamAhead() bool {
	i := 0
	if p.at(ast.TokAmp) || p.at(ast.TokMut) {
		i = 1
	}
	t := p.peek(i)
	if t.Kind != ast.TokIdent || p.tokText(t) != "self" {
		return false
	}
	k := p.peekKind(i + 1)
	return k == ast.TokComma || k == ast.TokRParen || i > 0 || p.peekWord(i+1, "into")
}

func (p *parser) parseSelfParam() *ast.SelfParam {
	n := &ast.SelfParam{}
	p.begin(&n.Base)
	switch {
	case p.at(ast.TokAmp):
		n.Mode = ast.ByShared
		p.next()
	case p.at(ast.TokMut):
		n.Mode = ast.ByMut
		p.next()
	}
	p.next()
	if p.atWord("into") {
		p.next()
		n.Into = p.parseName("a parameter name")
	}
	p.finish(n, &n.Base)
	return n
}

// parseParam reads [ "const" | "var" ] Name ":" Type [ "into" Name ].
func (p *parser) parseParam(fnLit bool) *ast.Param {
	n := &ast.Param{}
	p.begin(&n.Base)
	switch {
	case p.at(ast.TokConst):
		if fnLit {
			p.errHere("a fn literal takes no `const` parameter")
		}
		n.Const = true
		p.next()
	case p.at(ast.TokVar):
		n.Var = true
		p.next()
	case p.at(ast.TokMut) && p.peekKind(1) == ast.TokIdent && p.peekKind(2) == ast.TokColon:
		p.errHere("write `var " + p.tokText(p.peek(1)) + "`: a parameter is made mutable with `var`")
		p.next()
		n.Var = true
	}
	switch {
	case p.at(ast.TokUnderscore):
		p.errHere("a parameter needs a name")
		n.Name = p.identUnderscore()
	case p.atWord("self"):
		p.errHere("write `self`, `&self` or `mut self`; the `Type.` prefix gives the type")
		n.Name = p.ident()
	default:
		n.Name = p.parseDeclName(declParam)
	}
	if p.expect(ast.TokColon) {
		n.Type = p.parseType()
	} else {
		s0 := p.p
		p.skip(stopList)
		n.Type = p.badTypeFrom(s0)
	}
	if p.atWord("into") {
		p.next()
		n.Into = p.parseName("a parameter name")
	}
	p.finish(n, &n.Base)
	return n
}

// identUnderscore builds the Ident `_` from a TokUnderscore.
func (p *parser) identUnderscore() *ast.Ident {
	n := &ast.Ident{}
	p.begin(&n.Base)
	n.Name = p.in.Intern("_")
	p.next()
	p.finish(n, &n.Base)
	return n
}
