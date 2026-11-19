// Why: gen's unit tests run generators without the rest of the compiler, so
// they need a world built by hand.
// What: session tables, std-free declarations of the generator API under
// std.core's keys (apiDecls), a fake eval.Program serving hand-built MIR and
// layouts, a fake World over declaration views, the bb MIR builder, and
// encodeTemplate, a quote-template encoder from Mk text with $N holes.
// How: layouts are computed as the compiler does (scalars, structs in field
// order); every body is validated before it is served.

package gen

import (
	"encoding/binary"
	"flag"
	"fmt"
	"strings"
	"sync"
	"testing"

	"mk/internal/ast"
	"mk/internal/eval"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden")

type world struct {
	t      testing.TB
	in     *source.Interner
	items  *types.Items
	consts *types.ConstTable
	tab    *types.Table
	sm     *source.SourceMap
	mods   map[string]types.ModuleID
	known  types.KnownItems
	api    map[string]types.ItemID // "Gen:Emit" -> the intrinsic

	mu      sync.Mutex
	bodies  map[types.ItemID]*eval.Body
	layouts map[types.TypeID]*types.Layout
	views   map[types.ItemID]*DeclView
	// version is mixed into World.Hash per (item, kind, name), so a test
	// can change one view's hash.
	version map[string]int
	spanN   uint32
	file    *source.File
	// evalArena is Config.EvalArena once arenaFn declared it.
	evalArena types.ItemID
}

// apiDecls is the generator API as (owner, method, receiver, params...,
// result) with type names resolved by typeNamed.
var apiDecls = [][]string{
	{"Gen", "Emit", "mut", "Code", "void"},
	{"Gen", "Block", "&", "Block"},
	{"Gen", "Arena", "&", "&Arena"},
	{"Gen", "Fail", "mut", "str", "void"},
	{"Gen", "FailAt", "mut", "Field", "str", "void"},
	{"Gen", "FailOn", "mut", "Type", "str", "void"},
	{"Gen", "Fresh", "mut", "str", "Code"},
	{"Gen", "Ident", "&", "str", "Code"},
	{"Gen", "Str", "&", "str", "Code"},
	{"Block", "Add", "mut", "Code", "void"},
	{"Code", "Then", "val", "Code", "Code"},
	{"Type", "Name", "&", "Code"},
	{"Type", "Text", "&", "str"},
	{"Type", "Kind", "&", "TypeKind"},
	{"Type", "Exported", "&", "bool"},
	{"Type", "Same", "&", "Type", "bool"},
	{"Type", "Args", "&", "[]Type"},
	{"Type", "Elem", "&", "Type"},
	{"Type", "Fields", "&", "[]Field"},
	{"Type", "Variants", "&", "[]Variant"},
	{"Type", "Methods", "&", "[]Method"},
	{"Field", "Name", "&", "str"},
	{"Field", "Type", "&", "Type"},
	{"Field", "Exported", "&", "bool"},
	{"Variant", "Name", "&", "str"},
	{"Variant", "Fields", "&", "[]Field"},
	{"Method", "Name", "&", "str"},
	{"Method", "Receiver", "&", "Receiver"},
	{"Method", "Params", "&", "[]Param"},
	{"Method", "Result", "&", "Type"},
	{"Method", "Generic", "&", "bool"},
	{"Method", "HasFlows", "&", "bool"},
	{"Method", "Flows", "&", "[]Flow"},
	{"Flow", "Kind", "&", "FlowKind"},
	{"Flow", "Param", "&", "str"},
	{"Flow", "Path", "&", "str"},
	{"Flow", "Field", "&", "str"},
	{"Gen", "Input", "&", "str", "[]u8"},
	{"Method", "Signature", "&", "Code"},
	{"Param", "Name", "&", "str"},
	{"Param", "Type", "&", "Type"},
	{"Param", "Const", "&", "bool"},
}

func newWorld(t testing.TB) *world {
	in := source.NewInterner()
	items := types.NewItems()
	consts := types.NewConstTable()
	w := &world{t: t, in: in, items: items, consts: consts, tab: types.NewTable(in, items, consts),
		sm: source.NewSourceMap(), mods: map[string]types.ModuleID{}, api: map[string]types.ItemID{},
		bodies: map[types.ItemID]*eval.Body{}, layouts: map[types.TypeID]*types.Layout{},
		views: map[types.ItemID]*DeclView{}, version: map[string]int{}}
	// Lines of 40 bytes, so a span at offset 10·n renders as line n/4+1
	// in the JSON snapshots.
	w.file = w.sm.Add("main/main.mk", []byte(strings.Repeat("// placeholder line for gen test spans.\n", 2500)))
	w.module("main")
	handle := func(name string) types.ItemID {
		id := w.item("std.core", name, types.KindStruct, &types.StructDecl{Fields: []types.FieldDecl{{Name: in.Intern("h"), Type: types.U32}}})
		w.items.Get(id).Exported = true
		return id
	}
	k := &w.known
	k.Gen, k.TypeInfo, k.Field, k.Variant = handle("Gen"), handle("Type"), handle("Field"), handle("Variant")
	k.Method, k.Param, k.Code, k.Block = handle("Method"), handle("Param"), handle("Code"), handle("Block")
	k.Flow = handle("Flow")
	k.Arena = handle("Arena")
	enum := func(name string, vs ...string) types.ItemID {
		d := &types.EnumDecl{}
		for i, v := range vs {
			d.Variants = append(d.Variants, types.VariantDecl{Name: in.Intern(v), Discr: uint64(i)})
		}
		return w.item("std.core", name, types.KindEnum, d)
	}
	k.TypeKind = enum("TypeKind", "Struct", "Enum", "Error", "Interface", "Record", "Scalar", "Ref", "Slice", "Array",
		"Optional", "Fallible", "Fn", "RawPtr", "Opaque")
	k.Receiver = enum("Receiver", "Static", "Value", "Ref", "Mut")
	k.FlowKind = enum("FlowKind", "From", "Into", "Ends", "Store")
	for _, d := range apiDecls {
		owner := w.typeNamed(d[0])
		fd := &types.FuncDecl{Bodiless: true, Intrinsic: true}
		switch d[2] {
		case "mut":
			fd.Sig.Receiver = types.ByMut
			fd.Sig.Params = append(fd.Sig.Params, types.Param{Name: in.Intern("self"), Type: w.tab.Mut(owner)})
		case "&":
			fd.Sig.Receiver = types.ByShared
			fd.Sig.Params = append(fd.Sig.Params, types.Param{Name: in.Intern("self"), Type: w.tab.Ref(owner)})
		default:
			fd.Sig.Receiver = types.ByValue
			fd.Sig.Params = append(fd.Sig.Params, types.Param{Name: in.Intern("self"), Type: owner})
		}
		for i, p := range d[3 : len(d)-1] {
			fd.Sig.Params = append(fd.Sig.Params, types.Param{Name: in.Intern(fmt.Sprintf("p%d", i)), Type: w.typeNamed(p)})
		}
		fd.Sig.Result = w.typeNamed(d[len(d)-1])
		m := w.module("std.core")
		id := w.items.AddItem(&types.Item{Module: m, Name: in.Intern(d[1]), Kind: types.KindFunc, Exported: true,
			Key: types.MemberKey("std.core", d[0], d[1]), Decl: fd, Span: w.span(), Owner: w.tab.Get(owner).Item})
		fd.Sig.Item, fd.Sig.Name = id, in.Intern(d[1])
		w.api[d[0]+":"+d[1]] = id
	}
	return w
}

// typeNamed resolves the few type spellings apiDecls uses.
func (w *world) typeNamed(s string) types.TypeID {
	switch s {
	case "void":
		return types.Void
	case "str":
		return types.Str
	case "bool":
		return types.Bool
	case "u8":
		return types.U8
	}
	if s[0] == '&' {
		return w.tab.Ref(w.typeNamed(s[1:]))
	}
	if s[:2] == "[]" {
		return w.tab.Slice(w.typeNamed(s[2:]))
	}
	id, ok := w.items.ByKey(types.ItemKey("std.core", s))
	if !ok {
		w.t.Fatalf("test world: no std.core.%s", s)
	}
	return w.tab.Named(id, nil)
}

func (w *world) module(path string) types.ModuleID {
	if id, ok := w.mods[path]; ok {
		return id
	}
	m := &types.Module{Path: path}
	if path == "main" {
		m.Files = []source.FileID{w.file.ID}
	}
	id := w.items.AddModule(m)
	w.mods[path] = id
	return id
}

func (w *world) item(mod, name string, kind types.ItemKind, d types.Decl) types.ItemID {
	return w.items.AddItem(&types.Item{Module: w.module(mod), Name: w.in.Intern(name), Kind: kind,
		Key: types.ItemKey(mod, name), Decl: d, Span: w.span()})
}

func (w *world) span() source.Span {
	w.spanN++
	return source.Span{File: w.file.ID, Start: w.spanN * 10, End: w.spanN*10 + 5}
}

// structType declares a struct with named fields (name, type) and its view;
// exported marks the struct, and a field name starting upper-case is
// exported.
func (w *world) structType(mod, name string, exported bool, fields ...any) (types.ItemID, types.TypeID) {
	d := &types.StructDecl{}
	v := &DeclView{Kind: types.KindStruct, Exported: exported}
	for i := 0; i+1 < len(fields); i += 2 {
		fn := fields[i].(string)
		fv := FieldView{Name: w.in.Intern(fn), Exported: fn[0] >= 'A' && fn[0] <= 'Z', Span: w.span()}
		switch ft := fields[i+1].(type) {
		case types.TypeID:
			fv.Type = ft
			d.Fields = append(d.Fields, types.FieldDecl{Name: fv.Name, Type: ft})
		case string: // an unresolved name
			fv.Unresolved = w.in.Intern(ft)
		}
		v.Fields = append(v.Fields, fv)
	}
	id := w.item(mod, name, types.KindStruct, d)
	w.items.Get(id).Exported = exported
	v.Item = id
	w.views[id] = v
	return id, w.tab.Named(id, nil)
}

func (w *world) intVal(t types.TypeID, x int64) types.ConstID {
	n := 8
	if tt := w.tab.Get(t); tt.Kind == types.KInt {
		n = int(tt.Bits / 8)
	}
	b := make([]byte, n)
	types.StoreUint(b, n, uint64(x))
	return w.consts.Intern(types.ConstValue{Type: t, Align: uint32(n), Bytes: b})
}

// fn declares a function of module mod.
func (w *world) fn(mod, name string, result types.TypeID, params ...types.TypeID) types.ItemID {
	fd := &types.FuncDecl{}
	fd.Sig.Result = result
	for i, p := range params {
		fd.Sig.Params = append(fd.Sig.Params, types.Param{Name: w.in.Intern(fmt.Sprintf("p%d", i)), Type: p})
	}
	id := w.item(mod, name, types.KindFunc, fd)
	w.items.Get(id).Exported = true
	fd.Sig.Item, fd.Sig.Name = id, w.in.Intern(name)
	return id
}

// generator declares Name(g: mut Gen, params...) in module mod.
func (w *world) generator(mod, name string, params ...types.TypeID) types.ItemID {
	return w.fn(mod, name, types.Void, append([]types.TypeID{w.genMut()}, params...)...)
}

func (w *world) genMut() types.TypeID { return w.tab.Mut(w.tab.Named(w.known.Gen, nil)) }
func (w *world) typeT() types.TypeID  { return w.tab.Named(w.known.TypeInfo, nil) }
func (w *world) named(k types.ItemID) types.TypeID {
	return w.tab.Named(k, nil)
}

func (w *world) context(workers int) *Context {
	e := eval.New(eval.Config{Types: w.tab, Items: w.items, Consts: w.consts, Interner: w.in, Program: w,
		Known: w.known, Target: types.X86_64Windows, EvalArena: w.evalArena})
	return &Context{Types: w.tab, Items: w.items, Consts: w.consts, Interner: w.in, Sources: w.sm, Eval: e,
		World: w, Known: w.known, Target: types.X86_64Windows, Workers: workers}
}

// line is a gen line of module main with type arguments.
func (w *world) line(n int, path string, gen types.ItemID, args ...Arg) Line {
	id := w.items.AddItem(&types.Item{Module: w.module("main"), Kind: types.KindGen,
		Key: types.GenKey("main", int(w.spanN)+1000), Span: w.span(), Decl: &types.GenDecl{}})
	return Line{Item: id, Index: n, Span: w.span(), Path: path, Generator: gen, Args: args}
}

func typeArg(t types.TypeID) Arg { return Arg{Kind: ArgType, Type: t} }

// World.

func (w *world) Decl(item types.ItemID, from types.ModuleID) (*DeclView, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	v := w.views[item]
	if v == nil {
		return nil, false
	}
	if w.items.Get(item).Module == from {
		return v, true
	}
	cp := *v
	cp.Fields = nil
	for _, f := range v.Fields {
		if f.Exported {
			cp.Fields = append(cp.Fields, f)
		}
	}
	return &cp, true
}

func (w *world) Hash(item types.ItemID, k types.ReadKind, name string) source.Hash {
	w.mu.Lock()
	defer w.mu.Unlock()
	key := fmt.Sprintf("%s|%s|%s", w.items.Get(item).Key, k, name)
	return source.HashString("test.view", fmt.Sprintf("%s|%d", key, w.version[key]))
}

// Program.

func (w *world) Body(fn types.ItemID, args []types.GenericArg) eval.Body {
	w.mu.Lock()
	defer w.mu.Unlock()
	if b := w.bodies[fn]; b != nil && len(args) == 0 {
		return *b
	}
	return eval.Body{State: eval.BodyMissing}
}

func (w *world) Layout(t types.TypeID) (*types.Layout, source.Hash) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.layoutLocked(t), source.HashString("test.layout", w.tab.TypeString(t, nil))
}

