// Why: the range pass runs inside Lower on a generic body, before mono
// unrolls its fields loops, resolves its type tests and substitutes its
// const arguments, so every fact it uses must hold on every instance.
// What: TestGenericAgrees, TestFieldsRule8, TestInstanceModel,
// TestSplitShapes, TestAsmDiscardedOutput, and pcInstance.
// How: eval runs instance bodies only, and lower's tests cannot reach mono
// (archtest TestEdges), so pcInstance builds the instance bodies from the
// MIR contract's description of what mono does; pass on against pass off
// under eval is then the oracle, as in TestPC_MutationCaught.

package lower

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"mk/internal/eval"
	"mk/internal/mir"
	"mk/internal/types"
)

// pcInstance is the instance body of generic body g for args, built as
// the contract says mono builds it, for the forms these tests use and no
// other (anything else fails t): a local or rvalue type that is a type
// parameter, a reference to one or an array of a const-parameter length
// is substituted; RvConstArg becomes its value; a type test goes to Yes or
// No; and a generic fields loop (blocks exactly [Header, Exit), latch
// Header+2 ending goto Exit) is unrolled once per field of the
// instance's type, every field of a record and the exported fields of a
// struct, each copy's latch going to the next copy's
// header and the last to Exit, and with no field the edges into the
// header go to Exit. Its loop's value must be blank: no FieldRef, no
// hidden-generic local. Locals stay shared between copies, as mono keeps
// every local whose type does not mention the hidden generic.
func pcInstance(t testing.TB, l *lowered, g *mir.Body, args []types.GenericArg) *mir.Body {
	t.Helper()
	tab := l.w.tab
	var subst func(ty types.TypeID) types.TypeID
	substArgs := func(as []types.GenericArg) []types.GenericArg {
		out := make([]types.GenericArg, len(as))
		for i, a := range as {
			if a.IsConst {
				if a.Const.Kind == types.CAParam {
					a.Const = args[a.Const.Param].Const
				}
			} else {
				a.Type = subst(a.Type)
			}
			out[i] = a
		}
		return out
	}
	subst = func(ty types.TypeID) types.TypeID {
		if ty == types.NoType {
			return ty
		}
		switch x := tab.Get(ty); x.Kind {
		case types.KParam:
			if int(x.Index) < len(args) && !args[x.Index].IsConst {
				return args[x.Index].Type
			}
		case types.KRef:
			return tab.Ref(subst(x.Elem))
		case types.KArray:
			n := x.Len
			if n.Kind == types.CAParam {
				n = args[n.Param].Const
			}
			return tab.Array(n, subst(x.Elem))
		case types.KNamed:
			// A named type's own arguments (std.simd's Mask[u8, N]).
			if len(x.Args) > 0 {
				return tab.Named(x.Item, substArgs(x.Args))
			}
		}
		if s := tab.TypeString(ty, nil); strings.Contains(s, "$") {
			t.Fatalf("pcInstance: type %s is not one this model substitutes", s)
		}
		return ty
	}
	constArg := func(c types.ConstArg) types.ConstArg {
		if c.Kind == types.CAParam {
			return args[c.Param].Const
		}
		return c
	}
	b := *g
	b.GenericCount = 0
	b.Locals = append([]mir.Local(nil), g.Locals...)
	for i := range b.Locals {
		b.Locals[i].Type = subst(b.Locals[i].Type)
	}
	// The places the body uses keep their projections: none may name a
	// fields loop's field or a generic type (lower makes the loop's field
	// projection even for a blank value, unused).
	place := func(pl mir.Place) {
		for _, pr := range g.Proj(pl) {
			if s := tab.TypeString(pr.Type, nil); strings.Contains(s, "$") || pr.FieldLoop {
				t.Fatalf("pcInstance: a projection of type %s (a fields loop's value must be blank)", s)
			}
		}
	}
	op := func(o mir.Operand) {
		if o.Kind <= mir.OpMove {
			place(o.Place)
		}
	}
	for i := range g.Blocks {
		for _, s := range g.Blocks[i].Stmts {
			if s.Kind == mir.StAssign {
				place(s.Place)
				place(s.Rvalue.Place)
				op(s.Rvalue.A)
				op(s.Rvalue.B)
				for _, o := range g.Operands[s.Rvalue.Ops.Start:s.Rvalue.Ops.End()] {
					op(o)
				}
			}
		}
		switch tm := &g.Blocks[i].Term; tm.Kind {
		case mir.TermSwitch:
			op(tm.On)
		case mir.TermCall:
			place(tm.Dest)
			for _, a := range g.Args[tm.Args.Start:tm.Args.End()] {
				op(a.Operand)
			}
		}
	}
	// An unused projection naming the hidden generic gets a closed
	// stand-in, as mono gives a dead range's (types.Void).
	b.Projections = append([]mir.Projection(nil), g.Projections...)
	for i := range b.Projections {
		if pr := &b.Projections[i]; strings.Contains(tab.TypeString(pr.Type, nil), "$") {
			pr.Type, pr.FieldLoop = types.Void, false
		}
	}
	b.GenericArgs = append([]types.GenericArg(nil), g.GenericArgs...)
	b.Cases = append([]mir.SwitchCase(nil), g.Cases...)
	b.Loops = append([]mir.LoopInfo(nil), g.Loops...)
	b.Blocks = make([]mir.Block, len(g.Blocks))
	for i := range g.Blocks {
		blk := g.Blocks[i]
		blk.Stmts = append([]mir.Statement(nil), blk.Stmts...)
		for j := range blk.Stmts {
			s := &blk.Stmts[j]
			if s.Kind != mir.StAssign {
				continue
			}
			r := &s.Rvalue
			switch r.Kind {
			case mir.RvConstArg:
				c := constArg(r.Len)
				v := l.w.consts.Get(c.Value)
				ty := subst(r.Type)
				*r = mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(ty, types.LoadUint(v.Bytes, len(v.Bytes))), Type: ty}
			case mir.RvTypeIs, mir.RvSizeOf, mir.RvAlignOf, mir.RvFieldCount, mir.RvFieldName:
				t.Fatalf("pcInstance: rvalue kind %d is not modelled", r.Kind)
			default:
				r.Type = subst(r.Type)
				r.Len = constArg(r.Len)
			}
		}
		switch tm := &blk.Term; tm.Kind {
		case mir.TermTypeTest:
			to := tm.No
			if args[tm.Param].Type == subst(tm.Type) {
				to = tm.Yes
			}
			blk.Term = mir.Terminator{Kind: mir.TermGoto, Target: to, Span: tm.Span}
		case mir.TermCall:
			// A direct call's generic arguments are substituted, as mono
			// does; one whose argument is still open after that (a type
			// built on a parameter this model does not substitute) fails.
			if ga := g.GenericArgs[tm.Callee.Fn.Args.Start:tm.Callee.Fn.Args.End()]; len(ga) > 0 {
				tm.Callee.Fn.Args = mir.Range{Start: uint32(len(b.GenericArgs)), Len: uint32(len(ga))}
				b.GenericArgs = append(b.GenericArgs, substArgs(ga)...)
			}
			for _, a := range b.GenericArgs[tm.Callee.Fn.Args.Start:tm.Callee.Fn.Args.End()] {
				if s := tab.TypeString(a.Type, nil); strings.Contains(s, "$") || a.Const.Kind == types.CAParam {
					t.Fatalf("pcInstance: a call with a generic argument")
				}
			}
		}
		b.Blocks[i] = blk
	}
	for li := range b.Loops {
		lp := b.Loops[li]
		if lp.FieldsOf == types.NoType {
			continue
		}
		pcUnroll(t, l, &b, lp, pcLoopFields(t, l, subst(lp.FieldsOf)))
		b.Loops[li].FieldsOf, b.Loops[li].FieldsParam = types.NoType, 0
	}
	return &b
}

