// Why: regression tests for defects once found in iface's hashing and
// validation.
// What: error layouts in instance content hashes, type namespace keys,
// injective layout names, site-free fn literal facts, memoized validation,
// total shape and API hashes, and hashing under concurrent schedules.
// How: small fixtures per case, plus benchmarks of validation on a large
// generated session (bigWorld).

package iface

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// askWorld answers ReadLayout from a map keyed by Key+Name and records what
// it was asked.
type askWorld struct {
	m     map[string]source.Hash
	asked []types.Read
}

func (w *askWorld) Part(r types.Read) (source.Hash, bool) {
	w.asked = append(w.asked, r)
	if r.Kind != types.ReadLayout {
		return source.Hash{}, false
	}
	h, ok := w.m[r.Key.String()+r.Name]
	return h, ok
}

func (w *askWorld) wasAsked(key, name string) bool {
	for _, r := range w.asked {
		if r.Kind == types.ReadLayout && r.Key.String() == key && r.Name == name {
			return true
		}
	}
	return false
}

// localBody is a body of item with one local of type ty per entry, the
// places of extra projections read by a statement each.
func localBody(s *session, item types.ItemID, tys []types.TypeID, places func(b *mir.Body, locals []mir.LocalID) []mir.Place) *mir.Body {
	b := retBody(s, item, types.Void)
	var ls []mir.LocalID
	for _, ty := range tys {
		ls = append(ls, b.AddLocal(mir.Local{Type: ty, Kind: mir.LocalTemp, Loop: mir.NoLoop}))
	}
	if places != nil {
		for _, p := range places(b, ls) {
			b.Blocks[0].Stmts = append(b.Blocks[0].Stmts, mir.Statement{Kind: mir.StAssign, Place: mir.LocalPlace(ls[0]),
				Rvalue: mir.Rvalue{Kind: mir.RvUse, A: mir.Copy(p)}})
		}
	}
	return b
}

// TestInstanceContentHashErrorLayout: an error's layout is a fact of every
// instance whose code holds an E!T or a SelCase downcast of it.
func TestInstanceContentHashErrorLayout(t *testing.T) {
	s := withDecls(newGeo(false))
	errA := s.id("geo.ErrA")
	setA := s.tab.ErrorSet([]types.ErrorMember{{Item: errA}})
	union := localBody(s, s.id("geo.Main"), []types.TypeID{s.tab.ErrorUnion(setA, types.I32)}, nil)
	setT := s.tab.ErrorSetType(setA)
	down := localBody(s, s.id("geo.Main"), []types.TypeID{types.U8, setT}, func(b *mir.Body, ls []mir.LocalID) []mir.Place {
		return []mir.Place{b.Place(ls[1], mir.Projection{Kind: mir.ProjDowncast, Sel: mir.SelCase, A: uint32(errA), Type: setT},
			mir.Projection{Kind: mir.ProjField, A: 1, Type: types.U8})}
	})
	for name, body := range map[string]*mir.Body{"ErrA!i32 local": union, "SelCase downcast and field": down} {
		w := &askWorld{m: map[string]source.Hash{"geo.ErrA": source.HashString("t", "offsets 0, 1")}}
		before := s.h.InstanceContentHash(w, body)
		if !w.wasAsked("geo.ErrA", "") {
			t.Errorf("%s: the World was not asked for ErrA's layout (asked %v)", name, w.asked)
		}
		w.m["geo.ErrA"] = source.HashString("t", "offsets 0, 2")
		if s.h.InstanceContentHash(w, body) == before {
			t.Errorf("%s: an error layout change left InstanceContentHash equal", name)
		}
		w.m["geo.ErrA"] = source.HashString("t", "offsets 0, 1")
		d := s.items.Get(errA).Decl.(*types.ErrorDecl)
		saved := d.Fields
		d.Fields = append(d.Fields, types.FieldDecl{Name: s.in.Intern("b"), Type: types.U8, Exported: true})
		if s.h.InstanceContentHash(w, body) == before {
			t.Errorf("%s: an error field added left InstanceContentHash equal", name)
		}
		d.Fields = saved
		delete(w.m, "geo.ErrA")
		if !catchICE(func() { s.h.InstanceContentHash(w, body) }) {
			t.Errorf("%s: a missing error layout did not raise an ICE", name)
		}
	}
	// An AggError rvalue names its error.
	agg := localBody(s, s.id("geo.Main"), []types.TypeID{setT}, nil)
	agg.Blocks[0].Stmts = append(agg.Blocks[0].Stmts, mir.Statement{Kind: mir.StAssign, Place: mir.LocalPlace(1),
		Rvalue: mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggError, Item: errA, Type: setT}})
	w := &askWorld{m: map[string]source.Hash{"geo.ErrA": source.HashString("t", "a")}}
	s.h.InstanceContentHash(w, agg)
	if !w.wasAsked("geo.ErrA", "") {
		t.Error("AggError: the World was not asked for ErrA's layout")
	}
}

