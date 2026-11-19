// Why: every stage after lower trusts MIR, so each validator invariant needs
// a body that passes it and one that breaks it.
// What: tests of invariants M1 to M12, the printer, successors and the CFG
// checksum.
// How: a shared fixture builds a valid Show body; expectViolation mutates it
// and expects the named invariant in the error.

package mir

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

var update = flag.Bool("update", false, "rewrite golden files")

type fixture struct {
	in     *source.Interner
	items  *types.Items
	consts *types.ConstTable
	tab    *types.Table
	nf     types.ItemID // error NotFound
	load   types.ItemID // Load(id: i64): NotFound!i32
	show   types.ItemID // Show(id: i64): NotFound!i32, the body under test
	sigs   map[types.ItemID]*types.Signature
}

func newFixture() *fixture {
	in := source.NewInterner()
	items := types.NewItems()
	consts := types.NewConstTable()
	tab := types.NewTable(in, items, consts)
	m := items.AddModule(&types.Module{Path: "main"})
	add := func(name string, k types.ItemKind) types.ItemID {
		return items.AddItem(&types.Item{Module: m, Name: in.Intern(name), Kind: k, Key: types.ItemKey("main", name)})
	}
	f := &fixture{in: in, items: items, consts: consts, tab: tab, sigs: map[types.ItemID]*types.Signature{}}
	f.nf = add("NotFound", types.KindError)
	f.load = add("Load", types.KindFunc)
	f.show = add("Show", types.KindFunc)
	errs := tab.ErrorSet([]types.ErrorMember{{Item: f.nf}})
	for _, it := range []types.ItemID{f.load, f.show} {
		f.sigs[it] = &types.Signature{Item: it, Params: []types.Param{{Name: in.Intern("id"), Type: types.I64}},
			Result: types.I32, Errors: errs}
	}
	return f
}

func (f *fixture) env() *Env {
	return &Env{Types: f.tab, Sig: f.sigs[f.show], Callee: func(id types.ItemID) *types.Signature { return f.sigs[id] }}
}

// show builds Show(id) = try Load(id), with a defer copy on each exit edge:
//
//	bb0: _2 = call Load(copy _1) -> bb1 err try _3 -> bb3
//	bb1: _0 = ok(copy _2); goto bb2 (defer copy)
//	bb2 (defer 0): return normal
//	bb3: _0 = err(Widen(move _3)); goto bb4
//	bb4 (defer 0): return try
func (f *fixture) showBody() *Body {
	tab := f.tab
	ret := f.sigs[f.show].ReturnType(tab)
	errSet := tab.ErrorSetType(f.sigs[f.load].Errors)
	b := NewBody(f.show, source.Span{File: 1, Start: 0, End: 10}, ret)
	sp := b.AddSpan(source.Span{File: 1, Start: 2, End: 8})
	x := b.AddParam(types.I64, f.in.Intern("id"), sp)
	v := b.AddLocal(Local{Type: types.I32, Kind: LocalTemp, Loop: NoLoop, Span: sp})
	e := b.AddLocal(Local{Type: errSet, Kind: LocalTemp, Loop: NoLoop, Span: sp})
	b.Defers = append(b.Defers, DeferInfo{Span: sp})
	b0, b1 := b.AddBlock(OriginBody, NoDefer), b.AddBlock(OriginBody, NoDefer)
	b2 := b.AddBlock(OriginDefer, 0)
	b3 := b.AddBlock(OriginBody, NoDefer)
	b4 := b.AddBlock(OriginDefer, 0)
	b.Blocks[b0].Term = Terminator{Kind: TermCall, Span: sp, Callee: Callee{Kind: CallDirect, Fn: FnRef{Item: f.load}},
		Args: b.AddArgs(Arg{Operand: Copy(LocalPlace(x)), Mode: ArgCopy}), Dest: LocalPlace(v), Target: b1,
		HasErr: true, Err: ErrorEdge{Kind: EdgeTry, Block: b3, Error: e}, Spawn: NoLocal}
	b.Blocks[b1].Stmts = []Statement{{Kind: StAssign, Place: LocalPlace(ReturnLocal), Span: sp,
		Rvalue: Rvalue{Kind: RvAggregate, Agg: AggOk, Ops: b.AddOperands(Copy(LocalPlace(v)))}}}
	b.Blocks[b1].Term = Terminator{Kind: TermGoto, Target: b2, Span: sp}
	b.Blocks[b2].Term = Terminator{Kind: TermReturn, Exit: ExitNormal, Span: sp}
	b.Blocks[b3].Stmts = []Statement{{Kind: StAssign, Place: LocalPlace(ReturnLocal), Span: sp,
		Rvalue: Rvalue{Kind: RvAggregate, Agg: AggErr, Ops: b.AddOperands(Move(LocalPlace(e)))}}}
	b.Blocks[b3].Term = Terminator{Kind: TermGoto, Target: b4, Span: sp}
	b.Blocks[b4].Term = Terminator{Kind: TermReturn, Exit: ExitTry, Span: sp}
	return b
}

