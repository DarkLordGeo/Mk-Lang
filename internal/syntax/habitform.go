// Why: some forms from other languages parse as a valid Mk prefix and then
// fail on the next token, so the plain error names that token ("one
// statement per line", "comparisons do not chain") rather than the habit:
// `Point{x: 1}`, `G<int>(x)`, `c ? a : b`, Go's `F() int`, `dyn I`, Zig's
// `.{x}` and Go's `v, ok := m[k]`.
//
// What: braceLitAhead and braceLitHabit, angleCallAhead and angleCall,
// ternaryAhead and ternaryHabit, bareResultAhead and bareResultHabit,
// dynHabit, anonLitHabit and multiAssignForm.
//
// How: each check looks ahead on the line only far enough to be sure the
// tokens cannot be Mk, then records the habit with the Mk form (a proven
// edit where the rewrite keeps the meaning, else an alternative) and reads
// on past the form.

package syntax

import (
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
)

// head marks the head of an if, for, match or scope as read until the
// returned function runs (parseBlock clears the mark for the body).
func (p *parser) head() func() {
	saved := p.inHead
	p.inHead = true
	return func() { p.inHead = saved }
}

// braceLitAhead is true, in a value (parseValue: a binding's or an
// assignment's right side, return's operand, an argument, a field value or
// an element) and outside an if, for, match or scope head within it, at a
// '{' after a type-like path (its last name upper case) that opens a struct
// literal on the same line: `{x: 1`, `{ .x = 1` or a `{}` written directly
// after the name. There a '{' after an operand never opens a block; a
// contract, whose body follows its expression, is no value.
func (p *parser) braceLitAhead(x ast.Expr) bool {
	if !p.litOK || p.inHead || !isPathExpr(x) || p.lineEndAt(1) {
		return false
	}
	name := p.slice(x.Span().Start, x.Span().End)
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	switch p.peekKind(1) {
	case ast.TokIdent:
		return p.peekKind(2) == ast.TokColon && !p.lineEndAt(2)
	case ast.TokDot:
		return p.peekKind(2) == ast.TokIdent && p.peekKind(3) == ast.TokAssign
	case ast.TokRBrace:
		return p.tok().Start == p.lastEnd
	}
	return false
}

// braceFields reads the fields of a brace literal from its '{' through the
// '}': `name: value`, `.name = value` and, with positional, a bare value.
// It returns them as Mk record fields and whether every one was read; on a
// form it does not know it skips to the matching '}'.
func (p *parser) braceFields(positional bool) ([]string, bool) {
	p.next() // {
	var fields []string
	for p.skipTerms(); !p.at(ast.TokRBrace); p.skipTerms() {
		var name string
		switch {
		case p.at(ast.TokDot) && p.peekKind(1) == ast.TokIdent && p.peekKind(2) == ast.TokAssign:
			name = p.tokText(p.peek(1))
			p.next()
			p.next()
			p.next()
		case p.at(ast.TokIdent) && p.peekKind(1) == ast.TokColon:
			name = p.tokText(p.tok())
			p.next()
			p.next()
		case !positional:
			return fields, p.skipBraceRest()
		}
		vs := p.curStart()
		p.parseExpr()
		v := p.slice(vs, p.lastEnd)
		if name != "" {
			v = name + ": " + v
		}
		fields = append(fields, v)
		p.skipTerms()
		if !p.accept(ast.TokComma) && !p.at(ast.TokRBrace) {
			return fields, p.skipBraceRest()
		}
	}
	p.next() // }
	return fields, true
}

// skipTerms skips the Terms inside a brace literal, the one the lexer puts
// before its '}' among them.
func (p *parser) skipTerms() {
	for p.at(ast.TokTerm) {
		p.next()
	}
}

// skipBraceRest skips to the '}' that closes the literal and past it, and
// returns false: the literal was not read whole.
func (p *parser) skipBraceRest() bool {
	depth := 0
	for !p.at(ast.TokEOF) {
		switch p.kind() {
		case ast.TokLParen, ast.TokLBracket, ast.TokLBrace:
			depth++
		case ast.TokRParen, ast.TokRBracket:
			depth--
		case ast.TokRBrace:
			if depth == 0 {
				p.next()
				return false
			}
			depth--
		}
		p.next()
	}
	return false
}

// braceLitHabit reads `P{x: 1, y: 2}` or Zig's `P{ .x = 1 }` after the path
// x (E0101): a struct literal names its fields in parentheses. Rewriting the
// braces keeps every value's text, so the edit is proven when every field
// was read and the literal is on one line (no comment inside it is lost).
func (p *parser) braceLitHabit(x ast.Expr, start uint32) ast.Expr {
	open := p.tok()
	fields, whole := p.braceFields(false)
	name := p.slice(start, x.Span().End)
	form := name + "(" + strings.Join(fields, ", ") + ")"
	if !whole || strings.ContainsRune(form, '{') {
		// A field not read, or a brace literal inside a value: the fields'
		// text is no Mk form, so name the shape alone.
		form = name + "(field: value, …)"
		whole = false
	}
	h := habitHit{code: diag.E0101, at: open.Span(p.file),
		msg: "a struct literal names its fields in parentheses: write `" + form + "`"}
	if whole && p.f.LineCol(open.Start).Line == p.f.LineCol(p.lastEnd).Line {
		h.edits = []diag.Edit{p.edit(p.wsBefore(open.Start), p.lastEnd, "("+strings.Join(fields, ", ")+")")}
	} else {
		h.alts = []string{form}
	}
	p.hit(h)
	return x
}

