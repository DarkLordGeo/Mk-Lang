// Why: a SessionWorld must read the Decl of no impl outside the reader and
// its closure: such a module may be checked or decoded on another goroutine,
// and a Decl read while it is written can be a non-nil type with a nil
// pointer (once an ICE in a cold build's background store).
// What: tests of impls, unsafe Send shapes, orphan methods and
// reads naming an outside item, with race tests for writes and index refresh.
// How: builds two-module worlds, writes the outside module from a goroutine,
// and relies on -race and deterministic filter checks.

package iface

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"mk/internal/types"
)

// TestImplOutsideClosureNotRead: an impl of "other", outside app's
// closure, whose Decl is a typed nil (what a half-written interface value
// reads as) is not read by the shape part or the member namespace: impls()
// must not take it as an *ImplDecl and dereference it.
func TestImplOutsideClosureNotRead(t *testing.T) {
	f := newWorldFixture()
	shape := f.record(t, types.ReadShape, "geo.Wrap", "")
	members := f.record(t, types.ReadExports, "geo.Point:", "")
	id := f.item(f.other, types.ImplKey("other", "geo.Wrap", "geo.Wrap"), "", types.KindImpl, false)
	f.items.Get(id).Decl = (*types.ImplDecl)(nil)
	f.check(t, "shape part beside a half-written impl outside the closure", shape, true)
	f.check(t, "member namespace beside a half-written impl outside the closure", members, true)
}

// TestOutsideUnsafeSendNotInShape: an unsafe impl Send for geo.Wrap in
// "other", outside app's closure (an orphan, which coherence rejects in
// "other"), leaves app's shape part of geo.Wrap unchanged. It tests the
// Sees filter itself: the nil check alone does not pass it.
func TestOutsideUnsafeSendNotInShape(t *testing.T) {
	f := newWorldFixture()
	wrap := f.id("geo.Wrap")
	shape := f.record(t, types.ReadShape, "geo.Wrap", "")
	id := f.item(f.other, types.ImplKey("other", "Send", "geo.Wrap"), "", types.KindImpl, false)
	gens := []types.GenericParam{{Name: f.in.Intern("T"), Bounds: []types.Bound{{Kind: types.BoundSend}}}}
	f.items.Get(id).Decl = &types.ImplDecl{Generics: gens, Bound: types.Bound{Kind: types.BoundSend},
		For: f.tab.Named(wrap, []types.GenericArg{types.TypeArg(f.tab.Param(0))}), Unsafe: true}
	f.check(t, "an unsafe impl Send for geo.Wrap outside app's closure", shape, true)
}

// TestOutsideOrphanMethodNotMember: a Type.Name method keyed
// other.Point:Foo whose Owner is geo.Point (resolve sets Owner to a
// prelude type of another module; check rejects it, E0704) leaves
// geo.Point's member namespace for app unchanged: memberNS's Module filter.
func TestOutsideOrphanMethodNotMember(t *testing.T) {
	f := newWorldFixture()
	members := f.record(t, types.ReadExports, "geo.Point:", "")
	m := f.item(f.other, types.MemberKey("other", "Point", "Foo"), "Foo", types.KindFunc, true)
	f.items.Get(m).Owner = f.id("geo.Point")
	f.check(t, "an orphan Point.Foo in a module outside app's closure", members, true)
}

// TestReadNamingOutsideItemMisses: a read whose key names an item of
// "other", outside app's closure, misses: item() refuses it, so its Decl is
// never read while other's check may write it. The layout branch of the
// driver's frontWorld refuses it the same way.
func TestReadNamingOutsideItemMisses(t *testing.T) {
	f := newWorldFixture()
	id := f.item(f.other, types.KeyFromString("other.S"), "S", types.KindStruct, true)
	f.items.Get(id).Decl = &types.StructDecl{}
	for _, k := range []types.ReadKind{types.ReadSignature, types.ReadShape, types.ReadFields, types.ReadExports} {
		key := "other.S"
		if k == types.ReadExports {
			key = "other.S:"
		}
		r := types.Read{Kind: k, Key: types.KeyFromString(key)}
		if _, ok := f.world("app", types.X86_64Windows).Part(r); ok {
			t.Errorf("%v %s outside app's closure was answered", k, key)
		}
	}
	if _, ok := f.world("app", types.X86_64Windows).Part(types.Read{Kind: types.ReadExports, Key: types.KeyFromString("other")}); ok {
		t.Error("the namespace of module other, outside app's closure, was answered")
	}
}

// writeLoop runs write on another goroutine until stop is closed, counting
// its rounds.
func writeLoop(write func()) (stop func(), writes *atomic.Int64) {
	done := make(chan struct{})
	writes = new(atomic.Int64)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			write()
			writes.Add(1)
		}
	}()
	return func() { close(done); wg.Wait() }, writes
}

