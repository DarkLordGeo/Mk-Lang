// Why: a destructuring bind (`(a, b) := e`, `(a: x) := e`, `S(a, ..) := e`)
// must be irrefutable and must not skip a field that needs dropping or
// reach into another module's private fields.
//
// What: destructure, recordPat, structPat, patBind and poisonPat: E0309
// with a proven rename, E0305 for a missing field, E0306 for a refutable
// sub-pattern, E0408 for a non-plain field skipped by `..`, E0204 for
// another module's struct with private fields.
//
// How: the pattern is matched against the value's type field by field; a
// struct pattern takes the struct's fields with the type's arguments
// substituted, and every binding becomes a local of its field's type.

package check

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// dfield is one field a destructure can name: its name and its type.
type dfield struct {
	name     string
	t        types.TypeID
	exported bool
}

// destructure checks a destructuring bind and gives every binding its type.
func (u *unit) destructure(s *ast.DestructureStmt) {
	t := u.expr(s.Value, types.NoType)
	u.recordPat(s.Pat, t)
}

// recordPat matches a record or struct pattern against t.
func (u *unit) recordPat(p *ast.RecordPat, t types.TypeID) {
	if p == nil {
		return
	}
	var fs []dfield
	var syms []types.FieldDecl
	tname := u.str(t)
	ok := t != types.Poison
	if ok && p.Path != nil {
		syms, ok = u.structPat(p.Path, p, t)
		for _, f := range syms {
			fs = append(fs, dfield{name: u.c.cfg.Interner.Str(f.Name), t: f.Type, exported: f.Exported})
		}
	} else if ok {
		if u.tab.Kind(t) != types.KRecord {
			u.err(diag.E0301, s0(p), "a destructure without a type name needs a record, found `"+tname+"`").
				Text("expected", "a record").Text("found", tname)
			ok = false
		} else {
			for _, f := range u.tab.Get(t).Fields {
				fs = append(fs, dfield{name: u.c.cfg.Interner.Str(f.Name), t: f.Type, exported: true})
			}
		}
	}
	if !ok {
		u.poisonPat(p)
		return
	}
	in := u.c.cfg.Interner
	seen := make([]bool, len(fs))
	var unknown []*ast.PatField
	for _, f := range p.Fields {
		if f == nil || f.Name == nil {
			continue
		}
		k := -1
		for i := range fs {
			if fs[i].name == in.Str(f.Name.Name) {
				k = i
			}
		}
		if k < 0 {
			unknown = append(unknown, f)
			u.patBind(f, types.Poison)
			continue
		}
		seen[k] = true
		u.patBind(f, fs[k].t)
	}
	var unmatched []string
	for i, f := range fs {
		if !seen[i] {
			unmatched = append(unmatched, f.name)
		}
	}
	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = f.name
	}
	for _, f := range unknown {
		got := in.Str(f.Name.Name)
		d := u.err(diag.E0309, f.Name.Span(), "`"+tname+"` has no field `"+got+"`").Text("type", tname).Fact("fields", diag.TextList(names...))
		if len(unknown) == 1 && len(unmatched) == 1 {
			d.Fix(diag.Fix{Class: diag.Proven, Edits: []diag.Edit{{Span: f.Name.Span(), Text: unmatched[0]}}, Why: "the one field not named"})
		}
	}
	if len(unknown) > 0 {
		return
	}
	if !p.Rest && len(unmatched) > 0 {
		if u.loopPat {
			// A for head takes no `..` (E0101): bind every field.
			u.err(diag.E0305, s0(p), "the loop variable names every field of `"+tname+"`").Fact("missing", diag.TextList(unmatched...))
			return
		}
		u.err(diag.E0305, s0(p), "the destructure names every field of `"+tname+"`, or ends with `..`").Fact("missing", diag.TextList(unmatched...)).
			Alt("add `..` to skip the rest")
		return
	}
	for i, f := range fs {
		if !seen[i] && !u.c.satisfies(u.env, f.t, types.Bound{Kind: types.BoundPlain}, u.x.rec) {
			u.err(diag.E0408, s0(p), "`..` skips field `"+f.name+"`, which is not Plain").Text("reason", "skipped-field").
				Text("type", u.str(f.t)).Text("field", f.name)
		}
	}
}

