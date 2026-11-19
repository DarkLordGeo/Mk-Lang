// Why: a fn value may stand where a fn type is expected only when every flow
// it has is covered by a flow the type declares; check (E0608) and the MIR
// validator must apply the same relation.
//
// What: FlowsCovered, FieldSourcesOf, SameFnShape, FnFlows and
// Table.FnCovers.
//
// How: flows are compared positionally, before substitution; a value source
// on p is covered by the same kind on p or by H(p), since H(p) covers B(p)
// (doc/memory.md, fn types and interfaces). Per-field clauses compare field
// by field.

package types

// FlowsCovered reports whether every flow of a value is covered by a flow the
// target declares, compared positionally before substitution: a value
// source on parameter p is covered by a target source on p of the same kind
// or by H(p), since H(p) covers B(p) (a target B(p) does not cover a value
// H(p)); a store `*x into p`, a parameter's `x into p` and an `ends p` are
// covered only by the same flow in the target. Source paths are not
// compared. valueInto and targetInto are the parameters' Into, positionally.
// It is the relation check's E0608 applies and the MIR validator's M4 and M5
// accept; it decides nothing else.
//
// A per-field clause is compared field by field, a field a clause leaves out
// reading as its union (Sources): the value's union must be covered by the
// target's, which covers a per-field value under a whole target and every
// field the target leaves out; and each field the target names must cover
// the value's sources for that field (its own, or the value's union when the
// value leaves it out or is whole), which covers a whole value under a
// per-field target and two per-field clauses.
func FlowsCovered(value, target Flows, valueInto, targetInto []ParamIndex) bool {
	if !sourcesCovered(value.Sources, target.Sources) {
		return false
	}
	for _, tf := range target.Fields {
		if !sourcesCovered(FieldSourcesOf(value, tf.Field), tf.Sources) {
			return false
		}
	}
	for _, s := range value.Stores {
		ok := false
		for _, t := range target.Stores {
			if t == s {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for i, p := range valueInto {
		if p != NoParam && (i >= len(targetInto) || targetInto[i] != p) {
			return false
		}
	}
	for _, e := range value.Ends {
		ok := false
		for _, t := range target.Ends {
			if t == e {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// sourcesCovered: every value source is covered by a target source on the
// same parameter of the same kind, or by H(p), which covers B(p).
func sourcesCovered(value, target []Source) bool {
	for _, s := range value {
		ok := false
		for _, t := range target {
			if t.Param == s.Param && (!t.Behind || s.Behind) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// FieldSourcesOf is the sources of field f under fl: its entry in a
// per-field clause, else the union, Sources.
func FieldSourcesOf(fl Flows, f uint32) []Source {
	for _, x := range fl.Fields {
		if x.Field == f {
			return x.Sources
		}
	}
	return fl.Sources
}

// SameFnShape reports whether two fn types agree in everything but their
// flows: the same parameter types, result and error set. A parameter's Into
// is a flow, judged by FlowsCovered, so it is not compared here.
func SameFnShape(a, b *FnType) bool {
	if len(a.Params) != len(b.Params) || a.Result != b.Result || a.Errors != b.Errors {
		return false
	}
	for i, p := range a.Params {
		if p.Type != b.Params[i].Type {
			return false
		}
	}
	return true
}

// FnFlows is a fn type's flows as data, with its parameters' Into positionally
// (the arguments FlowsCovered takes).
func FnFlows(ft *FnType) (Flows, []ParamIndex) {
	into := make([]ParamIndex, len(ft.Params))
	for i, p := range ft.Params {
		into[i] = p.Into
	}
	return Flows{Sources: ft.Sources, Fields: ft.Fields, Stores: ft.Stores, Ends: ft.Ends}, into
}

// FnCovers reports whether a fn value of type value may stand where a fn of
// type target is expected (E0608): equal types, or two fn types of the
// same shape (SameFnShape) whose target declares flows covering the value's.
// Only the top level is compared, as check's conversion does: a ?fn or a []fn
// of different flows is a different type.
func (t *Table) FnCovers(value, target TypeID) bool {
	if value == target {
		return true
	}
	v, g := t.Get(value), t.Get(target)
	if v.Kind != KFn || g.Kind != KFn || !SameFnShape(v.Fn, g.Fn) {
		return false
	}
	vf, vi := FnFlows(v.Fn)
	tf, ti := FnFlows(g.Fn)
	return FlowsCovered(vf, tf, vi, ti)
}