// pcLoopFields is how many fields a fields loop over ty visits: every
// field of a record, the exported fields of a struct.
func pcLoopFields(t testing.TB, l *lowered, ty types.TypeID) int {
	t.Helper()
	switch x := l.w.tab.Get(ty); x.Kind {
	case types.KRecord:
		return len(x.Fields)
	case types.KNamed:
		sd, ok := l.w.items.Get(x.Item).Decl.(*types.StructDecl)
		if !ok {
			t.Fatalf("pcLoopFields: %s is not a struct", l.w.tab.TypeString(ty, nil))
		}
		n := 0
		for _, f := range sd.Fields {
			if f.Exported {
				n++
			}
		}
		return n
	}
	t.Fatalf("pcLoopFields: %s", l.w.tab.TypeString(ty, nil))
	return 0
}

// pcUnroll is mono's unrolling of fields loop lp into n copies of its
// range, in place in b.
func pcUnroll(t testing.TB, l *lowered, b *mir.Body, lp mir.LoopInfo, n int) {
	t.Helper()
	h, e := lp.Header, lp.Exit
	latch := h + 2
	if latch >= e || b.Blocks[latch].Term.Kind != mir.TermGoto || b.Blocks[latch].Term.Target != e {
		t.Fatalf("pcUnroll: bb%d is not the latch of a fields loop to bb%d", latch, e)
	}
	if n == 0 {
		for x := range b.Blocks {
			if tm := &b.Blocks[x].Term; (mir.BlockID(x) < h || mir.BlockID(x) >= e) && tm.Kind == mir.TermGoto && tm.Target == h {
				tm.Target = e
			}
		}
		return
	}
	size := e - h
	heads := make([]mir.BlockID, n+1)
	heads[0], heads[n] = h, e
	base := mir.BlockID(len(b.Blocks))
	for c := 1; c < n; c++ {
		heads[c] = base + mir.BlockID(c-1)*size
	}
	for c := 1; c < n; c++ {
		move := func(x mir.BlockID) mir.BlockID {
			if x >= h && x < e {
				return heads[c] + (x - h)
			}
			return x
		}
		for x := h; x < e; x++ {
			blk := b.Blocks[x]
			blk.Stmts = append([]mir.Statement(nil), blk.Stmts...)
			pcRetarget(b, &blk.Term, move)
			if x == latch {
				blk.Term.Target = heads[c+1]
			}
			b.Blocks = append(b.Blocks, blk)
		}
	}
	b.Blocks[latch].Term.Target = heads[1]
}

