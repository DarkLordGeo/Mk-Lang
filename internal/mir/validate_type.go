// Why: an inference variable or a poison type left in MIR means the checker
// let an error through; an instance body that still mentions a generic
// means mono missed a substitution. Both must stop at validation.
//
// What: typeOK, constArgOK and typeFlags.
//
// How: invariant M11, no KInfer or KPoison in any local, projection or
// generic argument at any depth; and, when Body.GenericCount is 0, a closed
// instance: no KParam, KSelf, TypeTest, CallInterface or loop FieldRef.
// typeFlags memoizes what each type contains.

package mir

import "mk/internal/types"

// Type flags found by walking a type to its leaves.
const (
	tyChecked uint8 = 1 << iota
	tyInferOrPoison
	tyGeneric // KParam, KSelf, a CAParam argument or a param error member
)

// typeOK checks that t is a valid ID with no KInfer or KPoison anywhere in it
// (M11) and, in an instance body, no generic part.
func (v *validator) typeOK(t types.TypeID) {
	f := v.typeFlags(t)
	if f&tyInferOrPoison != 0 {
		v.fail("M11", "type %s survived the checker", v.ts(t))
	}
	if f&tyGeneric != 0 && v.b.GenericCount == 0 {
		v.fail("M2", "instance body holds the generic type %s", v.ts(t))
	}
}

// constArgOK checks a const argument's type part the same way.
func (v *validator) constArgOK(c types.ConstArg) {
	switch c.Kind {
	case types.CAParam:
		if v.b.GenericCount == 0 {
			v.fail("M2", "instance body holds const parameter $%d", c.Param)
		}
	case types.CASizeOf:
		v.typeOK(c.Type)
	}
}

// typeFlags walks t once (memoized per TypeID) and returns its flags.
func (v *validator) typeFlags(t types.TypeID) uint8 {
	if t == types.NoType || int(t) >= v.tab.Len() {
		v.fail("M2", "type %d out of range", t)
	}
	if f, ok := v.tyMemo[t]; ok {
		return f
	}
	if v.tyMemo == nil {
		v.tyMemo = make(map[types.TypeID]uint8)
	}
	v.tyMemo[t] = tyChecked // children are interned first, so no cycle
	f := tyChecked
	ty := v.tab.Get(t)
	child := func(c types.TypeID) {
		if c != types.NoType {
			f |= v.typeFlags(c)
		}
	}
	constArg := func(c types.ConstArg) {
		switch c.Kind {
		case types.CAParam:
			f |= tyGeneric
		case types.CASizeOf:
			child(c.Type)
		}
	}
	errs := func(es types.ErrorSetID) {
		for _, m := range v.tab.Members(es) {
			if m.IsParam {
				f |= tyGeneric
			}
		}
	}
	switch ty.Kind {
	case types.KInfer, types.KPoison:
		f |= tyInferOrPoison
	case types.KParam, types.KSelf:
		f |= tyGeneric
	case types.KArray:
		constArg(ty.Len)
		child(ty.Elem)
	case types.KErrorUnion:
		errs(ty.Errors)
		child(ty.Elem)
	case types.KErrorSet:
		errs(ty.Errors)
	case types.KRecord:
		for _, fl := range ty.Fields {
			child(fl.Type)
		}
	case types.KNamed:
		for _, a := range ty.Args {
			if a.IsConst {
				constArg(a.Const)
			} else {
				child(a.Type)
			}
		}
	case types.KFn:
		for _, p := range ty.Fn.Params {
			child(p.Type)
		}
		child(ty.Fn.Result)
		errs(ty.Fn.Errors)
		for _, s := range ty.Fn.Sources {
			for _, st := range s.Path {
				child(st.Type)
			}
		}
		for _, f := range ty.Fn.Fields { // per-field flow clauses
			for _, s := range f.Sources {
				for _, st := range s.Path {
					child(st.Type)
				}
			}
		}
	default:
		child(ty.Elem)
	}
	v.tyMemo[t] = f
	return f
}
