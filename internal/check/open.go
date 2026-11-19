// Why: a library must be able to add enum variants and error-set members
// later without breaking an exhaustive match in another package.
//
// What: openAlias (where `open` may be written), openEnum and openSetOf (is
// a set open to this unit), openScanOf, openName, pathRoot and foreign.
//
// How: an enum or an error-set alias declared `open` may gain members, and
// so may every error set that holds all of an open alias's members, so a
// match in another package counts one more member that no pattern names
// (exhaust.go's future constructor). Two modules are of one package for
// this rule when their import paths share the first segment: std's modules
// are all `std`, a dependency's all start with its name, and a package's
// own start with their top directory or a selected module's first segment;
// anything else is foreign, the safe side.

package check

import (
	"slices"
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// openAlias reports whether an alias written `open` may be: a non-generic
// alias of an error set with no type-parameter member. Any other is E0706
// at the word, and is not open.
func (m *Module) openAlias(x *tctx, n *ast.AliasDecl, d *types.AliasDecl) bool {
	tab := m.c.cfg.Types
	if d.Type == types.Poison {
		return false
	}
	ok := len(d.Generics) == 0 && tab.Kind(d.Type) == types.KErrorSet
	if ok {
		for _, mem := range tab.Members(tab.Get(d.Type).Errors) {
			ok = ok && !mem.IsParam
		}
	}
	if !ok {
		x.errorf(diag.E0706, openWord(x.fx, n.Span().Start), "`open` is written before an enum or a non-generic alias of an error set").
			Text("reason", "open")
		return false
	}
	// The mark is written only in the package that declares every member:
	// error sets are
	// structural, so the alias is its members' set, whose growth only
	// their package can promise.
	items := m.c.cfg.Items
	root := pathRoot(m.mod.Path)
	own := false
	for _, mem := range tab.Members(tab.Get(d.Type).Errors) {
		it := items.Get(mem.Item)
		own = own || it.Module == m.ID
		p := items.Module(it.Module).Path
		if r := pathRoot(p); r != root {
			name := p
			if i := strings.LastIndexByte(p, '.'); i >= 0 {
				name = p[i+1:]
			}
			name += "." + m.c.cfg.Interner.Str(it.Name)
			x.errorf(diag.E0706, openWord(x.fx, n.Span().Start), "`open` is written only in the package that declares every member of the set, and `"+name+"` is of package `"+r+"`").
				Text("reason", "open-foreign")
			return false
		}
	}
	// And only in a module that declares one of them: a reader that holds a
	// value of the set has every member's module in its import closure, so
	// openSetOf reads only those, and no verdict depends on which other modules
	// the closure holds.
	if !own {
		x.errorf(diag.E0706, openWord(x.fx, n.Span().Start), "`open` is written only in a module that declares a member of the set, and this module declares none").
			Text("reason", "open-away")
		return false
	}
	return true
}

// openWord is the word open of an item starting at off: its first
// identifier token, since only export may come before it.
func openWord(fx *fileIndex, off uint32) source.Span {
	for _, t := range fx.file.Tokens {
		if t.Start >= off && t.Kind == ast.TokIdent {
			return source.Span{File: fx.info.FileID, Start: t.Start, End: t.End()}
		}
	}
	return source.Span{File: fx.info.FileID, Start: off, End: off}
}

// pathRoot is a module path's first segment.
func pathRoot(p string) string {
	if i := strings.IndexByte(p, '.'); i >= 0 {
		return p[:i]
	}
	return p
}

// foreign reports whether module id is of another package than the unit's
// module for the open-set rule.
func (u *unit) foreign(id types.ModuleID) bool {
	items := u.c.cfg.Items
	if id == types.NoModule || id == u.m.ID {
		return false
	}
	return pathRoot(items.Module(id).Path) != pathRoot(u.m.mod.Path)
}

// openEnum reports whether t's declaration is an enum written `open` in
// another package.
func (u *unit) openEnum(item types.ItemID) bool {
	it := u.c.cfg.Items.Get(item)
	d, ok := u.c.declOfItem(it).(*types.EnumDecl)
	return ok && d.Open && u.foreign(it.Module)
}

// openSetOf returns an exported open alias all of whose members set holds,
// NoItem when there is none. The mark is written only in the package
// that declares every member, and only in a module that declares one of
// them (openAlias), and error sets are structural, so the set is open when
// a module of another package that declares one of its members has an
// exported open alias whose members it holds: the set's own declaration,
// an open twin of it (`errs.QErr` beside `open type Any = A | B`), or an
// open set it holds among others (`fs.Error | Mine`, std's `Error |
// OutOfMemory` results), whose growth reaches it. With several, the one a
// diagnostic and mk fix's arm name is an alias equal to the set, then the
// one with the most members, then the first by module path and then by name:
// a match on tls.Error, which holds all of net.Error, names
// tls.Error. Every module is read whichever is chosen. Modules of
// the unit's own package open nothing for it, so a set with no member of
// another package is closed with nothing read. A private open alias is no
// promise to another package, which cannot name it.
//
// Only the members' modules are read: an alias whose members the set holds
// is in a module that declares one of them, a member of the set. A module
// that spells the set imports
// each of them, directly or through the module that spells a member, so a
// unit that holds a value of the set has them all among its imports, which
// are final (a module outside them may be in another goroutine's stage
// 5), and the answer is the same whatever else the import
// closure holds. The answer depends on declarations the match need not
// name, so each module read is recorded in the unit's reads, as borrow's
// consumers records its own: its namespace (an alias added, removed or
// renamed) and the signature of every exported alias in it, whatever its
// type (the word written or removed, the type changed, in iface's hash
// form: `type X = int` becoming `open type X = A | B` keeps the name and
// the kind).
func (u *unit) openSetOf(set types.ErrorSetID) types.ItemID {
	items := u.c.cfg.Items
	own := pathRoot(u.m.mod.Path)
	var mods []types.ModuleID
	for _, m := range u.tab.Members(set) {
		if m.IsParam {
			return types.NoItem
		}
		// A module of the unit's own package opens nothing for it: its
		// own matches name every member.
		id := items.Get(m.Item).Module
		if pathRoot(items.Module(id).Path) != own && !slices.Contains(mods, id) {
			mods = append(mods, id)
		}
	}
	slices.SortFunc(mods, func(a, b types.ModuleID) int { return strings.Compare(items.Module(a).Path, items.Module(b).Path) })
	found, size := types.NoItem, 0
	for _, id := range mods {
		if !u.m.deps[id] {
			continue // not final, and no value of the set reaches the unit without it
		}
		sc := u.m.openScanOf(id)
		u.x.rec.exports(types.ModuleKey(items.Module(id).Path))
		for _, a := range sc.aliases {
			u.x.rec.signature(items.Get(a).Key)
		}
		// The most members wins, and the first by module path and name
		// among equals: an alias equal to the set holds them all, so it is
		// chosen first.
		for _, a := range sc.open {
			if n := len(u.tab.Members(a.set)); n > size && u.tab.Subset(a.set, set) {
				found, size = a.item, n
			}
		}
	}
	return found
}

// openAliasSet is one exported alias written open of an error set, and
// that set.
type openAliasSet struct {
	item types.ItemID
	set  types.ErrorSetID
}

// openScan is what openSetOf reads of one module: its exported aliases,
// whatever their type, and those of them written open over an error set,
// by name.
type openScan struct {
	aliases []types.ItemID
	open    []openAliasSet
}

// openScanOf scans module id, one of the module's imports, once per module.
func (m *Module) openScanOf(id types.ModuleID) *openScan {
	m.openMu.Lock()
	defer m.openMu.Unlock()
	if sc, ok := m.open[id]; ok {
		return sc
	}
	items := m.c.cfg.Items
	tab := m.c.cfg.Types
	sc := &openScan{}
	for _, iid := range items.Module(id).Items {
		it := items.Get(iid)
		// Only exported aliases: a private one is no promise to another
		// package, which cannot name it (E0412 keeps it out of exported
		// signatures), and the module prefix's API hash, which a cached
		// importer is keyed on, does not see it.
		if it == nil || it.Kind != types.KindAlias || !it.Exported {
			continue
		}
		sc.aliases = append(sc.aliases, iid)
		d, ok := it.Decl.(*types.AliasDecl)
		if ok && d.Open && d.Type != types.Poison && tab.Kind(d.Type) == types.KErrorSet {
			sc.open = append(sc.open, openAliasSet{item: iid, set: tab.Get(d.Type).Errors})
		}
	}
	slices.SortFunc(sc.open, func(a, b openAliasSet) int {
		return strings.Compare(m.c.cfg.Interner.Str(items.Get(a.item).Name), m.c.cfg.Interner.Str(items.Get(b.item).Name))
	})
	if m.open == nil {
		m.open = map[types.ModuleID]*openScan{}
	}
	m.open[id] = sc
	return sc
}

// openName is how a diagnostic names an open declaration: `fs.Kind`, and a
// prelude name bare, `Os`, as the program writes it. Arch and Os are
// declared in std.target and reach the prelude as std.core's aliases, so
// they are named bare too.
func (u *unit) openName(item types.ItemID) string {
	items := u.c.cfg.Items
	it := items.Get(item)
	k := u.c.known
	if item == k.Arch || item == k.Os || (k.Slice != types.NoItem && items.Get(k.Slice).Module == it.Module) {
		return u.c.cfg.Interner.Str(it.Name)
	}
	p := items.Module(it.Module).Path
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		p = p[i+1:]
	}
	return p + "." + u.c.cfg.Interner.Str(it.Name)
}