// pcRetarget maps every successor of tm through f; a switch gets its own
// copy of its cases.
func pcRetarget(b *mir.Body, tm *mir.Terminator, f func(mir.BlockID) mir.BlockID) {
	switch tm.Kind {
	case mir.TermGoto:
		tm.Target = f(tm.Target)
	case mir.TermSwitch:
		start := uint32(len(b.Cases))
		for _, c := range b.Cases[tm.Cases.Start:tm.Cases.End()] {
			c.Target = f(c.Target)
			b.Cases = append(b.Cases, c)
		}
		tm.Cases.Start = start
		if tm.Default != mir.NoBlock {
			tm.Default = f(tm.Default)
		}
	case mir.TermCall:
		tm.Target = f(tm.Target)
		if tm.HasErr {
			tm.Err.Block = f(tm.Err.Block)
		}
	case mir.TermTypeTest, mir.TermOnerrGuard:
		tm.Yes, tm.No = f(tm.Yes), f(tm.No)
	}
}

// pcInstProg is evalProg with generic bodies instantiated by pcInstance.
type pcInstProg struct {
	*evalProg
	t     testing.TB
	insts map[string]*mir.Body
}

func (p *pcInstProg) Body(fn types.ItemID, args []types.GenericArg) eval.Body {
	k, ok := p.byItem[fn]
	if !ok {
		return eval.Body{State: eval.BodyMissing}
	}
	g := p.l.bodies[k]
	if len(args) == 0 {
		return eval.Body{MIR: g, State: eval.BodyReady}
	}
	key := fmt.Sprint(k, args)
	b := p.insts[key]
	if b == nil {
		b = pcInstance(p.t, p.l, g, args)
		p.insts[key] = b
	}
	return eval.Body{MIR: b, State: eval.BodyReady}
}