// s0 is a pattern's span.
func s0(p *ast.RecordPat) source.Span { return p.Span() }

// structPat checks `S(..)` against t: S must be t's struct (E0301), and a
// struct with private fields is destructured only in its module (E0204).
// It returns S's fields with t's arguments substituted.
func (u *unit) structPat(path *ast.Path, p *ast.RecordPat, t types.TypeID) ([]types.FieldDecl, bool) {
	r := u.ref(path)
	if r.Kind != resolve.RefItem {
		return nil, false
	}
	it := u.c.cfg.Items.Get(types.ItemID(r.A))
	d, isStruct := u.c.declOfItem(it).(*types.StructDecl)
	ty := u.tab.Get(t)
	if !isStruct || ty.Kind != types.KNamed || ty.Item != it.ID {
		u.err(diag.E0301, p.Span(), "the pattern names `"+u.c.cfg.Interner.Str(it.Name)+"`, the value is `"+u.str(t)+"`").
			Text("expected", u.c.cfg.Interner.Str(it.Name)).Text("found", u.str(t))
		return nil, false
	}
	u.x.rec.fields(it.Key, true)
	if it.Module != u.m.ID {
		for _, f := range d.Fields {
			if !f.Exported {
				fn := u.c.cfg.Interner.Str(f.Name)
				u.err(diag.E0204, p.Span(), "`"+u.str(t)+"` has private fields and is destructured only in its module").In(diag.PhaseTypes).
					Text("field", fn).Text("type", u.str(t))
				return nil, false
			}
		}
	}
	out := make([]types.FieldDecl, len(d.Fields))
	for i, f := range d.Fields {
		out[i] = f
		out[i].Type = u.c.subst(f.Type, ty.Args, types.NoType)
	}
	return out, true
}

// patBind binds a destructure field: `a` binds a local of the field's
// name; `a: p` matches p, which must be irrefutable (a binding, `_`, a
// nested struct destructure), else E0306.
func (u *unit) patBind(f *ast.PatField, t types.TypeID) {
	if f.Pat == nil {
		u.bindName(f.Name, t)
		return
	}
	u.irrefutable(f.Pat, t)
}

func (u *unit) irrefutable(p ast.Pattern, t types.TypeID) {
	u.setType(p, t)
	switch p := p.(type) {
	case *ast.NamePattern:
		if r := u.ref(p); r.Kind == resolve.RefLocal || r.Kind == resolve.RefNone {
			u.bindName(p.Name, t)
			return
		}
	case *ast.WildcardPattern:
		return
	case *ast.PathPattern:
		if r := u.ref(p.Path); r.Kind == resolve.RefItem && u.c.cfg.Items.Get(types.ItemID(r.A)).Kind == types.KindStruct && p.HasFields {
			rp := &ast.RecordPat{Path: p.Path, Fields: p.Fields}
			rp.Base = p.Base
			u.recordPat(rp, t)
			return
		}
	case *ast.BadPattern:
		return
	}
	if t != types.Poison {
		u.err(diag.E0306, p.Span(), "a destructure cannot fail: this pattern can").Text("reason", "refutable").Alt("use `match`")
	}
}

// poisonPat gives every binding of a pattern Poison.
func (u *unit) poisonPat(p *ast.RecordPat) {
	for _, f := range p.Fields {
		if f == nil {
			continue
		}
		if f.Pat == nil {
			u.bindName(f.Name, types.Poison)
			continue
		}
		ast.Inspect(f.Pat, func(n ast.Node) bool {
			if np, ok := n.(*ast.NamePattern); ok {
				u.bindName(np.Name, types.Poison)
			}
			return n != nil
		})
	}
}
