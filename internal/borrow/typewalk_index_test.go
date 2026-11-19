// Why: the view scans read an impl index that used to be built per body;
// sharing it must give the same answer and not keep modules alive.
// What: tests of typewalk_view.go's implsFor index, with the implWorld
// fixture: equal to the per-body build, built once per (view module, body
// module) pair, no pinned modules, and a nil Env.
// How: the per-body order is modules breadth first from the first scan's
// view module, then the body's, items in module order.

package borrow

import (
	"reflect"
	"runtime"
	"slices"
	"testing"
	"weak"

	"mk/internal/source"
	"mk/internal/types"
)

// implWorld is four modules: std (a view type, impls for it and for a
// reference to it, an impl for a non-view), a (imports std; impls for std's
// view and its own generic view), b (imports a), c (imports b and std), and
// d (imported by nobody, an impl for a's view).
type implWorld struct {
	in    *source.Interner
	items *types.Items
	tab   *types.Table
	mods  map[string]*types.Module
	views map[string]types.ItemID
	n     int
	// cache is the session's Env.Impls, shared by every walker.
	cache *ImplCache
}

func newImplWorld(t *testing.T) *implWorld {
	t.Helper()
	in := source.NewInterner()
	items := types.NewItems()
	w := &implWorld{in: in, items: items, tab: types.NewTable(in, items, types.NewConstTable()),
		mods: map[string]*types.Module{}, views: map[string]types.ItemID{}}
	std := w.module("std.core", true)
	a := w.module("a", false, std)
	b := w.module("b", false, a)
	c := w.module("c", false, b, std)
	d := w.module("d", false, a)
	sv := w.view(std, "SV", 0)
	w.view(std, "Plain", 0)
	av := w.view(a, "AV", 1)
	w.impl(std, w.tab.Named(sv, nil))
	w.impl(std, w.tab.Ref(w.tab.Named(sv, nil)))
	w.impl(std, types.Int) // not a named For: never indexed
	w.impl(std, w.tab.Named(w.views["Plain"], nil))
	w.impl(a, w.tab.Mut(w.tab.Named(sv, nil)))
	w.impl(a, w.tab.Named(av, []types.GenericArg{{Type: w.tab.Param(0)}}))
	w.impl(a, w.tab.Named(av, []types.GenericArg{{Type: types.I32}}))
	w.impl(b, w.tab.Named(sv, nil))
	w.impl(b, w.tab.Ref(w.tab.Named(av, []types.GenericArg{{Type: types.Int}})))
	w.impl(c, w.tab.Named(av, []types.GenericArg{{Type: types.Bool}}))
	w.impl(c, w.tab.Param(0)) // a blanket For: never indexed
	w.impl(d, w.tab.Named(av, []types.GenericArg{{Type: types.U8}}))
	return w
}

func (w *implWorld) module(path string, std bool, imports ...*types.Module) *types.Module {
	m := &types.Module{Path: path, Std: std}
	for _, i := range imports {
		m.Imports = append(m.Imports, i.ID)
	}
	w.items.AddModule(m)
	w.mods[path] = m
	return m
}

func (w *implWorld) view(m *types.Module, name string, generics int) types.ItemID {
	d := &types.StructDecl{View: true}
	for i := 0; i < generics; i++ {
		d.Generics = append(d.Generics, types.GenericParam{Name: w.in.Intern("T")})
	}
	id := w.items.AddItem(&types.Item{Module: m.ID, Name: w.in.Intern(name), Kind: types.KindStruct, Exported: true,
		Key: types.ItemKey(m.Path, name), Decl: d})
	m.Items = append(m.Items, id)
	w.views[name] = id
	return id
}

func (w *implWorld) impl(m *types.Module, forT types.TypeID) types.ItemID {
	w.n++
	name := "impl" + string(rune('A'+w.n))
	id := w.items.AddItem(&types.Item{Module: m.ID, Name: w.in.Intern(name), Kind: types.KindImpl,
		Key: types.ItemKey(m.Path, name), Decl: &types.ImplDecl{For: forT}})
	m.Items = append(m.Items, id)
	return id
}

func (w *implWorld) walker(body *types.Module) *walker {
	if w.cache == nil {
		w.cache = &ImplCache{}
	}
	return newWalker(&Env{Types: w.tab, Module: body, Impls: w.cache}, nil)
}

