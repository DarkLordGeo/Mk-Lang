// Why: TypeString spells a fn type by its parameters, errors and result
// only, so a `*res into res` added to a fn-typed parameter or field (a
// handler type, say) would leave mk apidiff's signature and fields facts
// unchanged. This file makes those flows a fact of their own.
//
// What: sigFnFlows and fieldsFnFlows, which collect the flows of every fn
// type in a signature or a field list, fnTypeFlows, which walks one type,
// and fnFlowSpelling, which spells one fn type's flows.
//
// How: the flows are spelled positionally ("from #2", "*#5 into #5"), as
// E0608 spells them, since a fn type's flows name no parameter.

package driver

import (
	"strconv"
	"strings"

	"mk/internal/types"
)

// sigFnFlows spells the flows of every fn type in s's parameters and result,
// each labelled with prefix and the parameter's name (or "result"), appended
// to out.
func (qx *queryIndex) sigFnFlows(s *types.Signature, prefix string, out []string) []string {
	for i, p := range s.Params {
		out = fnTypeFlows(qx.w.tab, p.Type, prefix+qx.paramName(s, types.ParamIndex(i+1)), out)
	}
	return fnTypeFlows(qx.w.tab, s.Result, prefix+"result", out)
}

// fieldsFnFlows is sigFnFlows for fields, labelled with prefix and the
// field's name. Only exported fields when exportedOnly.
func (qx *queryIndex) fieldsFnFlows(fs []types.FieldDecl, prefix string, exportedOnly bool, out []string) []string {
	for _, f := range fs {
		if exportedOnly && !f.Exported {
			continue
		}
		out = fnTypeFlows(qx.w.tab, f.Type, prefix+qx.str(f.Name), out)
	}
	return out
}

// fnTypeFlows appends "label: flows" for each fn type with flows reachable
// in t's spelling: through references, slices, arrays, optionals, error
// unions, records, generic arguments, and a fn type's own parameters and
// result. A named type's fields are its own item's facts, not walked here.
func fnTypeFlows(tab *types.Table, t types.TypeID, label string, out []string) []string {
	if t == types.NoType {
		return out
	}
	ty := tab.Get(t)
	switch ty.Kind {
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KRawPtr, types.KArray, types.KOptional, types.KErrorUnion:
		return fnTypeFlows(tab, ty.Elem, label, out)
	case types.KRecord:
		for _, f := range ty.Fields {
			out = fnTypeFlows(tab, f.Type, label, out)
		}
	case types.KNamed:
		for _, a := range ty.Args {
			if !a.IsConst {
				out = fnTypeFlows(tab, a.Type, label, out)
			}
		}
	case types.KFn:
		if ty.Fn == nil {
			return out
		}
		if s := fnFlowSpelling(ty.Fn); s != "" {
			out = append(out, label+": "+s)
		}
		for _, p := range ty.Fn.Params {
			out = fnTypeFlows(tab, p.Type, label, out)
		}
		out = fnTypeFlows(tab, ty.Fn.Result, label, out)
	}
	return out
}

// fnFlowSpelling is a fn type's flows positionally ("from #2", "B(#1)",
// "*#5 into #5", "#1 into #2", "ends #1"); "" when it has none.
func fnFlowSpelling(ft *types.FnType) string {
	name := func(p types.ParamIndex) string { return "#" + strconv.Itoa(int(p)) }
	var out []string
	for _, src := range ft.Sources {
		if src.Behind {
			out = append(out, "B("+name(src.Param)+")")
		} else {
			out = append(out, "from "+name(src.Param))
		}
	}
	for _, f := range ft.Fields {
		// A per-field clause, by field index (a fn type names none).
		var from []string
		for _, src := range f.Sources {
			if src.Behind {
				from = append(from, "B("+name(src.Param)+")")
			} else {
				from = append(from, name(src.Param))
			}
		}
		out = append(out, "field "+strconv.Itoa(int(f.Field))+" from "+strings.Join(from, " "))
	}
	for _, st := range ft.Stores {
		out = append(out, "*"+name(st.From)+" into "+name(st.Into))
	}
	for i, p := range ft.Params {
		if p.Into != types.NoParam {
			out = append(out, name(types.ParamIndex(i+1))+" into "+name(p.Into))
		}
	}
	for _, e := range ft.Ends {
		out = append(out, "ends "+name(e))
	}
	return strings.Join(out, ", ")
}
