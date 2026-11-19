// Why: programmers bring habits from Rust, Go and C (`->`, `::`, lifetimes,
// a ';' after a statement, `x as T`, a C-style for); a plain "expected X"
// error for each would be noise, so the parser names the habit and offers
// the Mk spelling as a fix.
//
// What: habitHit and the punctuation habits (arrowHabit, colonColonHabit,
// lifetimeHabit, semiHabit, rustArrayType, cStyleForHabit, fatArrowHabit,
// inclusiveRangeHabit, questionChain, asHabit), and mergeHits.
//
// How: each habit records a hit with its code, span and edits, and parsing
// goes on with the Mk reading. At the end of the file mergeHits joins the
// hits of one line into one diagnostic whose fix applies all their edits,
// dropping overlapping ones.

package syntax

import (
	"sort"
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
)

// habitHit is one recognized habit; hits are merged one diagnostic per
// line at the end of the file.
type habitHit struct {
	code   diag.Code
	at     source.Span
	msg    string
	edits  []diag.Edit // proven edits
	target []diag.Edit // edits applied to the target only (E1222's Cast)
	alts   []string
	facts  []diag.Field
	lines  [2]uint32 // first and last line the target covers; zero means at's line
}

// hit records a habit.
func (p *parser) hit(h habitHit) *habitHit {
	p.hits = append(p.hits, h)
	return &p.hits[len(p.hits)-1]
}

func (p *parser) edit(start, end uint32, text string) diag.Edit {
	return diag.Edit{Span: p.span(start, end), Text: text}
}

// wsAfter is the end of the run of spaces and tabs at off.
func (p *parser) wsAfter(off uint32) uint32 {
	for off < uint32(len(p.text)) && (p.text[off] == ' ' || p.text[off] == '\t') {
		off++
	}
	return off
}

// wsBefore is the start of the run of spaces and tabs that ends at off.
func (p *parser) wsBefore(off uint32) uint32 {
	for off > 0 && (p.text[off-1] == ' ' || p.text[off-1] == '\t') {
		off--
	}
	return off
}

// slice is the text of [a, b), empty when b is not after a.
func (p *parser) slice(a, b uint32) string {
	if b <= a || b > uint32(len(p.text)) {
		return ""
	}
	return p.text[a:b]
}

// Token habits.

// arrowHabit records E1211 at a "->" in result position: " ->" becomes ":".
func (p *parser) arrowHabit() {
	t := p.tok()
	p.hit(habitHit{code: diag.E1211, at: t.Span(p.file), msg: "the result type follows `:`, not `->`",
		edits: []diag.Edit{p.edit(p.wsBefore(t.Start), t.End(), ":")}})
}

// colonColonHabit records E1213: "::" becomes ".".
func (p *parser) colonColonHabit() {
	t := p.tok()
	p.hit(habitHit{code: diag.E1213, at: t.Span(p.file), msg: "paths use `.`, not `::`",
		edits: []diag.Edit{p.edit(t.Start, t.End(), ".")}})
}

// lifetimeHabit records E1203: the lifetime and one following space are
// deleted.
func (p *parser) lifetimeHabit() { p.lifetimeHit(p.tok(), true) }

// lifetimeHit records E1203 at the lifetime t: with proven, the delete edit;
// without (a lifetime with no operand after it, where deleting it would leave
// no operand), the alternative of the char literal it may have meant.
func (p *parser) lifetimeHit(t ast.Token, proven bool) {
	if !proven {
		p.hit(habitHit{code: diag.E1203, at: t.Span(p.file), msg: "Mk has no lifetimes",
			alts: []string{p.tokText(t) + "'"}})
		return
	}
	end := t.End()
	if end < uint32(len(p.text)) && p.text[end] == ' ' {
		end++
	}
	p.hit(habitHit{code: diag.E1203, at: t.Span(p.file), msg: "Mk has no lifetimes",
		edits: []diag.Edit{p.edit(t.Start, end, "")}})
}

// ampMutHabit records E1212 at "&" "mut": the "&" and the space after it go.
func (p *parser) ampMutHabit() {
	amp := p.toks[p.lastTok]
	p.hit(habitHit{code: diag.E1212, at: p.span(amp.Start, p.tok().End()), msg: "write `mut x`, not `&mut x`",
		edits: []diag.Edit{p.edit(amp.Start, p.curStart(), "")}})
}