func (w *world) LayoutName(args []types.GenericArg) string {
	if len(args) == 0 {
		return ""
	}
	return fmt.Sprint(args)
}

func (w *world) Const(item types.ItemID) eval.Const {
	if d, ok := w.items.Get(item).Decl.(*types.ConstDecl); ok && d.HasValue {
		return eval.Const{Value: d.Value}
	}
	return eval.Const{Poisoned: true}
}

func (w *world) ConstHash(c types.ConstID) source.Hash {
	v := w.consts.Get(c)
	return source.HashBytes("test.const", append(binary.LittleEndian.AppendUint32(nil, uint32(v.Type)), v.Bytes...))
}

func (w *world) TargetHash(f types.TargetField) source.Hash {
	return source.HashString("mk.target.v1", f.String())
}

func (w *world) layoutLocked(t types.TypeID) *types.Layout {
	if l := w.layouts[t]; l != nil {
		return l
	}
	l := w.compute(t)
	w.layouts[t] = l
	return l
}

func scalar(n uint64) *types.Layout {
	return &types.Layout{Shape: types.Shape{Size: n, Align: uint32(max(n, 1))}}
}

func (w *world) structOf(ts []types.TypeID) *types.Layout {
	l := &types.Layout{}
	off, align := uint64(0), uint32(1)
	for _, ft := range ts {
		fl := w.layoutLocked(ft)
		a := fl.Shape.Align
		off = (off + uint64(a) - 1) / uint64(a) * uint64(a)
		l.Fields = append(l.Fields, types.FieldLayout{Offset: off, Type: ft})
		off += fl.Shape.Size
		align = max(align, a)
	}
	l.Shape.Size, l.Shape.Align = (off+uint64(align)-1)/uint64(align)*uint64(align), align
	return l
}

