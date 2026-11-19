// Why: a body never sees what its parameters refer to, so each carrier
// parameter stands for its referents through a few abstract nodes, and
// every loan rule on a parameter works on those nodes.
//
// What: nodeInfo, abstractNodes (p#1, p#2 and p#A of a parameter's type),
// maxReferents, splitAllocators, nodeFlags and allocRefs.
//
// How: p#1 stands for the first layer of referents, p#2 for the second and
// everything reached from it, p#A for the allocators below the first level,
// so a deep node never merges an allocator with what a mut reference or a
// view reaches. The walk stops at ANY on a polymorphically recursive type.

package borrow

import (
	"mk/internal/types"
)

// nodeInfo is one abstract node of a carrier parameter: the types it
// stands for, whether it stands for any type, and whether the loan that
// targets it is exclusive.
type nodeInfo struct {
	types []types.TypeID
	any   bool
	excl  bool
}

func (n *nodeInfo) addType(t types.TypeID) bool {
	for _, o := range n.types {
		if o == t {
			return false
		}
	}
	n.types = append(n.types, t)
	return true
}

// abstractNodes computes p#1, p#2 and p#A for a parameter of type t: p#1's
// types are the layer-1 referents and p.outer is exclusive iff some layer-1
// step is; p#2's types are the layer-2 referents plus, transitively, the
// referents of their refSteps, and p.inner is exclusive iff some layer-2 step
// is (deeper steps do not count). An allocator (Arena, Alloc, Heap) met at
// layer 2 or deeper goes to p#A instead of p#2, so the deep node never merges
// an allocator with what a mut reference or a view reaches. The walk does not
// go on through an allocator: it hands nothing back, so its own steps add
// nothing to p#2. nA has no types when the parameter reaches no allocator
// below its first level.
func (w *walker) abstractNodes(t types.TypeID) (n1, n2, nA nodeInfo) {
	for _, s := range w.refSteps(t).steps {
		n1.excl = n1.excl || s.excl
		if s.any {
			n1.any = true
		} else {
			n1.addType(s.referent)
		}
	}
	// Layer 2: the steps of every layer-1 referent; ANY steps to ANY.
	var work []types.TypeID
	seen := make(map[types.TypeID]bool)
	perItem := make(map[types.ItemID]int)
	addLayer := func(steps []refStep, layer2 bool) {
		for _, s := range steps {
			if layer2 {
				n2.excl = n2.excl || s.excl
			}
			switch {
			case s.any:
				n2.any = true
			case seen[s.referent]:
			case w.grows(s.referent, perItem, len(seen)):
				// A polymorphically recursive type (struct G[T] { next:
				// ?&G[?T] }) reaches a new referent type at every hop;
				// the walk stops at ANY, which stands for all of them.
				n2.any = true
			default:
				seen[s.referent] = true
				if w.factsOf(s.referent).Allocator {
					// An allocator hands nothing back, so
					// nothing is reached through it; its own steps
					// (an Arena's view ANY) stay out of p#2.
					nA.addType(s.referent)
					continue
				}
				n2.addType(s.referent)
				work = append(work, s.referent)
			}
		}
	}
	if n1.any {
		addLayer([]refStep{{excl: true, any: true}}, true)
	}
	for _, u := range n1.types {
		addLayer(w.refSteps(u).steps, true)
	}
	for len(work) > 0 {
		u := work[0]
		work = work[1:]
		addLayer(w.refSteps(u).steps, false)
	}
	return n1, n2, nA
}

// maxReferents bounds the layer-2 walk of abstractNodes: the distinct
// referent types of one constructor, and of all, past which the walk is
// taken to be unbounded (E0708's depth).
const (
	maxReferentsPerItem = 64
	maxReferents        = 4096
)

// grows reports whether the layer-2 walk has met as many referent types of
// t's constructor (its named type under ?, [N] and E!) as it may, or as
// many referents as it may, counting t. seen is the number met so far.
func (w *walker) grows(t types.TypeID, perItem map[types.ItemID]int, seen int) bool {
	if seen >= maxReferents {
		return true
	}
	ty := w.tab.Get(t)
	for ty.Kind == types.KOptional || ty.Kind == types.KArray || ty.Kind == types.KErrorUnion {
		ty = w.tab.Get(ty.Elem)
	}
	if ty.Kind != types.KNamed || ty.Item == types.NoItem {
		return false
	}
	perItem[ty.Item]++
	return perItem[ty.Item] > maxReferentsPerItem
}

// splitAllocators splits p#1 (allocators apart at the first level): when n
// stands for allocators and for something else, the allocators go to a node
// of their own, so the rest is judged without them and they still stop the
// walk on a later hop. A node of allocators alone, or of none, is
// returned unchanged with an empty second node. The layer-2 walk is computed
// before the split and does not change.
func (w *walker) splitAllocators(n nodeInfo) (rest, alloc nodeInfo) {
	rest = nodeInfo{any: n.any, excl: n.excl}
	alloc.excl = n.excl
	for _, t := range n.types {
		if w.factsOf(t).Allocator {
			alloc.addType(t)
		} else {
			rest.addType(t)
		}
	}
	if len(alloc.types) == 0 || len(rest.types) == 0 && !rest.any {
		return n, nodeInfo{}
	}
	return rest, alloc
}

// nodeFlags are the flags of an abstract node: carrier (some type is
// a carrier or ANY), writable (some type is ANY, interior or holds a view),
// view (ANY or holds a view), allocator (the node stands for allocators
// alone: it has types, no ANY, and every type is Arena, Alloc or Heap). A
// node that merges an allocator with anything else is no allocator, so the
// writable walk and the direct write rule pass it.
func (w *walker) nodeFlags(n *nodeInfo) (carrier, writable, view, allocator bool) {
	if n.any {
		carrier, writable, view = true, true, true
	}
	allocator = !n.any && len(n.types) > 0
	for _, t := range n.types {
		f := w.factsOf(t)
		carrier = carrier || f.Carrier
		hv := w.holdsView(t)
		writable = writable || f.Interior || hv
		view = view || hv
		allocator = allocator && f.Allocator
	}
	return
}

// allocRefs reports whether every reference stored directly in t's bytes
// refers to an allocator, and there is one: t is &Arena, ?&Alloc, a record
// of allocator references and the like. A loan read from a field of such a
// type stays with the parameter's allocator node p#A.
func (w *walker) allocRefs(t types.TypeID) bool {
	steps := w.refSteps(t).steps
	if len(steps) == 0 {
		return false
	}
	for _, s := range steps {
		if s.any || !w.factsOf(s.referent).Allocator {
			return false
		}
	}
	return true
}
