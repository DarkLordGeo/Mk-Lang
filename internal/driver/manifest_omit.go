// Why: in a manifest, a constructor call of a std.pkg struct may leave out
// any field whose value would be "", [] or false, while the checker's rule
// that a constructor names every field (E0305) stays unchanged for all
// other Mk code.
//
// What: the manifest step's field supply: supplyOmitted, supplyFile,
// pkgStruct and emptyValue. A positional call or a misspelled field is
// still E0305.
//
// How: the supplied argument is added to the parsed manifest, not to its
// text: an `ast.Arg` named like the field, holding the empty value, with an
// empty span at the call's closing parenthesis and a fresh node id. So the
// text, every token and every span of the original file stay as written,
// no second copy of the file enters the SourceMap (whose files the LSP
// lists), and no diagnostic needs its spans mapped back. A field is
// supplied only when its std.pkg type is str, a slice or bool, read from
// the checked std.pkg world.

package driver

import (
	"mk/internal/ast"
	"mk/internal/source"
	"mk/internal/types"
)

// supplyOmitted adds the missing fields of every all-named `pkg.<T>(...)`
// call in the manifest files of b (std's excluded), T a struct of std.pkg.
// It returns the number of fields supplied.
func (ms *manifestStep) supplyOmitted(b *build) int {
	n := 0
	for _, m := range b.mods {
		if m.std {
			continue
		}
		for _, f := range m.files {
			if f.ast != nil {
				n += ms.supplyFile(f.ast)
			}
		}
	}
	return n
}

// supplyFile supplies the omitted fields of the std.pkg calls in file.
func (ms *manifestStep) supplyFile(file *ast.File) int {
	in := ms.w.in
	var calls []*ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			calls = append(calls, c)
		}
		return n != nil
	})
	n := 0
	for _, c := range calls {
		fe, ok := c.Fun.(*ast.FieldExpr)
		if !ok || fe.Name == nil {
			continue
		}
		if x, ok := fe.X.(*ast.Ident); !ok || in.Str(x.Name) != "pkg" {
			continue
		}
		sd := ms.pkgStruct(in.Str(fe.Name.Name))
		if sd == nil {
			continue
		}
		named := map[string]bool{}
		positional := false
		for _, a := range c.Args {
			if a == nil || a.Name == nil {
				positional = true
				break
			}
			named[in.Str(a.Name.Name)] = true
		}
		if positional {
			continue // still E0305: a struct is built by named fields only
		}
		end := c.Span().End
		if end > c.Span().Start {
			end-- // the closing parenthesis
		}
		at := source.Span{File: c.Span().File, Start: end, End: end}
		for _, fd := range sd.Fields {
			name := in.Str(fd.Name)
			if named[name] {
				continue
			}
			v := ms.emptyValue(fd.Type, at, file)
			if v == nil {
				continue
			}
			c.Args = append(c.Args, &ast.Arg{Base: ms.node(file, at), Name: &ast.Ident{Base: ms.node(file, at), Name: fd.Name}, Value: v})
			n++
		}
	}
	return n
}

// node is a fresh node of file at the empty span at.
func (ms *manifestStep) node(file *ast.File, at source.Span) ast.Base {
	file.NodeCount++
	return ast.Base{NID: ast.NodeID(file.NodeCount), Sp: at}
}

// pkgStruct is the checked declaration of std.pkg's exported struct name,
// or nil.
func (ms *manifestStep) pkgStruct(name string) *types.StructDecl {
	id, ok := ms.mw.items.ByKey(types.ItemKey(pkgModule, name))
	if !ok {
		return nil
	}
	sd, _ := ms.mw.items.Get(id).Decl.(*types.StructDecl)
	return sd
}

// emptyValue is the expression of t's empty value, "" for str, [] for a
// slice and false for bool, or nil for any other type (never omitted).
func (ms *manifestStep) emptyValue(t types.TypeID, at source.Span, file *ast.File) ast.Expr {
	switch ms.mw.tab.Get(t).Kind {
	case types.KStr:
		return &ast.BasicLit{Base: ms.node(file, at), Kind: ast.TokStr, Raw: `""`}
	case types.KSlice:
		return &ast.ArrayLit{Base: ms.node(file, at)}
	case types.KBool:
		return &ast.Ident{Base: ms.node(file, at), Name: ms.w.in.Intern("false")}
	}
	return nil
}
