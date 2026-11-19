// Why: check's type walks that follow references into generic instances
// (contentsInto, hasMutSeen, reachesInterior and privateFieldChanging in
// flows.go) would never end on a polymorphically recursive type.
//
// What: recur, the walk's bound, with enter, leave, key, size, grewFrom and
// occurs.
//
// How: per constructor it counts the growths on the current path (an
// instance whose type argument holds, as a proper part, a type argument of
// the last instance of the same constructor above it) and stops past a
// fixed number of growths or grown instances.

package check

import (
	"mk/internal/types"
)

// recur is internal/borrow's bound (recur.go there; check may not import
// borrow, internal/archtest's DAG) on check's type table, with one
// difference: key counts no anonymous record, so the record rule in enter
// never applies here. It counts, per constructor, the growths on the
// walk's current path (an instance one of whose type arguments holds, as a
// proper part, a type argument of the last instance of the same
// constructor above it) and stops past maxRecurGrowth growths, or past
// maxRecurInstances grown instances of one constructor. check's walks go
// through a record's fields into the named instance inside it, so they
// need no record bucket, and borrow's (records of strictly growing size)
// gave a false E0601 on 67 chained structs (TestRecurKeysMatchBorrow pins
// the difference).
// A type that is not polymorphically recursive never trips it. Each caller
// answers at the stop as its consumer's conservative side (flows.go).
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

func newRecur(tab *types.Table) *recur {
	return &recur{tab: tab, path: map[types.ItemID][]recurAt{}, met: map[types.ItemID]int{}, sizes: map[types.TypeID]int{}}
}

// recordKey is borrow's bucket for anonymous records; check's key never
// returns it (see recur), so enter's record rule is borrow's text only.
const recordKey = ^types.ItemID(0)

// key is the constructor recur counts t under: a generic named type's
// item, after optionals, arrays and error unions (as borrow's
// walker.grows strips them); ok is false for anything else, a record
// included (borrow's key gives a record recordKey).
func (r *recur) key(t types.TypeID) (it types.ItemID, ok bool) {
	ty := r.tab.Get(t)
	for ty.Kind == types.KOptional || ty.Kind == types.KArray || ty.Kind == types.KErrorUnion {
		ty = r.tab.Get(ty.Elem)
	}
	switch {
	case ty.Kind == types.KNamed && ty.Item != types.NoItem && len(ty.Args) > 0:
		return ty.Item, true
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