// TestImplIndexSharedEqualsPerBody: for every body module and every first
// scan's view module, the shared index answers every view item exactly as
// the per-body build over the same breadth-first closure does, in order.
func TestImplIndexSharedEqualsPerBody(t *testing.T) {
	w := newImplWorld(t)
	nonEmpty := 0
	for _, body := range []string{"std.core", "a", "b", "c", "d"} {
		for _, first := range []string{"std.core", "a", "d"} {
			vmod := w.mods[first].ID
			for round := 0; round < 2; round++ { // the second round reads the shared index
				wk := w.walker(w.mods[body])
				ref := wk.implIndexBuild(wk.implClosure(vmod))
				for name, v := range w.views {
					got := wk.implsFor(v, vmod)
					if !reflect.DeepEqual(got, ref[v]) {
						t.Errorf("body %s, first view module %s, round %d, view %s: shared %v, per-body %v",
							body, first, round, name, got, ref[v])
					}
					if len(got) > 0 {
						nonEmpty++
					}
				}
				// A later scan from another view module reuses the first
				// scan's index, as the per-body build did.
				if got := wk.implsFor(w.views["AV"], w.mods["std.core"].ID); !reflect.DeepEqual(got, ref[w.views["AV"]]) {
					t.Errorf("body %s, first %s: a later scan read another index: %v, want %v", body, first, got, ref[w.views["AV"]])
				}
			}
		}
	}
	if nonEmpty == 0 {
		t.Fatal("the fixture indexed nothing")
	}
	// The order is the closure's: std's two impls for SV (For SV, For &SV),
	// a's (For mut SV), b's (For SV), for a body in c whose first scan
	// starts at std.core.
	wk := w.walker(w.mods["c"])
	got := wk.implsFor(w.views["SV"], w.mods["std.core"].ID)
	var mods []string
	for _, id := range got {
		mods = append(mods, w.items.Module(w.items.Get(id).Module).Path)
	}
	if want := []string{"std.core", "std.core", "b", "a"}; !reflect.DeepEqual(mods, want) {
		t.Errorf("SV's impls from c come from %v, want %v (breadth first: std.core, c, b, a)", mods, want)
	}
}

// TestImplIndexBuiltOncePerPair: many bodies of one module whose first
// scans start at the same view module share one index, so the item scans
// no longer grow with bodies times the closure's items; a module whose
// item list is replaced makes the next body rebuild that module's index,
// with the new impl in it, and only that one.
func TestImplIndexBuiltOncePerPair(t *testing.T) {
	w := newImplWorld(t)
	c, std := w.mods["c"], w.mods["std.core"]
	index := func() *sharedImpls {
		wk := w.walker(c)
		wk.implsFor(w.views["SV"], std.ID)
		return wk.implShared
	}
	first := index()
	for i := 0; i < 200; i++ {
		if index() != first {
			t.Fatalf("body %d built its own index", i+1)
		}
	}
	b := w.mods["b"]
	b.Items = append([]types.ItemID(nil), b.Items...) // a new list
	added := w.impl(b, w.tab.Named(w.views["SV"], nil))
	second := index()
	if second == first {
		t.Fatal("after b's item list changed, a body still read the old index")
	}
	// Only b's own index was rebuilt; the other modules' are shared.
	for i, m := range second.mods {
		if (first.mods[i] == m) == (m.id == b.ID) {
			t.Errorf("module %d: rebuilt %v, want rebuilt only for b", m.id, first.mods[i] != m)
		}
	}
	wk := w.walker(c)
	got := wk.implsFor(w.views["SV"], std.ID)
	if !slices.Contains(got, added) {
		t.Errorf("the rebuilt index %v lacks the new impl %d", got, added)
	}
	ref := wk.implIndexBuild(wk.implClosure(std.ID))
	if !reflect.DeepEqual(got, ref[w.views["SV"]]) {
		t.Errorf("rebuilt %v, per-body %v", got, ref[w.views["SV"]])
	}
}

// TestImplCachesDoNotPinModules: a session's cache reaches no Module, so a
// world's Modules are collected while a cache it filled is still live.
func TestImplCachesDoNotPinModules(t *testing.T) {
	cache := &ImplCache{}
	wp := func() weak.Pointer[types.Module] {
		w := newImplWorld(t)
		w.cache = cache
		w.walker(w.mods["c"]).implsFor(w.views["SV"], w.mods["std.core"].ID)
		if len(cache.pairs) == 0 {
			t.Fatal("the scan did not fill the session's cache")
		}
		return weak.Make(w.mods["c"])
	}()
	for i := 0; i < 4 && wp.Value() != nil; i++ {
		runtime.GC()
	}
	if wp.Value() != nil {
		t.Fatal("a Module is still reachable from a live cache after its world was dropped")
	}
	runtime.KeepAlive(cache)
}

// TestImplCacheNilEnv: an Env without a cache gives each body its own, with
// the same answers.
func TestImplCacheNilEnv(t *testing.T) {
	w := newImplWorld(t)
	std := w.mods["std.core"]
	shared := w.walker(w.mods["c"]).implsFor(w.views["SV"], std.ID)
	a := newWalker(&Env{Types: w.tab, Module: w.mods["c"]}, nil)
	b := newWalker(&Env{Types: w.tab, Module: w.mods["c"]}, nil)
	if got := a.implsFor(w.views["SV"], std.ID); !reflect.DeepEqual(got, shared) {
		t.Errorf("own cache %v, shared %v", got, shared)
	}
	b.implsFor(w.views["SV"], std.ID)
	if a.impls == b.impls || a.implShared == b.implShared {
		t.Error("two bodies without Env.Impls shared a cache")
	}
}
