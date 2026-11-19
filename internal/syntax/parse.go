// Why: callers need one entry point that always returns a whole tree and a
// stable, sorted list of diagnostics, even when the parser itself faults.
//
// What: ParseFile and sortDiags.
//
// How: lexes, parses under source.Guard (an internal fault becomes one E0000
// diagnostic), merges habit hits and sorts diagnostics by (offset, code).

package syntax

import (
	"sort"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
)

// ParseFile lexes and parses f. The returned file is always total: every item
// that did not parse is an ast.BadItem, every bad element a Bad* node, and
// ast.Validate(file, f.Text) holds. The diagnostics are every parse-phase
// error of the file, habits merged per line, in (offset, code) order. The work
// runs under source.Guard; an internal fault returns a file with its tokens and
// no items, and one diag.FromInternal diagnostic (E0000).
func ParseFile(f *source.File, in *source.Interner, opts Options) (*ast.File, []*diag.Diagnostic) {
	lx := lexWith(f.ID, f.Text, false)
	file := &ast.File{Path: f.Path, Tokens: lx.toks}
	var diags []*diag.Diagnostic
	whole := source.Span{File: f.ID, Start: 0, End: uint32(len(f.Text))}
	ie := source.Guard("parse "+f.Path, whole, func() {
		p := newParser(f, in, opts, lx)
		p.parseFile(file)
		file.NodeCount = uint32(p.nextID)
		p.mergeHits()
		diags = append(append(diags, lx.diags...), p.diags...)
	})
	if ie != nil {
		file.Items = nil
		file.NID = 1
		file.Sp = whole
		file.NodeCount = 1
		diags = append(append([]*diag.Diagnostic(nil), lx.diags...), diag.FromInternal(ie))
	}
	sortDiags(diags)
	return file, diags
}

// sortDiags orders diagnostics by (offset, code), stably.
func sortDiags(ds []*diag.Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].At.Start != ds[j].At.Start {
			return ds[i].At.Start < ds[j].At.Start
		}
		return ds[i].Code < ds[j].Code
	})
}
