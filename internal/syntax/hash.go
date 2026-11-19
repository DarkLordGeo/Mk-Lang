// Why: the incremental cache must not rebuild an item when only comments,
// whitespace or its position change, so items are keyed by a hash of their
// tokens, not of their bytes.
//
// What: ItemHash and FileHash, the token hashes, and DocComment, an item's
// doc comment for tooling.
//
// How: SHA-256 over the non-trivia tokens, each as a self-delimiting
// string; termSkipper leaves out the line ends the parser joined inside a
// signature (continuationRanges), so moving a flow or contract to its own
// line changes nothing.

package syntax

import (
	"strings"

	"mk/internal/ast"
	"mk/internal/source"
)

// ItemHash is the token hash of one item: SHA-256 in domain
// "mk.syntax.item.v1" over the non-trivia tokens of it.Info().Toks, each
// written as one self-delimiting string (its text; the empty string for
// TokTerm), except the continuation Terms (a Term at a signature's own brace
// level, inside its span or between it and its body's '{'), which are layout.
// No offset, line, NodeID or Symbol enters it, so comments, whitespace, doc
// comments, the item's position and moving a flow or contract onto its own
// line change nothing. A Term inside a block nested in a signature (a fn
// literal in a contract, an else handler in an array length) ends a statement
// and is hashed.
func ItemHash(f *ast.File, text string, it ast.Item) source.Hash {
	h := source.NewHasher("mk.syntax.item.v1")
	r := it.Info().Toks
	w := termSkipper{ranges: continuationRanges(it, nil)}
	for i := r.First; i < r.End && int(i) < len(f.Tokens); i++ {
		w.write(h, f.Tokens[i], text)
	}
	return h.Sum()
}

// FileHash is the same hash over every non-trivia token of the file, with
// the same continuation Terms left out, in domain "mk.syntax.file.v1". It is
// linear in the tokens: the ranges are walked with one cursor.
func FileHash(f *ast.File, text string) source.Hash {
	h := source.NewHasher("mk.syntax.file.v1")
	var ranges []span2
	for _, it := range f.Items {
		ranges = continuationRanges(it, ranges)
	}
	w := termSkipper{ranges: ranges}
	for _, t := range f.Tokens {
		w.write(h, t, text)
	}
	return h.Sum()
}

// span2 is a half-open offset range [a, b).
type span2 struct{ a, b uint32 }

// termSkipper writes tokens to a hasher in offset order and leaves out the
// continuation Terms: a Term inside one of ranges (in offset order, disjoint)
// at brace depth zero counted from the range's start. The lexer emits a Term
// only in statement mode (no bracket open, or a '{' innermost), so inside a
// signature a Term at depth zero is a line the parser joined by
// continuation, and a Term under a '{' ends a statement of a nested block (a
// fn literal, an else handler). Brackets are counted with the lexer's rule.
type termSkipper struct {
	ranges []span2
	cur    int
	in     bool
	stack  []ast.TokenKind
}

func (w *termSkipper) write(h *source.Hasher, t ast.Token, text string) {
	if t.Kind.IsTrivia() || t.Kind == ast.TokEOF {
		return
	}
	for w.cur < len(w.ranges) && t.Start >= w.ranges[w.cur].b {
		w.cur++
		w.in = false
	}
	inRange := w.cur < len(w.ranges) && t.Start >= w.ranges[w.cur].a
	if inRange && !w.in {
		w.in = true
		w.stack = w.stack[:0]
	}
	if inRange {
		switch t.Kind {
		case ast.TokLParen, ast.TokLBracket, ast.TokLBrace:
			w.stack = append(w.stack, t.Kind)
		case ast.TokRParen, ast.TokRBracket, ast.TokRBrace:
			w.stack = popTo(w.stack, openerOf(t.Kind))
		case ast.TokTerm:
			if len(w.stack) == 0 {
				return
			}
		}
	}
	if t.Kind == ast.TokTerm {
		h.String("")
		return
	}
	h.String(t.Text(text))
}

// continuationRanges appends to out the offset ranges of an item in which a
// Term may be a continuation Term: inside a signature, or between a
// signature's end and its body's '{'. They come in offset order.
func continuationRanges(it ast.Item, out []span2) []span2 {
	add := func(sig *ast.Signature, body *ast.Block) {
		if sig == nil {
			return
		}
		end := sig.Sp.End
		if body != nil && body.Sp.Start > end {
			end = body.Sp.Start
		}
		if end > sig.Sp.Start+1 {
			out = append(out, span2{sig.Sp.Start + 1, end})
		}
	}
	switch n := it.(type) {
	case *ast.FuncDecl:
		add(n.Sig, n.Body)
	case *ast.ExternDecl:
		add(n.Sig, n.Body)
	case *ast.InterfaceDecl:
		for _, m := range n.Methods {
			add(m, nil)
		}
	case *ast.ImplDecl:
		for _, m := range n.Methods {
			add(m.Sig, m.Body)
		}
	}
	return out
}

// DocComment returns the text of the /// lines that open it.Info().Toks, with
// the "///" and one following space removed from each line, joined by "\n";
// "" when the item has none. Tooling only; the compiler never reads it.
func DocComment(f *ast.File, text string, it ast.Item) string {
	r := it.Info().Toks
	var lines []string
	for i := r.First; i < r.End && int(i) < len(f.Tokens); i++ {
		t := f.Tokens[i]
		switch t.Kind {
		case ast.TokWhitespace, ast.TokNewline:
			continue
		case ast.TokDocComment:
			s := strings.TrimPrefix(t.Text(text), "///")
			lines = append(lines, strings.TrimPrefix(s, " "))
			continue
		}
		break
	}
	return strings.Join(lines, "\n")
}