// pcEvalInst lowers src with tweak (and the post-borrow bounds fold) and
// evaluates its H(): int over instance bodies; the result is its value or
// its panic's code and span.
func pcEvalInst(t *testing.T, src string, tweak func(*Config)) string {
	t.Helper()
	l := pcLower(t, src, pcOpt{fold: true, tweak: tweak})
	p := &pcInstProg{evalProg: &evalProg{l: l, byItem: map[types.ItemID]string{}}, t: t, insts: map[string]*mir.Body{}}
	var h types.ItemID
	for _, id := range l.b.m.Units() {
		k := l.b.key(id)
		p.byItem[id] = k
		if k == "H" {
			h = id
		}
	}
	ev := eval.New(eval.Config{Types: l.w.tab, Items: l.w.items, Consts: l.w.consts, Interner: l.w.in, Program: p, Known: *l.w.cfg.Known})
	r := ev.Eval(&types.EvalRequest{What: types.EvalCall, Item: h, Fuel: 1 << 22, Stage: types.StageBodies, Module: l.b.id})
	if r.Err != nil {
		return fmt.Sprintf("error %d panic %d at %s", r.Err.Kind, r.Err.Panic, l.w.lc(r.Err.At))
	}
	v := l.w.consts.Get(r.Value)
	return fmt.Sprintf("%d", int64(binary.LittleEndian.Uint64(v.Bytes)))
}

// pcFieldStructs are the structs whose exported fields a fields loop
// visits: Z none, P two of three.
const pcFieldStructs = "struct Z {\n\tx: int\n}\nstruct P {\n\texport a: int\n\tb: int\n\texport c: int\n}\n"

// pcFieldArg is an argument whose fields loop runs n times: Z for 0, P for
// 2, else a record of n int fields.
func pcFieldArg(n int) string {
	switch n {
	case 0:
		return "Z(x: 1)"
	case 2:
		return "P(a: 1, b: 2, c: 3)"
	}
	var b strings.Builder
	b.WriteString("(")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "f%d: 1", i)
	}
	b.WriteString(")")
	return b.String()
}

// pcFieldCounts are the field counts each fields program runs with:
// 0, 1, 3, 300 and 2.
var pcFieldCounts = []int{0, 1, 2, 3, 300}

