// Why: quote { ... } turns source text into a Code value that compile-time
// generators splice and emit; lower produces the call that builds it.
//
// What: quoteExpr, which lowers a quote to dest = CallBuiltin
// BuiltinQuote(template, s0, s1, ...) with dest of type Code; quoteTemplate,
// which encodes the template; bytesConst, the template's []u8 constant.
//
// How: the template is an OpConst of type []u8 in a small versioned format
// ("mkq1"), and the splices are operands in source order. Whitespace and
// comments are dropped, so editing them inside a quote leaves its bytes
// unchanged.

package lower

import (
	"encoding/binary"

	"mk/internal/ast"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// quoteMagic is the template's magic and version.
const quoteMagic = "mkq1"

// quoteExpr lowers quote { ... }: the splice operands first, in source
// order, then the builtin call into a fresh Code temporary.
func (bl *builder) quoteExpr(x *ast.QuoteExpr) mir.Rvalue {
	if bl.cfg.Sources == nil {
		source.ICEf(x.Span(), "lower: a quote needs Config.Sources for its token text")
	}
	f := bl.in.File()
	src := bl.cfg.Sources.File(f.Span().File)
	if src == nil {
		source.ICEf(x.Span(), "lower: no source text for the quote's file")
	}
	tmpl := bl.bytesConst(quoteTemplate(f, src.Text, x), x.Span())
	args := []mir.Arg{{Operand: tmpl, Mode: mir.ArgCopy}}
	for _, sp := range x.Splices {
		op := bl.operand(sp.X)
		if !bl.open() {
			return use(mir.Imm(types.Never, 0))
		}
		args = append(args, mir.Arg{Operand: op, Mode: bl.argMode(op, sp.Span())})
	}
	dest := bl.newTemp(bl.typeOf(x), x.Span())
	target := bl.newBlock()
	bl.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallBuiltin, Builtin: mir.BuiltinQuote},
		Args: bl.b.AddArgs(args...), Dest: bl.b.Place(dest), Target: target, Spawn: mir.NoLocal}, x.Span())
	bl.cur = target
	return use(bl.valueOfTemp(dest, x.Span()))
}

// bytesConst is a []u8 constant in the evaluator's form (as check's
// strConst): {ptr, len} words, the pointer a RelocData
// to a [N]u8 const of the bytes ([0]u8 when empty, as strConst). Equal
// bytes intern to one const.
func (bl *builder) bytesConst(bs []byte, at source.Span) mir.Operand {
	return bl.dataConst(bl.tab.Slice(types.U8), bs, at)
}

// quoteText reports whether a token kind's text is stored in a template:
// names and literals, whose spelling varies.
func quoteText(k ast.TokenKind) bool {
	switch k {
	case ast.TokIdent, ast.TokInt, ast.TokFloat, ast.TokChar, ast.TokByte, ast.TokStr, ast.TokRawStr,
		ast.TokMultiStr, ast.TokByteStr:
		return true
	}
	return false
}

// quoteTemplate encodes the template of q (version 1): "mkq1", uvarint N, then
// N entries, each a uvarint ast.TokenKind, for a name or literal a uvarint
// length and the text, for a hole TokDollar and the uvarint splice index. text
// is the file's normalized text.
//
// Whitespace and comments are dropped and newlines kept, so a whitespace or
// comment edit inside the quote leaves the bytes unchanged. TokTerm,
// the zero-width terminator the newline rule inserts, is dropped too:
// it has no text, and the newline rule inserts it again when gen's output is
// parsed. A hole replaces every token of its splice, $ included. gen shares
// the golden file (testdata/quote_template.golden), not this function.
func quoteTemplate(f *ast.File, text string, q *ast.QuoteExpr) []byte {
	var body []byte
	n, k := 0, 0
	toks := f.Tokens[q.Toks.First:q.Toks.End]
	for j := 0; j < len(toks); j++ {
		tk := toks[j]
		switch tk.Kind {
		case ast.TokWhitespace, ast.TokComment, ast.TokDocComment, ast.TokTerm:
			continue
		}
		if k < len(q.Splices) && tk.Start >= q.Splices[k].Span().Start {
			sp := q.Splices[k].Span()
			if tk.Kind != ast.TokDollar || tk.Start != sp.Start {
				source.ICEf(sp, "lower: splice %d does not start at a $ token", k)
			}
			body = binary.AppendUvarint(body, uint64(ast.TokDollar))
			body = binary.AppendUvarint(body, uint64(k))
			for j+1 < len(toks) && toks[j+1].Start < sp.End {
				j++
			}
			n++
			k++
			continue
		}
		body = binary.AppendUvarint(body, uint64(tk.Kind))
		if quoteText(tk.Kind) {
			body = binary.AppendUvarint(body, uint64(tk.Len))
			body = append(body, text[tk.Start:tk.End()]...)
		}
		n++
	}
	if k != len(q.Splices) {
		source.ICEf(q.Span(), "lower: %d of %d splices met in the quote's tokens", k, len(q.Splices))
	}
	out := append([]byte(quoteMagic), binary.AppendUvarint(nil, uint64(n))...)
	return append(out, body...)
}