func TestValidateAcceptsShow(t *testing.T) {
	f := newFixture()
	if err := f.showBody().Validate(f.env()); err != nil {
		t.Fatal(err)
	}
}

// expectViolation runs a breaker on a fresh body and wants the invariant.
func expectViolation(t *testing.T, inv string, brk func(f *fixture, b *Body)) {
	t.Helper()
	f := newFixture()
	b := f.showBody()
	brk(f, b)
	err := b.Validate(f.env())
	if err == nil || !strings.Contains(err.Error(), inv+":") {
		t.Fatalf("want %s violation, got %v", inv, err)
	}
}

// TestValidateEntryAndReturnLocal is M1.
func TestValidateEntryAndReturnLocal(t *testing.T) {
	expectViolation(t, "M1", func(f *fixture, b *Body) { b.Locals[0].Type = types.I32 })
	expectViolation(t, "M1", func(f *fixture, b *Body) { b.Locals[1].Type = types.I32 })
	expectViolation(t, "M1", func(f *fixture, b *Body) { b.Locals[1].Kind = LocalUser })
}

func TestM2Ranges(t *testing.T) {
	expectViolation(t, "M2", func(f *fixture, b *Body) { b.Blocks[1].Term.Target = 99 })
	expectViolation(t, "M2", func(f *fixture, b *Body) { b.Blocks[0].Term.Args = Range{Start: 5, Len: 3} })
	expectViolation(t, "M2", func(f *fixture, b *Body) { b.Blocks = nil })
}

// TestValidateProjectionTypes is M3.
func TestValidateProjectionTypes(t *testing.T) {
	f := newFixture()
	b := f.showBody()
	r := b.AddLocal(Local{Type: f.tab.Ref(types.I32), Kind: LocalUser, Name: f.in.Intern("r"), Loop: NoLoop})
	ok := b.Place(r, Projection{Kind: ProjDeref, Type: types.I32})
	b.Blocks[1].Stmts = append(b.Blocks[1].Stmts, Statement{Kind: StAssign, Place: ok, Rvalue: Rvalue{Kind: RvUse, A: Imm(types.I32, 7)}})
	if err := b.Validate(f.env()); err != nil {
		t.Fatalf("deref of &i32 rejected: %v", err)
	}
	expectViolation(t, "M3", func(f *fixture, b *Body) {
		bad := b.Place(1, Projection{Kind: ProjDeref, Type: types.I32})
		b.Blocks[1].Stmts = append(b.Blocks[1].Stmts, Statement{Kind: StAssign, Place: bad, Rvalue: Rvalue{Kind: RvUse, A: Imm(types.I32, 7)}})
	})
}

func TestM4AssignTypes(t *testing.T) {
	expectViolation(t, "M4", func(f *fixture, b *Body) {
		b.Blocks[1].Stmts = append(b.Blocks[1].Stmts, Statement{Kind: StAssign, Place: LocalPlace(2),
			Rvalue: Rvalue{Kind: RvLen, Place: LocalPlace(1)}})
	})
}

// TestValidateFallibleCallHasErrEdge is M5, with the spawn rule.
func TestValidateFallibleCallHasErrEdge(t *testing.T) {
	expectViolation(t, "M5", func(f *fixture, b *Body) { b.Blocks[0].Term.HasErr = false })
	expectViolation(t, "M5", func(f *fixture, b *Body) { b.Locals[3].Type = types.I32 })
	expectViolation(t, "M5", func(f *fixture, b *Body) {
		s := b.AddLocal(Local{Type: types.Void, Kind: LocalScope, Loop: NoLoop})
		b.Blocks[0].Term.Spawn = s
	})
	f := newFixture()
	b := f.showBody()
	s := b.AddLocal(Local{Type: types.Void, Kind: LocalScope, Loop: NoLoop})
	b.Blocks[0].Term.Spawn = s
	b.Blocks[0].Term.HasErr = false
	b.Blocks[3].Stmts = nil
	if err := b.Validate(f.env()); err != nil {
		t.Fatalf("spawned fallible call without an edge rejected: %v", err)
	}
}

// TestValidateReturnReadsLocal0 is M6: Return carries only its exit kind.
func TestValidateReturnReadsLocal0(t *testing.T) {
	expectViolation(t, "M6", func(f *fixture, b *Body) { b.Blocks[2].Term.Exit = ExitKind(9) })
	if !ExitTry.RunsOnerr() || ExitElse.RunsOnerr() || ExitMayBeError.RunsOnerr() || !ExitMayBeError.IsE0411Exit() || ExitElse.IsE0411Exit() {
		t.Fatal("exit predicates")
	}
}