// pcGeneric are generic programs whose checks depend on how often a fields
// loop runs (%s is G's argument), on a type test or on a const argument
// (%s is H's call). Each has a run that panics with the pass right, so
// each tests a check rather than a value.
var pcGeneric = []struct {
	name, src string
	calls     []string // nil: one run per pcFieldCounts
}{
	// The add runs once per field; u8 overflows at the third.
	{name: "fields/add", src: "G[A: Fields[Format] + Plain](args: A): u8 {\n\tvar n: u8 = 0\n\tfor _, _ in fields(&args) {\n\t\tn += 100\n\t}\n\treturn n\n}\nH(): int {\n\treturn int(G(%s))\n}\n"},
	// The cast: k grows by 200 a field; Cast[u8] fails from two.
	{name: "fields/cast", src: "G[A: Fields[Format] + Plain](args: A): u8 {\n\tvar k: int = 0\n\tfor _, _ in fields(&args) {\n\t\tk += 200\n\t}\n\treturn Cast[u8](k)\n}\nH(): int {\n\treturn int(G(%s))\n}\n"},
	// With no field the body never runs: n is still 0 after the loop.
	{name: "fields/zero", src: "G[A: Fields[Format] + Plain](args: A): u8 {\n\tvar n: u8 = 0\n\tfor _, _ in fields(&args) {\n\t\tn = 5\n\t}\n\treturn n - 1\n}\nH(): int {\n\treturn int(G(%s))\n}\n"},
	// A counted loop inside a fields loop: the inner sum restarts per
	// field, the outer one does not (i16: three fields overflow).
	{name: "fields/nested", src: "G[A: Fields[Format] + Plain](args: A): i16 {\n\tvar t: i16 = 0\n\tfor _, _ in fields(&args) {\n\t\tvar s: i16 = 0\n\t\tfor _ in 0..100 {\n\t\t\ts += 1\n\t\t}\n\t\tt += s * 100\n\t}\n\treturn t\n}\nH(): int {\n\treturn int(G(%s))\n}\n"},
	// A break on the second field: n saw at most one add of 200, which
	// the add after the loop must still check.
	{name: "fields/break", src: "G[A: Fields[Format] + Plain](args: A): u8 {\n\tvar n: u8 = 0\n\tvar c := 0\n\tfor _, _ in fields(&args) {\n\t\tc += 1\n\t\tif c == 2 {\n\t\t\tbreak\n\t\t}\n\t\tn += 200\n\t}\n\treturn n + 100\n}\nH(): int {\n\treturn int(G(%s))\n}\n"},
	// A continue skips the add on odd fields.
	{name: "fields/continue", src: "G[A: Fields[Format] + Plain](args: A): u8 {\n\tvar n: u8 = 0\n\tvar c := 0\n\tfor _, _ in fields(&args) {\n\t\tc += 1\n\t\tif c %% 2 == 1 {\n\t\t\tcontinue\n\t\t}\n\t\tn += 120\n\t}\n\treturn n\n}\nH(): int {\n\treturn int(G(%s))\n}\n"},
	// A bounds check on a counter the fields loop advances.
	{name: "fields/bounds", src: "G[A: Fields[Format] + Plain](args: A): int {\n\ta: [2]int = [7; 2]\n\tvar i := 0\n\tvar r := 0\n\tfor _, _ in fields(&args) {\n\t\tr = a[i]\n\t\ti += 1\n\t}\n\treturn r\n}\nH(): int {\n\treturn G(%s)\n}\n"},
	// Rule 8 with a fields loop as its loop L: c steps
	// once per field and bounds the site by c < 3, so s += 1 is proved and
	// s <= 123 after the loop; s + 5 stays checked and traps from three
	// fields (TestFieldsRule8 pins the proof).
	{name: "fields/rule8-acc", src: "G[A: Fields[Format] + Plain](args: A): i8 {\n\tvar s: i8 = 120\n\tvar c := 0\n\tfor _, _ in fields(&args) {\n\t\tif c < 3 {\n\t\t\ts += 1\n\t\t}\n\t\tc += 1\n\t}\n\treturn s + 5\n}\nH(): int {\n\treturn int(G(%s))\n}\n"},
	// The counter's only step is in a fields loop nested in L: with no
	// field the header -> Exit edge makes the cycle step-free, so s += 1
	// stays checked and traps at 128.
	{name: "fields/rule8-step", src: "G[A: Fields[Format] + Plain](args: A): i8 {\n\tvar s: i8 = 0\n\tvar c := 0\n\tvar k := 0\n\tfor c < 1 {\n\t\ts += 1\n\t\tfor _, _ in fields(&args) {\n\t\t\tc += 1\n\t\t}\n\t\tk += 1\n\t\tif k == 300 {\n\t\t\treturn 100\n\t\t}\n\t}\n\treturn s\n}\nH(): int {\n\treturn int(G(%s))\n}\n"},
	// Type tests: both edges stay feasible in the generic body.
	{name: "typetest/param", src: "G[T](x: u8): u8 {\n\tvar y: u8 = x\n\tif T is int {\n\t\ty = 200\n\t}\n\treturn y + 100\n}\nH(): int {\n\treturn int(%s)\n}\n",
		calls: []string{"G[int](1)", "G[u8](1)", "G[str](155)", "G[str](156)"}},
	{name: "typetest/const", src: "G[T](x: u8): u8 {\n\tvar y: u8 = 0\n\tif T is int {\n\t\ty = 200\n\t}\n\treturn y + 100 + x\n}\nH(): int {\n\treturn int(%s)\n}\n",
		calls: []string{"G[int](0)", "G[int](1)", "G[u8](155)", "G[u8](156)"}},
	// Const arguments: an array length and a loop bound.
	{name: "const/len", src: "G[const N: int](x: u8): u8 {\n\ta: [N]u8 = [0; N]\n\treturn Cast[u8](a.Len()) + x\n}\nH(): int {\n\treturn int(%s)\n}\n",
		calls: []string{"G[0](255)", "G[1](254)", "G[1](255)", "G[200](55)", "G[200](56)", "G[256](0)"}},
	{name: "const/loop", src: "G[const N: int](x: u8): u8 {\n\tvar s: u8 = x\n\tfor _ in 0..N {\n\t\ts += 1\n\t}\n\treturn s\n}\nH(): int {\n\treturn int(%s)\n}\n",
		calls: []string{"G[0](255)", "G[1](254)", "G[1](255)", "G[255](0)", "G[300](0)"}},
	// Rule 12 in a generic fields loop: the && join's true edge has 0 <= i
	// < 4 for a[i]; past the loop a[i] traps from four fields.
	{name: "fields/split", src: "G[A: Fields[Format] + Plain](args: A): int {\n\tvar a: [4]int = [7; 4]\n\tvar i := 0\n\tvar r := 0\n\tfor _, _ in fields(&args) {\n\t\tif i >= 0 && i < a.Len() {\n\t\t\tr += a[i]\n\t\t}\n\t\ti += 1\n\t}\n\treturn r + a[i]\n}\nH(): int {\n\treturn G(%s)\n}\n"},
	// Rule 11 in a generic body: N is a const parameter, so CountSet is
	// bounded by the lane-count clause's 64; with 64 lanes set the add
	// overflows, with 32 or 16 it does not.
	{name: "countset/generic", src: "import std.simd\n\nG[const N: int](m: simd.Mask[u8, N]): int requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 {\n\treturn m.CountSet() + 9223372036854775744\n}\nH(): int {\n\treturn %s\n}\n",
		calls: []string{pcAllSet64, "G(simd.Vec[u8, 32].Splat(1) == simd.Vec[u8, 32].Splat(1))", "G(simd.Vec[u8, 16].Splat(1) != simd.Vec[u8, 16].Splat(1))"}},
}