// TestReadMembersModuleTypeKeyCollision: module geo.Point beside struct
// Point of module geo. A member miss on the type is keyed by
// TypeNamespaceKey and never read as the module.
func TestReadMembersModuleTypeKeyCollision(t *testing.T) {
	f := newWorldFixture()
	gp := f.items.AddModule(&types.Module{Path: "geo.Point"})
	f.closure = map[string]bool{"geo.Point": true} // app reads module geo.Point
	tk := TypeNamespaceKey(types.KeyFromString("geo.Point"))
	if tk != types.MemberKey("geo", "Point", "") {
		t.Errorf("TypeNamespaceKey = %q, want MemberKey(geo, Point, \"\")", tk)
	}
	miss := f.record(t, types.ReadNoItem, tk.String(), "z")
	members := f.record(t, types.ReadExports, tk.String(), "")
	modMiss := f.record(t, types.ReadNoItem, "geo.Point", "z")
	modExports := f.record(t, types.ReadExports, "geo.Point", "")
	if members.Hash == modExports.Hash {
		t.Fatal("the type's member list and the module's export list hash alike")
	}
	for _, r := range []types.Read{miss, members, modMiss, modExports} {
		f.check(t, "unchanged "+r.Kind.String()+" "+r.Key.String(), r, true)
	}
	d := f.items.Get(f.id("geo.Point")).Decl.(*types.StructDecl)
	d.Fields = append(d.Fields, types.FieldDecl{Name: f.in.Intern("z"), Type: types.F64, Exported: true})
	f.check(t, "field z appears: the member miss", miss, false)
	f.check(t, "field z appears: the member list", members, false)
	f.check(t, "field z appears: the module miss", modMiss, true)
	f.check(t, "field z appears: the module exports", modExports, true)
	f.item(gp, types.ItemKey("geo.Point", "z"), "z", types.KindFunc, true)
	f.check(t, "item z of module geo.Point appears: the module miss", modMiss, false)
	f.check(t, "item z of module geo.Point appears: the module exports", modExports, false)
	if ok, _ := Valid(f.world("app", types.X86_64Windows), []types.Read{{Kind: types.ReadExports, Key: types.KeyFromString("geo.K:")}}); ok {
		t.Error("the member list of a const held")
	}
}

// TestLayoutNameInjective: LayoutName names an instance, so two Points of two
// modules, and two different literals, never share one.
func TestLayoutNameInjective(t *testing.T) {
	s := withDecls(newGeo(false))
	other := s.items.AddModule(&types.Module{Path: "other"})
	op := s.item(other, types.KeyFromString("other.Point"), "Point", types.KindStruct, true)
	s.items.Get(op).Decl = &types.StructDecl{}
	gpT, opT := s.tab.Named(s.id("geo.Point"), nil), s.tab.Named(op, nil)
	n1 := s.h.LayoutName([]types.GenericArg{types.TypeArg(gpT)})
	n2 := s.h.LayoutName([]types.GenericArg{types.TypeArg(opT)})
	if n1 == n2 {
		t.Fatalf("LayoutName of [geo.Point] and [other.Point] are both %q", n1)
	}
	l1, l2 := addLiteral(s, "geo.Add#fn0", 0), addLiteral(s, "geo.Div#fn4", 0)
	l3 := addLiteral(s, "geo.Div#fn5", types.PanicAssert)
	ln := func(l types.ItemID) string { return s.h.LayoutName([]types.GenericArg{literalArg(s, l)}) }
	if ln(l1) != ln(l2) || ln(l1) == ln(l3) {
		t.Error("LayoutName of fn literal arguments is not by content")
	}
	wrap := s.id("geo.Wrap")
	wg := localBody(s, s.id("geo.Main"), []types.TypeID{s.tab.Named(wrap, []types.GenericArg{types.TypeArg(gpT)})}, nil)
	wo := localBody(s, s.id("geo.Main"), []types.TypeID{s.tab.Named(wrap, []types.GenericArg{types.TypeArg(opT)})}, nil)
	w := &askWorld{m: map[string]source.Hash{"geo.Point": source.HashString("t", "gp"), "other.Point": source.HashString("t", "op"),
		"geo.Wrap" + n1: source.HashString("t", "wg"), "geo.Wrap" + n2: source.HashString("t", "wo")}}
	g0, o0 := s.h.InstanceContentHash(w, wg), s.h.InstanceContentHash(w, wo)
	w.m["geo.Wrap"+n2] = source.HashString("t", "wo2")
	if s.h.InstanceContentHash(w, wg) != g0 {
		t.Error("Wrap[other.Point]'s layout changed Wrap[geo.Point]'s content hash")
	}
	if s.h.InstanceContentHash(w, wo) == o0 {
		t.Error("Wrap[other.Point]'s layout change left its content hash equal")
	}
}

