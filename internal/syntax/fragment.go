// Why: compile-time generation (internal/gen) prints Mk text from a quote and
// must parse it back into items, statements, a type or an expression.
//
// What: FragmentKind and ParseFragment.
//
// How: lexes the text, runs the normal parser at the requested production,
// and bounds depth as ParseFile does: a too-deep node becomes a Bad node.

package syntax

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
)

// FragmentKind is what a generated fragment must be.
type FragmentKind uint8

const (
	FragItems FragmentKind = iota // { Item Term }, as a file
	FragStmts                     // { Stmt Term }, as a block body without braces
	FragType                      // one Type
	FragExpr                      // one Expr
)

// ParseFragment parses generated text held in f (internal/gen builds f from
// the printed output of a quote) as the given kind. The ast.File holds the
// tokens and NodeCount; nodes is the items, statements, or the one type or
// expression. A gen item inside generated output is reported by gen as
// E0905; ParseFragment reports it as E0101 "generated output holds no gen"
// so gen can wrap it.
func ParseFragment(f *source.File, in *source.Interner, kind FragmentKind, opts Options) (*ast.File, []ast.Node, []*diag.Diagnostic) {
	lx := lexWith(f.ID, f.Text, kind == FragStmts)
	file := &ast.File{Path: f.Path, Tokens: lx.toks}
	var nodes []ast.Node
	var diags []*diag.Diagnostic
	whole := source.Span{File: f.ID, Start: 0, End: uint32(len(f.Text))}
	ie := source.Guard("parse fragment "+f.Path, whole, func() {
		p := newParser(f, in, opts, lx)
		p.nextID = 1
		file.NID = 1
		file.Sp = whole
		p.region()
		switch kind {
		case FragItems:
			p.parseFile(file)
			for _, it := range file.Items {
				if g, ok := it.(*ast.GenItem); ok {
					p.syntaxErr(g.Sp, "generated output holds no gen")
				}
				nodes = append(nodes, it)
			}
		case FragStmts:
			p.fn = &fnCtx{}
			for !p.at(ast.TokEOF) {
				if p.at(ast.TokTerm) {
					p.next()
					continue
				}
				p.open = 0
				p.tooDeep = false
				before := p.p
				st := p.parseStmt()
				if p.tooDeep {
					// As ParseFile does for an item: the deep
					// statement is discarded for a BadStmt over its tokens,
					// skipped from its start by the bracket count.
					p.p = before
					p.softTerm = false
					p.skip(stopStmt)
					p.open, p.tooDeep = 0, false
					st = p.badStmtFrom(before)
				}
				nodes = append(nodes, st)
				p.endElem("one statement per line")
				if p.p == before && !p.at(ast.TokEOF) {
					p.next()
				}
			}
		case FragType, FragExpr:
			p.fn = &fnCtx{}
			var x ast.Node
			if kind == FragType {
				x = p.parseType()
			} else {
				x = p.parseExpr()
			}
			if p.tooDeep {
				// The whole fragment is the one deep node: it becomes a Bad
				// node over every token, so no returned tree is deeper than
				// the limit (DefaultMaxDepth).
				for !p.at(ast.TokEOF) {
					p.next()
				}
				p.open, p.tooDeep = 0, false
				if kind == FragType {
					x = p.badTypeFrom(0)
				} else {
					x = p.badExprFrom(0)
				}
			}
			nodes = append(nodes, x)
			p.accept(ast.TokTerm)
			if !p.at(ast.TokEOF) {
				p.errHere("expected the end of the fragment")
			}
		}
		file.NodeCount = uint32(p.nextID)
		p.flushKeywordNames(0, uint32(len(p.toks)))
		p.mergeHits()
		diags = append(append(diags, lx.diags...), p.diags...)
	})
	if ie != nil {
		nodes = nil
		file.Items = nil
		file.NodeCount = 1
		diags = append(append([]*diag.Diagnostic(nil), lx.diags...), diag.FromInternal(ie))
	}
	sortDiags(diags)
	return file, nodes, diags
}