// pcAllSet64 is countset/generic's call with all 64 lanes of a Mask[u8,
// 64] set.
const pcAllSet64 = "G(simd.Vec[u8, 64].Splat(1) == simd.Vec[u8, 64].Splat(1))"

// TestGenericAgrees: every generic program gives the same
// value, or the same panic at the same site, with the pass on (and the
// post-borrow bounds fold) and off, for every field count, type argument
// and const argument; and some run of each panics.
func TestGenericAgrees(t *testing.T) {
	for _, g := range pcGeneric {
		t.Run(g.name, func(t *testing.T) {
			var srcs, names []string
			if g.calls == nil {
				for _, n := range pcFieldCounts {
					srcs = append(srcs, pcFieldStructs+fmt.Sprintf(g.src, pcFieldArg(n)))
					names = append(names, fmt.Sprintf("%d fields", n))
				}
			} else {
				for _, c := range g.calls {
					srcs = append(srcs, fmt.Sprintf(g.src, c))
					names = append(names, c)
				}
			}
			panicked := false
			for i, src := range srcs {
				on := pcEvalInst(t, src, nil)
				off := pcEvalInst(t, src, func(c *Config) { c.NoRanges = true })
				if on != off {
					t.Errorf("%s: pass on gives %q, off gives %q", names[i], on, off)
				}
				panicked = panicked || strings.Contains(off, " panic ")
				t.Logf("%s: %s", names[i], off)
			}
			if !panicked {
				t.Errorf("no run panics, so the program tests no check")
			}
		})
	}
}

