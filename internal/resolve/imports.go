// Why: the rule for what name an import binds is needed both here and by the
// driver, which reads import lines before it resolves anything; keeping it
// in one function keeps the two in agreement.
//
// What: ImportLine and ImportsOf.
//
// How: an import binds its alias, else the last segment of its path.
// ImportsOf lists one file's import lines in source order and skips an
// import whose path did not parse.

package resolve

import (
	"strings"

	"mk/internal/ast"
	"mk/internal/source"
)

// ImportLine is one import line, syntactically.
type ImportLine struct {
	Binding  source.Symbol
	Path     string
	Node     ast.NodeID
	At       source.Span // the import item
	PathSpan source.Span
}

// ImportsOf lists the import lines of one file in source order: binding
// name (the alias, else the last path segment), the path as written with
// '.', and the spans of the line and of the path. An import whose path did
// not parse (no segment) is skipped.
func ImportsOf(f *ast.File, in *source.Interner) []ImportLine {
	var out []ImportLine
	for _, it := range f.Items {
		im, ok := it.(*ast.Import)
		if !ok || im.Path == nil || len(im.Path.Names) == 0 {
			continue
		}
		parts := make([]string, len(im.Path.Names))
		for i, n := range im.Path.Names {
			parts[i] = in.Str(n.Name)
		}
		bind := im.Path.Names[len(im.Path.Names)-1].Name
		if im.Alias != nil {
			bind = im.Alias.Name
		}
		out = append(out, ImportLine{
			Binding:  bind,
			Path:     strings.Join(parts, "."),
			Node:     im.ID(),
			At:       im.Span(),
			PathSpan: im.Path.Span(),
		})
	}
	return out
}
