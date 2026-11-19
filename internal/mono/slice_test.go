// Why: the Slice[T] namespace functions are instantiated over T only, with a
// slice self, never over the type that implements the namespace.
// What: TestSliceFnOverTOnly, TestSharedReceiverForMutSelf and
// TestInstantiateDirect for slice.go.
// How: hand-built MIR in the shapes lower emits for the std bodies of
// std/core/core_slice.mk and core_sort.mk.

package mono

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// sliceWorld is a world with an interface Slice[T] (Items, ItemsMut), its
// namespace functions Len (bodiless), Swap (mut self) and Count and Has
// (&self), a generic leaf Use[T](mut []T), and List[int] implementing it.
type sliceWorld struct {
	*world
	iface, length, swap, count, has, use types.ItemID
	list, listItems, listItemsMut        types.ItemID
	listInt                              types.TypeID
}

func newSliceWorld() *sliceWorld {
	w := &sliceWorld{world: newWorld()}
	tab := w.tab
	p0 := tab.Param(0)
	self := tab.SelfType()
	w.iface = w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "Slice"), Name: w.in.Intern("Slice"), Kind: types.KindInterface,
		Decl: &types.InterfaceDecl{Generics: []types.GenericParam{{Name: w.in.Intern("T")}}, Methods: []types.Signature{
			{Name: w.in.Intern("Items"), Params: []types.Param{{Name: w.in.Intern("self"), Type: tab.Ref(self)}}, Result: tab.Slice(p0)},
			{Name: w.in.Intern("ItemsMut"), Params: []types.Param{{Name: w.in.Intern("self"), Type: tab.Mut(self)}}, Result: tab.MutSlice(p0)},
		}}})
	sl := tab.Named(w.iface, []types.GenericArg{ty(p0)})
	owned := func(name string, params []types.TypeID, result types.TypeID) types.ItemID {
		id := w.fn("Slice:"+name, 1, params, result)
		it := w.items.Get(id)
		it.Owner, it.Name = w.iface, w.in.Intern(name)
		return id
	}
	w.length = owned("Len", []types.TypeID{tab.Ref(sl)}, types.Int)
	fd := w.items.Get(w.length).Decl.(*types.FuncDecl)
	fd.Bodiless, fd.Intrinsic = true, true
	w.use = w.fn("Use", 1, []types.TypeID{tab.MutSlice(p0)}, types.Void)
	w.body(w.use).ret()

	// Swap(mut self) as core_slice.mk's: n := self.Len(); Use(self.ItemsMut()).
	w.swap = owned("Swap", []types.TypeID{tab.Mut(sl), types.Int}, types.Void)
	sb := w.body(w.swap)
	view := sb.temp(tab.Ref(sl))
	sb.assign(view, mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: sb.b.Place(1, mir.Projection{Kind: mir.ProjDeref, Type: sl})})
	sb.call(w.length, []types.GenericArg{ty(p0)}, sb.temp(types.Int), arg(copyOf(sb.b, view)))
	s := sb.temp(tab.MutSlice(p0))
	sb.callTerm(mir.Callee{Kind: mir.CallInterface, Iface: w.iface, Method: 1, SelfType: sl}, s, mir.NoLocal,
		mir.Arg{Operand: copyOf(sb.b, 1), Mode: mir.ArgReborrow})
	sb.call(w.use, []types.GenericArg{ty(p0)}, sb.temp(types.Void), mir.Arg{Operand: mir.Move(sb.b.Place(s)), Mode: mir.ArgMove})
	sb.ret()

	// Count(&self): s := self.Items(); return s.Len() (RvLen, as lower's).
	w.count = owned("Count", []types.TypeID{tab.Ref(sl)}, types.Int)
	cb := w.body(w.count)
	cs := cb.temp(tab.Slice(p0))
	cb.callTerm(mir.Callee{Kind: mir.CallInterface, Iface: w.iface, Method: 0, SelfType: sl}, cs, mir.NoLocal, arg(copyOf(cb.b, 1)))
	cb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvLen, Place: cb.b.Place(cs)})
	cb.ret()

	// Has(&self): one namespace function calling another on self, as
	// Slice[T].Has calls Index.
	w.has = owned("Has", []types.TypeID{tab.Ref(sl)}, types.Int)
	hb := w.body(w.has)
	hb.call(w.count, []types.GenericArg{ty(p0)}, mir.ReturnLocal, arg(copyOf(hb.b, 1)))
	hb.ret()

	// List[int] and its impl's Items and ItemsMut.
	w.list = w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "List"), Name: w.in.Intern("List"), Kind: types.KindStruct, Decl: &types.StructDecl{}})
	w.listInt = tab.Named(w.list, []types.GenericArg{ty(types.Int)})
	w.listItems = w.fn("ListItems", 0, []types.TypeID{tab.Ref(w.listInt)}, tab.Slice(types.Int))
	lb := w.body(w.listItems)
	lb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: mir.Const(tab.Slice(types.Int), w.consts.Intern(types.ConstValue{Type: tab.Slice(types.Int), Align: 8, Bytes: make([]byte, 16)}))})
	lb.ret()
	w.listItemsMut = w.fn("ListItemsMut", 0, []types.TypeID{tab.Mut(w.listInt)}, tab.MutSlice(types.Int))
	w.body(w.listItemsMut).ret()
	w.impls[implKey{w.iface, 0, w.listInt}] = w.listItems
	w.impls[implKey{w.iface, 1, w.listInt}] = w.listItemsMut
	return w
}

