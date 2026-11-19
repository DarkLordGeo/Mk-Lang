// Why: every instance body is the generic body with its parameters
// replaced; types, const arguments and error sets all need the same
// substitution, built once and shared.
//
// What: Subst and the substituter (types, const arguments, error-set
// parameters, the Sources of fn types), with ice and iceError, mono's internal
// error.
//
// How: one generic index space per item; composite types are rebuilt and
// interned bottom-up with a per-instance memo, and an error-set parameter
// stands for the members of the set its argument names.

package mono

import (
	"fmt"

	"mk/internal/types"
)

// iceError is the panic value of a mono internal error. mono cannot name
// source (the DAG row lists mir, types, iface, diag), so it cannot raise a
// source.ICE; source.Guard recovers this as an error panic, keeping the
// message.
type iceError struct{ msg string }

func (e *iceError) Error() string { return "internal compiler error: " + e.msg }

// ice raises a mono internal error: a violated invariant of the compiler.
func ice(format string, args ...any) {
	panic(&iceError{msg: "mono: " + fmt.Sprintf(format, args...)})
}

// Subst replaces KParam i by args[i] in t, CAParam i by args[i]'s const
// argument, and an error-set parameter i by the members of the set type
// args[i] names, rebuilding and interning composite types bottom-up.
// It is pure: CASizeOf keeps its (substituted) type, and KSelf is left as it
// is. A KParam or CAParam past args, a type parameter given a const argument
// or a const parameter given a type is an ICE.
func Subst(tt *types.Table, t types.TypeID, args []types.GenericArg) types.TypeID {
	if len(args) == 0 {
		return t
	}
	s := substituter{tab: tt, args: args}
	return s.typ(t)
}

// substituter is one substitution: args for the parameters, self for KSelf
// (NoType keeps it), and fold, when set, turns CASizeOf of a closed type
// into a CAValue. memo caches per type for one instance.
type substituter struct {
	tab  *types.Table
	args []types.GenericArg
	self types.TypeID
	fold func(types.TypeID) types.ConstArg
	memo map[types.TypeID]types.TypeID
	// sliceSelf, set by an instance builder (never by Subst), turns the self
	// type of a Slice[T] namespace function, &Slice[X] or mut Slice[X], into
	// []X or mut []X (see slice.go).
	sliceSelf bool
}

func (s *substituter) typ(t types.TypeID) types.TypeID {
	if t == types.NoType {
		return t
	}
	if r, ok := s.memo[t]; ok {
		return r
	}
	r := s.typ1(t)
	if s.memo != nil {
		s.memo[t] = r
	}
	return r
}

func (s *substituter) typ1(t types.TypeID) types.TypeID {
	tab := s.tab
	ty := tab.Get(t)
	switch ty.Kind {
	case types.KParam:
		if int(ty.Index) >= len(s.args) {
			ice("Subst: type parameter %d of %d arguments", ty.Index, len(s.args))
		}
		a := s.args[ty.Index]
		if a.IsConst {
			ice("Subst: type parameter %d given a const argument", ty.Index)
		}
		return a.Type
	case types.KSelf:
		if s.self != types.NoType {
			return s.self
		}
		return t
	case types.KRef:
		if x, ok := s.ifaceArg(ty.Elem); ok {
			return tab.Slice(s.typ(x))
		}
		return tab.Ref(s.typ(ty.Elem))
	case types.KMut:
		if x, ok := s.ifaceArg(ty.Elem); ok {
			return tab.MutSlice(s.typ(x))
		}
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
		// A negative const length is undecided: a negative argument builds
		// [N]T with N < 0 here, and no rule is invented for it.
		return tab.Array(s.constArg(ty.Len), s.typ(ty.Elem))
	case types.KErrorUnion:
		es := s.errors(ty.Errors)
		if es == types.EmptyErrors {
			ice("Subst: %s substitutes to an empty error set", tab.TypeString(t, nil))
		}
		return tab.ErrorUnion(es, s.typ(ty.Elem))
	case types.KErrorSet:
		es := s.errors(ty.Errors)
		if es == types.EmptyErrors {
			ice("Subst: %s substitutes to an empty error set", tab.TypeString(t, nil))
		}
		return tab.ErrorSetType(es)
	case types.KRecord:
		fs := make([]types.RecordField, len(ty.Fields))
		for i, f := range ty.Fields {
			fs[i] = types.RecordField{Name: f.Name, Type: s.typ(f.Type)}
		}
		return tab.Record(fs)
	case types.KNamed:
		if len(ty.Args) == 0 {
			return t
		}
		return tab.Named(ty.Item, s.genericArgs(ty.Args))
	case types.KFn:
		fn := *ty.Fn
		fn.Params = make([]types.FnParam, len(ty.Fn.Params))
		for i, p := range ty.Fn.Params {
			fn.Params[i] = types.FnParam{Type: s.typ(p.Type), Into: p.Into}
		}
		fn.Result = s.typ(ty.Fn.Result)
		fn.Errors = s.errors(ty.Fn.Errors)
		fn.Sources = s.sources(ty.Fn.Sources)
		if len(ty.Fn.Fields) > 0 {
			// A per-field clause's paths, as Sources'.
			fn.Fields = make([]types.FieldSources, len(ty.Fn.Fields))
			for i, f := range ty.Fn.Fields {
				fn.Fields[i] = types.FieldSources{Field: f.Field, Sources: s.sources(f.Sources)}
			}
		}
		return tab.Fn(fn)
	}
	return t
}

