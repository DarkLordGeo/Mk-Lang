// Why: generated code is read by people (in errors, in the editor, in
// review), so it must print as text that reads like handwritten code, and
// every name in it must map back to where it came from.
//
// What: printer with header, item and tokens; splitItems, contStart and
// continues for item and signature boundaries; space, the spacing rule
// between two tokens.
//
// How: one pass over the token stream writes the header, one trace comment
// per item (without line numbers, so the text is stable), and the tokens with
// gofmt-like spacing and bracket-depth indentation; a line that starts with
// closing brackets sits at the level they close. TokenOrigin and ItemOrigin
// side tables are built as the offsets become known.

package gen

import (
	"strings"

	"mk/internal/ast"
)

// printItem is one top-level item of one Emit, with its trace.
type printItem struct {
	toks    []tok
	trace   string // the trace comment's text, "gen derive.Eq#1 for User"
	origin  ItemOrigin
	itemPos int // index into the printer's items, filled by print
}

// printer accumulates the generated file.
type printer struct {
	b       strings.Builder
	origins []TokenOrigin
	items   []ItemOrigin
}

// contStart reports whether a line starting with t continues a signature:
// from, ends, *, requires or ensures, as the parser's contStart reads them;
// and '{', the body after such a line. No item starts with any of them.
func contStart(t tok) bool {
	switch t.kind {
	case ast.TokStar, ast.TokRequires, ast.TokEnsures, ast.TokLBrace:
		return true
	case ast.TokIdent:
		return t.text == "from" || t.text == "ends"
	}
	return false
}