// sliceMain is a non-generic root calling the namespace functions with every
// receiver shape lower passes: reborrow of a mut []int, move of a mut [3]int
// borrow, copy of a &List[int], reborrow of a mut List[int], copy of a
// &[3]int and of a []int.
func (w *sliceWorld) sliceMain() types.ItemID {
	tab := w.tab
	arrT := tab.Array(types.ConstArg{Kind: types.CAValue, Value: intConst(w.consts, 3)}, types.Int)
	main := w.fn("Main", 0, []types.TypeID{tab.MutSlice(types.Int), tab.Ref(w.listInt), tab.Mut(w.listInt), tab.Slice(types.Int)}, types.Void)
	mb := w.body(main)
	ints := []types.GenericArg{ty(types.Int)}
	mb.call(w.swap, ints, mb.temp(types.Void), mir.Arg{Operand: copyOf(mb.b, 1), Mode: mir.ArgReborrow}, arg(mir.Imm(types.Int, 0)))
	arr := mb.temp(arrT)
	mb.assign(arr, mir.Rvalue{Kind: mir.RvRepeat, A: mir.Imm(types.Int, 0), Len: types.ConstArg{Kind: types.CAValue, Value: intConst(w.consts, 3)}, Type: arrT})
	ma := mb.temp(tab.Mut(arrT))
	mb.assign(ma, mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Exclusive, Place: mb.b.Place(arr)})
	mb.call(w.swap, ints, mb.temp(types.Void), mir.Arg{Operand: mir.Move(mb.b.Place(ma)), Mode: mir.ArgMove}, arg(mir.Imm(types.Int, 1)))
	mb.call(w.has, ints, mb.temp(types.Int), arg(copyOf(mb.b, 2)))
	mb.call(w.swap, ints, mb.temp(types.Void), mir.Arg{Operand: copyOf(mb.b, 3), Mode: mir.ArgReborrow}, arg(mir.Imm(types.Int, 2)))
	ra := mb.temp(tab.Ref(arrT))
	mb.assign(ra, mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: mb.b.Place(arr)})
	mb.call(w.count, ints, mb.temp(types.Int), arg(copyOf(mb.b, ra)))
	mb.call(w.has, ints, mb.temp(types.Int), arg(copyOf(mb.b, 4)))
	mb.ret()
	return main
}