// TestImplWritesOutsideClosureRace: Part's answers for app while
// another goroutine writes the Decls of impls of "other", as that module's
// check does. Under -race, the race the store hit is reported here when
// impls() reads those Decls; without -race it checks the answers stay put.
// The loop reads until the writer has written many times while it reads.
func TestImplWritesOutsideClosureRace(t *testing.T) {
	f := newWorldFixture()
	shape := f.record(t, types.ReadShape, "geo.Wrap", "")
	members := f.record(t, types.ReadExports, "geo.Point:", "")
	id := f.item(f.other, types.ImplKey("other", "geo.Wrap", "geo.Point"), "", types.KindImpl, false)
	wrap := f.id("geo.Wrap")
	point := f.tab.Named(f.id("geo.Point"), nil)
	it := f.items.Get(id)
	stop, writes := writeLoop(func() {
		it.Decl = nil
		it.Decl = &types.ImplDecl{Bound: types.Bound{Kind: types.BoundInterface, Item: wrap}, For: point}
	})
	defer stop()
	for i := 0; i < 200 || writes.Load() < 2000; i++ {
		w := f.world("app", types.X86_64Windows)
		if ok, failed := Valid(w, []types.Read{shape, members}); !ok {
			t.Errorf("round %d: %v no longer holds", i, failed)
			break
		}
	}
}

// TestOwnerWriteOutsideClosureRace: another goroutine writes the Owner
// of other.Point:Foo, as resolve's declareMethod does after AddItem, while
// app's members read of geo.Point validates. Under -race this reports a
// race without memberNS's Module filter.
func TestOwnerWriteOutsideClosureRace(t *testing.T) {
	f := newWorldFixture()
	members := f.record(t, types.ReadExports, "geo.Point:", "")
	m := f.item(f.other, types.MemberKey("other", "Point", "Foo"), "Foo", types.KindFunc, true)
	it := f.items.Get(m)
	point := f.id("geo.Point")
	stop, writes := writeLoop(func() {
		it.Owner = types.NoItem
		it.Owner = point
	})
	defer stop()
	for i := 0; i < 200 || writes.Load() < 2000; i++ {
		if ok, _ := Valid(f.world("app", types.X86_64Windows), []types.Read{members}); !ok {
			t.Fatalf("round %d: the members read no longer holds", i)
		}
	}
}

// TestReadNamingOutsideItemRace: a read naming an item of "other"
// while another goroutine writes that item's Decl, as other's check does.
// Under -race this reports a race if item() reads such an item.
func TestReadNamingOutsideItemRace(t *testing.T) {
	f := newWorldFixture()
	id := f.item(f.other, types.KeyFromString("other.S"), "S", types.KindStruct, true)
	it := f.items.Get(id)
	it.Decl = &types.StructDecl{}
	shape := types.Read{Kind: types.ReadShape, Key: types.KeyFromString("other.S")}
	sig := types.Read{Kind: types.ReadSignature, Key: types.KeyFromString("other.S")}
	stop, writes := writeLoop(func() {
		it.Decl = nil
		it.Decl = &types.StructDecl{}
	})
	defer stop()
	for i := 0; i < 200 || writes.Load() < 2000; i++ {
		w := f.world("app", types.X86_64Windows)
		if _, ok := w.Part(shape); ok {
			t.Fatal("a shape read of other.S was answered")
		}
		if _, ok := w.Part(sig); ok {
			t.Fatal("a signature read of other.S was answered")
		}
	}
}

// TestIndexReadsNoIDOfItemBeingAdded: another goroutine adds impl
// items of "other" (as a module's resolve or decode does) while the
// Hasher's index is refreshed (implItems, which impls calls for every
// world). AddItem publishes the slot before it writes it.ID, so an index
// that recorded it.ID could record NoItem, and then every later world of
// the session called Items.Get(0), an ICE. Without -race this
// failed 10 of 10 runs before refresh took the slot as the ID.
func TestIndexReadsNoIDOfItemBeingAdded(t *testing.T) {
	f := newWorldFixture()
	const total = 300000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < total; i++ {
			f.items.AddItem(&types.Item{Module: f.other, Kind: types.KindImpl,
				Key: types.ImplKey("other", "geo.Wrap", "other.T"+strconv.Itoa(i))})
		}
	}()
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
		}
		f.h.implItems()
	}
	zeros := 0
	for _, id := range f.h.implItems() {
		if id == types.NoItem {
			zeros++
		}
	}
	if zeros > 0 {
		t.Errorf("the index recorded %d impl items before their ID was set", zeros)
	}
	if _, ok := f.world("app", types.X86_64Windows).Part(types.Read{Kind: types.ReadShape, Key: types.KeyFromString("geo.Wrap")}); !ok {
		t.Error("geo.Wrap's shape read was not answered")
	}
}
