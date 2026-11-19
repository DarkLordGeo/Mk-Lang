// Why: constructing a struct, variant or error names every field once and
// may infer the type's generic arguments; its diagnostics need precise
// spans. It lives apart from expr_call.go to keep both files under the
// line limit.
//
// What: construct (E0305 missing, unknown, repeated or positional fields,
// E0204 for another module's private fields), positional (the proven fix
// for `Point(x, y)`), the span helpers between and tail, identityArgs.
//
// How: fields are matched by name against the declaration with the type's
// arguments substituted; arguments not written are inferred from the
// fields' values and the expected type.

package check

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// construct checks `S(f: e, ..)`, `E.V(..)` or `Err(..)`: every
// field named once (E0305 `missing`, `unknown`, `repeated`, positional,
// payload-free written with parentheses), private fields of another
// module's struct (E0204), generic arguments inferred.
func (u *unit) construct(e *ast.CallExpr, h head, variant int, name ast.Node, want types.TypeID) types.TypeID {
	c := u.c
	if h.it == nil || h.t == types.Poison {
		u.walkArgs(e)
		return types.Poison
	}
	it := h.it
	var fields []types.FieldDecl
	var gens []types.GenericParam
	switch d := c.declOfItem(it).(type) {
	case *types.StructDecl:
		fields, gens = d.Fields, d.Generics
	case *types.ErrorDecl:
		fields = d.Fields
	case *types.EnumDecl:
		if variant < 0 || variant >= len(d.Variants) {
			u.walkArgs(e)
			return types.Poison
		}
		fields, gens = d.Variants[variant].Fields, d.Generics
	default:
		u.walkArgs(e)
		return types.Poison
	}
	u.x.rec.fields(it.Key, true)
	in := u.newInfer(gens, types.NoType)
	if ty := u.tab.Get(h.t); !h.open && ty.Kind == types.KNamed {
		in.preset(0, ty.Args)
		for i := range in.bound {
			in.checked = append(in.checked, in.bound[i])
		}
	}
	var self types.TypeID
	if it.Kind == types.KindError {
		self = h.t
	} else {
		self = u.tab.Named(it.ID, identityArgs(u.tab, gens))
	}
	tname := u.c.cfg.Interner.Str(it.Name)
	if variant >= 0 {
		tname += "." + u.c.cfg.Interner.Str(c.declOfItem(it).(*types.EnumDecl).Variants[variant].Name)
	}
	var missing, unknown, repeated []string
	positional := false
	seen := make([]bool, len(fields))
	for _, a := range e.Args {
		if a == nil || a.Value == nil {
			continue
		}
		if a.Name == nil {
			positional = true
			u.expr(a.Value, types.Poison)
			continue
		}
		k := -1
		for i, f := range fields {
			if f.Name == a.Name.Name {
				k = i
			}
		}
		an := u.c.cfg.Interner.Str(a.Name.Name)
		switch {
		case k < 0:
			unknown = append(unknown, an)
			u.expr(a.Value, types.Poison)
		case seen[k]:
			repeated = append(repeated, an)
			u.expr(a.Value, types.Poison)
		default:
			seen[k] = true
			*u.b.use(a.ID()) = Use{Kind: UseField, Item: it.ID, Index: uint32(k)}
			in.argOrLater(a.Value, fields[k].Type)
		}
	}
	if !positional {
		for i, f := range fields {
			if !seen[i] {
				missing = append(missing, u.c.cfg.Interner.Str(f.Name))
			}
		}
	}
	switch {
	case len(fields) == 0 && it.Kind != types.KindStruct:
		u.err(diag.E0305, e.Span(), "`"+tname+"` has no fields; write it without parentheses").Text("reason", "payload-free").
			Fix(diag.Fix{Class: diag.Proven, Edits: []diag.Edit{{Span: tail(e.Fun.Span(), e.Span()), Text: ""}}, Why: "a payload-free value is written bare"})
	case positional:
		u.positional(e, tname, fields)
	case len(missing) > 0 || len(unknown) > 0 || len(repeated) > 0:
		d := u.err(diag.E0305, e.Span(), "`"+tname+"` names its fields exactly once each")
		if len(missing) > 0 {
			d.Fact("missing", diag.TextList(missing...))
		}
		if len(unknown) > 0 {
			d.Fact("unknown", diag.TextList(unknown...))
		}
		if len(repeated) > 0 {
			d.Text("reason", "repeated").Fact("repeated", diag.TextList(repeated...))
		}
	}
	if it.Kind == types.KindStruct && it.Module != u.m.ID {
		for _, f := range fields {
			if !f.Exported {
				fn := u.c.cfg.Interner.Str(f.Name)
				u.err(diag.E0204, e.Span(), "`"+tname+"` has private fields and cannot be constructed outside its module").In(diag.PhaseTypes).
					Text("field", fn).Text("type", tname)
				break
			}
		}
	}
	if it.Kind == types.KindError {
		*u.b.use(e.ID()) = Use{Kind: UseConstruct, Item: it.ID}
		return self
	}
	if want != types.NoType && want != types.Poison && in.open(in.at(self)) {
		in.unify(in.at(self), want)
	}
	in.lateArgs()
	nameAt := e.Fun.Span()
	if name != nil {
		nameAt = name.Span()
	}
	inst, ok := in.finish(nameAt, e.Span())
	idx := uint32(0)
	if variant >= 0 {
		idx = uint32(variant)
	}
	*u.b.use(e.ID()) = Use{Kind: UseConstruct, Item: it.ID, Index: idx, Inst: inst}
	if !ok {
		return types.Poison
	}
	return in.final(self)
}

// positional is E0305 for `Point(x, y)`: construction names every
// field; the fix is proven when the bare names are the fields in order.
func (u *unit) positional(e *ast.CallExpr, tname string, fields []types.FieldDecl) {
	d := u.err(diag.E0305, e.Span(), "construction names every field: `"+tname+"(f: v, ..)`").Text("reason", "positional")
	if len(e.Args) != len(fields) {
		return
	}
	var edits []diag.Edit
	for i, a := range e.Args {
		id, ok := a.Value.(*ast.Ident)
		if a.Name != nil || !ok || id.Name != fields[i].Name {
			return
		}
		edits = append(edits, diag.Edit{Span: between(id.Span(), id.Span()), Text: u.c.cfg.Interner.Str(id.Name) + ": "})
	}
	d.Fix(diag.Fix{Class: diag.Proven, Edits: edits, Why: "the names are the fields in order"})
}

// between is the span from a's start to b's start: the text before b.
func between(a, b source.Span) source.Span {
	return source.Span{File: a.File, Start: a.Start, End: b.Start}
}

// tail is the span from a's end to b's end: the text after a inside b.
func tail(a, b source.Span) source.Span {
	return source.Span{File: a.File, Start: a.End, End: b.End}
}

// identityArgs are a generic item's own parameters as arguments: the type
// of its values inside its declaration, which infer substitutes.
func identityArgs(tab *types.Table, gens []types.GenericParam) []types.GenericArg {
	out := make([]types.GenericArg, len(gens))
	for i, g := range gens {
		if g.Const {
			out[i] = types.ConstGenericArg(types.ConstArg{Kind: types.CAParam, Param: uint32(i), Type: g.Type})
		} else {
			out[i] = types.TypeArg(tab.Param(uint32(i)))
		}
	}
	return out
}