// TestFieldsRule8 pins that rule 8 takes a fields loop as its loop:
// in fields/rule8-acc's generic G the i8 `s += 1` in the
// fields loop is proved (AddNoWrap) and `s + 5` after it stays checked; in
// fields/rule8-step the i8 `s += 1` stays checked (Add).
func TestFieldsRule8(t *testing.T) {
	want := map[string][]mir.BinOp{
		"fields/rule8-acc":  {mir.AddNoWrap, mir.Add}, // s += 1, s + 5
		"fields/rule8-step": {mir.Add},                // s += 1
	}
	for _, g := range pcGeneric {
		w, ok := want[g.name]
		if !ok {
			continue
		}
		l := pcLower(t, pcFieldStructs+fmt.Sprintf(g.src, pcFieldArg(1)), pcOpt{fold: true})
		b := l.body("G")
		var got []mir.BinOp
		for i := range b.Blocks {
			for _, s := range b.Blocks[i].Stmts {
				r := &s.Rvalue
				if s.Kind == mir.StAssign && r.Kind == mir.RvBinary && r.Type == types.I8 && (r.Bin == mir.Add || r.Bin == mir.AddNoWrap) {
					got = append(got, r.Bin)
				}
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(w) {
			t.Errorf("%s: G's i8 adds are %v, want %v\n%s", g.name, got, w, l.print("G"))
		}
	}
}

// TestInstanceModel checks pcInstance against the forms it models:
// the instance of fields/add for three fields has three adds and no
// generic left, and with zero fields none.
func TestInstanceModel(t *testing.T) {
	for _, n := range []int{0, 1, 3} {
		src := pcFieldStructs + fmt.Sprintf(pcGeneric[0].src, pcFieldArg(n))
		l := pcLower(t, src, pcOpt{tweak: func(c *Config) { c.NoRanges = true }})
		g := l.body("G")
		h := l.body("H")
		var args []types.GenericArg
		for i := range h.Blocks {
			if tm := &h.Blocks[i].Term; tm.Kind == mir.TermCall {
				args = h.GenericArgs[tm.Callee.Fn.Args.Start:tm.Callee.Fn.Args.End()]
			}
		}
		b := pcInstance(t, l, g, args)
		adds := 0
		for i := range b.Blocks {
			if !pcReached(b, mir.BlockID(i)) {
				continue
			}
			for _, s := range b.Blocks[i].Stmts {
				if s.Kind == mir.StAssign && s.Rvalue.Kind == mir.RvBinary && s.Rvalue.Bin == mir.Add {
					adds++
				}
			}
		}
		if adds != n || b.GenericCount != 0 {
			t.Errorf("%d fields: %d reachable adds and %d generics, want %d and 0", n, adds, b.GenericCount, n)
		}
	}
}

// pcReached reports whether blk is reachable from b's entry.
func pcReached(b *mir.Body, blk mir.BlockID) bool {
	seen := make([]bool, len(b.Blocks))
	work := []mir.BlockID{mir.EntryBlock}
	seen[mir.EntryBlock] = true
	var buf []mir.BlockID
	for len(work) > 0 {
		x := work[len(work)-1]
		work = work[:len(work)-1]
		if x == blk {
			return true
		}
		buf = b.Successors(x, buf[:0])
		for _, s := range buf {
			if !seen[s] {
				seen[s] = true
				work = append(work, s)
			}
		}
	}
	return false
}

// TestSplitShapes pins which blocks rule 12 takes as split joins: the
// && join of a lowered condition is one; after its short predecessor is
// rewritten to reach the join on two edges (a switch whose case and
// default are both the join, whose out-states may differ) it is none, as
// it is with the rule's off switch.
func TestSplitShapes(t *testing.T) {
	src := "F(s: []u8, i: int): u8 {\n\tif i >= 0 && i < s.Len() {\n\t\treturn s[i]\n\t}\n\treturn 0\n}\n"
	l := pcLower(t, src, pcOpt{tweak: func(c *Config) { c.NoRanges = true }})
	b := l.body("F")
	joins := func(b *mir.Body) []mir.BlockID {
		p := &rangePass{cfg: l.w.lowerCfg(), b: b, tab: l.w.tab}
		if !p.setup() {
			t.Fatal("setup refused the body")
		}
		var out []mir.BlockID
		for blk, at := range p.splitAt {
			if at >= 0 {
				out = append(out, mir.BlockID(blk))
			}
		}
		return out
	}
	js := joins(b)
	if len(js) != 1 {
		t.Fatalf("split joins %v, want one:\n%s", js, l.print("F"))
	}
	j := js[0]
	// The short predecessor: the one assigning the bool an immediate.
	var short mir.BlockID = mir.NoBlock
	for i := range b.Blocks {
		blk := &b.Blocks[i]
		if blk.Term.Kind == mir.TermGoto && blk.Term.Target == j && len(blk.Stmts) > 0 {
			if s := blk.Stmts[len(blk.Stmts)-1]; s.Kind == mir.StAssign && s.Rvalue.Kind == mir.RvUse && s.Rvalue.A.Kind == mir.OpImm {
				short = mir.BlockID(i)
			}
		}
	}
	if short == mir.NoBlock {
		t.Fatalf("no short predecessor of bb%d:\n%s", j, l.print("F"))
	}
	on := b.Blocks[j].Term.On // a tracked bool, read as the switch's operand
	start := uint32(len(b.Cases))
	b.Cases = append(b.Cases, mir.SwitchCase{Value: 0, Target: j})
	b.Blocks[short].Term = mir.Terminator{Kind: mir.TermSwitch, On: on, Cases: mir.Range{Start: start, Len: 1}, Default: j, Span: b.Blocks[short].Term.Span}
	if js := joins(b); len(js) != 0 {
		t.Errorf("with two edges from bb%d into bb%d the split joins are %v, want none", short, j, js)
	}
}

// TestAsmDiscardedOutput: an asm output written to "_"
// has Out.Local NoLocal, and the pass neither indexes by it nor
// stops analysing the body; an output to a real local still makes that
// local untracked, since the asm writes it.
func TestAsmDiscardedOutput(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		toLocal   bool // the output writes the add's left operand
		want      mir.BinOp
	}{
		{"unproved", "F(x: int): int {\n\treturn x + 1\n}\n", false, mir.Add},
		{"proved", "F(): int {\n\tvar x := 7\n\treturn x + 1\n}\n", false, mir.AddNoWrap},
		{"written", "F(): int {\n\tvar x := 7\n\treturn x + 1\n}\n", true, mir.Add},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := pcLower(t, tc.src, pcOpt{tweak: func(c *Config) { c.NoRanges = true }})
			b := l.body("F")
			var add *mir.Rvalue
			at := mir.BlockID(0)
			for i := range b.Blocks {
				for j := range b.Blocks[i].Stmts {
					if r := &b.Blocks[i].Stmts[j].Rvalue; b.Blocks[i].Stmts[j].Kind == mir.StAssign && r.Kind == mir.RvBinary && r.Bin == mir.Add {
						add, at = r, mir.BlockID(i)
					}
				}
			}
			if add == nil {
				t.Fatalf("no checked add:\n%s", l.print("F"))
			}
			out := mir.Place{Local: mir.NoLocal}
			if tc.toLocal {
				out = mir.Place{Local: add.A.Place.Local}
			}
			var ops []mir.AsmOperand
			for _, k := range []mir.AsmOperandKind{mir.AsmOut, mir.AsmLateOut} {
				ops = append(ops, mir.AsmOperand{Kind: k, Reg: "rax", Explicit: true, Out: mir.Place{Local: mir.NoLocal}, Type: types.Int})
			}
			ops = append(ops, mir.AsmOperand{Kind: mir.AsmInOut, Reg: "rdx", Explicit: true, In: mir.Imm(types.Int, 0), Out: out, Type: types.Int})
			id := b.AddAsm(mir.Asm{Template: "rdtsc", Operands: ops})
			blk := &b.Blocks[at]
			blk.Stmts = append([]mir.Statement{{Kind: mir.StAsm, Asm: id}}, blk.Stmts...)
			rangeCheck(l.w.lowerCfg(), b, false) // a panic here fails the test
			got := mir.Add
			for _, s := range blk.Stmts {
				if s.Kind == mir.StAssign && s.Rvalue.Kind == mir.RvBinary && (s.Rvalue.Bin == mir.Add || s.Rvalue.Bin == mir.AddNoWrap) {
					got = s.Rvalue.Bin
				}
			}
			if got != tc.want {
				t.Errorf("the add is %v, want %v:\n%s", got, tc.want, l.print("F"))
			}
		})
	}
}