// semiHabit records E1206 for a ';' that ends a statement, member or item
// and consumes it as the Term. It returns true when it did.
func (p *parser) semiHabit() bool {
	t := p.tok()
	p.next()
	h := habitHit{code: diag.E1206, at: t.Span(p.file), msg: "Mk ends statements with a newline, not `;`"}
	if p.lineEnd() || p.at(ast.TokTerm) || p.at(ast.TokRBrace) || p.at(ast.TokEOF) {
		h.edits = []diag.Edit{p.edit(p.wsBefore(t.Start), t.End(), "")}
		if p.at(ast.TokTerm) {
			p.next()
		} else {
			p.softTerm = p.lineEnd()
		}
	} else {
		line := int(p.f.LineCol(t.Start).Line)
		text := p.f.Line(line)
		indent := text[:len(text)-len(strings.TrimLeft(text, " \t"))]
		h.edits = []diag.Edit{p.edit(t.Start, p.wsAfter(t.End()), "\n"+indent)}
		h.lines = [2]uint32{uint32(line), uint32(line)}
	}
	p.hit(h)
	return true
}

// semiInBrackets records E1206 for a ';' inside ( ) or [ ]: it becomes ','.
func (p *parser) semiInBrackets() {
	t := p.tok()
	p.hit(habitHit{code: diag.E1206, at: t.Span(p.file), msg: "separate with `,`, not `;`",
		edits: []diag.Edit{p.edit(t.Start, t.End(), ",")}})
	p.next()
}

// rustArrayType reads the Rust array type "[" Type ";" Expr "]" (E1206 with
// the alternative "[N]T"). The tree holds a BadType over it, because the
// ast.ArrayType keeps Len before Elem in source order.
func (p *parser) rustArrayType() ast.Type {
	depth := 0
	semi := -1
	// The scan is bounded so nested brackets stay linear; a Rust array type
	// is short.
	for i := 1; i < 256; i++ {
		k := p.peekKind(i)
		switch k {
		case ast.TokLParen, ast.TokLBracket, ast.TokLBrace:
			depth++
			continue
		case ast.TokRParen, ast.TokRBrace:
			depth--
		case ast.TokRBracket:
			if depth == 0 {
				goto done
			}
			depth--
		case ast.TokSemi:
			if depth == 0 {
				semi = i
				goto done
			}
		case ast.TokEOF, ast.TokTerm:
			goto done
		}
		if depth < 0 {
			goto done
		}
	}
done:
	if semi < 0 {
		return nil
	}
	s0 := p.p
	open := p.tok()
	p.next()
	elemStart := p.curStart()
	p.parseType()
	elemText := p.slice(elemStart, p.lastEnd)
	p.next() // ;
	lenStart := p.curStart()
	p.parseExpr()
	lenText := p.slice(lenStart, p.lastEnd)
	p.expect(ast.TokRBracket)
	p.hit(habitHit{code: diag.E1206, at: p.span(open.Start, p.lastEnd), msg: "an array type is `[N]T`",
		alts: []string{"[" + lenText + "]" + elemText}})
	return p.badTypeFrom(s0)
}

// cStyleFor is true at a for head that holds a ';' outside brackets before
// the '{' of its block: the C-style "for a; b; c {". The scan
// is bounded like rustArrayType's; a ';' inside ( ) or [ ] stays
// semiInBrackets's.
func (p *parser) cStyleFor() bool {
	depth := 0
	for i := 0; i < 256; i++ {
		switch p.peekKind(i) {
		case ast.TokLParen, ast.TokLBracket:
			depth++
		case ast.TokLBrace:
			if depth == 0 {
				return false
			}
			depth++
		case ast.TokRParen, ast.TokRBracket, ast.TokRBrace:
			depth--
			if depth < 0 {
				return false
			}
		case ast.TokSemi:
			if depth == 0 {
				return true
			}
		case ast.TokTerm, ast.TokEOF:
			return false
		}
	}
	return false
}

// cStyleForHabit records one E1206 at the first ';' of a C-style for head,
// with alternatives only (no target), and skips to the '{' of the block.
// The head is a BadExpr over the skipped tokens.
func (p *parser) cStyleForHabit() ast.Node {
	s0 := p.p
	depth := 0
	hit := false
	for !p.at(ast.TokEOF) && !p.at(ast.TokTerm) && !(depth == 0 && p.at(ast.TokLBrace)) {
		switch p.kind() {
		case ast.TokLParen, ast.TokLBracket, ast.TokLBrace:
			depth++
		case ast.TokRParen, ast.TokRBracket, ast.TokRBrace:
			if depth > 0 {
				depth--
			}
		case ast.TokSemi:
			if depth == 0 && !hit {
				hit = true
				p.hit(habitHit{code: diag.E1206, at: p.curSpan(), msg: "Mk has no C-style `for`",
					alts: []string{"for i in a..b {", "for cond {"}})
			}
		}
		p.next()
	}
	return p.badExprFrom(s0)
}

