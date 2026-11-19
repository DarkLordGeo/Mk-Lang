// Why: `for name, value in fields(&x)` runs its body once per field, each
// time with a different value type, so check must type the body per field
// or once over a hidden type parameter, and must bound the cost.
//
// What: fieldsLoop, visibleFields, fieldsGeneric (the generic form over a
// hidden type parameter bounded by Fields[B]), fieldsConcrete (the unroll
// into one Copy per field), the scratch tables window, scratch, scratchAt
// and reset, local, and fieldsNodeCap (the unroll cap, E0708).
//
// How: a concrete loop is checked once per field into scratch tables for
// its nesting depth, and only the loop's own node range is copied out, so a
// copy costs the loop's size, not the unit's. Diagnostics of different
// copies at one node and code are reported once, with fact field.

package check

import (
	"slices"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/types"
)

// fieldsNodeCap is the most nodes a unit may check across its unrolled
// fields-loop copies (1,048,576, chosen, not measured). A
// test lowers it.
var fieldsNodeCap = 1 << 20

// fieldsLoop checks one fields loop. It never diverges.
func (u *unit) fieldsLoop(s *ast.ForStmt, h *ast.InHead, src *ast.FieldsSource) {
	x := src.X
	if un, ok := x.(*ast.UnaryExpr); ok && un.Op == ast.TokAmp {
		x = un.X
	} else {
		u.err(diag.E0301, src.X.Span(), "a fields loop takes a reference: write `fields(&x)`").Text("reason", "fields-of")
		u.expr(src.X, types.NoType)
		return
	}
	xt := u.expr(x, types.NoType)
	n := 0
	for k := u.tab.Kind(xt); k == types.KRef || k == types.KMut; k = u.tab.Kind(xt) {
		xt, n = u.tab.Get(xt).Elem, n+1
	}
	if n > 0 {
		*u.b.adjust(x.ID()) = Adjust{Kind: AdjDeref, Deref: uint8(n)}
	}
	// `&x` is typed as the reference it is, to x's value after the derefs:
	// lower reads that type when x is not a place (`fields(&Make())`), and
	// a Poison there with no diagnostic was its ICE (FuzzNoSilentPoison
	// 9c2443104f774598).
	if xt == types.Poison {
		u.setType(src.X, types.Poison)
	} else {
		u.setType(src.X, u.tab.Ref(xt))
	}
	xt = u.nt(xt)
	switch ty := u.tab.Get(xt); ty.Kind {
	case types.KPoison:
		return
	case types.KParam:
		u.fieldsGeneric(s, h, xt)
	case types.KRecord, types.KNamed:
		names, ftypes, ok := u.visibleFields(xt)
		if !ok {
			u.err(diag.E0301, x.Span(), "a fields loop is over a struct or a record, found `"+u.str(xt)+"`").Text("reason", "fields-of").Text("found", u.str(xt))
			return
		}
		u.fieldsConcrete(s, h, names, ftypes)
	default:
		u.err(diag.E0301, x.Span(), "a fields loop is over a struct or a record, found `"+u.str(xt)+"`").Text("reason", "fields-of").Text("found", u.str(xt))
	}
}

// visibleFields are the fields a fields loop sees: every field of a
// record (sorted order); all fields of a struct of this module outside a
// generic body; its exported fields otherwise.
func (u *unit) visibleFields(t types.TypeID) ([]string, []types.TypeID, bool) {
	ty := u.tab.Get(t)
	var names []string
	var ts []types.TypeID
	switch ty.Kind {
	case types.KRecord:
		for _, f := range ty.Fields {
			names = append(names, u.c.cfg.Interner.Str(f.Name))
			ts = append(ts, f.Type)
		}
		return names, ts, true
	case types.KNamed:
		it := u.c.cfg.Items.Get(ty.Item)
		sd, ok := u.c.declOfItem(it).(*types.StructDecl)
		if !ok {
			return nil, nil, false
		}
		all := it.Module == u.m.ID && len(u.x.gens) == 0
		for _, f := range sd.Fields {
			if all || f.Exported {
				names = append(names, u.c.cfg.Interner.Str(f.Name))
				ts = append(ts, u.c.subst(f.Type, ty.Args, types.NoType))
			}
		}
		u.x.rec.fields(it.Key, all)
		return names, ts, true
	}
	return nil, nil, false
}