func (w *world) compute(t types.TypeID) *types.Layout {
	ty := w.tab.Get(t)
	switch ty.Kind {
	case types.KVoid, types.KNever:
		return &types.Layout{Shape: types.Shape{Size: 0, Align: 1}}
	case types.KInt, types.KFloat:
		return scalar(uint64(ty.Bits / 8))
	case types.KIntPtr, types.KRef, types.KMut, types.KRawPtr, types.KFn:
		return scalar(8)
	case types.KBool:
		return scalar(1)
	case types.KChar:
		return scalar(4)
	case types.KStr, types.KSlice, types.KMutSlice:
		return &types.Layout{Shape: types.Shape{Size: 16, Align: 8}}
	case types.KArray:
		e := w.layoutLocked(ty.Elem)
		v := w.consts.Get(ty.Len.Value)
		n := types.LoadUint(v.Bytes, len(v.Bytes))
		return &types.Layout{Shape: types.Shape{Size: e.Shape.Size * n, Align: e.Shape.Align}}
	case types.KNamed:
		switch d := w.items.Get(ty.Item).Decl.(type) {
		case *types.StructDecl:
			var ts []types.TypeID
			for _, f := range d.Fields {
				ft := f.Type
				if p := w.tab.Get(ft); p.Kind == types.KParam && int(p.Index) < len(ty.Args) {
					ft = ty.Args[p.Index].Type
				}
				ts = append(ts, ft)
			}
			return w.structOf(ts)
		case *types.EnumDecl:
			l := &types.Layout{Shape: types.Shape{Size: 1, Align: 1}, HasTag: true, Tag: types.TagLayout{Size: 1}}
			for _, v := range d.Variants {
				l.Variants = append(l.Variants, types.VariantLayout{Discr: v.Discr})
			}
			return l
		}
	}
	w.t.Fatalf("test layout: no layout for %s", w.tab.TypeString(t, nil))
	return nil
}