// TestValidateCleanupSinglePredecessor is M7.
func TestValidateCleanupSinglePredecessor(t *testing.T) {
	expectViolation(t, "M7", func(f *fixture, b *Body) { b.Blocks[3].Term.Target = 2 })
	expectViolation(t, "M7", func(f *fixture, b *Body) { b.Defers[0].Onerr = true })
}

func TestM8OnerrGuard(t *testing.T) {
	expectViolation(t, "M8", func(f *fixture, b *Body) {
		b.Blocks[3].Term = Terminator{Kind: TermOnerrGuard, Value: 1, Yes: 1, No: 4}
	})
}

// TestValidateLoopOrigins is M9.
func TestValidateLoopOrigins(t *testing.T) {
	build := func(f *fixture, b *Body, assigns int) {
		xs := b.AddLocal(Local{Type: f.tab.MutSlice(types.I32), Kind: LocalUser, Loop: NoLoop})
		b.Loops = append(b.Loops, LoopInfo{Header: 1, Exit: 2, Holder: NoLocal})
		h := b.AddLocal(Local{Type: f.tab.Mut(f.tab.MutSlice(types.I32)), Kind: LocalLoopHolder, Loop: 0})
		b.Loops[0].Holder = h
		for i := 0; i < assigns; i++ {
			b.Blocks[1].Stmts = append(b.Blocks[1].Stmts, Statement{Kind: StAssign, Place: LocalPlace(h),
				Rvalue: Rvalue{Kind: RvRef, Borrow: Exclusive, Origin: OriginLoopHolder, Loop: 0, Place: LocalPlace(xs)}})
		}
	}
	f := newFixture()
	b := f.showBody()
	build(f, b, 1)
	if err := b.Validate(f.env()); err != nil {
		t.Fatalf("one holder loan rejected: %v", err)
	}
	expectViolation(t, "M9", func(f *fixture, b *Body) { build(f, b, 2) })
	expectViolation(t, "M9", func(f *fixture, b *Body) {
		b.Blocks[1].Stmts = append(b.Blocks[1].Stmts, Statement{Kind: StJoin, Local: 2})
	})
}

// TestValidateReborrowMode is M10.
func TestValidateReborrowMode(t *testing.T) {
	expectViolation(t, "M10", func(f *fixture, b *Body) { b.Args[0].Mode = ArgReborrow })
	f := newFixture()
	b := f.showBody()
	facts := func(t types.TypeID) (types.Facts, bool) { return types.Facts{Kind: types.Plain}, true }
	env := f.env()
	env.Facts = facts
	b.Args[0].Mode = ArgMove
	if err := b.Validate(env); err == nil || !strings.Contains(err.Error(), "M10") {
		t.Fatalf("move of a plain value accepted: %v", err)
	}
}

func TestM11NoInferOrPoison(t *testing.T) {
	expectViolation(t, "M11", func(f *fixture, b *Body) { b.Locals[2].Type = f.tab.Infer(0) })
	expectViolation(t, "M11", func(f *fixture, b *Body) { b.Locals[2].Type = types.Poison })
}

func TestM12Spans(t *testing.T) {
	expectViolation(t, "M12", func(f *fixture, b *Body) { b.Blocks[0].Term.Span = 40 })
	expectViolation(t, "M12", func(f *fixture, b *Body) { b.Spans = nil })
}

func TestSuccessorsAndChecksum(t *testing.T) {
	f := newFixture()
	b := f.showBody()
	if got := b.Successors(0, nil); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("successors of bb0: %v", got)
	}
	pc := b.PredecessorCounts()
	if pc[0] != 0 || pc[2] != 1 || pc[4] != 1 {
		t.Fatalf("predecessor counts %v", pc)
	}
	c1 := b.CFGChecksum()
	b.Spans[1] = source.Span{File: 1, Start: 100, End: 120}
	b.Locals[2].Name = f.in.Intern("renamed")
	if b.CFGChecksum() != c1 {
		t.Fatal("spans or names changed the CFG checksum")
	}
	b.Blocks[3].Term.Target = 2
	if b.CFGChecksum() == c1 {
		t.Fatal("an edge change kept the CFG checksum")
	}
}

// TestPrinterGolden pins the text form of the Show body.
func TestPrinterGolden(t *testing.T) {
	f := newFixture()
	got := (&Printer{Types: f.tab, Items: f.items}).Print(f.showBody())
	golden := filepath.Join("testdata", "show.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Fatalf("printer output changed; rerun with -update and read the diff:\n%s", got)
	}
}
