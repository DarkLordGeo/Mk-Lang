// Why: a result that is a struct or record may carry loans from different
// sources in different fields; a per-field clause `from (f: a, g: b.c)`
// says which, so borrow can split the result instead of tying every field
// to every source.
//
// What: splitShape (the struct or record a clause names fields of, through
// ?T and E!T), perFieldFlows (stage 5: E0203 for an unknown field or a
// result with no fields, E0206 for a field named twice, E0607 for a field
// that holds no loans), fieldFlowText and fieldClauseText (spelling).
//
// How: each field's sources are read as a flat clause's (fromSource). The
// result is types.Flows.Fields, sorted by field, with Flows.Sources the
// union in that order, so two spellings that differ in clause order are one
// signature. A view result is split as a struct is.

package check

import (
	"slices"
	"strings"

	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// splitShape is the struct or record a per-field clause names fields of:
// the result through ?T and E!T (before substitution). names and decl are
// its fields' names and declared types (a struct's as written, so a type
// parameter stays a parameter), private marks a struct field that is not
// exported. ok is false for any other type.
type splitShape struct {
	t       types.TypeID
	names   []source.Symbol
	decl    []types.TypeID
	private []bool
	module  types.ModuleID
}

func (c *Checker) splitShapeOf(r types.TypeID) (splitShape, bool) {
	tab := c.cfg.Types
	for tab.Kind(r) == types.KOptional || tab.Kind(r) == types.KErrorUnion {
		r = tab.Get(r).Elem
	}
	sh := splitShape{t: r}
	ty := tab.Get(r)
	switch ty.Kind {
	case types.KNamed:
		it := c.cfg.Items.Get(ty.Item)
		d, ok := it.Decl.(*types.StructDecl)
		if !ok {
			return sh, false
		}
		sh.module = it.Module
		for _, f := range d.Fields {
			sh.names = append(sh.names, f.Name)
			sh.decl = append(sh.decl, f.Type)
			sh.private = append(sh.private, !f.Exported)
		}
		return sh, true
	case types.KRecord:
		for _, f := range ty.Fields {
			sh.names = append(sh.names, f.Name)
			sh.decl = append(sh.decl, f.Type)
			sh.private = append(sh.private, false)
		}
		return sh, true
	}
	return sh, false
}

// perFieldFlows is stage 5 for a per-field clause: it fills fl.Fields and
// fl.Sources (the union) and returns ds with the clause's diagnostics; any
// diagnostic marks fl.ClauseError, so borrow skips the body's boundary
// check as it does for E0607.
func (c *Checker) perFieldFlows(fn flowFn, p *preds, fields []fieldSrcClause, fl *types.Flows, ds []*diag.Diagnostic) []*diag.Diagnostic {
	sig := fn.sig
	tab := c.cfg.Types
	in := c.cfg.Interner
	sh, ok := c.splitShapeOf(sig.Result)
	if !ok {
		// A result that is no struct or record has no field to name.
		for _, f := range fields {
			name := in.Str(f.name)
			ds = append(ds, diag.New(diag.E0203, f.at, "no field `"+name+"` in "+tab.TypeString(sig.Result, sig.Generics)+
				": a per-field `from` names fields of a struct or record result").
				Text("type", tab.TypeString(sig.Result, sig.Generics)).Fact("members", diag.TextList()))
		}
		fl.ClauseError = true
		return ds
	}
	// A view result takes a per-field clause as a struct does: what it
	// keeps outside its fields is no field and
	// cannot be named, so borrow gives it every source of the clause
	// (internal/borrow fields.go shapeOf, resultFV).
	var names []string // the fields this module may name
	for i, n := range sh.names {
		if !sh.private[i] || sh.module == fn.mod {
			names = append(names, in.Str(n))
		}
	}
	seen := map[int]bool{}
	var out []types.FieldSources
	for _, f := range fields {
		name := in.Str(f.name)
		idx := slices.Index(sh.names, f.name)
		if idx >= 0 && sh.private[idx] && sh.module != fn.mod {
			idx = -1 // a private field is named only by its declaring module
		}
		if idx < 0 {
			ds = append(ds, diag.New(diag.E0203, f.at, "no field `"+name+"` in "+tab.TypeString(sh.t, sig.Generics)).
				Text("type", tab.TypeString(sh.t, sig.Generics)).Fact("members", diag.TextList(names...)))
			fl.ClauseError = true
			continue
		}
		if seen[idx] {
			ds = append(ds, diag.New(diag.E0206, f.at, "field `"+name+"` is named twice in this `from` clause").
				Text("name", name).In(diag.PhaseTypes)) // stage 5, as decl.go's discriminants
			fl.ClauseError = true
			continue
		}
		seen[idx] = true
		if !c.isCarrier(sh.decl[idx]) {
			// Loan-holding is the field's type as written, a carrier
			// or a type parameter.
			ds = append(ds, diag.New(diag.E0607, f.at, "this `from` clause names `"+name+"`, which holds no loans").
				Text("field", name).Text("reason", "no-loans"))
			fl.ClauseError = true
			continue
		}
		var srcs []types.Source
		for _, fc := range f.from {
			src, d := c.fromSource(fn, fc)
			if d != nil {
				ds = append(ds, d)
				fl.ClauseError = true
				continue
			}
			if !p.carrier(sig.Result) || !p.carrier(sig.Param(fc.param).Type) {
				continue // accepted with no effect
			}
			srcs = append(srcs, src)
		}
		out = append(out, types.FieldSources{Field: uint32(idx), Sources: srcs})
	}
	// Sorted by field, and the union in that order, so two spellings
	// that differ in clause order are one signature and one fn type.
	slices.SortFunc(out, func(a, b types.FieldSources) int { return int(a.Field) - int(b.Field) })
	for _, f := range out {
		for _, src := range f.Sources {
			if !slices.ContainsFunc(fl.Sources, func(s types.Source) bool { return sameSource(s, src) }) {
				fl.Sources = append(fl.Sources, src)
			}
		}
	}
	fl.Fields = out
	return ds
}

func sameSource(a, b types.Source) bool {
	return a.Param == b.Param && a.Behind == b.Behind && slices.Equal(a.Path, b.Path)
}

// fieldFlowText spells a per-field clause as written: `from (f: a, g: b.c)`
// with each field's sources by sourceName.
func (c *Checker) fieldFlowText(s *types.Signature, sourceName func(types.Source) string) string {
	return c.fieldClauseText(s.Result, s.Flows.Fields, sourceName)
}

// fieldClauseText spells the per-field clause fields of a result of type
// result: `from (f: a, g: b)`, each field by its name in result's split
// shape and its sources by sourceName. E0608's expected and found facts
// use it too, for fn values and impl methods.
func (c *Checker) fieldClauseText(result types.TypeID, fields []types.FieldSources, sourceName func(types.Source) string) string {
	sh, ok := c.splitShapeOf(result)
	var parts []string
	for _, f := range fields {
		n := "?"
		if ok && int(f.Field) < len(sh.names) {
			n = c.cfg.Interner.Str(sh.names[f.Field])
		}
		var srcs []string
		for _, src := range f.Sources {
			srcs = append(srcs, sourceName(src))
		}
		parts = append(parts, n+": "+strings.Join(srcs, ", "))
	}
	return "from (" + strings.Join(parts, ", ") + ")"
}
