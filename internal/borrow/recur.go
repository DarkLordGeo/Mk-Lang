// Why: type walks that follow references into generic instances
// (firstTakes, walkFrom, holdsItem, fnStoresInto) never end on a
// polymorphically recursive type, which builds a new instance at every hop.
//
// What: recur (the bound one walk carries), its exported wrapper Recur for
// walks outside borrow, and the growth test (enter, grown, grewFrom, size,
// occurs).
//
// How: recur counts, per constructor, the growths on the current path; only
// a recursion that feeds its own arguments back grows, so a valid
// non-recursive type never trips the bound. Past the bound a walk answers
// as an ANY step would.

package borrow

import (
	"mk/internal/types"
)

// recur bounds one walk over a polymorphically recursive type. Such a type
// (struct G[T] { next: ?&G[?T] }, or the same through a slice field or an
// enum variant) reaches a new instance of its constructor at every hop, so
// a walk keyed by TypeID alone never ends. A valid type that is not
// recursive must never trip the bound, so recur counts, per constructor,
// the growths on the walk's current path: for a named constructor, an
// instance one of whose type arguments holds, as a proper part, a type
// argument of the last instance of the same constructor above it (G[int]
// then G[?int]); for a record, one larger than every record above it. Only
// a recursion that feeds its own arguments back grows that way, so a long
// path through unrelated types (W[S1], W[?S2], W[S3], ...), a wide type
// (List[A], List[B], ...) or a nest that shrinks (B[B[B[int]]] with a field
// of type T) never grows. A walk stops at an instance past maxRecurGrowth
// growths on its path, or once it has met maxRecurInstances instances of
// one constructor that grew (a backstop for a family whose size the clamp
// in size hides), and answers there as an ANY step would, the side that
// refuses more. abstractNodes keeps its own bound (walker.grows), whose
// walk is breadth first.
type recur struct {
	tab   *types.Table
	path  map[types.ItemID][]recurAt
	met   map[types.ItemID]int
	sizes map[types.TypeID]int
}

// recurAt is one instance on the path: its size, the growths up to it,
// the largest size on the path so far (for records) and the instance.
type recurAt struct {
	size, growth, max int
	t                 types.TypeID
}

const (
	maxRecurGrowth    = 64
	maxRecurInstances = 4096
	maxRecurSize      = 1 << 20
)

func (w *walker) newRecur() *recur { return newRecurOn(w.tab) }

func newRecurOn(tab *types.Table) *recur {
	return &recur{tab: tab, path: map[types.ItemID][]recurAt{}, met: map[types.ItemID]int{}, sizes: map[types.TypeID]int{}}
}

// Recur is recur for a type walk outside borrow (codegen's liveErrors):
// Enter is recur.enter, and the caller answers at a stop
// as its own consumer's conservative side.
type Recur struct{ r *recur }

// NewRecur starts a bound for one walk over tab's types.
func NewRecur(tab *types.Table) *Recur { return &Recur{newRecurOn(tab)} }

// Enter is called as the walk goes into t: stop reports that the walk
// stops at t; otherwise a non-nil leave is called as it returns from t.
func (r *Recur) Enter(t types.TypeID) (stop bool, leave func()) { return r.r.enter(t) }

// Grown is recur.grown: t, just entered, grew from an instance of its
// constructor above it, so a walk may answer there from t's closure.
func (r *Recur) Grown(t types.TypeID) bool { return r.r.grown(t) }

// recordKey is the one constructor recur counts every anonymous record
// under: a walk that follows referents flattened through a record
// (walkFrom's refSteps, `next: ?&(a: G[?T], b: int)`) meets the records,
// never the named instance inside them.
const recordKey = ^types.ItemID(0)

// key is the constructor recur counts t under: a generic named type's
// item, recordKey for a record, after optionals, arrays and error unions
// (as walker.grows strips them); ok is false for anything else.
func (r *recur) key(t types.TypeID) (it types.ItemID, ok bool) {
	ty := r.tab.Get(t)
	for ty.Kind == types.KOptional || ty.Kind == types.KArray || ty.Kind == types.KErrorUnion {
		ty = r.tab.Get(ty.Elem)
	}
	switch {
	case ty.Kind == types.KNamed && ty.Item != types.NoItem && len(ty.Args) > 0:
		return ty.Item, true
	case ty.Kind == types.KRecord:
		return recordKey, true
	}
	return types.NoItem, false
}

