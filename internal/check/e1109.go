// Why: a parameter or the result of an export extern function with a body,
// in a package with a library bin (ModuleInput.Library), crosses the
// library boundary to C, so its type must have a C form.
//
// What: cFormBoundary, which reports E1109 no-c-form for each reason
// CFormFunc gives.
//
// How: the set is cform.go's HasCForm, which the driver's header writer
// spells in C. A reference to a type with a C form is a C pointer and has
// one itself (MkPanicHook's &PanicInfo). A vector is E1107 and not E1109
// (vectorABI judges it first), and a private struct keeps E0412.

package check

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/types"
)

// cFormBoundary reports E1109 for each reason CFormFunc gives that sig (an
// export extern in a library package) has no C form: a generic, variadic
// or failing function, a const parameter, or a parameter or result type
// with none (the driver's header writer judges by the same
// predicate, so it has nothing left to leave out).
func (c *Checker) cFormBoundary(x *tctx, sig *types.Signature, s *ast.Signature) {
	tab, items := c.cfg.Types, c.cfg.Items
	skip := func(t types.TypeID) bool { return t == types.Poison || c.holdsVector(t, 0) }
	whole := s.Span()
	if s.Name != nil {
		whole = s.Name.Span()
	}
	misses := CFormFunc(tab, items, sig)
	generic := len(sig.Generics) > 0
	for _, miss := range misses {
		// A generic function's own E1109 covers its parameters and result
		// of a type parameter: one diagnostic, not one per T.
		if generic && (miss.Kind == MissParam || miss.Kind == MissResult) {
			continue
		}
		switch miss.Kind {
		case MissGeneric, MissVariadic:
			at, what := whole, "a generic function"
			if miss.Kind == MissVariadic {
				what = "a variadic function"
				if s.Ellipsis.File.IsValid() {
					at = s.Ellipsis
				}
			} else if s.TypeParams != nil {
				at = s.TypeParams.Span()
			}
			x.errorf(diag.E1109, at, what+" has no C form, and an `export extern` crosses the library boundary").
				Text("param", "signature").Text("type", what).
				Alt("export a non-generic wrapper with fixed parameters").Alt("drop `export` so the function stays inside the library")
		case MissConst, MissParam:
			p := sig.Params[miss.Param]
			if miss.Kind == MissParam && skip(p.Type) {
				continue
			}
			at := p.Span
			if miss.Param < len(s.Params) && s.Params[miss.Param] != nil {
				at = s.Params[miss.Param].Span()
			}
			ts := tab.TypeString(p.Type, nil)
			msg := "`" + ts + "` has no C form, and this parameter of an `export extern` crosses the library boundary"
			if miss.Kind == MissConst {
				msg = "a `const` parameter has no C form (C passes no compile-time argument), and this `export extern` crosses the library boundary"
			}
			x.errorf(diag.E1109, at, msg).
				Text("param", c.cfg.Interner.Str(p.Name)).Text("type", ts).
				Alt("pass a raw pointer, a reference or a C-form struct").Alt("drop `export` so the function stays inside the library")
		case MissErrors, MissResult:
			// A function that can fail returns an error union, which has no
			// C form either.
			rt := tab.ReturnType(sig.Result, sig.Errors)
			if s.Result == nil || skip(sig.Result) {
				continue
			}
			ts := tab.TypeString(rt, nil)
			x.errorf(diag.E1109, s.Result.Span(), "`"+ts+"` has no C form, and this result of an `export extern` crosses the library boundary").
				Text("param", "result").Text("type", ts).
				Alt("return a C-form type, or write the result through a pointer parameter").Alt("drop `export` so the function stays inside the library")
		}
	}
}