// fieldsGeneric is the generic form: x: &A with A: Fields[B]. The body is
// checked once with name: str and value: &F, F a hidden generic of the unit
// (Table.Param(len(Sig.Generics)+k)) bounded by B alone.
func (u *unit) fieldsGeneric(s *ast.ForStmt, h *ast.InHead, a types.TypeID) {
	pi := u.tab.Get(a).Index
	var inner []types.Bound
	found := false
	if int(pi) < len(u.env.gen) {
		for _, b := range u.env.gen[pi].Bounds {
			if b.Kind == types.BoundFields {
				found = true
				if b.Item != types.NoItem {
					inner = append(inner, types.Bound{Kind: types.BoundInterface, Item: b.Item, Args: b.Args})
				}
			}
		}
	}
	if !found {
		if int32(pi)+1 != u.fmtA {
			u.needsBound(s.Span(), a, "a fields loop (bound Fields[B])")
			return
		}
		// One E0705, then the body as under the declared bound, so
		// nothing in it cascades.
		u.argsMisuse(s.Span(), "fields loop over the record")
		inner = append(inner, types.Bound{Kind: types.BoundInterface, Item: u.c.known.Format})
	}
	k := len(u.b.Hidden)
	base := 0
	if u.sig != nil {
		base = len(u.sig.Generics)
	}
	// A head with one name is the parser's E0101 ("a fields loop names the
	// field and its value"); check still walks the recovered tree, so the
	// hidden generic takes the loop's span then (FuzzCheck f1ab159c3ca01cb5).
	hp := types.GenericParam{Bounds: inner, Span: s.Span()}
	if h.Value != nil {
		hp.Name, hp.Span = h.Value.Name, h.Value.Span()
	}
	u.b.Hidden = append(u.b.Hidden, hp)
	gens := append(append([]types.GenericParam(nil), u.env.gen...), make([]types.GenericParam, base+k+1-len(u.env.gen))...)
	gens[base+k] = hp
	u.env.gen = gens
	u.env.narrow = append(u.env.narrow, make([]types.TypeID, len(gens)-len(u.env.narrow))...)
	u.x.gens = gens
	f := u.tab.Param(uint32(base + k))
	u.bindName(h.Key, types.Str)
	u.bindName(h.Value, u.tab.Ref(f))
	if h.Value != nil {
		// A blank value declares no local; its node's type still tells
		// lower this is the generic form over hidden generic f (with none,
		// lower unrolled zero copies and mono saw a hidden generic with no
		// fields loop: an ICE for every `for k, _ in fields(&x)`).
		u.setType(h.Value, u.tab.Ref(f))
	}
	u.loops = append(u.loops, loopCtx{node: s.ID()})
	u.block(s.Body)
	u.loops = u.loops[:len(u.loops)-1]
}

// fieldsConcrete unrolls the loop: for field k the head and body are checked
// with name a const str and value: &F_k, into Copy{Loop, Field: k} over the
// node range from the loop head to the end of its body; the main tables keep
// NoType there. Each copy's bindings are fresh locals, appended in the
// order resolve declared them (Body.Locals is in declaration order, never in
// map order). Diagnostics of different copies at one node and code
// are reported once, from the first copy, with fact field.
//
// Each copy is checked into the unit's scratch tables for its nesting depth,
// allocated once per unit and depth, and only the loop's own node range is
// copied out, so a copy costs the loop's size, not the unit's.
func (u *unit) fieldsConcrete(s *ast.ForStmt, h *ast.InHead, names []string, ftypes []types.TypeID) {
	lo, _ := nodeRange(h)
	_, hi := nodeRange(s.Body)
	size := int(hi-lo) + 1
	if u.unrolled+size*len(names) > fieldsNodeCap {
		u.err(diag.E0708, s.Span(), "this fields loop would unroll past the per-unit limit").
			Text("reason", "fields-unroll").Int("limit", int64(fieldsNodeCap))
		return
	}
	u.unrolled += size * len(names)
	saved := struct {
		types    []types.TypeID
		uses     []Use
		adjust   []Adjust
		consts   []types.ConstID
		bind     []LocalID
		diverges Bits
		diags    []*diag.Diagnostic
		win      window
		parent   int32
	}{u.b.Types, u.b.Uses, u.b.Adjust, u.b.Consts, u.b.Bind, u.b.Diverges, u.b.Diags, u.b.win, u.copyAt}
	type seenKey struct {
		code  diag.Code
		start uint32
	}
	seen := map[seenKey]bool{}
	for i := range saved.diags {
		d := saved.diags[i]
		seen[seenKey{d.Code, d.At.Start}] = true
	}
	// The locals declared inside the loop, in resolve's (declaration) order,
	// with the entries of remap and declLocal they replace in a copy.
	type declared struct {
		rid, oldRemap, oldDecl LocalID
		node                   ast.NodeID
		hadRemap               bool
	}
	var decls []declared
	if u.rb != nil {
		for i := 1; i < len(u.rb.Locals) && i < len(u.b.Locals); i++ {
			if d := u.rb.Locals[i].Decl; d != ast.NoNode && d >= lo && d <= hi {
				r, had := u.remap[LocalID(i)]
				decls = append(decls, declared{rid: LocalID(i), node: d, oldRemap: r, hadRemap: had, oldDecl: u.declLocal[d]})
			}
		}
	}
	nilRemap := u.remap == nil
	if nilRemap && len(decls) > 0 {
		u.remap = map[LocalID]LocalID{}
	}
	sc := u.scratchAt(len(u.b.Types))
	u.copyDepth++
	from, to := int(lo-u.b.Base), int(hi-u.b.Base)+1
	var out []*diag.Diagnostic
	for k := range names {
		u.b.Types, u.b.Uses, u.b.Adjust, u.b.Consts = sc.types, sc.uses, sc.adjust, sc.consts
		u.b.Bind, u.b.Diverges, u.b.Diags = sc.bind, sc.diverges, nil
		u.b.win = window{lo: from, hi: to}
		for _, d := range decls {
			src := u.b.Locals[d.rid]
			fresh := LocalID(len(u.b.Locals))
			u.b.Locals = append(u.b.Locals, Local{Name: src.Name, Type: types.NoType, Var: src.Var, Node: src.Node})
			u.remap[d.rid] = fresh
			u.declLocal[d.node] = fresh
		}
		idx := int32(len(u.b.Unrolled))
		u.b.Unrolled = append(u.b.Unrolled, Copy{Loop: s.ID(), Field: uint32(k), Parent: saved.parent, Base: lo})
		u.copyAt = idx
		u.bindName(h.Key, types.Str)
		if h.Key != nil {
			u.b.setConst(h.Key.ID(), u.c.strConst([]byte(names[k])))
		}
		u.bindName(h.Value, u.tab.Ref(ftypes[k]))
		u.loops = append(u.loops, loopCtx{node: s.ID()})
		u.block(s.Body)
		u.loops = u.loops[:len(u.loops)-1]
		// Tables a copy allocated on first use stay in the scratch.
		sc.uses, sc.adjust, sc.consts = u.b.Uses, u.b.Adjust, u.b.Consts
		c := &u.b.Unrolled[idx]
		c.Types = slices.Clone(sc.types[from:to])
		c.Bind = slices.Clone(sc.bind[from:to])
		if sc.uses != nil {
			c.Uses = slices.Clone(sc.uses[from:to])
		}
		if sc.adjust != nil {
			c.Adjust = slices.Clone(sc.adjust[from:to])
		}
		if sc.consts != nil {
			c.Consts = slices.Clone(sc.consts[from:to])
		}
		c.Diverges = newBits(to - from)
		for id := lo; id <= hi; id++ {
			if sc.diverges.Has(id, u.b.Base) {
				c.Diverges.set(id, lo)
			}
		}
		sc.reset(from, to, u.b.win.out)
		for _, d := range u.b.Diags {
			key := seenKey{d.Code, d.At.Start}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, d.Text("field", names[k]))
		}
	}
	u.copyDepth--
	for _, d := range decls {
		if d.hadRemap {
			u.remap[d.rid] = d.oldRemap
		} else {
			delete(u.remap, d.rid)
		}
		if d.oldDecl != 0 {
			u.declLocal[d.node] = d.oldDecl
		} else {
			delete(u.declLocal, d.node)
		}
	}
	if nilRemap {
		u.remap = nil
	}
	u.b.Types, u.b.Uses, u.b.Adjust, u.b.Consts = saved.types, saved.uses, saved.adjust, saved.consts
	u.b.Bind, u.b.Diverges, u.b.win = saved.bind, saved.diverges, saved.win
	u.b.Diags = append(saved.diags, out...)
	u.copyAt = saved.parent
}