func (s *substituter) sources(ss []types.Source) []types.Source {
	if len(ss) == 0 {
		return ss
	}
	out := make([]types.Source, len(ss))
	for i, src := range ss {
		out[i] = src
		if len(src.Path) > 0 {
			out[i].Path = make([]types.FieldStep, len(src.Path))
			for j, st := range src.Path {
				out[i].Path[j] = types.FieldStep{Field: st.Field, Type: s.typ(st.Type)}
			}
		}
	}
	return out
}

func (s *substituter) genericArgs(as []types.GenericArg) []types.GenericArg {
	out := make([]types.GenericArg, len(as))
	for i, a := range as {
		out[i] = s.genericArg(a)
	}
	return out
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
		if int(a.Param) >= len(s.args) {
			ice("Subst: const parameter %d of %d arguments", a.Param, len(s.args))
		}
		g := s.args[a.Param]
		if !g.IsConst {
			ice("Subst: const parameter %d given a type argument", a.Param)
		}
		if g.Const.Kind == types.CASizeOf && s.fold != nil && closed(s.tab, g.Const.Type) {
			return s.fold(g.Const.Type)
		}
		return g.Const
	case types.CASizeOf:
		a.Type = s.typ(a.Type)
		if s.fold != nil && closed(s.tab, a.Type) {
			return s.fold(a.Type)
		}
	}
	return a
}

// errors substitutes the parameter members of a set: a parameter standing
// for an error set (Scope[E]) is replaced by the members of the set
// type its argument names; an argument that is itself a parameter (a
// substitution into another generic's context) stays a parameter member.
func (s *substituter) errors(es types.ErrorSetID) types.ErrorSetID {
	tab := s.tab
	ms := tab.Members(es)
	changed := false
	for _, m := range ms {
		if m.IsParam {
			changed = true
		}
	}
	if !changed {
		return es
	}
	var out []types.ErrorMember
	for _, m := range ms {
		if !m.IsParam {
			out = append(out, m)
			continue
		}
		if int(m.Param) >= len(s.args) || s.args[m.Param].IsConst {
			ice("Subst: error-set parameter %d of %d arguments", m.Param, len(s.args))
		}
		at := tab.Get(s.args[m.Param].Type)
		switch at.Kind {
		case types.KErrorSet:
			out = append(out, tab.Members(at.Errors)...)
		case types.KParam:
			out = append(out, types.ErrorMember{IsParam: true, Param: at.Index})
		default:
			ice("Subst: error-set parameter %d given a %v", m.Param, at.Kind)
		}
	}
	return tab.ErrorSet(out)
}

// closed reports whether t mentions no KParam, KSelf, KInfer or KPoison at
// any depth, nor a CAParam or a parameter error member.
func closed(tab *types.Table, t types.TypeID) bool {
	if t == types.NoType {
		return true
	}
	ty := tab.Get(t)
	switch ty.Kind {
	case types.KParam, types.KSelf, types.KInfer, types.KPoison:
		return false
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KRawPtr, types.KOptional:
		return closed(tab, ty.Elem)
	case types.KArray:
		return closedConst(tab, ty.Len) && closed(tab, ty.Elem)
	case types.KErrorUnion:
		return closedErrors(tab, ty.Errors) && closed(tab, ty.Elem)
	case types.KErrorSet:
		return closedErrors(tab, ty.Errors)
	case types.KRecord:
		for _, f := range ty.Fields {
			if !closed(tab, f.Type) {
				return false
			}
		}
	case types.KNamed:
		return closedArgs(tab, ty.Args)
	case types.KFn:
		for _, p := range ty.Fn.Params {
			if !closed(tab, p.Type) {
				return false
			}
		}
		return closed(tab, ty.Fn.Result) && closedErrors(tab, ty.Fn.Errors)
	}
	return true
}

func closedArgs(tab *types.Table, as []types.GenericArg) bool {
	for _, a := range as {
		if a.IsConst && !closedConst(tab, a.Const) || !a.IsConst && !closed(tab, a.Type) {
			return false
		}
	}
	return true
}

func closedConst(tab *types.Table, a types.ConstArg) bool {
	switch a.Kind {
	case types.CAParam:
		return false
	case types.CASizeOf:
		return closed(tab, a.Type)
	}
	return true
}

func closedErrors(tab *types.Table, es types.ErrorSetID) bool {
	for _, m := range tab.Members(es) {
		if m.IsParam {
			return false
		}
	}
	return true
}
