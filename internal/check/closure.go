// Why: hasMut gives up at a recursion bound on a polymorphically recursive
// type and answers true, the side that refuses more; the diagnostic should
// then say "recursive-type", not claim a mut that cannot occur.
//
// What: mutInClosure, which sourceFieldReason (flows.go) asks before it
// reports the reason "mut".
//
// How: a walk over the type's nodes and every named type's declared fields
// and payloads as written, each declaration once. Substitution only puts
// argument nodes where parameters were, so every instance's nodes have a
// head visited here.

package check

import (
	"mk/internal/types"
)

// mutInClosure reports whether a mut reference or mut slice can occur in
// any instance a walk from t meets, however deep a polymorphic recursion
// grows: it visits t's own nodes and, for every named type met, its
// declaration's struct fields or enum payload fields as written (a type
// parameter there stands for an argument met elsewhere). Generic
// substitution only puts argument nodes in the places of a declaration's
// parameters, so every node of every such instance has the head of a node
// visited here (borrow's TypeClosure, the same argument). It counts
// private fields too, which can only add a true. A head it does not know
// (an interface, a declaration-less item) counts as true.
func (c *Checker) mutInClosure(t types.TypeID) bool {
	tab := c.cfg.Types
	seen := map[types.TypeID]bool{}
	decls := map[types.ItemID]bool{}
	stack := []types.TypeID{t}
	push := func(x types.TypeID) {
		if x != types.NoType && !seen[x] {
			seen[x] = true
			stack = append(stack, x)
		}
	}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		ty := tab.Get(x)
		switch ty.Kind {
		case types.KMut, types.KMutSlice:
			return true
		case types.KNamed:
			for _, a := range ty.Args {
				if !a.IsConst {
					push(a.Type)
				}
			}
			if ty.Item == types.NoItem {
				return true
			}
			if decls[ty.Item] {
				continue
			}
			decls[ty.Item] = true
			switch d := c.cfg.Items.Get(ty.Item).Decl.(type) {
			case *types.StructDecl:
				for _, f := range d.Fields {
					push(f.Type)
				}
			case *types.EnumDecl:
				for _, v := range d.Variants {
					for _, f := range v.Fields {
						push(f.Type)
					}
				}
			default:
				return true
			}
		case types.KRecord:
			for _, f := range ty.Fields {
				push(f.Type)
			}
		case types.KFn:
			// hasMutSeen does not look into a fn type's parameters.
		default:
			push(ty.Elem)
		}
	}
	return false
}