// anonLitHabit reads Zig's anonymous literal `.{x}` or `.{ .x = 1 }` in
// operand position (E0101): Mk passes a record, `(x)`, and prints with
// `sys.Print("{x}\n", (x))`.
func (p *parser) anonLitHabit() ast.Expr {
	s0 := p.p
	dot := p.tok()
	p.next()
	fields, whole := p.braceFields(true)
	rec := "(" + strings.Join(fields, ", ") + ")"
	if !whole || strings.ContainsRune(rec, '{') {
		rec = "(x)"
	}
	p.hit(habitHit{code: diag.E0101, at: p.span(dot.Start, dot.End()+1),
		msg:  "Mk has no `.{ }` literal: pass a record, `" + rec + "`",
		alts: []string{rec, "sys.Print(\"{x}\\n\", (x))"}})
	return p.badExprFrom(s0)
}

// angleCallAhead is true at a '<' directly after a name that opens type
// arguments of a call, `G<int>(x)` or `m.G<List[int]>(x)`: names, dots and
// type punctuation up to a '>' that a '(' follows directly. As comparisons
// the same tokens would chain, which is never Mk. A comma stops the scan:
// `G(a<b, c>(d+1))` is two comparisons. The scan is bounded like
// rustArrayType's.
func (p *parser) angleCallAhead() bool {
	if p.tok().Start != p.lastEnd {
		return false
	}
	for i := 1; i < 64; i++ {
		switch p.peekKind(i) {
		case ast.TokIdent, ast.TokDot, ast.TokLBracket, ast.TokRBracket, ast.TokQuestion, ast.TokAmp, ast.TokMut:
			continue
		case ast.TokGt:
			return i > 1 && p.peekKind(i+1) == ast.TokLParen && p.peek(i+1).Start == p.peek(i).End()
		}
		return false
	}
	return false
}

// angleCall reports `G<T>` before a call's arguments (E0101, `<` and `>`
// rewritten to brackets in the target and the alternative) and consumes
// through the '>'. A comparison chain written without spaces reads the same,
// so the rewrite is no proven fix.
func (p *parser) angleCall(x ast.Expr, start uint32) ast.Expr {
	lt := p.tok()
	p.next()
	argStart := p.curStart()
	for !p.at(ast.TokGt) {
		p.next()
	}
	args := p.slice(argStart, p.lastEnd)
	gt := p.tok()
	p.next()
	head := p.slice(start, lt.Start) + "[" + args + "]"
	form := head + "(…)"
	p.hit(habitHit{code: diag.E0101, at: lt.Span(p.file), msg: "type arguments use brackets: write `" + form + "`",
		alts:   []string{head},
		target: []diag.Edit{p.edit(lt.Start, lt.End(), "["), p.edit(gt.Start, gt.End(), "]")}})
	return x
}

// ternaryAhead is true at a '?' that an operand follows on the line and a
// ':' after it at bracket depth zero: C's `c ? a : b`, never a postfix `?`.
// The scan is bounded like rustArrayType's.
func (p *parser) ternaryAhead() bool {
	if p.lineEndAt(1) || !startsOperand(p.peekKind(1)) {
		return false
	}
	depth := 0
	for i := 1; i < 256; i++ {
		switch p.peekKind(i) {
		case ast.TokLParen, ast.TokLBracket, ast.TokLBrace:
			depth++
		case ast.TokRParen, ast.TokRBracket, ast.TokRBrace:
			depth--
			if depth < 0 {
				return false
			}
		case ast.TokColon:
			if depth == 0 {
				return true
			}
		case ast.TokTerm, ast.TokEOF:
			return false
		}
	}
	return false
}

// ternaryHabit reads `? a : b` after the condition that starts at token s0
// (E1208, with the `if` form as the alternative) and returns a BadExpr over
// the whole.
func (p *parser) ternaryHabit(s0 int, start uint32) ast.Expr {
	q := p.curSpan()
	cond := p.slice(start, p.lastEnd)
	p.next()
	aStart := p.curStart()
	p.parseBin(precOr, aStart)
	a := p.slice(aStart, p.lastEnd)
	b := "…"
	if p.expect(ast.TokColon) {
		bStart := p.curStart()
		p.parseExpr()
		b = p.slice(bStart, p.lastEnd)
	}
	form := "if " + cond + " { " + a + " } else { " + b + " }"
	p.hit(habitHit{code: diag.E1208, at: q, msg: "Mk has no `c ? a : b`: write `" + form + "`", alts: []string{form}})
	return p.badExprFrom(s0)
}

