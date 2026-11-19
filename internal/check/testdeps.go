// Why: a package's test_deps are resolved whenever it is checked, so its
// test blocks type-check, but a library must not come to depend on them.
//
// What: TestDepUses, E0217 test-dep-outside-test for every use of a
// test_deps name outside a `test` block (in a body, a signature, a type or
// a global); the import line itself is allowed.
//
// How: the rule reads resolve's side tables only. Every use of another
// package's module goes through the file's import binding (an import has no
// selective form), which resolve records as a module or item reference on
// the node that names it.

package check

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/types"
)

// TestDepUses is E0217 for each use, outside a test block of info's
// module, of a name that resolves into a module the file imports through
// a test dependency: isTest reports whether an import path's first
// segment is a test_deps name of the module's package. The judgement is
// per file and per import binding, not per module: one package can be
// bound under a deps name in one file and a test_deps name in another,
// and only the test_deps binding's uses are reported. A module a file
// binds both ways is never reported in that file. Each use is reported
// once, at the outermost node that names it.
func TestDepUses(info *resolve.Info, items *types.Items, isTest func(importPath string) bool) []*diag.Diagnostic {
	if info == nil || isTest == nil {
		return nil
	}
	var out []*diag.Diagnostic
	for _, fi := range info.Files {
		if fi == nil || fi.File == nil {
			continue
		}
		test := map[types.ModuleID]string{} // module: the test import path
		plain := map[types.ModuleID]bool{}
		for _, b := range fi.Imports {
			if b.Module == types.NoModule || b.Node == ast.NoNode {
				continue
			}
			if isTest(b.Path) {
				test[b.Module] = b.Path
			} else {
				plain[b.Module] = true
			}
		}
		for m := range plain {
			delete(test, m)
		}
		if len(test) == 0 {
			continue
		}
		for _, it := range fi.File.Items {
			switch it.(type) {
			case *ast.TestItem, *ast.Import:
				continue // a test block, or the import line: allowed
			}
			ast.Inspect(it, func(n ast.Node) bool {
				if n == nil {
					return false
				}
				id := n.ID()
				if id == ast.NoNode || int(id) >= len(fi.Refs) {
					return true
				}
				r := fi.Refs[id]
				var mod types.ModuleID
				switch r.Kind {
				case resolve.RefModule:
					mod = types.ModuleID(r.A)
				case resolve.RefItem:
					if items == nil {
						return true
					}
					mod = items.Get(types.ItemID(r.A)).Module
				default:
					return true
				}
				name, ok := test[mod]
				if !ok || mod == info.Module {
					return true
				}
				out = append(out, diag.New(diag.E0217, n.Span(),
					"module "+name+" comes from a test dependency (test_deps), so its names may be used only inside test blocks").
					Text("module", name).
					Alt("move the use into a test block, or move the dependency from test_deps to deps"))
				return false
			})
		}
	}
	return out
}