// MIR builder.

type bb struct {
	w       *world
	b       *mir.Body
	pending []mir.Statement
}

// body starts the body of fn: its parameters are locals 1.., block 0 open.
func (w *world) body(fn types.ItemID) *bb {
	fd := w.items.Get(fn).Decl.(*types.FuncDecl)
	x := &bb{w: w, b: mir.NewBody(fn, w.span(), fd.Sig.Result)}
	for _, p := range fd.Sig.Params {
		x.b.AddParam(p.Type, p.Name, x.sp())
	}
	x.b.AddBlock(mir.OriginBody, mir.NoDefer)
	return x
}

func (x *bb) sp() mir.SpanID { return x.b.AddSpan(x.w.span()) }

func (x *bb) local(t types.TypeID) mir.LocalID {
	return x.b.AddLocal(mir.Local{Type: t, Kind: mir.LocalTemp, Loop: mir.NoLoop, Span: x.sp()})
}

func (x *bb) block() mir.BlockID { return x.b.AddBlock(mir.OriginBody, mir.NoDefer) }

func (x *bb) assign(blk mir.BlockID, p mir.Place, rv mir.Rvalue) {
	x.b.Blocks[blk].Stmts = append(x.b.Blocks[blk].Stmts, mir.Statement{Kind: mir.StAssign, Place: p, Rvalue: rv, Span: x.sp()})
}