// fatArrowHabit reads "=>" and the arm body after a match pattern (E1214).
func (p *parser) fatArrowHabit() *ast.Block {
	arrow := p.tok()
	p.next()
	h := habitHit{code: diag.E1214, at: arrow.Span(p.file), msg: "an arm is `Pattern { body }`, not `Pattern => body`"}
	var body *ast.Block
	if p.at(ast.TokLBrace) {
		body = p.parseBlock()
		h.edits = append(h.edits, p.edit(p.wsBefore(arrow.Start), arrow.End(), ""))
	} else {
		e := p.parseExpr()
		st := &ast.ExprStmt{X: e}
		p.adopt(st, &st.Base, e.Span())
		body = &ast.Block{Stmts: []ast.Stmt{st}}
		p.adopt(body, &body.Base, e.Span())
		h.edits = append(h.edits, p.edit(p.wsBefore(arrow.Start), arrow.End(), " {"))
		if !p.at(ast.TokComma) {
			h.edits = append(h.edits, p.edit(e.Span().End, e.Span().End, " }"))
		}
	}
	if p.at(ast.TokComma) {
		c := p.tok()
		text := ""
		if len(h.edits) == 1 && h.edits[0].Text == " {" {
			text = " }"
		}
		h.edits = append(h.edits, p.edit(c.Start, c.End(), text))
		p.next()
		if p.lineEnd() {
			p.softTerm = true
		}
	}
	p.hit(h)
	return body
}

// inclusiveEq consumes the '=' of "..=" and returns its span, or the zero
// Span when there is none.
func (p *parser) inclusiveEq() source.Span {
	if p.at(ast.TokAssign) && p.tok().Start == p.lastEnd {
		sp := p.curSpan()
		p.next()
		return sp
	}
	return source.Span{}
}

// inclusiveRangeHabit records E1220 for "..=": delete '=' and add " + 1"
// after the high bound, parenthesized when it binds looser than '+'.
func (p *parser) inclusiveRangeHabit(eq source.Span, hi ast.Expr) {
	h := habitHit{code: diag.E1220, at: p.span(eq.Start-2, eq.End), msg: "Mk ranges exclude the end: write `a..b + 1`"}
	h.edits = append(h.edits, diag.Edit{Span: eq})
	if hi != nil {
		end := hi.Span().End
		if loose(hi) {
			h.edits = append(h.edits, p.edit(hi.Span().Start, hi.Span().Start, "("), p.edit(end, end, ") + 1"))
		} else {
			h.edits = append(h.edits, p.edit(end, end, " + 1"))
		}
	}
	p.hit(h)
}

// loose is true for an expression whose top node binds looser than '+'.
func loose(x ast.Expr) bool {
	switch y := x.(type) {
	case *ast.ElseExpr:
		return true
	case *ast.BinaryExpr:
		return binPrec(y.Op) < precAdd
	}
	return false
}

// qmark is one postfix '?' of a chain (E1208).
type qmark struct {
	span   source.Span
	onCall bool
	last   bool
}

// questionChain records one E1208 for the '?'s of a postfix chain that
// starts at start: nested try per '?', proven when every '?' follows a call.
func (p *parser) questionChain(start uint32, qs []qmark) {
	if len(qs) == 0 {
		return
	}
	h := habitHit{code: diag.E1208, at: qs[0].span, msg: "Mk propagates errors with prefix `try`, not postfix `?`"}
	allCalls := true
	for _, q := range qs {
		allCalls = allCalls && q.onCall
	}
	if !allCalls {
		h.alts = []string{"try " + p.slice(start, qs[0].span.Start), p.slice(start, qs[0].span.Start) + " else { return … }"}
		p.hit(h)
		return
	}
	lastIsEnd := qs[len(qs)-1].last
	ins := "(try "
	if lastIsEnd {
		ins = "try "
	}
	ins += strings.Repeat("(try ", len(qs)-1)
	h.edits = append(h.edits, p.edit(start, start, ins))
	for i, q := range qs {
		if i == len(qs)-1 && lastIsEnd {
			h.edits = append(h.edits, diag.Edit{Span: q.span})
		} else {
			h.edits = append(h.edits, diag.Edit{Span: q.span, Text: ")"})
		}
	}
	p.hit(h)
}

// asHabit records E1222 for "x as T" and keeps x.
func (p *parser) asHabit(x ast.Expr, start uint32) ast.Expr {
	asTok := p.tok()
	p.next()
	tstart := p.curStart()
	p.parseType()
	tt := p.slice(tstart, p.lastEnd)
	xt := p.slice(start, x.Span().End)
	h := habitHit{code: diag.E1222, at: asTok.Span(p.file), msg: "Mk has no `as`: convert with `Cast[T](x)` or `T(x)`",
		alts:   []string{"Cast[" + tt + "](" + xt + ")", tt + "(" + xt + ")"},
		target: []diag.Edit{p.edit(start, p.lastEnd, "Cast["+tt+"]("+xt+")")}}
	p.hit(h)
	return x
}

// Merge.