// enter is called as the walk goes into t. For a type key does not count
// it returns (false, nil). Otherwise stop reports whether the walk stops at
// t; when it does not, the walk calls leave as it returns from t.
func (r *recur) enter(t types.TypeID) (stop bool, leave func()) {
	it, ok := r.key(t)
	if !ok {
		return false, nil
	}
	at := recurAt{size: r.size(t), t: t}
	at.max = at.size
	if s := r.path[it]; len(s) > 0 {
		top := s[len(s)-1]
		at.growth = top.growth
		at.max = max(top.max, at.size)
		if it == recordKey && at.size > top.max || it != recordKey && r.grewFrom(t, top.t) {
			at.growth++
		}
	}
	if at.growth > maxRecurGrowth {
		return true, nil
	}
	if at.growth > 0 {
		r.met[it]++
		if r.met[it] > maxRecurInstances {
			return true, nil
		}
	}
	r.path[it] = append(r.path[it], at)
	return false, func() { r.path[it] = r.path[it][:len(r.path[it])-1] }
}

// grown reports whether t, entered and not stopped, grew from the last
// instance of its constructor above it on the path (or one above that
// did): the walk is inside a recursion that feeds its arguments back.
func (r *recur) grown(t types.TypeID) bool {
	it, ok := r.key(t)
	if !ok {
		return false
	}
	s := r.path[it]
	return len(s) > 0 && s[len(s)-1].t == t && s[len(s)-1].growth > 0
}

// size is the number of type nodes in t (its elements, type arguments,
// record fields and fn parameters, at any depth), at most maxRecurSize.
func (r *recur) size(t types.TypeID) int {
	if t == types.NoType {
		return 0
	}
	if n, ok := r.sizes[t]; ok {
		return n
	}
	r.sizes[t] = 1
	ty := r.tab.Get(t)
	n := 1
	switch ty.Kind {
	case types.KNamed:
		for _, a := range ty.Args {
			if a.IsConst {
				n++
			} else {
				n += r.size(a.Type)
			}
		}
	case types.KRecord:
		for _, f := range ty.Fields {
			n += r.size(f.Type)
		}
	case types.KFn:
		for _, p := range ty.Fn.Params {
			n += r.size(p.Type)
		}
		n += r.size(ty.Fn.Result)
	default:
		n += r.size(ty.Elem)
	}
	n = min(n, maxRecurSize)
	r.sizes[t] = n
	return n
}

// grewFrom reports whether some type argument of prev, the last instance
// of t's constructor above it on the path, occurs as a proper part of a
// type argument of t (after optionals, arrays and error unions, as key
// strips them): the recursion fed prev's argument back into t's.
func (r *recur) grewFrom(t, prev types.TypeID) bool {
	strip := func(x types.TypeID) *types.Type {
		ty := r.tab.Get(x)
		for ty.Kind == types.KOptional || ty.Kind == types.KArray || ty.Kind == types.KErrorUnion {
			ty = r.tab.Get(ty.Elem)
		}
		return ty
	}
	pt, nt := strip(prev), strip(t)
	for _, a := range pt.Args {
		if a.IsConst {
			continue
		}
		for _, b := range nt.Args {
			if !b.IsConst && b.Type != a.Type && r.occurs(a.Type, b.Type, map[types.TypeID]bool{}) {
				return true
			}
		}
	}
	return false
}

// occurs reports whether x occurs in t: t itself, or in its elements, type
// arguments, record fields or fn parameters and result, at any depth.
func (r *recur) occurs(x, t types.TypeID, seen map[types.TypeID]bool) bool {
	if x == t {
		return true
	}
	if t == types.NoType || seen[t] {
		return false
	}
	seen[t] = true
	ty := r.tab.Get(t)
	switch ty.Kind {
	case types.KNamed:
		for _, a := range ty.Args {
			if !a.IsConst && r.occurs(x, a.Type, seen) {
				return true
			}
		}
	case types.KRecord:
		for _, f := range ty.Fields {
			if r.occurs(x, f.Type, seen) {
				return true
			}
		}
	case types.KFn:
		for _, p := range ty.Fn.Params {
			if r.occurs(x, p.Type, seen) {
				return true
			}
		}
		return r.occurs(x, ty.Fn.Result, seen)
	default:
		return r.occurs(x, ty.Elem, seen)
	}
	return false
}