// fnRefBody is a body of item that takes lit as a value (RvFnRef) and calls
// it directly.
func fnRefBody(s *session, item, lit types.ItemID) *mir.Body {
	b := retBody(s, item, types.Void)
	fnT := s.tab.Fn(types.FnType{Params: []types.FnParam{{Type: types.I32}, {Type: types.I32}}, Result: types.Bool})
	v := b.AddLocal(mir.Local{Type: fnT, Kind: mir.LocalTemp, Loop: mir.NoLoop})
	r := b.AddLocal(mir.Local{Type: types.Bool, Kind: mir.LocalTemp, Loop: mir.NoLoop})
	ret := b.AddBlock(mir.OriginBody, mir.NoDefer)
	b.Blocks[ret].Term = mir.Terminator{Kind: mir.TermReturn}
	b.Blocks[0].Stmts = []mir.Statement{{Kind: mir.StAssign, Place: mir.LocalPlace(v), Rvalue: mir.Rvalue{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: lit}, Type: fnT}}}
	args := b.AddArgs(mir.Arg{Operand: mir.Imm(types.I32, 1)}, mir.Arg{Operand: mir.Imm(types.I32, 2)})
	b.Blocks[0].Term = mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: lit}},
		Args: args, Dest: mir.LocalPlace(r), Target: ret}
	return b
}

// TestContentHashSiteFree: an instance that references a fn literal
// hashes it by content, never by its LiteralKey.
func TestContentHashSiteFree(t *testing.T) {
	s := withDecls(newGeo(false))
	l1, l2 := addLiteral(s, "geo.Add#fn0", 0), addLiteral(s, "geo.Div#fn3", 0)
	l3 := addLiteral(s, "geo.Div#fn4", types.PanicAssert)
	w := layoutWorld{}
	main := s.id("geo.Main")
	h1 := s.h.InstanceContentHash(w, fnRefBody(s, main, l1))
	if h2 := s.h.InstanceContentHash(w, fnRefBody(s, main, l2)); h1 != h2 {
		t.Error("identical literals at two sites give two instance content hashes")
	}
	if h3 := s.h.InstanceContentHash(w, fnRefBody(s, main, l3)); h1 == h3 {
		t.Error("different literals give one instance content hash")
	}
	// A nominal callee is still by key and signature.
	add := s.id("geo.Add")
	nb := fnRefBody(s, main, add)
	wp := layoutWorld{"geo.Point": source.HashString("t", "p")}
	n0 := s.h.InstanceContentHash(wp, nb)
	s.items.Get(add).Decl.(*types.FuncDecl).Sig.Exported = false
	if s.h.InstanceContentHash(wp, nb) == n0 {
		t.Error("a nominal callee's signature change left the content hash equal")
	}
}

// TestShapesHashTotal: a shape part or API part with an input missing is an
// ICE.
func TestShapesHashTotal(t *testing.T) {
	s, f := ifaceSession(false)
	m := geoInterface(s, f)
	m.Shapes = nil
	if !catchICE(func() { EncodeInterface(s.env, s.h, m) }) {
		t.Error("an exported non-generic struct with no shape record and no term hashed")
	}
	if !catchICE(func() { s.h.ShapesHash(m.Items, nil) }) {
		t.Error("ShapesHash with the shape records left out did not raise an ICE")
	}
	b, fb := ifaceSession(false)
	b.items.Get(b.id("geo.Add")).Decl = nil
	if !catchICE(func() { b.h.APIHash(geoInterface(b, fb).Items) }) {
		t.Error("an exported item with no declaration hashed into the API part")
	}
}

