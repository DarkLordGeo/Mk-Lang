// Why: a binary pays only for what it names. The support pack's ErrorName
// table would otherwise carry a row, and a name string, for every error std
// declares.
//
// What: liveErrors, the set of errors that can exist as a value of the
// program, and setMembers, which adds an error set's members.
//
// How: the table holds a row for each error that can exist as a value of
// the program. An error value is made only by an AggError
// rvalue of a reached instance body (aggregate.go is codegen's only maker of
// an error code), or held by a constant the evaluator built (a const
// operand, a const item's or argument's value), whose type spells its set,
// a member's payload included; every code ErrorName can be asked about is
// one of these.

package codegen

import (
	"mk/internal/borrow"
	"mk/internal/mir"
	"mk/internal/types"
)

// liveErrors are the error items the unit can make: the item of every
// AggError in a reached body, and every member of an error set a constant
// operand's, const item's or const argument's type holds, at any depth, a
// member's payload fields included (a constant error's payload may hold
// another error).
func (g *packGen) liveErrors() map[types.ItemID]bool {
	live := map[types.ItemID]bool{}
	seen := map[types.TypeID]bool{}
	rc := borrow.NewRecur(g.tt)
	var holds func(t types.TypeID)
	holds = func(t types.TypeID) {
		if t == types.NoType || t == types.Poison || seen[t] {
			return
		}
		seen[t] = true
		// A polymorphically recursive type (`next: ?&G[?T]`) has a new
		// instance at every level: from the first that grew, every error
		// any instance below can hold is one its declarations or
		// arguments name, so the closure of t adds them all and the walk
		// stops there.
		stop, leave := rc.Enter(t)
		if leave != nil {
			defer leave()
		}
		if stop || rc.Grown(t) {
			borrow.TypeClosure(g.tt, g.items, t, func(x types.TypeID, _ bool) bool {
				if k := g.tt.Kind(x); k == types.KErrorSet || k == types.KErrorUnion {
					for _, m := range g.tt.Members(g.tt.Get(x).Errors) {
						if !m.IsParam {
							live[m.Item] = true
						}
					}
				}
				return false
			})
			return
		}
		ty := g.tt.Get(t)
		switch ty.Kind {
		case types.KErrorSet:
			g.setMembers(ty.Errors, live, holds)
		case types.KErrorUnion:
			g.setMembers(ty.Errors, live, holds)
			holds(ty.Elem)
		case types.KOptional, types.KArray, types.KSlice, types.KMutSlice, types.KRef, types.KMut:
			holds(ty.Elem)
		case types.KRecord:
			for _, f := range ty.Fields {
				holds(f.Type)
			}
		case types.KNamed:
			l := g.env.Layout(t)
			if l == nil {
				return
			}
			for _, f := range l.Fields {
				holds(f.Type)
			}
			for _, v := range l.Variants {
				for _, f := range v.Fields {
					holds(f.Type)
				}
			}
		}
	}
	operand := func(o mir.Operand) {
		if o.Kind == mir.OpConst {
			holds(o.Type)
		}
	}
	for i := range g.u.Funcs {
		b := g.u.Funcs[i].Body
		if b == nil {
			continue
		}
		for _, o := range b.Operands {
			operand(o)
		}
		for _, a := range b.Args {
			operand(a.Operand)
		}
		for bi := range b.Blocks {
			for _, st := range b.Blocks[bi].Stmts {
				if st.Kind != mir.StAssign {
					continue
				}
				rv := &st.Rvalue
				switch {
				case rv.Kind == mir.RvAggregate && rv.Agg == mir.AggError:
					live[rv.Item] = true
				case rv.Kind == mir.RvConstRef || rv.Kind == mir.RvGlobalRef || rv.Kind == mir.RvConstArg:
					// The value of a const item or argument, or a reference
					// to a const's storage, which the evaluator built
					// (`Pick(&Boom, i)`, `const Boom: [2]Outer`).
					holds(rv.Type)
				}
				operand(rv.A)
				operand(rv.B)
			}
		}
	}
	for _, gl := range g.u.Globals {
		if d, ok := g.items.Get(gl.Item).Decl.(*types.GlobalDecl); ok {
			holds(d.Type)
		}
	}
	return live
}

// setMembers adds the error items of set to live, and passes the type of
// each member's payload fields to holds (whose seen set ends the walk), also
// for a member a body makes, since a constant of it may carry any payload.
func (g *packGen) setMembers(set types.ErrorSetID, live map[types.ItemID]bool, holds func(types.TypeID)) {
	for _, m := range g.tt.Members(set) {
		if m.IsParam {
			continue
		}
		live[m.Item] = true
		if d, ok := g.items.Get(m.Item).Decl.(*types.ErrorDecl); ok {
			for _, f := range d.Fields {
				holds(f.Type)
			}
		}
	}
}