// window is the slot range [lo, hi) of the fields-loop copy being checked
// into scratch tables (hi == 0 outside one); out lists the slots outside it
// that the copy touched, which are cleared after it.
type window struct {
	lo, hi int
	out    []int
}

// scratch is one nesting depth's tables for checking fields-loop copies,
// sized to the unit and all zero between copies.
type scratch struct {
	types    []types.TypeID
	uses     []Use
	adjust   []Adjust
	consts   []types.ConstID
	bind     []LocalID
	diverges Bits
}

// scratchAt is the scratch of the current copy depth over n slots.
func (u *unit) scratchAt(n int) *scratch {
	for len(u.scratch) <= u.copyDepth {
		u.scratch = append(u.scratch, nil)
	}
	sc := u.scratch[u.copyDepth]
	if sc == nil {
		sc = &scratch{types: make([]types.TypeID, n), bind: make([]LocalID, n), diverges: newBits(n)}
		u.scratch[u.copyDepth] = sc
	}
	return sc
}

// reset zeroes the slots [from, to) and every slot in extra.
func (sc *scratch) reset(from, to int, extra []int) {
	clear(sc.types[from:to])
	clear(sc.bind[from:to])
	if sc.uses != nil {
		clear(sc.uses[from:to])
	}
	if sc.adjust != nil {
		clear(sc.adjust[from:to])
	}
	if sc.consts != nil {
		clear(sc.consts[from:to])
	}
	for i := from; i < to; i++ {
		sc.diverges.unset(i)
	}
	for _, i := range extra {
		sc.types[i], sc.bind[i] = types.NoType, 0
		if sc.uses != nil {
			sc.uses[i] = Use{}
		}
		if sc.adjust != nil {
			sc.adjust[i] = Adjust{}
		}
		if sc.consts != nil {
			sc.consts[i] = types.NoConst
		}
		sc.diverges.unset(i)
	}
}

// local maps a resolve local to this unit's local: itself, or inside a
// fields-loop copy its fresh copy.
func (u *unit) local(id LocalID) LocalID {
	if r, ok := u.remap[id]; ok {
		return r
	}
	return id
}
