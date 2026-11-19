// Why: generic signatures are written over an item's parameters; each use
// needs them with its own type arguments filled in.
//
// What: closed (does a type mention no parameter, Self or inference
// variable) and subst, which replaces parameters by arguments, with errors
// for error-set parameters.
//
// How: a structural walk over elements, arguments, record fields, fn
// parameters and results, const arguments and error sets. A parameter index
// past the arguments stays: a method's own parameters follow its owner's.

package check

import (
	"mk/internal/source"
	"mk/internal/types"
)

// closed reports whether t mentions no KParam, KSelf or KInfer, at any
// depth: in elements, arguments, record fields, fn parameters and results,
// const arguments and error sets.
func (c *Checker) closed(t types.TypeID) bool {
	tab := c.cfg.Types
	ty := tab.Get(t)
	switch ty.Kind {
	case types.KParam, types.KSelf, types.KInfer:
		return false
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KRawPtr, types.KOptional:
		return c.closed(ty.Elem)
	case types.KArray:
		return c.closedConst(ty.Len) && c.closed(ty.Elem)
	case types.KErrorUnion:
		return c.closedErrors(ty.Errors) && c.closed(ty.Elem)
	case types.KErrorSet:
		return c.closedErrors(ty.Errors)
	case types.KRecord:
		for _, f := range ty.Fields {
			if !c.closed(f.Type) {
				return false
			}
		}
	case types.KNamed:
		for _, a := range ty.Args {
			if a.IsConst && !c.closedConst(a.Const) || !a.IsConst && !c.closed(a.Type) {
				return false
			}
		}
	case types.KFn:
		for _, p := range ty.Fn.Params {
			if !c.closed(p.Type) {
				return false
			}
		}
		return c.closed(ty.Fn.Result) && c.closedErrors(ty.Fn.Errors)
	}
	return true
}

func (c *Checker) closedConst(a types.ConstArg) bool {
	switch a.Kind {
	case types.CAParam:
		return false
	case types.CASizeOf:
		return c.closed(a.Type)
	}
	return true
}

func (c *Checker) closedErrors(es types.ErrorSetID) bool {
	for _, m := range c.cfg.Types.Members(es) {
		if m.IsParam {
			return false
		}
	}
	return true
}

// subst replaces KParam i by args[i] in t (and CAParam i in const
// arguments, and an error-set parameter i by the members of the set args[i]
// names). self, when not NoType, replaces KSelf. A parameter index past args
// stays as it is: a method's own parameters after its owner's.
func (c *Checker) subst(t types.TypeID, args []types.GenericArg, self types.TypeID) types.TypeID {
	if len(args) == 0 && self == types.NoType {
		return t
	}
	s := substituter{c: c, args: args, self: self}
	return s.typ(t)
}

type substituter struct {
	c    *Checker
	args []types.GenericArg
	self types.TypeID
	// lenient drops an error-set parameter whose argument is not a set and
	// gives a const parameter given a type the value 0 (placeholder
	// arguments of shapeterm.go).
	lenient bool
}

func (s *substituter) typ(t types.TypeID) types.TypeID {
	tab := s.c.cfg.Types
	ty := tab.Get(t)
	switch ty.Kind {
	case types.KParam:
		if int(ty.Index) < len(s.args) {
			a := s.args[ty.Index]
			if a.IsConst {
				source.ICEf(source.Span{}, "check.subst: type parameter %d given a const argument", ty.Index)
			}
			return a.Type
		}
		return t
	case types.KSelf:
		if s.self != types.NoType {
			return s.self
		}
		return t
	case types.KRef:
		return tab.Ref(s.typ(ty.Elem))
	case types.KMut:
		return tab.Mut(s.typ(ty.Elem))
	case types.KSlice:
		return tab.Slice(s.typ(ty.Elem))
	case types.KMutSlice:
		return tab.MutSlice(s.typ(ty.Elem))
	case types.KRawPtr:
		return tab.RawPtr(s.typ(ty.Elem))
	case types.KOptional:
		return tab.Optional(s.typ(ty.Elem))
	case types.KArray:
		return tab.Array(s.constArg(ty.Len), s.typ(ty.Elem))
	case types.KErrorUnion:
		es := s.errors(ty.Errors)
		if es == types.EmptyErrors {
			return types.Poison // every member was a Poison or dropped parameter
		}
		return tab.ErrorUnion(es, s.typ(ty.Elem))
	case types.KErrorSet:
		es := s.errors(ty.Errors)
		if es == types.EmptyErrors {
			return types.Poison // every member was a Poison or dropped parameter
		}
		return tab.ErrorSetType(es)
	case types.KRecord:
		fs := make([]types.RecordField, len(ty.Fields))
		for i, f := range ty.Fields {
			fs[i] = types.RecordField{Name: f.Name, Type: s.typ(f.Type)}
		}
		return tab.Record(fs)
	case types.KNamed:
		as := make([]types.GenericArg, len(ty.Args))
		for i, a := range ty.Args {
			as[i] = s.genericArg(a)
		}
		return tab.Named(ty.Item, as)
	case types.KFn:
		fn := *ty.Fn
		fn.Params = make([]types.FnParam, len(ty.Fn.Params))
		for i, p := range ty.Fn.Params {
			fn.Params[i] = types.FnParam{Type: s.typ(p.Type), Into: p.Into}
		}
		fn.Result = s.typ(ty.Fn.Result)
		fn.Errors = s.errors(ty.Fn.Errors)
		return tab.Fn(fn)
	}
	return t
}

func (s *substituter) genericArg(a types.GenericArg) types.GenericArg {
	if a.IsConst {
		return types.ConstGenericArg(s.constArg(a.Const))
	}
	return types.TypeArg(s.typ(a.Type))
}

func (s *substituter) constArg(a types.ConstArg) types.ConstArg {
	switch a.Kind {
	case types.CAParam:
		if int(a.Param) < len(s.args) {
			g := s.args[a.Param]
			if s.lenient && !g.IsConst {
				return s.c.intArg(0)
			}
			if !g.IsConst {
				source.ICEf(source.Span{}, "check.subst: const parameter %d given a type argument", a.Param)
			}
			return g.Const
		}
	case types.CASizeOf:
		a.Type = s.typ(a.Type)
	}
	return a
}

// errors substitutes the parameter members of a set: a parameter standing
// for an error set (Scope[E]) is replaced by the members of the set
// type its argument names.
func (s *substituter) errors(es types.ErrorSetID) types.ErrorSetID {
	tab := s.c.cfg.Types
	ms := tab.Members(es)
	changed := false
	for _, m := range ms {
		if m.IsParam && int(m.Param) < len(s.args) {
			changed = true
		}
	}
	if !changed {
		return es
	}
	var out []types.ErrorMember
	for _, m := range ms {
		if !m.IsParam || int(m.Param) >= len(s.args) {
			out = append(out, m)
			continue
		}
		at := tab.Get(s.args[m.Param].Type)
		switch at.Kind {
		case types.KErrorSet:
			out = append(out, tab.Members(at.Errors)...)
		case types.KParam:
			out = append(out, types.ErrorMember{IsParam: true, Param: at.Index})
		case types.KPoison:
		default:
			if s.lenient {
				continue
			}
			source.ICEf(source.Span{}, "check.subst: error-set parameter %d given %s", m.Param, at.Kind)
		}
	}
	return tab.ErrorSet(out)
}
