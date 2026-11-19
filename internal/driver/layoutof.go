// Why: the layout conformance test compares every field offset in
// tests/conformance/diff/layout.<target>.golden with the checker's
// types.Layout, so tests need a way to ask the checker for a layout.
// What: TypeLayout and Session.LayoutOf, a test accessor for check's layouts.
// How: reads the last Check's tables and changes nothing a command reports.

package driver

import (
	"fmt"

	"mk/internal/source"
	"mk/internal/types"
)

// TypeLayout is check's layout of one closed type, as a test reads it.
type TypeLayout struct {
	// Kind is the type's kind, an alias resolved: KNamed for a struct or an
	// enum, KArray, KSlice, KStr, KOptional, KErrorUnion, KRecord, a scalar.
	Kind types.TypeKind
	// Len is an array's length (its size over its element's size, as check
	// lays it out); 0 for every other kind.
	Len uint64
	// Elem is an array's element shape; zero for every other kind.
	Elem types.Shape
	// Layout is check's types.Layout: size, alignment, field offsets in
	// layout order, tag and variants. Its TypeIDs belong to the session.
	Layout types.Layout
}

// LayoutOf returns check's layout of the type that the root module (the file
// in single-file mode, the package root's module otherwise) declares as
// name: a struct, an enum or a type alias, none generic. It reads the last
// Check and is for tests; use it from the session's goroutine, after Check.
// It never panics: no Check yet, a Check that did not reach the types stage,
// an unknown name, a generic or non-type item, a type that did not check, or
// an internal error computing the layout is an error.
func (s *Session) LayoutOf(name string) (TypeLayout, error) {
	w := s.w
	if w == nil || s.pkgB == nil {
		return TypeLayout{}, fmt.Errorf("LayoutOf(%s): no Check has run", name)
	}
	if w.sem5 == nil || w.tab == nil {
		return TypeLayout{}, fmt.Errorf("LayoutOf(%s): the last Check stopped before the types stage", name)
	}
	var root *modNode
	for _, m := range s.pkgB.mods {
		if m.dir == "" {
			root = m
			break
		}
	}
	if root == nil {
		return TypeLayout{}, fmt.Errorf("LayoutOf(%s): the package root forms no module", name)
	}
	if root.cmod == nil {
		// The module was not checked: a names error in it or below it
		// (pipelineAll's namesClean), or a fault in its stage 3.
		return TypeLayout{}, fmt.Errorf("LayoutOf(%s): the last Check stopped before the types stage in %s", name, root.path)
	}
	id, ok := w.items.ByKey(types.ItemKey(root.path, name))
	if !ok {
		return TypeLayout{}, fmt.Errorf("LayoutOf(%s): module %s declares no %s", name, root.path, name)
	}
	var generics []types.GenericParam
	named := true
	alias := types.NoType
	switch d := w.items.Get(id).Decl.(type) {
	case *types.StructDecl:
		generics = d.Generics
	case *types.EnumDecl:
		generics = d.Generics
	case *types.AliasDecl:
		generics, named, alias = d.Generics, false, d.Type
	default:
		return TypeLayout{}, fmt.Errorf("LayoutOf(%s): %s is not a struct, an enum or a type alias", name, name)
	}
	if len(generics) > 0 {
		return TypeLayout{}, fmt.Errorf("LayoutOf(%s): %s is generic", name, name)
	}
	if !named && alias == types.NoType {
		return TypeLayout{}, fmt.Errorf("LayoutOf(%s): alias %s did not check", name, name)
	}
	var out TypeLayout
	var poison bool
	if ie := source.Guard("layout of "+name, source.Span{}, func() {
		t := alias
		if named {
			t = w.tab.Named(id, nil)
		}
		ty := w.tab.Get(t)
		if ty.Kind == types.KPoison {
			poison = true
			return
		}
		chk := w.sem5.chk
		out = TypeLayout{Kind: ty.Kind, Layout: chk.Layout(t)}
		if ty.Kind == types.KArray {
			out.Elem = chk.Layout(ty.Elem).Shape
			if out.Elem.Size > 0 {
				out.Len = out.Layout.Shape.Size / out.Elem.Size
			}
		}
	}); ie != nil {
		return TypeLayout{}, fmt.Errorf("LayoutOf(%s): %s", name, ie.String())
	}
	if poison {
		return TypeLayout{}, fmt.Errorf("LayoutOf(%s): %s did not check", name, name)
	}
	return out, nil
}