func (x *bb) term(blk mir.BlockID, t mir.Terminator) {
	t.Span = x.sp()
	x.b.Blocks[blk].Term = t
}

func (x *bb) ret(blk mir.BlockID) { x.term(blk, mir.Terminator{Kind: mir.TermReturn}) }

func (x *bb) gotoB(blk, to mir.BlockID) { x.term(blk, mir.Terminator{Kind: mir.TermGoto, Target: to}) }

func (x *bb) branch(blk mir.BlockID, on mir.Operand, yes, no mir.BlockID) {
	x.term(blk, mir.Terminator{Kind: mir.TermSwitch, On: on, Cases: x.b.AddCases(mir.SwitchCase{Value: 1, Target: yes}), Default: no})
}

// call ends blk with a direct call of fn into a fresh local of fn's result
// type, and returns that local and the next block.
func (x *bb) call(blk mir.BlockID, fn types.ItemID, args ...mir.Operand) (mir.LocalID, mir.BlockID) {
	fd := x.w.items.Get(fn).Decl.(*types.FuncDecl)
	dest := x.local(fd.Sig.Result)
	next := x.block()
	var as []mir.Arg
	for _, a := range args {
		as = append(as, mir.Arg{Operand: a})
	}
	x.term(blk, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: fn}},
		Args: x.b.AddArgs(as...), Dest: lp(dest), Target: next, Spawn: mir.NoLocal})
	return dest, next
}

// api calls the generator API method "Owner:Name".
func (x *bb) api(blk mir.BlockID, name string, args ...mir.Operand) (mir.LocalID, mir.BlockID) {
	fn, ok := x.w.api[name]
	if !ok {
		x.w.t.Fatalf("no API method %s", name)
	}
	return x.call(blk, fn, args...)
}

// quote ends blk with BuiltinQuote of the template text (holes $0, $1, ...)
// over the splice operands.
func (x *bb) quote(blk mir.BlockID, text string, splices ...mir.Operand) (mir.LocalID, mir.BlockID) {
	dest := x.local(x.w.named(x.w.known.Code))
	next := x.block()
	as := []mir.Arg{{Operand: x.w.bytesConst(encodeTemplate(x.w.t, text))}}
	for _, s := range splices {
		as = append(as, mir.Arg{Operand: s})
	}
	x.term(blk, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallBuiltin, Builtin: mir.BuiltinQuote},
		Args: x.b.AddArgs(as...), Dest: lp(dest), Target: next, Spawn: mir.NoLocal})
	return dest, next
}

// ref is &local (or mut local), deref follows a reference local.
func (x *bb) ref(l mir.LocalID, excl bool) mir.LocalID {
	return x.refPlace(lp(l), x.b.Locals[l].Type, excl)
}