// bareResultAhead is true at a type name on the line of a signature's ')'
// where the result's ':' is missing, Go's `F() int {`: a name that starts
// no flow and no clause, followed by a body, a type's brackets or path, or
// the error operator.
func (p *parser) bareResultAhead() bool {
	if !p.at(ast.TokIdent) || p.lineEnd() || p.flowStart(p.tok()) || p.clauseAt(0) != "" {
		return false
	}
	switch p.peekKind(1) {
	case ast.TokLBrace, ast.TokLBracket, ast.TokDot, ast.TokBang, ast.TokPipe:
		return true
	}
	return false
}

// bareResultHabit reads the result type that bareResultAhead found (E0101,
// the ':' inserted as a proven edit).
func (p *parser) bareResultHabit() ast.Type {
	at := p.curSpan()
	insert := p.lastEnd
	tStart := p.curStart()
	p.sigResult = true
	t := p.parseType()
	p.sigResult = false
	p.hit(habitHit{code: diag.E0101, at: at, msg: "the result type follows `:`: write `): " + p.slice(tStart, p.lastEnd) + "`",
		edits: []diag.Edit{p.edit(insert, insert, ":")}})
	return t
}

// dynHabit records E1221 at Rust's `dyn I` in a type and consumes `dyn`:
// Mk's value of an interface is the type `gen dyn.Of(I)` declares, `IDyn`.
// The rewrite needs the gen line, so it is the target only.
func (p *parser) dynHabit() {
	w := p.tok()
	name := p.tokText(p.peek(1))
	mk := name + "Dyn"
	p.hit(habitHit{code: diag.E1221, at: p.span(w.Start, p.peek(1).End()),
		msg:    "Mk has no `dyn` types: `gen dyn.Of(" + name + ")` declares `" + mk + "`",
		alts:   []string{"gen dyn.Of(" + name + ")", mk},
		target: []diag.Edit{p.edit(w.Start, p.peek(1).End(), mk)},
		facts:  []diag.Field{{Key: "mk_name", Value: diag.TextFact(mk)}}})
	p.next()
}

// oneIndex is true when tokens first through last are one index of a name
// path, `m[k]` or `a.m[k]`: the '[' after the path is closed by the last
// token, so `xs[i], ys[j]` is not one.
func (p *parser) oneIndex(first, last int) bool {
	k := first
	for p.peekKind(k) == ast.TokIdent && p.peekKind(k+1) == ast.TokDot {
		k += 2
	}
	if p.peekKind(k) != ast.TokIdent || p.peekKind(k+1) != ast.TokLBracket || p.peekKind(last) != ast.TokRBracket {
		return false
	}
	depth := 0
	for j := k + 1; j <= last; j++ {
		switch p.peekKind(j) {
		case ast.TokLParen, ast.TokLBracket, ast.TokLBrace:
			depth++
		case ast.TokRParen, ast.TokRBracket, ast.TokRBrace:
			depth--
			if depth == 0 && j != last {
				return false
			}
		}
	}
	return depth == 0
}

// multiAssignForm is the message and alternatives for `a, b := e` at
// statement start (multiAssignAhead): Go's map lookup `v, ok := m[k]` is
// `if v := m.Get(&k) { }`, Go's `x, err := F()` is `try` or `else`, and any
// other is a record destructure `(a, b) := F()`.
func (p *parser) multiAssignForm() (string, []string) {
	j := 0
	var names []string
	for ; p.peekKind(j+1) == ast.TokComma; j += 2 {
		names = append(names, p.tokText(p.peek(j)))
	}
	names = append(names, p.tokText(p.peek(j)))
	first := j + 2
	last := first
	for k := first; k < first+1024 && p.peekKind(k) != ast.TokTerm && p.peekKind(k) != ast.TokEOF; k++ {
		last = k
	}
	if p.peekKind(first) == ast.TokTerm || p.peekKind(first) == ast.TokEOF {
		return "write `(a, b) := F()`", nil
	}
	rhs := p.slice(p.peek(first).Start, p.peek(last).End())
	switch {
	case len(names) == 2 && p.oneIndex(first, last):
		i := strings.IndexByte(rhs, '[')
		form := "if " + names[0] + " := " + rhs[:i] + ".Get(&" + rhs[i+1:len(rhs)-1] + ") { }"
		return "Mk looks a key up with `Get`, which returns an optional: write `" + form + "`", []string{form}
	case len(names) == 2 && names[1] == "err":
		return "Mk returns an error in the result, not as a second value: write `" + names[0] + " := try " + rhs + "`",
			[]string{names[0] + " := try " + rhs, names[0] + " := " + rhs + " else e { … }"}
	}
	return "write `(a, b) := F()`", nil
}