// splitItems cuts an Emit's tokens into top-level items: at newlines outside
// every bracket, dropping the newlines around them, except before a line
// that continues the item's signature (contStart), so one function whose
// requires or body sits on a line of its own stays one item.
func splitItems(ts []tok) [][]tok {
	var out [][]tok
	depth, start := 0, 0
	flush := func(end int) {
		it := ts[start:end]
		for len(it) > 0 && it[0].kind == ast.TokNewline {
			it = it[1:]
		}
		for len(it) > 0 && it[len(it)-1].kind == ast.TokNewline {
			it = it[:len(it)-1]
		}
		if len(it) > 0 {
			out = append(out, it)
		}
	}
	for i, t := range ts {
		switch t.kind {
		case ast.TokLBrace, ast.TokLParen, ast.TokLBracket:
			depth++
		case ast.TokRBrace, ast.TokRParen, ast.TokRBracket:
			depth = max(depth-1, 0)
		case ast.TokNewline:
			if depth == 0 && !continues(ts, start, i) {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(ts))
	return out
}

// continues reports whether the newline at ts[nl] lies inside the item
// begun at start: the item so far has a token, does not end with '}' (a
// body or declaration closed it), and the next line starts with contStart.
func continues(ts []tok, start, nl int) bool {
	last := -1
	for k := nl - 1; k >= start; k-- {
		if ts[k].kind != ast.TokNewline {
			last = k
			break
		}
	}
	if last < 0 || ts[last].kind == ast.TokRBrace {
		return false
	}
	for k := nl + 1; k < len(ts); k++ {
		if ts[k].kind != ast.TokNewline {
			return contStart(ts[k])
		}
	}
	return false
}

// header writes the generated file's first line and a blank line.
func (p *printer) header(module, target string) {
	p.b.WriteString("// Generated from the gen lines of " + module + " for " + target + ". Not read by the compiler.\n\n")
}

// item prints one item: its trace comment line, its tokens, a blank line
// and appends its ItemOrigin.
func (p *printer) item(it *printItem) {
	p.b.WriteString("// " + it.trace + "\n")
	start := uint32(p.b.Len())
	end := p.tokens(it.toks)
	p.b.WriteString("\n\n")
	o := it.origin
	o.Output.Start, o.Output.End = start, end
	p.items = append(p.items, o)
}

// operandEnd reports whether a token ends an operand, so a following & * !
// - ? is binary, not a prefix.
func operandEnd(k ast.TokenKind) bool {
	return k == ast.TokIdent || k == ast.TokUnderscore || k.IsLiteral() || k == ast.TokRParen ||
		k == ast.TokRBracket || k == ast.TokRBrace
}

// prefixOp reports the operators that may be prefixes.
func prefixOp(k ast.TokenKind) bool {
	return k == ast.TokAmp || k == ast.TokStar || k == ast.TokBang || k == ast.TokMinus || k == ast.TokQuestion
}

// tokens prints one item's tokens and records the origins of
// its names; it returns the offset after the last token.
func (p *printer) tokens(ts []tok) uint32 {
	// Lines of the item, newlines collapsed to at most one blank line.
	var lines [][]tok
	var cur []tok
	blank := 0
	for _, t := range ts {
		if t.kind == ast.TokNewline {
			if len(cur) > 0 {
				// No blank line just inside a block: before a line that
				// starts with '}'.
				if n := len(lines); n > 0 && lines[n-1] == nil && cur[0].kind == ast.TokRBrace {
					lines = lines[:n-1]
				}
				lines = append(lines, cur)
				cur, blank = nil, 0
			} else if n := len(lines); blank == 0 && n > 0 && lines[n-1][len(lines[n-1])-1].kind != ast.TokLBrace {
				// ...nor after a line that ends with '{'.
				lines = append(lines, nil)
				blank = 1
			}
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		if n := len(lines); n > 0 && lines[n-1] == nil && cur[0].kind == ast.TokRBrace {
			lines = lines[:n-1]
		}
		lines = append(lines, cur)
	}
	for len(lines) > 0 && lines[len(lines)-1] == nil {
		lines = lines[:len(lines)-1]
	}
	depth := 0
	end := uint32(p.b.Len())
	// prefixBracket holds, per open '[', whether it opened a type prefix
	// ([]T, [N]T) rather than an index, so ']' then a name prints "[]T".
	var prefixBracket []bool
	// groups holds, per line that ended inside '(' or '[' it opened, how
	// many it left open: each such line indents the lines after it by one
	// level until they are closed, however many it opened, as gofmt does
	// ("f(a, g(" then one level, then "))"). Newlines inside ( ) and [ ]
	// are ignored by the parser, so a quote may break a long
	// list there. A line that also opened a '{' indents by that block alone
	// ("xs.Sort(fn(a: int): bool {" then one level, then "})"), so its group
	// only counts brackets (indent false).
	type group struct {
		n      int
		indent bool
	}
	var groups []group
	indents := func() int {
		k := 0
		for _, g := range groups {
			if g.indent {
				k++
			}
		}
		return k
	}
	for li, line := range lines {
		if li > 0 {
			p.b.WriteByte('\n')
		}
		if line == nil {
			continue
		}
		closers, pclosers := 0, 0
		for _, t := range line {
			if t.kind == ast.TokRBrace {
				closers++
			} else if t.kind == ast.TokRParen || t.kind == ast.TokRBracket {
				pclosers++
			} else {
				break
			}
		}
		// The groups the line's leading ')' and ']' close come off before
		// it is indented, so "))" lines up with the line that opened them.
		for n := pclosers; n > 0 && len(groups) > 0; {
			top := &groups[len(groups)-1]
			k := min(n, top.n)
			top.n -= k
			n -= k
			if top.n == 0 {
				groups = groups[:len(groups)-1]
			}
		}
		depthStart := depth
		indent := max(depth-closers, 0) + indents()
		if depth == 0 && len(groups) == 0 && li > 0 && line[0].kind != ast.TokLBrace && contStart(line[0]) {
			// A signature's continuation line, indented as Mk style
			// writes it: "    requires x > 0".
			indent = 1
		}
		p.b.WriteString(strings.Repeat("    ", indent))
		open := 0 // '(' and '[' this line opened and has not closed
		var prev tok
		prevPrefix := false
		closedPrefix := false
		for i, t := range line {
			if i > 0 && space(prev, t, prevPrefix, closedPrefix) {
				p.b.WriteByte(' ')
			}
			if t.kind == ast.TokIdent && t.origin != 0 {
				p.origins = append(p.origins, TokenOrigin{Offset: uint32(p.b.Len()), Kind: t.origin, Module: t.module, Item: t.item})
			}
			p.b.WriteString(t.spell())
			end = uint32(p.b.Len())
			// prevClosed: prev was the ']' of a type prefix, so what
			// follows is that prefix's element type: a '[' there opens
			// another prefix ([][]T) and a prefix operator is a prefix
			// ([]&T), not an index or a binary operator.
			prevClosed := closedPrefix
			closedPrefix = false
			switch t.kind {
			case ast.TokLBrace:
				depth++
			case ast.TokRBrace:
				depth = max(depth-1, 0)
			case ast.TokLBracket:
				prefixBracket = append(prefixBracket, i == 0 || prevClosed || !(prev.kind == ast.TokIdent || prev.kind == ast.TokRParen || prev.kind == ast.TokRBracket))
			case ast.TokRBracket:
				if n := len(prefixBracket); n > 0 {
					closedPrefix = prefixBracket[n-1]
					prefixBracket = prefixBracket[:n-1]
				}
			}
			if i >= closers+pclosers {
				switch t.kind {
				case ast.TokLParen, ast.TokLBracket:
					open++
				case ast.TokRParen, ast.TokRBracket:
					if open > 0 {
						open--
					} else if n := len(groups); n > 0 {
						if groups[n-1].n--; groups[n-1].n == 0 {
							groups = groups[:n-1]
						}
					}
				}
			}
			prevPrefix = prefixOp(t.kind) && (i == 0 || prevClosed || !operandEnd(prev.kind))
			prev = t
		}
		if open > 0 {
			groups = append(groups, group{n: open, indent: depth <= depthStart})
		}
	}
	return end
}

// space decides the space between two tokens on one line.
func space(a, b tok, aPrefix, aClosedPrefix bool) bool {
	if a.tight || b.tight {
		return false
	}
	switch a.kind {
	case ast.TokLParen, ast.TokLBracket, ast.TokDot, ast.TokDollar, ast.TokDotDot:
		return false
	}
	if aPrefix {
		return false
	}
	switch b.kind {
	case ast.TokRParen, ast.TokRBracket, ast.TokComma, ast.TokDot, ast.TokColon, ast.TokDotDot:
		return false
	case ast.TokLParen, ast.TokLBracket:
		if a.kind == ast.TokIdent || a.kind == ast.TokRParen || a.kind == ast.TokRBracket {
			return false
		}
		// fn(int): bool, the function type.
		if a.kind == ast.TokFn && b.kind == ast.TokLParen {
			return false
		}
	case ast.TokBang:
		// E!T: an infix ! in an error-union type takes no spaces.
		if operandEnd(a.kind) {
			return false
		}
	}
	if a.kind == ast.TokBang && !aPrefix {
		return false
	}
	// "[]T", "[N]T": a closed type prefix binds its element.
	if aClosedPrefix && a.kind == ast.TokRBracket {
		return false
	}
	return true
}
