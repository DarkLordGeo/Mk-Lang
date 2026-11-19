// Why: a contract clause must be spelled as text the same way whether its
// module was checked from source or loaded from the cache without an AST;
// the restated-clause rule compares those spellings. It lives apart from
// consts.go to keep both files under the line limit.
//
// What: contractText (Contract.Text), clauseText, exprText,
// exprTextNames, exprTextSub, typeOrExprText, substClause and labelAt.
//
// How: exprText walks the clause's expression and writes a canonical
// spelling; substClause and exprTextSub replace names in it, skipping
// fields, field labels and literals (str, char, byte and raw literals).

package check

import (
	"strings"

	"mk/internal/ast"
	"mk/internal/source"
	"mk/internal/types"
)

// contractText is a contract's Contract.Text: its expression as
// exprText spells it, set in stage 5 so that a module Load decoded, which
// has no AST, answers clauseText and the restated-clause rule the same.
func (c *Checker) contractText(n *ast.Contract) string {
	if n == nil || n.X == nil {
		return ""
	}
	var b strings.Builder
	c.exprText(&b, n.X)
	return b.String()
}

// substClause is exprTextSub over a clause's text: every name that is not
// a field (no '.' before it), not a field label (labelAt) and not inside a
// literal is replaced by sub's text for it (substWords' scan). exprText
// spells a name as its identifier, so this gives the AST walk's spelling.
// Literals are skipped as clauseNames skips them: str and char literals
// with their escapes, the prefixed `b'a'` and `b"bytes"`, and the raw
// `r"C:\"`, which takes no escape (read as the name r and a quoted literal
// with escapes, `F(r"\") == N` would keep its N).
func (c *Checker) substClause(text string, sub map[source.Symbol]string) string {
	if len(sub) == 0 {
		return text
	}
	byName := make(map[string]string, len(sub))
	for k, v := range sub {
		byName[c.cfg.Interner.Str(k)] = v
	}
	return substWords(text, func(w string, _ int, _ string) (string, bool) {
		if s, ok := byName[w]; ok {
			return s, false
		}
		return w, true
	})
}

// labelAt reports whether the word of text that ends at j is a field label:
// exprText writes a label as the word, ':' and a space (`P(a: k)`), and no
// other form of a clause's text puts ':' after a word.
func labelAt(text string, j int) bool { return j < len(text) && text[j] == ':' }

// clauseText spells a requires predicate's expression for the fact clause:
// its owner's Contract.Text, else the predicate's AST.
func (c *Checker) clauseText(pred *types.Item) string {
	if pred.Owner != types.NoItem {
		if fd, ok := c.cfg.Items.Get(pred.Owner).Decl.(*types.FuncDecl); ok {
			for _, ct := range fd.Sig.Contracts {
				if ct.Body == pred.ID && ct.Text != "" {
					return ct.Text
				}
			}
		}
	}
	m := c.module(pred.Module)
	if m == nil || m.byItem[pred.ID] == nil {
		return ""
	}
	n, _ := m.nodeOf(pred.ID)
	ct, ok := n.(*ast.Contract)
	if !ok {
		return ""
	}
	var b strings.Builder
	c.exprText(&b, ct.X)
	return b.String()
}

// exprText spells the expression forms a clause is made of.
func (c *Checker) exprText(b *strings.Builder, e ast.Expr) { c.exprTextSub(b, e, nil, nil) }

// exprTextNames is exprText that hands names each node that writes a name
// of the text, with the offset in b where it starts: an Ident (not a
// field's), a FieldExpr (before its left side; resolve's ref of a
// module-qualified item is on it) and a named type's PathType
// (restate.go's callerNames).
func (c *Checker) exprTextNames(b *strings.Builder, e ast.Expr, names func(ast.Node, int)) {
	c.exprTextSub(b, e, nil, names)
}

// exprTextSub is exprText with names replaced by sub (the restated-clause
// rule), and names as exprTextNames has it (nil for none).
func (c *Checker) exprTextSub(b *strings.Builder, e ast.Expr, sub map[source.Symbol]string, names func(ast.Node, int)) {
	switch e := e.(type) {
	case *ast.Ident:
		if s, ok := sub[e.Name]; ok {
			b.WriteString(s)
			return
		}
		if names != nil {
			names(e, b.Len())
		}
		b.WriteString(c.cfg.Interner.Str(e.Name))
	case *ast.BasicLit:
		b.WriteString(e.Raw)
	case *ast.ParenExpr:
		b.WriteByte('(')
		c.exprTextSub(b, e.X, sub, names)
		b.WriteByte(')')
	case *ast.UnaryExpr:
		b.WriteString(e.Op.FixedText())
		c.exprTextSub(b, e.X, sub, names)
	case *ast.BinaryExpr:
		c.exprTextSub(b, e.X, sub, names)
		b.WriteString(" " + e.Op.FixedText() + " ")
		c.exprTextSub(b, e.Y, sub, names)
	case *ast.FieldExpr:
		if names != nil {
			names(e, b.Len())
		}
		c.exprTextSub(b, e.X, sub, names)
		b.WriteByte('.')
		b.WriteString(c.cfg.Interner.Str(e.Name.Name))
	case *ast.CallExpr:
		c.exprTextSub(b, e.Fun, sub, names)
		b.WriteByte('(')
		for i, a := range e.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			if a != nil && a.Name != nil {
				// A field label, `P(a: k)`: without it `P(a: 1, b: k)` and
				// `P(b: 1, a: k)` spell alike. Every scanner of clause text
				// reads a word before ':' as a label, never a name (labelAt).
				b.WriteString(c.cfg.Interner.Str(a.Name.Name))
				b.WriteString(": ")
			}
			if a != nil && a.Value != nil {
				c.exprTextSub(b, a.Value, sub, names)
			}
		}
		b.WriteByte(')')
	case *ast.IndexExpr:
		// SizeOf[U]() in a clause, spelled in full (not `...()`).
		c.exprTextSub(b, e.X, sub, names)
		b.WriteByte('[')
		for i, a := range e.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			c.typeOrExprText(b, a, sub, names)
		}
		b.WriteByte(']')
	default:
		b.WriteString("...")
	}
}

// typeOrExprText spells a bracket argument: an expression as exprTextSub
// does, a named type as its path with its own arguments, else "...".
func (c *Checker) typeOrExprText(b *strings.Builder, a ast.TypeOrExpr, sub map[source.Symbol]string, names func(ast.Node, int)) {
	switch a := a.(type) {
	case ast.Expr:
		c.exprTextSub(b, a, sub, names)
	case *ast.PathType:
		if a.Path == nil {
			b.WriteString("...")
			return
		}
		if names != nil && len(a.Path.Names) > 0 {
			if _, ok := sub[a.Path.Names[0].Name]; !ok || len(a.Path.Names) > 1 {
				names(a, b.Len())
			}
		}
		for i, n := range a.Path.Names {
			if i > 0 {
				b.WriteByte('.')
			}
			if s, ok := sub[n.Name]; ok && len(a.Path.Names) == 1 {
				b.WriteString(s)
				continue
			}
			b.WriteString(c.cfg.Interner.Str(n.Name))
		}
		if a.Args != nil {
			b.WriteByte('[')
			for i, x := range a.Args.Args {
				if i > 0 {
					b.WriteString(", ")
				}
				c.typeOrExprText(b, x, sub, names)
			}
			b.WriteByte(']')
		}
	default:
		b.WriteString("...")
	}
}