// TestSliceFnOverTOnly: every receiver shape gives the one instance
// Slice:Swap[int] (over T only, never over the implementer), whose
// self is mut []int, with no interface call, ItemsMut a reslice of self and
// the bodiless Len an RvLen; the caller gets the implementer's Items and
// ItemsMut calls and the array reslices; everything validates and two runs
// are equal. Without slice.go this ICEd "no impl of m.Slice method 1 for
// Slice[int]" (mk check tests/programs/wc.mk).
func TestSliceFnOverTOnly(t *testing.T) {
	w := newSliceWorld()
	main := w.sliceMain()
	rootBlocks := len(w.bodies[main].Blocks)
	in, ds := w.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	per := map[types.ItemID]int{}
	for i := range in.List {
		per[in.List[i].Item]++
	}
	for _, id := range []types.ItemID{w.swap, w.has, w.count, w.listItems, w.listItemsMut} {
		if per[id] != 1 {
			t.Errorf("%s: %d instances, want 1", w.items.Get(id).Key, per[id])
		}
	}
	if per[w.length] != 0 {
		t.Errorf("the bodiless Len has %d instances; every call should be RvLen", per[w.length])
	}
	sw := w.inst(t, in, "m.Slice:Swap[int]")
	if got := sw.Body.Locals[1].Type; got != w.tab.MutSlice(types.Int) {
		t.Errorf("Swap's self is %s, want mut []int", w.tab.TypeString(got, nil))
	}
	kinds := map[mir.RvalueKind]int{}
	for bi := range sw.Body.Blocks {
		for _, st := range sw.Body.Blocks[bi].Stmts {
			kinds[st.Rvalue.Kind]++
		}
		if c := sw.Body.Blocks[bi].Term.Callee; sw.Body.Blocks[bi].Term.Kind == mir.TermCall && c.Kind != mir.CallDirect {
			t.Errorf("Swap keeps a %v call", c.Kind)
		}
	}
	if kinds[mir.RvLen] != 1 || kinds[mir.RvSlice] != 2 {
		t.Errorf("Swap: %d RvLen and %d RvSlice, want 1 and 2 (&(*self) and ItemsMut)", kinds[mir.RvLen], kinds[mir.RvSlice])
	}
	if c := w.inst(t, in, "m.Slice:Count[int]"); c.Body.Locals[1].Type != w.tab.Slice(types.Int) {
		t.Errorf("Count's self is %s, want []int", w.tab.TypeString(c.Body.Locals[1].Type, nil))
	}
	// The caller: Items and ItemsMut of List called, receivers all slices.
	mi := w.inst(t, in, "m.Main")
	callees := map[types.ItemID]int{}
	for bi := range mi.Body.Blocks {
		tm := &mi.Body.Blocks[bi].Term
		if tm.Kind != mir.TermCall {
			continue
		}
		callees[tm.Callee.Fn.Item]++
		if sliceFn(w.items, tm.Callee.Fn.Item) {
			rt := w.tab.Get(mi.Body.OperandType(mi.Body.Args[tm.Args.Start].Operand)).Kind
			if rt != types.KSlice && rt != types.KMutSlice {
				t.Errorf("a receiver of %s is a %v", w.items.Get(tm.Callee.Fn.Item).Key, rt)
			}
		}
	}
	if callees[w.listItems] != 1 || callees[w.listItemsMut] != 1 || callees[w.swap] != 3 {
		t.Errorf("Main calls Items %d, ItemsMut %d, Swap %d times; want 1, 1, 3", callees[w.listItems], callees[w.listItemsMut], callees[w.swap])
	}
	if w.bodies[main].Blocks[0].Term.Callee.Fn.Item != w.swap || len(w.bodies[main].Blocks) != rootBlocks || w.tab.Kind(w.bodies[w.swap].Locals[1].Type) != types.KMut {
		t.Error("the generic or root body was modified")
	}
	in2, _ := w.collect(t, Options{}, mainRoot(main))
	if a, b := dumpInstances(w.world, in), dumpInstances(w.world, in2); a != b {
		t.Errorf("two runs differ:\n%s\n---\n%s", a, b)
	}
}

// TestSharedReceiverForMutSelf: a mut self function on a shared receiver
// is check's or borrow's E0507; reaching mono it is an ICE that names it,
// never a miscompile.
func TestSharedReceiverForMutSelf(t *testing.T) {
	w := newSliceWorld()
	tab := w.tab
	bad := w.fn("Bad", 0, []types.TypeID{tab.Slice(types.Int)}, types.Void)
	bb := w.body(bad)
	bb.call(w.swap, []types.GenericArg{ty(types.Int)}, bb.temp(types.Void), arg(copyOf(bb.b, 1)), arg(mir.Imm(types.Int, 0)))
	bb.ret()
	mustICE(t, "a mut self on a shared slice (E0507)", func() { Collect(w, []Root{mainRoot(bad)}, Options{}) })
}

// TestInstantiateDirect: Instantiate (eval's path) gives the same slice
// self without collection.
func TestInstantiateDirect(t *testing.T) {
	w := newSliceWorld()
	var b *mir.Body
	if ie := source.Guard("t", source.Span{}, func() {
		b = Instantiate(w, w.has, []types.GenericArg{ty(types.Bool)}, Options{Validate: true})
	}); ie != nil {
		t.Fatalf("Instantiate: %s", ie)
	}
	if b.Locals[1].Type != w.tab.Slice(types.Bool) {
		t.Fatalf("Has[bool]'s self is %s", w.tab.TypeString(b.Locals[1].Type, nil))
	}
}

func dumpInstances(w *world, in *Instances) string {
	p := &mir.Printer{Types: w.tab, Items: w.items}
	var s strings.Builder
	for i := range in.List {
		s.WriteString(in.List[i].Key.Text + " " + in.List[i].Symbol + "\n")
		if in.List[i].Body != nil {
			s.WriteString(p.Print(in.List[i].Body))
		}
	}
	return s.String()
}
