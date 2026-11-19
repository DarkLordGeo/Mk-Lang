// Why: mono must be testable on MIR built by hand, without the driver.
// What: world, a hand-built set of tables (types, items, consts, MIR bodies)
// behind a fake mono.Env, and the fb body builder with its helpers.
// How: tests declare items and write bodies block by block, then call
// w.collect; mustICE checks an expected internal error.

package mono

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/iface"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// world is one compilation's tables and a fake Env over them.
type world struct {
	in     *source.Interner
	items  *types.Items
	consts *types.ConstTable
	tab    *types.Table
	h      *iface.Hasher
	mod    types.ModuleID
	bodies map[types.ItemID]*mir.Body
	impls  map[implKey]types.ItemID
	onerr  map[onerrKey]bool
	pos    uint32
}

type onerrKey struct {
	item  types.ItemID
	block mir.BlockID
}

type implKey struct {
	iface  types.ItemID
	method uint32
	self   types.TypeID
}

func newWorld() *world {
	w := &world{in: source.NewInterner(), items: types.NewItems(), consts: types.NewConstTable(),
		bodies: map[types.ItemID]*mir.Body{}, impls: map[implKey]types.ItemID{}, onerr: map[onerrKey]bool{}}
	w.tab = types.NewTable(w.in, w.items, w.consts)
	w.h = iface.NewHasher(w.tab, w.consts, func(id types.ItemID) *mir.Body { return w.bodies[id] })
	w.mod = w.items.AddModule(&types.Module{Path: "m"})
	return w
}

func (w *world) Table() *types.Table            { return w.tab }
func (w *world) Items() *types.Items            { return w.items }
func (w *world) Consts() *types.ConstTable      { return w.consts }
func (w *world) Body(id types.ItemID) *mir.Body { return w.bodies[id] }
func (w *world) Hasher() *iface.Hasher          { return w.h }
func (w *world) World() iface.World             { return stubWorld{} }
func (w *world) OnerrRuns(item types.ItemID, b mir.BlockID) bool {
	if v, ok := w.onerr[onerrKey{item, b}]; ok {
		return v
	}
	return true
}
func (w *world) RuntimeEntry(k types.Key) (types.ItemID, bool) {
	return w.items.ByKey(k)
}

func (w *world) ImplMethod(i types.ItemID, m uint32, self types.TypeID) (types.ItemID, []types.GenericArg, bool) {
	f, ok := w.impls[implKey{i, m, self}]
	return f, nil, ok
}

// Layout gives every type a size from its kind: enough for SizeOf folding.
func (w *world) Layout(t types.TypeID) *types.Layout {
	ty := w.tab.Get(t)
	var size uint64 = 8
	switch ty.Kind {
	case types.KInt, types.KFloat:
		size = uint64(ty.Bits) / 8
	case types.KBool:
		size = 1
	case types.KChar:
		size = 4
	case types.KSlice, types.KMutSlice, types.KStr:
		size = 16
	case types.KArray:
		n := types.LoadUint(w.consts.Get(ty.Len.Value).Bytes, 8)
		size = n * w.Layout(ty.Elem).Shape.Size
	}
	return &types.Layout{Shape: types.Shape{Size: size, Align: uint32(min(size, 8))}}
}

// stubWorld answers every part with a hash of the read itself.
type stubWorld struct{}

func (stubWorld) Part(r types.Read) (source.Hash, bool) {
	return source.HashString("mono.test.world", fmt.Sprint(r.Kind, r.Key.String(), r.Name)), true
}

// span is a fresh source span, distinct for every call.
func (w *world) span() source.Span {
	w.pos += 10
	return source.Span{File: 1, Start: w.pos, End: w.pos + 5}
}

// fn declares a function item with generics (type parameters named T0..),
// params and a result; its body is set separately.
func (w *world) fn(name string, generics int, params []types.TypeID, result types.TypeID) types.ItemID {
	return w.fnWith(name, generics, params, nil, result)
}

// fnWith is fn with some parameters const (consts[i] marks params[i]).
func (w *world) fnWith(name string, generics int, params []types.TypeID, consts []bool, result types.TypeID) types.ItemID {
	id := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", name), Name: w.in.Intern(name), Kind: types.KindFunc})
	sig := types.Signature{Item: id, Name: w.in.Intern(name), Result: result, Span: w.span()}
	for i := 0; i < generics; i++ {
		sig.Generics = append(sig.Generics, types.GenericParam{Name: w.in.Intern(fmt.Sprintf("T%d", i))})
	}
	sig.OwnGenerics = uint32(generics)
	for i, p := range params {
		sig.Params = append(sig.Params, types.Param{Name: w.in.Intern(fmt.Sprintf("p%d", i)), Type: p, Const: i < len(consts) && consts[i]})
	}
	w.items.Get(id).Decl = &types.FuncDecl{Sig: sig}
	return id
}