func (x *bb) refPlace(p mir.Place, t types.TypeID, excl bool) mir.LocalID {
	k, rt := mir.Shared, x.w.tab.Ref(t)
	if excl {
		k, rt = mir.Exclusive, x.w.tab.Mut(t)
	}
	r := x.local(rt)
	x.pending = append(x.pending, mir.Statement{Kind: mir.StAssign, Place: lp(r),
		Rvalue: mir.Rvalue{Kind: mir.RvRef, Borrow: k, Place: p}, Span: x.sp()})
	return r
}

func (x *bb) deref(l mir.LocalID) mir.Place {
	t := x.w.tab.Get(x.b.Locals[l].Type).Elem
	return x.b.Place(l, mir.Projection{Kind: mir.ProjDeref, Type: t})
}

// flush appends the pending reference statements to blk.
func (x *bb) flush(blk mir.BlockID) {
	x.b.Blocks[blk].Stmts = append(x.b.Blocks[blk].Stmts, x.pending...)
	x.pending = nil
}

func (x *bb) done() {
	x.w.t.Helper()
	if err := x.b.Validate(&mir.Env{Types: x.w.tab}); err != nil {
		x.w.t.Fatalf("invalid test MIR: %v\n%s", err, (&mir.Printer{Types: x.w.tab, Items: x.w.items}).Print(x.b))
	}
	x.w.mu.Lock()
	x.w.bodies[x.b.Item] = &eval.Body{MIR: x.b, Hash: source.HashString("test.body", x.w.items.Get(x.b.Item).Key.String()),
		State: eval.BodyReady}
	x.w.mu.Unlock()
}

func lp(l mir.LocalID) mir.Place { return mir.LocalPlace(l) }

func cp(l mir.LocalID) mir.Operand { return mir.Copy(lp(l)) }

func use(o mir.Operand) mir.Rvalue { return mir.Rvalue{Kind: mir.RvUse, A: o} }

// bytesConst is a []u8 constant operand (lower's form).
func (w *world) bytesConst(bs []byte) mir.Operand {
	t := w.tab.Slice(types.U8)
	v := types.ConstValue{Type: t, Align: 8, Bytes: make([]byte, 16)}
	types.StoreUint(v.Bytes[8:], 8, uint64(len(bs)))
	if len(bs) > 0 {
		arr := w.tab.Array(types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, int64(len(bs)))}, types.U8)
		data := w.consts.Intern(types.ConstValue{Type: arr, Align: 1, Bytes: append([]byte(nil), bs...)})
		v.Relocs = []types.Reloc{{Offset: 0, Kind: types.RelocData, Data: data}}
	}
	return mir.Const(t, w.consts.Intern(v))
}

// strConst is a str constant operand.
func (w *world) strConst(s string) mir.Operand {
	c := (&Context{Types: w.tab, Consts: w.consts}).strConst([]byte(s))
	return mir.Const(types.Str, c)
}

// encodeTemplate writes a quote template from Mk text: trivia dropped,
// newlines kept, "$N" a hole for splice N.
func encodeTemplate(t testing.TB, text string) []byte {
	toks, ds := syntax.Lex(0, text)
	if len(ds) > 0 {
		t.Fatalf("template %q: %s", text, ds[0].Message)
	}
	var body []byte
	n := 0
	for i := 0; i < len(toks); i++ {
		tk := toks[i]
		switch {
		case (tk.Kind.IsTrivia() && tk.Kind != ast.TokNewline) || tk.Kind == ast.TokTerm || tk.Kind == ast.TokEOF:
			continue
		case tk.Kind == ast.TokDollar:
			j := i + 1
			for toks[j].Kind == ast.TokWhitespace {
				j++
			}
			v, ok := syntax.IntValue(toks[j].Text(text))
			if !ok {
				t.Fatalf("template %q: $ without an index", text)
			}
			body = binary.AppendUvarint(body, uint64(ast.TokDollar))
			body = binary.AppendUvarint(body, v)
			i = j
		default:
			body = binary.AppendUvarint(body, uint64(tk.Kind))
			if hasText(tk.Kind) {
				s := tk.Text(text)
				body = binary.AppendUvarint(body, uint64(len(s)))
				body = append(body, s...)
			}
		}
		n++
	}
	return append(append([]byte(quoteMagic), binary.AppendUvarint(nil, uint64(n))...), body...)
}
