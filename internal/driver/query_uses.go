// Why: two answers need the package's source-level use sites rather than
// its MIR: `mk query uses` (every std item the package references, read by
// tools/dogfood cover) and `mk query callers F` for a std F, since lowering
// expands some calls (Print's among them) into a runner or into
// straight-line calls the call graph hides.
//
// What: UseEntry, Session.pkgUseSites and stdUses, useCallers, stdAliasFolds,
// primMethods and holeFormats (the Format impls a print hole reaches).
//
// How: reads resolve's side tables for every name resolve binds (types in
// signatures included) and the Uses of every checked body for what check
// binds (methods, interface methods, a primitive's methods), through the
// typed bodies lspapi.go re-checks on first use.

package driver

import (
	"cmp"
	"slices"
	"strings"

	"mk/internal/ast"
	checker "mk/internal/check"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// UseEntry is one std item of mk query uses: its key as the profile spells
// a symbol without "mk." and without an instance hash (std.fs.Rename,
// std.fs.File:Close, std.core.str:Trim,
// std.core#impl(std.core.Slice for std.core.List):ItemsMut), and its sites,
// path:line:col, sorted.
type UseEntry struct {
	Item  string   `json:"item"`
	Sites []string `json:"sites"`
}

// useSite is one resolved use in a package module: the item, the unit
// whose body holds it (NoItem for a use resolve binds outside a body, a
// signature's type), the span of the name, and whether the use calls the
// item (it is a call's callee, or the Next a `for x in it` loop calls),
// not only names it (a fn value).
type useSite struct {
	item types.ItemID
	unit types.ItemID
	sp   source.Span
	call bool
}

// pkgUseSites are the resolved uses of the package's modules, each
// (item, span) once, in no particular order.
func (s *Session) pkgUseSites() []useSite {
	mm := s.memo()
	if mm == nil || s.w == nil || s.w.sem5 == nil {
		return nil
	}
	type at struct {
		item types.ItemID
		pos  source.Pos
	}
	seen := map[at]int{}
	var out []useSite
	add := func(item, unit types.ItemID, sp source.Span, call bool) {
		if item == types.NoItem || sp.File == source.NoFile || sp == s.w.items.Get(item).Span {
			return
		}
		k := at{item, sp.StartPos()}
		if i, ok := seen[k]; ok {
			if out[i].unit == types.NoItem {
				out[i].unit = unit // a body's use names its unit
			}
			out[i].call = out[i].call || call
			return
		}
		seen[k] = len(out)
		out = append(out, useSite{item: item, unit: unit, sp: sp, call: call})
	}
	prims := s.primMethods()
	for _, m := range s.pkgMods() {
		if m.info == nil {
			continue
		}
		for _, fi := range m.info.Files {
			nodes := mm.nodesOf(fi.File)
			for nid, r := range fi.Refs {
				if r.Kind != resolve.RefItem || nid >= len(nodes) || nodes[nid] == nil {
					continue
				}
				id := types.ItemID(r.A)
				if sp, ok := useSpan(nodes[nid], s.w.items.Get(id).Name, int(r.Segs)); ok {
					add(id, types.NoItem, sp, false)
				}
			}
		}
		if m.cmod == nil {
			continue
		}
		for _, uid := range s.unitsOf(m) {
			b := s.body(m, uid)
			if b == nil || b.File == nil {
				continue
			}
			unit := b.Item
			nodes := mm.nodesOf(b.File)
			callees := calleeNodes(b.Node)
			for i, u := range b.Uses {
				nid := int(b.Base) + i
				if nid >= len(nodes) || nodes[nid] == nil {
					continue
				}
				n := nodes[nid]
				id := types.NoItem
				switch u.Kind {
				case checker.UseItem, checker.UseMethod, checker.UseConst, checker.UseGlobal,
					checker.UseConstruct, checker.UseError, checker.UseVariant:
					id = u.Item
				case checker.UseField:
					// A field read of a named struct uses the struct
					// (info.is_dir uses fs.Info); a record's
					// field has no item.
					id = u.Item
				case checker.UseIfaceMethod:
					id = s.ifaceMethodItem(u.Item, u.Index)
				case checker.UseBuiltin:
					id = s.builtinItem(b, n, prims)
				}
				if id == types.NoItem {
					continue
				}
				if rs, ok := n.(*ast.RefSource); ok {
					// The Next a `for x in it` loop calls is recorded on the
					// loop's source: its site is the iterated value.
					if rs.X != nil {
						add(id, unit, rs.X.Span(), true)
					}
					continue
				}
				if sp, ok := useSpan(n, s.w.items.Get(id).Name, 0); ok {
					add(id, unit, sp, callees[n] != nil)
				}
				if c := callees[n]; c != nil {
					for _, h := range s.holeFormats(b, id, c) {
						add(h.item, unit, h.sp, true)
					}
				}
			}
		}
	}
	return out
}

// calleeNodes are the nodes under n a use of a called item may sit on,
// each mapped to its call: every call, its callee, and the callee under
// parentheses or generic arguments (F[T](x)).
func calleeNodes(n ast.Node) map[ast.Node]*ast.CallExpr {
	out := map[ast.Node]*ast.CallExpr{}
	ast.Inspect(n, func(m ast.Node) bool {
		c, ok := m.(*ast.CallExpr)
		if !ok || c == nil {
			return m != nil
		}
		out[c] = c
		for f := c.Fun; f != nil; {
			out[f] = c
			switch x := f.(type) {
			case *ast.ParenExpr:
				f = x.X
			case *ast.IndexExpr:
				f = x.X
			default:
				f = nil
			}
		}
		return true
	})
	return out
}

// ifaceMethodItem is method index k of interface iface as an item (its key
// is the interface's plus ":" and the name), else the interface itself.
func (s *Session) ifaceMethodItem(iface types.ItemID, k uint32) types.ItemID {
	it := s.w.items.Get(iface)
	d, ok := it.Decl.(*types.InterfaceDecl)
	if !ok || int(k) >= len(d.Methods) {
		return iface
	}
	if id, ok := s.w.items.ByKey(types.ImplMemberKey(it.Key, s.w.in.Str(d.Methods[k].Name))); ok {
		return id
	}
	return iface
}

// primKey is a primitive's method: its type and name.
type primKey struct {
	prim types.TypeID
	name source.Symbol
}

// primMethods maps each std method of a primitive (str.Len) to its item.
func (s *Session) primMethods() map[primKey]types.ItemID {
	out := map[primKey]types.ItemID{}
	for i := 1; i < s.w.items.NumItems(); i++ {
		it := s.w.items.Get(types.ItemID(i))
		if it == nil || it.OwnerPrim == types.NoType {
			continue
		}
		k := primKey{it.OwnerPrim, it.Name}
		if _, ok := out[k]; !ok {
			out[k] = it.ID
		}
	}
	return out
}

// builtinItem is the std item of a builtin use that is a primitive's
// method call (s.Len(): check binds it to the builtin, not the item), else
// NoItem (Assert, Cast and the other builtins are no item).
func (s *Session) builtinItem(b *checker.Body, n ast.Node, prims map[primKey]types.ItemID) types.ItemID {
	if c, ok := n.(*ast.CallExpr); ok {
		n = c.Fun
	}
	fe, ok := n.(*ast.FieldExpr)
	if !ok || fe.Name == nil || fe.X == nil {
		return types.NoItem
	}
	i := int(fe.X.ID()) - int(b.Base)
	if i < 0 || i >= len(b.Types) {
		return types.NoItem
	}
	t := b.Types[i]
	for k := 0; k < 8 && t != types.NoType && t != types.Poison; k++ {
		if k := s.w.tab.Kind(t); k != types.KRef && k != types.KMut {
			break
		}
		t = s.w.tab.Get(t).Elem
	}
	switch s.w.tab.Kind(t) {
	case types.KSlice, types.KMutSlice, types.KArray:
		// xs.Len() on a []T, mut []T or [N]T is the builtin, and its line
		// is Slice[T].Len.
		if id, ok := s.w.items.ByKey(types.MemberKey("std.core", "Slice", s.w.in.Str(fe.Name.Name))); ok {
			return id
		}
		return types.NoItem
	}
	return prims[primKey{t, fe.Name.Name}]
}

// isStdKey reports whether an item key is std's.
func isStdKey(k string) bool {
	return k == "std" || strings.HasPrefix(k, "std.") || strings.HasPrefix(k, "std#")
}

// stdUses is mk query uses: every std item the package references, by key,
// each with its sites; also the answer's text lines, one per item.
func (s *Session) stdUses() ([]UseEntry, []string) {
	byItem := map[types.ItemID][]source.Span{}
	fold := s.stdAliasFolds()
	for _, u := range s.pkgUseSites() {
		if isStdKey(s.w.items.Get(u.item).Key.String()) {
			id := u.item
			if a, ok := fold[id]; ok {
				id = a
			}
			byItem[id] = append(byItem[id], u.sp)
		}
	}
	out := []UseEntry{}
	for id, sps := range byItem {
		var sites []source.Position
		for _, sp := range sps {
			p := s.w.sm.Position(sp.StartPos())
			p.Path = s.w.sitePath(sp.File) // as every query answer spells a site
			sites = append(sites, p)
		}
		slices.SortFunc(sites, comparePos)
		// A site may name the alias and its target both (a construction
		// through the prelude's TargetInfo); it is one use.
		sites = slices.CompactFunc(sites, func(a, b source.Position) bool { return comparePos(a, b) == 0 })
		e := UseEntry{Item: s.w.items.Get(id).Key.String()}
		for _, p := range sites {
			e.Sites = append(e.Sites, p.Path+":"+itoaQ(int(p.Line))+":"+itoaQ(int(p.Col)))
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b UseEntry) int { return cmp.Compare(a.Item, b.Item) })
	lines := make([]string, 0, len(out))
	for _, e := range out {
		lines = append(lines, e.Item+"  "+strings.Join(e.Sites, ", "))
	}
	if len(lines) == 0 {
		lines = []string{"the package references no std item"}
	}
	return out, lines
}

// stdAliasFolds maps a std-internal declaration that a std alias
// re-exports (stdAliasTarget) to that alias, when no user code can spell
// the declaration itself: std.target's TargetInfo, Arch and Os to the
// prelude's, so a pattern's or a Target field's use, which reaches
// the declaration, is named by the alias, as a use it spells is. A target in the
// prelude module keeps its name: std.core's Duration is the prelude's
// Duration as well as std.time's. Of two aliases of one target the
// prelude's wins, then the smaller key.
func (s *Session) stdAliasFolds() map[types.ItemID]types.ItemID {
	out := map[types.ItemID]types.ItemID{}
	for i := 1; i < s.w.items.NumItems(); i++ {
		id := types.ItemID(i)
		it := s.w.items.Get(id)
		if it.Kind != types.KindAlias {
			continue
		}
		t, ok := stdAliasTarget(s.w, it)
		if !ok || s.w.items.Module(s.w.items.Get(t).Module).Path == preludeModule {
			continue
		}
		if old, ok := out[t]; ok {
			om := s.w.items.Module(s.w.items.Get(old).Module).Path
			nm := s.w.items.Module(it.Module).Path
			if om == preludeModule || nm != preludeModule && s.w.items.Get(old).Key.String() < it.Key.String() {
				continue
			}
		}
		out[t] = id
	}
	return out
}

func comparePos(a, b source.Position) int {
	if c := cmp.Compare(a.Path, b.Path); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Line, b.Line); c != 0 {
		return c
	}
	return cmp.Compare(a.Col, b.Col)
}