// extern declares a bodiless extern function.
func (w *world) extern(name string, params []types.TypeID, result types.TypeID) types.ItemID {
	id := w.fn(name, 0, params, result)
	fd := w.items.Get(id).Decl.(*types.FuncDecl)
	fd.Sig.Extern, fd.Bodiless = true, true
	return id
}

// literal declares a fn literal item of the enclosing function, with
// generics copied from it.
func (w *world) literal(enclosing types.ItemID, n int, params []types.TypeID, result types.TypeID) types.ItemID {
	en := w.items.Get(enclosing)
	id := w.items.AddItem(&types.Item{Module: w.mod, Owner: enclosing, Key: types.LiteralKey(en.Key, n), Kind: types.KindLiteral})
	sig := types.Signature{Item: id, Result: result, Span: w.span()}
	for i, p := range params {
		sig.Params = append(sig.Params, types.Param{Name: w.in.Intern(fmt.Sprintf("p%d", i)), Type: p})
	}
	w.items.Get(id).Decl = &types.FuncDecl{Sig: sig}
	return id
}

// fb builds one function body block by block.
type fb struct {
	w   *world
	b   *mir.Body
	cur mir.BlockID
}

// body starts the body of item: its parameters as locals 1..n.
func (w *world) body(item types.ItemID) *fb {
	fd := w.items.Get(item).Decl.(*types.FuncDecl)
	sig := &fd.Sig
	b := mir.NewBody(item, w.span(), sig.ReturnType(w.tab))
	b.GenericCount = uint32(len(sig.Generics))
	for _, p := range sig.Params {
		b.AddParam(p.Type, p.Name, 0)
	}
	f := &fb{w: w, b: b}
	f.cur = b.AddBlock(mir.OriginBody, mir.NoDefer)
	w.bodies[item] = b
	return f
}

func (f *fb) temp(t types.TypeID) mir.LocalID {
	return f.b.AddLocal(mir.Local{Type: t, Kind: mir.LocalTemp, Loop: mir.NoLoop, Span: 0})
}

func (f *fb) assign(l mir.LocalID, rv mir.Rvalue) {
	sp := f.b.AddSpan(f.w.span())
	blk := &f.b.Blocks[f.cur]
	blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StAssign, Place: f.b.Place(l), Rvalue: rv, Span: sp})
}

// call ends the current block with a direct call of callee at gargs and
// continues in a new block; it returns the call's span ID.
func (f *fb) call(callee types.ItemID, gargs []types.GenericArg, dest mir.LocalID, args ...mir.Arg) mir.SpanID {
	return f.callTerm(mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: callee, Args: f.b.AddGenericArgs(gargs...)}}, dest, mir.NoLocal, args...)
}

func (f *fb) callTerm(c mir.Callee, dest, spawn mir.LocalID, args ...mir.Arg) mir.SpanID {
	sp := f.b.AddSpan(f.w.span())
	next := f.b.AddBlock(mir.OriginBody, mir.NoDefer)
	f.b.Blocks[f.cur].Term = mir.Terminator{Kind: mir.TermCall, Span: sp, Callee: c, Args: f.b.AddArgs(args...),
		Dest: f.b.Place(dest), Target: next, Spawn: spawn}
	f.cur = next
	return sp
}

// ret ends the body with a normal return.
func (f *fb) ret() *mir.Body {
	f.b.Blocks[f.cur].Term = mir.Terminator{Kind: mir.TermReturn, Span: f.b.AddSpan(f.w.span()), Exit: mir.ExitNormal}
	return f.b
}

func arg(o mir.Operand) mir.Arg { return mir.Arg{Operand: o, Mode: mir.ArgCopy} }

func copyOf(b *mir.Body, l mir.LocalID) mir.Operand { return mir.Copy(b.Place(l)) }

func ty(t types.TypeID) types.GenericArg { return types.TypeArg(t) }

// leaf declares a non-generic function that returns void.
func (w *world) leaf(name string) types.ItemID {
	id := w.fn(name, 0, nil, types.Void)
	w.body(id).ret()
	return id
}

// collect runs Collect with validation on.
func (w *world) collect(t *testing.T, opt Options, roots ...Root) (*Instances, []*diag.Diagnostic) {
	t.Helper()
	opt.Validate = true
	var in *Instances
	var ds []*diag.Diagnostic
	if ie := source.Guard("mono test", source.Span{}, func() { in, ds = Collect(w, roots, opt) }); ie != nil {
		t.Fatalf("Collect: %s\n%s", ie, ie.Stack)
	}
	return in, ds
}

func mainRoot(id types.ItemID) Root { return Root{Kind: RootMain, Item: id} }

// mustICE runs f and reports the mono ICE it raises.
func mustICE(t *testing.T, want string, f func()) {
	t.Helper()
	ie := source.Guard("mono test", source.Span{}, f)
	if ie == nil {
		t.Fatalf("no ICE, want %q", want)
	}
	if !strings.Contains(ie.What, want) {
		t.Fatalf("ICE %q, want it to contain %q", ie.What, want)
	}
}