// bigWorld is the world fixture with modules more modules of per items
// each (functions named F0, F1, ...).
func bigWorld(modules, per int) *worldFixture {
	f := newWorldFixture()
	for m := 0; m < modules; m++ {
		path := fmt.Sprintf("big%d", m)
		id := f.items.AddModule(&types.Module{Path: path})
		for i := 0; i < per; i++ {
			name := fmt.Sprintf("F%d", i)
			f.item(id, types.ItemKey(path, name), name, types.KindFunc, true)
		}
	}
	return f
}

// TestValidateCostIndependentOfSessionSize: a first ReadNoItem in a fresh
// world costs the size of the module it names, not of the session, and a
// second Valid on the same world allocates nothing.
func TestValidateCostIndependentOfSessionSize(t *testing.T) {
	cost := func(f *worldFixture) float64 {
		r := f.record(t, types.ReadNoItem, "geo", "Missing")
		return testing.AllocsPerRun(20, func() {
			if ok, _ := Valid(f.world("app", types.X86_64Windows), []types.Read{r}); !ok {
				t.Fatal("the read does not hold")
			}
		})
	}
	small, big := cost(newWorldFixture()), cost(bigWorld(1, 100_000))
	if big > small {
		t.Errorf("a fresh world's ReadNoItem allocates %.0f times with 100,000 more items in another module, %.0f without: it scans the session", big, small)
	}
}

func TestValidateMemoized(t *testing.T) {
	f := newWorldFixture()
	reads := []types.Read{
		f.record(t, types.ReadNoItem, "geo", "Missing"),
		f.record(t, types.ReadNoItem, TypeNamespaceKey(types.KeyFromString("geo.Point")).String(), "zz"),
		f.record(t, types.ReadExports, "geo", ""),
		f.record(t, types.ReadBody, "geo.Add", ""),
		f.record(t, types.ReadSignature, "geo.Div", ""),
		f.record(t, types.ReadFields, "geo.Point", ""),
	}
	w := f.world("app", types.X86_64Windows)
	if ok, r := Valid(w, reads); !ok {
		t.Fatalf("fixture read %v does not hold", r)
	}
	if n := testing.AllocsPerRun(10, func() { Valid(w, reads) }); n != 0 {
		t.Errorf("a second Valid on one world allocates %.0f times; Part is not memoized", n)
	}
}

// BenchmarkValidateLargeSession: 1,000 reads over a session of 100,000
// items in 1,000 modules, mixing ReadNoItem on many modules, ReadSignature
// of a function with a contract and ReadBody of a 64,000-statement body.
// "fresh" makes a new world per iteration (one validation round);
// "fresh-noitem" does so for the 500 ReadNoItem reads alone (500 modules of
// 100 names); "memo" validates again on one world.
func BenchmarkValidateLargeSession(b *testing.B) {
	f := bigWorld(1000, 100)
	f.bodies[f.id("geo.Add")] = bigBody(64_000)
	var reads []types.Read
	for i := 0; i < 1000; i++ {
		switch i % 4 {
		case 0, 1:
			reads = append(reads, f.record(b, types.ReadNoItem, fmt.Sprintf("big%d", i%1000), fmt.Sprintf("Missing%d", i)))
		case 2:
			reads = append(reads, f.record(b, types.ReadSignature, "geo.Div", ""))
		case 3:
			reads = append(reads, f.record(b, types.ReadBody, "geo.Add", ""))
		}
	}
	if ok, r := Valid(f.world("app", types.X86_64Windows), reads); !ok {
		b.Fatalf("fixture read %v does not hold", r)
	}
	b.Run("fresh", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			Valid(f.world("app", types.X86_64Windows), reads)
		}
	})
	var noItem []types.Read
	for _, r := range reads {
		if r.Kind == types.ReadNoItem {
			noItem = append(noItem, r)
		}
	}
	b.Run("fresh-noitem", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			Valid(f.world("app", types.X86_64Windows), noItem)
		}
	})
	b.Run("memo", func(b *testing.B) {
		w := f.world("app", types.X86_64Windows)
		Valid(w, reads)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			Valid(w, reads)
		}
	})
}