// useCallers are the callers of F that the package's checked bodies show:
// the unit of each use of F that is a call's callee, as name and
// path:line, the form callers prints. A use that only names F (a fn value
// passed on) is no call.
func (qx *queryIndex) useCallers(f types.ItemID) []string {
	s := qx.s
	var out []string
	for _, u := range s.pkgUseSites() {
		if u.item != f || u.unit == types.NoItem || !u.call {
			continue
		}
		p := s.w.sm.Position(u.sp.StartPos())
		out = append(out, qx.name(u.unit)+"  "+qx.w.sitePath(u.sp.File)+":"+itoaQ(int(p.Line)))
	}
	return out
}

// holeSite is a Format implementation a format hole calls, and the span of
// the argument that holds the value.
type holeSite struct {
	item types.ItemID
	sp   source.Span
}

// holeFormats are the Format implementations the holes of call c print
// through: c's callee f has a parameter whose type parameter is bounded
// Fields[Format] (Print, System.Print, Arena.Fmt, Logger.Info), and each
// field of the record passed for it whose type has a std impl Format
// (net.Addr's) is a use of that impl's Format, at the argument. A hole
// prints through the value's Format, and nothing in the source names it.
func (s *Session) holeFormats(b *checker.Body, f types.ItemID, c *ast.CallExpr) []holeSite {
	d, ok := s.w.items.Get(f).Decl.(*types.FuncDecl)
	if !ok {
		return nil
	}
	formatsFields := false
	for _, g := range d.Sig.Generics {
		for _, bd := range g.Bounds {
			if bd.Kind == types.BoundFields && bd.Inner == types.BoundInterface && bd.Item != types.NoItem &&
				s.w.items.Get(bd.Item).Key.String() == "std.core.Format" {
				formatsFields = true
			}
		}
	}
	if !formatsFields {
		return nil
	}
	var out []holeSite
	for _, a := range c.Args {
		if a == nil || a.Value == nil {
			continue
		}
		i := int(a.Value.ID()) - int(b.Base)
		if i < 0 || i >= len(b.Types) || s.w.tab.Kind(b.Types[i]) != types.KRecord {
			continue
		}
		for _, fd := range s.w.tab.Get(b.Types[i]).Fields {
			t := fd.Type
			for k := s.w.tab.Kind(t); k == types.KRef || k == types.KMut; k = s.w.tab.Kind(t) {
				t = s.w.tab.Get(t).Elem
			}
			if s.w.tab.Kind(t) != types.KNamed {
				continue
			}
			key := s.w.items.Get(s.w.tab.Get(t).Item).Key.String()
			dot := strings.LastIndexByte(key, '.')
			if dot < 0 || !isStdKey(key) {
				continue
			}
			impl := types.ImplKey(key[:dot], "std.core.Format", key)
			if id, ok := s.w.items.ByKey(types.ImplMemberKey(impl, "Format")); ok {
				out = append(out, holeSite{item: id, sp: a.Value.Span()})
			}
		}
	}
	return out
}