// mergeHits turns the hits into one diagnostic per line.
func (p *parser) mergeHits() {
	if len(p.hits) == 0 {
		return
	}
	hits := p.hits
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].at.Start != hits[j].at.Start {
			return hits[i].at.Start < hits[j].at.Start
		}
		return hits[i].code < hits[j].code
	})
	type grp struct {
		line uint32
		hs   []habitHit
	}
	// Every edit of every hit with its start line, sorted by line, so each
	// group finds the edits in its target's lines by binary search instead
	// of rescanning every hit (linear in the hits plus the edits each target
	// takes).
	type lineEdit struct {
		line uint32
		e    diag.Edit
	}
	var byLine []lineEdit
	for _, h := range hits {
		for _, e := range h.edits {
			byLine = append(byLine, lineEdit{p.f.LineCol(e.Span.Start).Line, e})
		}
		for _, e := range h.target {
			byLine = append(byLine, lineEdit{p.f.LineCol(e.Span.Start).Line, e})
		}
	}
	sort.SliceStable(byLine, func(i, j int) bool { return byLine[i].line < byLine[j].line })
	var groups []grp
	for _, h := range hits {
		line := p.f.LineCol(h.at.Start).Line
		if len(groups) > 0 && groups[len(groups)-1].line == line {
			groups[len(groups)-1].hs = append(groups[len(groups)-1].hs, h)
			continue
		}
		groups = append(groups, grp{line: line, hs: []habitHit{h}})
	}
	for _, g := range groups {
		first := g.hs[0]
		d := diag.New(first.code, first.at, first.msg).In(diag.PhaseParse)
		var codes []string
		seen := map[diag.Code]bool{}
		var proven, all []diag.Edit
		lo, hi := g.line, g.line
		for _, h := range g.hs {
			if !seen[h.code] {
				seen[h.code] = true
				codes = append(codes, h.code.String())
			}
			proven = append(proven, h.edits...)
			all = append(all, h.edits...)
			all = append(all, h.target...)
			if h.lines[0] != 0 {
				lo, hi = min(lo, h.lines[0]), max(hi, h.lines[1])
			}
			for _, e := range append(append([]diag.Edit(nil), h.edits...), h.target...) {
				lo = min(lo, p.f.LineCol(e.Span.Start).Line)
				hi = max(hi, p.f.LineCol(e.Span.End).Line)
			}
		}
		d.Fact("habits", diag.TextList(codes...))
		if len(all) > 0 {
			// Every hit's edits that lie in the target's lines take part, so a
			// member line under E1219 shows its "T." and E1219's target shows
			// the members' own rewrites.
			a := sort.Search(len(byLine), func(i int) bool { return byLine[i].line >= lo })
			var inRange []diag.Edit
			for i := a; i < len(byLine) && byLine[i].line <= hi; i++ {
				inRange = append(inRange, byLine[i].e)
			}
			d.Text("target", p.applyEdits(lo, hi, inRange))
		}
		for _, f := range first.facts {
			d.Fact(f.Key, f.Value)
		}
		if len(proven) > 0 {
			d.Fix(diag.Fix{Class: diag.Proven, Edits: dropOverlaps(proven), Why: "rewrite the habit as Mk"})
		}
		for _, h := range g.hs {
			for _, a := range h.alts {
				d.Alt(a)
			}
		}
		p.diags = append(p.diags, d)
	}
}

// dropOverlaps keeps edits in offset order and drops any that overlaps an
// earlier one.
func dropOverlaps(es []diag.Edit) []diag.Edit {
	sorted := append([]diag.Edit(nil), es...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Span.Start != sorted[j].Span.Start {
			return sorted[i].Span.Start < sorted[j].Span.Start
		}
		return sorted[i].Span.End < sorted[j].Span.End
	})
	var out []diag.Edit
	end := uint32(0)
	for i, e := range sorted {
		if i > 0 && e.Span.Start < end {
			continue
		}
		out = append(out, e)
		if e.Span.End > end {
			end = e.Span.End
		}
	}
	return out
}

// applyEdits returns lines lo..hi of the file with the edits that lie in
// them applied, joined by "\n". The edits are disjoint after dropOverlaps and
// sorted by offset, so one forward pass gives what applying them right to
// left gives, in time linear in the text and the edits.
func (p *parser) applyEdits(lo, hi uint32, es []diag.Edit) string {
	start := p.f.LineStart(int(lo))
	end := uint32(len(p.text))
	if int(hi) < p.f.LineCount() {
		end = p.f.LineStart(int(hi)+1) - 1
	}
	var b strings.Builder
	pos := start
	for _, e := range dropOverlaps(es) {
		if e.Span.Start < start || e.Span.End > end {
			continue
		}
		b.WriteString(p.text[pos:e.Span.Start])
		b.WriteString(e.Text)
		pos = e.Span.End
	}
	b.WriteString(p.text[pos:end])
	return b.String()
}
