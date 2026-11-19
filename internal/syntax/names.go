// Why: a keyword, universe name or primitive type name used as a declared
// name is E0102, and the parser should still read on and offer a rename.
//
// What: nameKind, parseDeclName and checkDeclared for declared names,
// flushKeywordNames with freshName for the per-item renames, and parsePath.
//
// How: a keyword in name position is read as a name with that text and
// queued; at the end of the item each keyword-named local or parameter gets
// one E0102 whose fix renames the declaration and every use in the item.
// Declaring a universe name other than ok and err is E0102 as well.

package syntax

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
)

// nameKind says what a declared name declares, for E0102's fix class: a
// keyword-named local or parameter gets a proven rename, everything else
// alternatives.
type nameKind uint8

const (
	declItem nameKind = iota
	declField
	declParam
	declLocal
	declTypeParam
	declImport
)

// parseDeclName reads a declared name. A keyword is E0102 and read as
// a name with that text; a declared universe or primitive name other than ok
// and err is E0102. With nothing name-like there it reports E0101 and returns
// an empty Ident.
func (p *parser) parseDeclName(kind nameKind) *ast.Ident {
	t := p.tok()
	switch {
	case t.Kind == ast.TokIdent:
		id := p.ident()
		p.checkDeclared(id, kind)
		return id
	case t.Kind.IsKeyword():
		p.kwNames = append(p.kwNames, kwName{text: p.tokText(t), span: t.Span(p.file), decl: kind == declLocal || kind == declParam, other: kind != declLocal && kind != declParam})
		return p.ident()
	}
	p.expected("a name")
	return p.missingIdent()
}

// checkDeclared reports a declared universe or primitive type name.
func (p *parser) checkDeclared(id *ast.Ident, kind nameKind) {
	s := id.Name
	if s == source.SymOk || s == source.SymErr {
		return
	}
	var what string
	switch {
	case source.IsPrimitiveType(s):
		what = "is a predeclared type name"
	case source.IsUniverse(s):
		what = "is a universe name"
	default:
		return
	}
	text := p.in.Str(s)
	d := diag.New(diag.E0102, id.Sp, "`"+text+"` "+what).In(diag.PhaseParse)
	d.Text("name", text)
	if kind == declItem || kind == declTypeParam {
		// A top-level name or a type parameter is PascalCase, and E0207
		// refuses its `_`, so no suffix is suggested.
		d.Alt("rename it to a free PascalCase name")
	} else {
		d.Alt("rename it to `" + text + "_`")
	}
	p.diags = append(p.diags, d)
}

// keywordAsName reports E0102 for a keyword used where a name is required
// (after '.', as a path segment, as a member name); the caller then reads it
// as an Ident.
func (p *parser) keywordAsName() {
	t := p.tok()
	p.kwNames = append(p.kwNames, kwName{text: p.tokText(t), span: t.Span(p.file)})
}

// kwName is a keyword read as a name: a declaration (a local or parameter,
// which gets the proven rename), another declared name, or a use.
type kwName struct {
	text  string
	span  source.Span
	decl  bool
	other bool
}

// flushKeywordNames reports the keyword names of one item.
// Each keyword declared as a local or parameter gets one E0102 with a proven
// rename: the new name is the keyword plus "_" (or "_2", "_3" … when that
// text is already a name in the item), edited at the declaration and at every
// use of the keyword as a name in the item, and those uses are folded into
// it. Every other keyword name gets its own E0102 with the rename as an
// alternative.
func (p *parser) flushKeywordNames(first, end uint32) {
	names := p.kwNames
	p.kwNames = nil
	folded := map[string]bool{}
	for _, k := range names {
		if !k.decl || folded[k.text] {
			continue
		}
		folded[k.text] = true
		newName := p.freshName(k.text, first, end)
		d := diag.New(diag.E0102, k.span, "`"+k.text+"` is a keyword").In(diag.PhaseParse)
		d.Text("name", k.text)
		var edits []diag.Edit
		for _, u := range names {
			if u.text == k.text && !u.other {
				edits = append(edits, diag.Edit{Span: u.span, Text: newName})
			}
		}
		d.Fix(diag.Fix{Class: diag.Proven, Edits: edits, Why: "rename `" + k.text + "` to `" + newName + "`"})
		p.diags = append(p.diags, d)
	}
	for _, k := range names {
		if folded[k.text] && !k.other {
			continue
		}
		d := diag.New(diag.E0102, k.span, "`"+k.text+"` is a keyword").In(diag.PhaseParse)
		d.Text("name", k.text)
		d.Alt("rename it to `" + p.freshName(k.text, first, end) + "`")
		p.diags = append(p.diags, d)
	}
}

// freshName is text plus "_", or "_2", "_3" … when that is already an
// identifier among the tokens [first, end).
func (p *parser) freshName(text string, first, end uint32) string {
	used := map[string]bool{}
	for i := first; i < end && int(i) < len(p.toks); i++ {
		if t := p.toks[i]; t.Kind == ast.TokIdent {
			used[p.tokText(t)] = true
		}
	}
	name := text + "_"
	for n := 2; used[name]; n++ {
		name = text + "_" + itoa(n)
	}
	return name
}

// itoa formats a small non-negative integer.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// parsePath reads Name { "." Name }; keyword segments are E0102.
func (p *parser) parsePath() *ast.Path {
	n := &ast.Path{}
	p.begin(&n.Base)
	n.Names = append(n.Names, p.parseName("a name"))
	for (p.at(ast.TokDot) || p.at(ast.TokColonColon)) && (p.peekKind(1) == ast.TokIdent || p.peekKind(1).IsKeyword()) {
		if p.at(ast.TokColonColon) {
			p.colonColonHabit()
		}
		p.next()
		n.Names = append(n.Names, p.parseName("a name"))
	}
	p.finish(n, &n.Base)
	return n
}