// irSchedules is TestHashIgnoresInternOrder's goroutine half: every
// hash and encoding of session b computed by 8 goroutines, each in its own
// shuffled order, on one cold Hasher, equals session a's sequential result.
func irSchedules(t *testing.T, a *session, fa *source.File, b *session, fb *source.File) {
	ta, tb := fixtureTypes(a), fixtureTypes(b)
	ca, cb := fixtureConsts(a), fixtureConsts(b)
	ba, bb := everyBody(a, fa), everyBody(b, fb)
	var ia, ib []*types.Item
	for _, g := range geoItems {
		if it := a.items.Get(a.id(g.key)); it.Decl != nil {
			ia, ib = append(ia, it), append(ib, b.items.Get(b.id(g.key)))
		}
	}
	ma, mbs := geoInterface(a, fa), make([]*Interface, 8)
	for i := range mbs {
		mbs[i] = geoInterface(b, fb)
	}
	var want []source.Hash
	for _, ty := range ta {
		want = append(want, a.h.Type(ty))
	}
	for _, c := range ca {
		want = append(want, a.h.Const(c))
	}
	for _, it := range ia {
		want = append(want, a.h.Decl(it))
	}
	want = append(want, a.h.Body(ba))
	wantIface := EncodeInterface(a.env, a.h, ma)

	cold := NewHasher(b.tab, b.consts, func(id types.ItemID) *mir.Body { return b.bodies[id] })
	jobs := make([]func() source.Hash, 0, len(want))
	for _, ty := range tb {
		jobs = append(jobs, func() source.Hash { return cold.Type(ty) })
	}
	for _, c := range cb {
		jobs = append(jobs, func() source.Hash { return cold.Const(c) })
	}
	for _, it := range ib {
		jobs = append(jobs, func() source.Hash { return cold.Decl(it) })
	}
	jobs = append(jobs, func() source.Hash { return cold.Body(bb) })
	var wg sync.WaitGroup
	got := make([][]source.Hash, 8)
	ifaces := make([][]byte, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			order := rand.New(rand.NewPCG(uint64(g), 216)).Perm(len(jobs))
			got[g] = make([]source.Hash, len(jobs))
			for i, j := range order {
				if i == len(order)/2 {
					ifaces[g] = EncodeInterface(b.env, cold, mbs[g])
				}
				got[g][j] = jobs[j]()
			}
		}()
	}
	wg.Wait()
	for g := range got {
		for j := range want {
			if got[g][j] != want[j] {
				t.Errorf("goroutine %d: hash %d differs from the sequential one of the other intern order", g, j)
			}
		}
		if !bytes.Equal(ifaces[g], wantIface) {
			t.Errorf("goroutine %d: interface bytes differ from the sequential encoding", g)
		}
	}
}

// BenchmarkValidateNoItemBigSession: 250 ReadNoItem reads on module geo, in a
// fresh world, with 100,000 items in module big0.
func BenchmarkValidateNoItemBigSession(b *testing.B) {
	f := bigWorld(1, 100_000)
	var reads []types.Read
	for i := 0; i < 250; i++ {
		reads = append(reads, f.record(b, types.ReadNoItem, "geo", fmt.Sprintf("Missing%d", i)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if ok, _ := Valid(f.world("app", types.X86_64Windows), reads); !ok {
			b.Fatal("the reads do not hold")
		}
	}
}

// The index reads only what AddItem fixes: resolve sets a method's Owner
// after AddItem (and a pending owner only when the module settles), so a
// method indexed before its Owner is set is still the type's member, and
// never a name of its module.
func TestIndexOwnerSetAfterAddItem(t *testing.T) {
	f := newWorldFixture()
	members := f.record(t, types.ReadExports, TypeNamespaceKey(types.KeyFromString("geo.Point")).String(), "")
	exports := f.record(t, types.ReadExports, "geo", "")
	m := f.item(f.mod, types.MemberKey("geo", "Point", "Norm"), "Norm", types.KindFunc, true)
	Valid(f.world("app", types.X86_64Windows), []types.Read{members, exports}) // indexes Norm with no Owner
	f.items.Get(m).Owner = f.id("geo.Point")
	f.check(t, "the method's owner set after it was indexed: the member list", members, false)
	f.check(t, "the method's owner set after it was indexed: the module's exports", exports, true)
}
