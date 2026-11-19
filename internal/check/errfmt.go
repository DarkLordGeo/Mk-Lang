// Why: every error gets a Format method for free when all its payload
// fields have Format; the provided method has no source body, so stage 5
// must decide whether it exists and lower must get a plan to build it from.
//
// What: errorFormats, keepErrorFormat and dropItems (keep or drop
// resolve's synthesized impl), ErrorFormat and ErrorFormatField (the plan),
// writtenErrorFormat (E0704), errorSetFormat and errorSetMethod (Format on
// an error set), errFmtFacts.
//
// How: after the impl table is built, a synthesized impl is kept when every
// payload field type has Format, with the reads recorded on the error, and
// is otherwise removed from every list; a kept method's unit is a plan, not
// a checked body.

package check

import (
	"slices"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// ErrorFormat is the plan of a provided Format method: the
// text pieces and, between each two, one payload field formatted through
// its own Format. len(Pieces) == len(Fields)+1.
type ErrorFormat struct {
	Error  types.ItemID // the error item
	Pieces []string     // "NotFound(id: ", ")"; one piece "Denied" with no fields
	Fields []ErrorFormatField
	Str    types.ItemID     // std.core Writer.Str
	Errors types.ErrorSetID // WriteError's set: the method's error set
}

// ErrorFormatField is one payload field of an ErrorFormat.
type ErrorFormatField struct {
	Index uint32       // the field's index in the error's fields
	Type  types.TypeID // the field's type
	// Method is the field type's Format impl method, with Args its impl's
	// generic arguments. NoItem when Type is an error set, whose Format is
	// the switch over its members.
	Method types.ItemID
	Args   []types.GenericArg
}

// errFmtState is the stage-5 decision of one error's provided Format.
type errFmtState uint8

const (
	efNone errFmtState = iota
	efBusy
	efKept
	efDropped
)

// synthOf reports whether item id is one of resolve's synthesized items.
func (m *Module) synthOf(id types.ItemID) bool {
	ii := m.byItem[id]
	return ii != nil && ii.Synth != resolve.SynthNone
}

// errFmtItems are the std.core items a provided Format needs, found by
// key; ok is false in a std without them (a test's
// fake prelude), and then nothing is provided.
func (c *Checker) errFmtItems() (writer, writeErr, str types.ItemID, ok bool) {
	k := c.known
	if k.Format == types.NoItem || k.Writer == types.NoItem {
		return
	}
	w := c.cfg.Items.Get(k.Writer)
	mod := c.cfg.Items.Module(w.Module)
	we, ok1 := c.cfg.Items.ByKey(types.ItemKey(mod.Path, "WriteError"))
	st, ok2 := c.cfg.Items.ByKey(types.KeyFromString(w.Key.String() + ":Str"))
	if !ok1 || !ok2 {
		return
	}
	return k.Writer, we, st, true
}

// errorFormats runs after the module's impl table is built: each
// synthesized impl is kept when every payload field type has
// Format (the reads recorded on the error), and dropped from the
// module's lists otherwise. A kept impl is registered as a written one is.
func (m *Module) errorFormats() {
	type pair struct{ impl, meth *types.Item }
	byErr := map[types.ItemID]pair{}
	var order []types.ItemID
	for _, ii := range m.info.Items {
		if ii.Synth != resolve.SynthErrorFormat {
			continue
		}
		it := m.c.cfg.Items.Get(ii.Item)
		e := m.d.at[nodeKey{ii.File, ii.Node}]
		p := byErr[e]
		if it.Kind == types.KindImpl {
			p.impl = it
			order = append(order, e)
		} else {
			p.meth = it
		}
		byErr[e] = p
	}
	if len(order) == 0 {
		return
	}
	m.errFmtGap = map[types.ItemID]int{}
	state := map[types.ItemID]errFmtState{}
	_, _, _, ok := m.c.errFmtItems()
	var decide func(e types.ItemID) bool
	decide = func(e types.ItemID) bool {
		switch state[e] {
		case efKept:
			return true
		case efDropped, efBusy:
			return false
		}
		p, mine := byErr[e]
		if !mine {
			return false
		}
		state[e] = efBusy
		keep := ok && p.impl != nil && p.meth != nil
		eit := m.c.cfg.Items.Get(e)
		ed, _ := eit.Decl.(*types.ErrorDecl)
		if ed == nil {
			keep = false
		}
		if keep {
			env := m.c.NewEnv(nil)
			env.view = m
			rec := m.rec(eit)
			for i, f := range ed.Fields {
				for _, dep := range m.sameModuleErrors(f.Type) {
					decide(dep)
				}
				if f.Type == types.Poison {
					keep = false
					break
				}
				if _, has := m.c.implOf(env, m.c.known.Format, nil, f.Type, rec, 0); !has {
					keep = false
					m.errFmtGap[e] = i
					break
				}
			}
		}
		if keep {
			m.keepErrorFormat(eit, p.impl, p.meth)
			state[e] = efKept
			return true
		}
		state[e] = efDropped
		return false
	}
	drop := map[types.ItemID]bool{}
	for _, e := range order {
		p := byErr[e]
		m.guard(p.impl, func() {
			if !decide(e) {
				drop[p.impl.ID] = true
				if p.meth != nil {
					drop[p.meth.ID] = true
				}
			}
		})
	}
	if len(drop) > 0 {
		m.dropItems(drop)
	}
	// implList is in item order, as Load's rebuild makes it.
	slices.SortStableFunc(m.implList, func(a, b types.ItemID) int { return m.d.order[a] - m.d.order[b] })
	m.gapAlts()
}

// sameModuleErrors are the errors of this module that t names anywhere:
// t itself, an error set's members, and those under a constructor's
// arguments, an element type or a record's fields (W[Z] with a
// conditional impl Format for W[T: Format]). They are decided before t's
// reader.
func (m *Module) sameModuleErrors(t types.TypeID) []types.ItemID {
	tab := m.c.cfg.Types
	var out []types.ItemID
	seen := map[types.TypeID]bool{}
	var walk func(t types.TypeID)
	walk = func(t types.TypeID) {
		if t == types.NoType || t == types.Poison || seen[t] {
			return
		}
		seen[t] = true
		ty := tab.Get(t)
		switch ty.Kind {
		case types.KErrorSet:
			for _, em := range tab.Members(ty.Errors) {
				if it := m.c.cfg.Items.Get(em.Item); !em.IsParam && it.Kind == types.KindError && it.Module == m.ID {
					out = append(out, it.ID)
				}
			}
			return
		case types.KErrorUnion:
			walk(tab.ErrorSetType(ty.Errors))
		}
		walk(ty.Elem)
		for _, a := range ty.Args {
			if !a.IsConst {
				walk(a.Type)
			}
		}
		for _, f := range ty.Fields {
			walk(f.Type)
		}
	}
	walk(t)
	return out
}

// keepErrorFormat fills the provided impl's and method's Decls and registers
// the impl: Format(&self, w: mut Writer): WriteError!void, the
// interface's own signature with Self := E.
func (m *Module) keepErrorFormat(e, impl, meth *types.Item) {
	c := m.c
	ifaceIt := c.cfg.Items.Get(c.known.Format)
	idecl, _ := c.declOfItem(ifaceIt).(*types.InterfaceDecl)
	if idecl == nil || len(idecl.Methods) != 1 {
		source.ICEf(impl.Span, "check: std.core.Format is not an interface of one method")
	}
	d := &types.ImplDecl{Bound: types.Bound{Kind: types.BoundInterface, Item: ifaceIt.ID}, For: errorType(c.cfg.Types, e.ID), Methods: []types.ItemID{meth.ID}}
	impl.Decl = d
	sig := *m.expectedSig(d, idecl, &idecl.Methods[0])
	sig.Item, sig.Name, sig.Span, sig.Exported = meth.ID, meth.Name, meth.Span, meth.Exported
	meth.Decl = &types.FuncDecl{Sig: sig}
	m.d.state[impl.ID], m.d.state[meth.ID] = declDone, declDone
	m.coherence(impl)
}

// dropItems removes dropped synthesized items from the module's lists
// before Units is built: they stay in types.Items,
// in no impl table, member set or unit.
func (m *Module) dropItems(drop map[types.ItemID]bool) {
	keepInfo := m.info.Items[:0]
	for _, ii := range m.info.Items {
		if !drop[ii.Item] {
			keepInfo = append(keepInfo, ii)
		}
	}
	clear(m.info.Items[len(keepInfo):])
	m.info.Items = keepInfo
	keepIDs := m.mod.Items[:0]
	for _, id := range m.mod.Items {
		if !drop[id] {
			keepIDs = append(keepIDs, id)
		}
	}
	m.mod.Items = keepIDs
	for id := range drop {
		delete(m.byItem, id)
		delete(m.d.order, id)
	}
}

// errorFormatPlan builds the provided method's unit: the text pieces and
// each field's Format impl method, by ImplOf.
func (u *unit) errorFormatPlan(n *ast.ErrorDecl) {
	c := u.c
	impl := c.cfg.Items.Get(u.it.Owner)
	d, _ := impl.Decl.(*types.ImplDecl)
	if d == nil {
		source.ICEf(u.it.Span, "check: a provided Format method of no impl")
	}
	ms := u.tab.Members(u.tab.Get(d.For).Errors)
	if len(ms) != 1 || ms[0].IsParam {
		source.ICEf(u.it.Span, "check: a provided Format for %s", u.str(d.For))
	}
	e := c.cfg.Items.Get(ms[0].Item)
	ed := e.Decl.(*types.ErrorDecl)
	_, we, str, ok := c.errFmtItems()
	if !ok {
		source.ICEf(u.it.Span, "check: a provided Format with no std.core Writer")
	}
	plan := &ErrorFormat{Error: e.ID, Str: str, Errors: u.sig.Errors}
	_ = we
	name := c.cfg.Interner.Str(e.Name)
	if len(ed.Fields) == 0 {
		plan.Pieces = []string{name}
	} else {
		piece := name + "("
		for i, f := range ed.Fields {
			if i > 0 {
				piece += ", "
			}
			plan.Pieces = append(plan.Pieces, piece+c.cfg.Interner.Str(f.Name)+": ")
			piece = ""
			ff := ErrorFormatField{Index: uint32(i), Type: f.Type}
			id, has := c.implOf(u.env, c.known.Format, nil, f.Type, u.x.rec, 0)
			if !has {
				source.ICEf(u.it.Span, "check: a provided Format whose field %d has none", i)
			}
			if id != types.NoItem {
				ff.Method = implMethod(c, id, "Format")
				ff.Args, _ = c.ImplArgs(id, f.Type)
			}
			plan.Fields = append(plan.Fields, ff)
		}
		plan.Pieces = append(plan.Pieces, ")")
	}
	u.b.ErrorFormat = plan
	_ = n
}

// errorType is the type of error e's values: the set of e alone.
func errorType(tab *types.Table, e types.ItemID) types.TypeID {
	return tab.ErrorSetType(tab.ErrorSet([]types.ErrorMember{{Item: e}}))
}

// implMethod is impl's method named name, NoItem when it has none.
func implMethod(c *Checker, impl types.ItemID, name string) types.ItemID {
	d, _ := c.cfg.Items.Get(impl).Decl.(*types.ImplDecl)
	if d == nil {
		return types.NoItem
	}
	for _, mid := range d.Methods {
		if c.cfg.Interner.Str(c.cfg.Items.Get(mid).Name) == name {
			return mid
		}
	}
	return types.NoItem
}

// writtenErrorFormat judges a written Format impl: `impl Format for E` in
// E's module has the provided impl's key, so resolve gave it no item; it is
// E0704 at its `impl` keyword, and nothing inside it is checked. It runs
// in implPass, before errorFormats, so whether the provided impl is kept is
// not known yet; the "give the field a Format" alternative is added there.
func (m *Module) writtenErrorFormat(at source.Span, provided types.ItemID) {
	c := m.c
	e := types.NoItem
	if ii := m.byItem[provided]; ii != nil {
		e = m.d.at[nodeKey{ii.File, ii.Node}]
	}
	name := "the error"
	if e != types.NoItem {
		name = c.cfg.Interner.Str(c.cfg.Items.Get(e).Name)
	}
	d := diag.New(diag.E0704, at, "`"+name+"` is an error: its `Format` is the compiler's").
		Text("interface", "Format").Text("type", name).Text("reason", "compiler-provided").
		Alt("remove this impl: an error's `Format` is the compiler's")
	m.Diags = append(m.Diags, d)
	m.writtenFmt = append(m.writtenFmt, writtenFmt{d: d, err: e})
}

// writtenFmt is one written-impl diagnostic, completed by errorFormats.
type writtenFmt struct {
	d   *diag.Diagnostic
	err types.ItemID
}

// gapAlts adds a second alternative to each written impl's E0704 whose
// error's provided Format was dropped: "give the field f a Format".
func (m *Module) gapAlts() {
	for _, w := range m.writtenFmt {
		i, gap := m.errFmtGap[w.err]
		if !gap {
			continue
		}
		ed, _ := m.c.cfg.Items.Get(w.err).Decl.(*types.ErrorDecl)
		if ed == nil || i >= len(ed.Fields) {
			continue
		}
		w.d.Alt("give the field `" + m.c.cfg.Interner.Str(ed.Fields[i].Name) + "` a `Format`")
	}
	m.writtenFmt = nil
}

// errorSetFormat answers Format for an error set: when iface is
// std.core.Format and t is an
// error set of more than one member, or with a parameter member, set is
// true and ok says whether every member has Format (a parameter member by
// its bounds, as Scope[E]'s are), with a read recorded per member. A set
// of one error is that error's type, whose impl is the provided one.
func (c *Checker) errorSetFormat(env *Env, ifaceIt *types.Item, t types.TypeID, rec recorder, depth int) (ok, set bool) {
	tab := c.cfg.Types
	if ifaceIt.ID != c.known.Format || c.known.Format == types.NoItem || t == types.Poison || tab.Kind(t) != types.KErrorSet {
		return false, false
	}
	ms := tab.Members(tab.Get(t).Errors)
	if len(ms) == 0 || len(ms) == 1 && !ms[0].IsParam {
		return false, false
	}
	b := types.Bound{Kind: types.BoundInterface, Item: ifaceIt.ID}
	for _, mem := range ms {
		mt := tab.Param(mem.Param)
		if !mem.IsParam {
			mt = errorType(tab, mem.Item)
		}
		if !c.satisfiesDepth(env, mt, b, rec, depth+1) {
			return false, true
		}
	}
	return true, true
}

// errorSetMethod is the call `e.Format(w)` on an error-set value:
// an interface method call with Self the set, the receiver passed by
// reference as for a bound's method, when every member has Format. ok is
// false for any other call, which the ordinary lookup then reports.
func (u *unit) errorSetMethod(e *ast.CallExpr, fe *ast.FieldExpr, v types.TypeID, n int, idx *ast.IndexExpr, want types.TypeID) (types.TypeID, bool) {
	c := u.c
	f := c.known.Format
	if f == types.NoItem || c.cfg.Interner.Str(fe.Name.Name) != "Format" {
		return types.NoType, false
	}
	it := c.cfg.Items.Get(f)
	if ok, set := c.errorSetFormat(u.env, it, v, u.x.rec, 0); !set || !ok {
		return types.NoType, false
	}
	d, ok := c.declOfItem(it).(*types.InterfaceDecl)
	if !ok || len(d.Methods) != 1 {
		return types.NoType, false
	}
	s := &d.Methods[0]
	u.x.rec.signature(it.Key)
	cs := &callee{kind: UseIfaceMethod, item: f, index: 0, sig: s, self: v, explicit: idx, name: fe.Name, fe: fe}
	cs.recv = fe.X
	cs.recvPassed = u.receiver(fe.X, s.Receiver, v, n)
	return u.applyCall(e, cs, want), true
}

// errFmtFacts adds detail to a failed Format bound: when t is an error,
// or an error set with a
// member, whose provided Format was dropped, d gains the facts `field` and
// `field_type` and its message names the error and the field.
func (c *Checker) errFmtFacts(d *diag.Diagnostic, t types.TypeID) {
	tab := c.cfg.Types
	if d == nil || t == types.Poison || tab.Kind(t) != types.KErrorSet {
		return
	}
	for _, mem := range tab.Members(tab.Get(t).Errors) {
		if mem.IsParam {
			continue
		}
		e := c.cfg.Items.Get(mem.Item)
		ed, _ := e.Decl.(*types.ErrorDecl)
		i, gap := c.errFmtGapOf(e, ed)
		if !gap {
			continue
		}
		f := ed.Fields[i]
		name, fn, ft := c.cfg.Interner.Str(e.Name), c.cfg.Interner.Str(f.Name), tab.TypeString(f.Type, nil)
		d.Message = "`" + name + "` has no `Format`: its field `" + fn + ": " + ft + "` has none"
		d.Text("field", fn).Text("field_type", ft)
		return
	}
}

// errFmtGapOf is the first field of error e lacking Format when e has no
// Format: stage 5's record for a checked module, else (a module loaded from
// the cache, which keeps no such record) asked again of the impl tables.
// The question is a diagnostic's detail, so its reads are not recorded.
func (c *Checker) errFmtGapOf(e *types.Item, ed *types.ErrorDecl) (int, bool) {
	if ed == nil {
		return 0, false
	}
	if om := c.module(e.Module); om != nil && om.errFmtGap != nil {
		if i, ok := om.errFmtGap[e.ID]; ok {
			return i, i < len(ed.Fields)
		}
	}
	none := recorder{rs: &types.ReadSet{}}
	env := c.NewEnv(nil)
	if _, has := c.implOf(env, c.known.Format, nil, errorType(c.cfg.Types, e.ID), none, 0); has {
		return 0, false
	}
	for i, f := range ed.Fields {
		if _, has := c.implOf(env, c.known.Format, nil, f.Type, none, 0); !has {
			return i, true
		}
	}
	return 0, false
}
