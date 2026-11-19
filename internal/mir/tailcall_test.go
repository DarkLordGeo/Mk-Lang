// Why: `become` lowers to TermTailCall, which must match the caller exactly
// for codegen to emit a guaranteed tail call.
// What: TestTailCall, TestTailCallGenericCallee and
// TestTailCallFnValueReborrow, over the validator, printer and Successors.
// How: valid direct and fn-value tail calls print as tailcall with no
// successor; each broken form is invariant M5 or M10.

package mir

import (
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

// tailBody builds Show(id) = become Load(id): one block whose terminator is
// the tail call. Load and Show share the signature NotFound!i32 (id: i64).
func (f *fixture) tailBody() *Body {
	b := NewBody(f.show, source.Span{File: 1, Start: 0, End: 10}, f.sigs[f.show].ReturnType(f.tab))
	sp := b.AddSpan(source.Span{File: 1, Start: 2, End: 8})
	x := b.AddParam(types.I64, f.in.Intern("id"), sp)
	b0 := b.AddBlock(OriginBody, NoDefer)
	b.Blocks[b0].Term = Terminator{Kind: TermTailCall, Span: sp, Callee: Callee{Kind: CallDirect, Fn: FnRef{Item: f.load}},
		Args: b.AddArgs(Arg{Operand: Copy(LocalPlace(x)), Mode: ArgCopy}), Spawn: NoLocal}
	return b
}

func tailViolation(t *testing.T, what string, brk func(f *fixture, b *Body)) {
	t.Helper()
	f := newFixture()
	b := f.tailBody()
	brk(f, b)
	err := b.Validate(f.env())
	t.Logf("%s: %v", what, err)
	if err == nil || !strings.Contains(err.Error(), "M5:") {
		t.Errorf("%s: want an M5 violation, got %v", what, err)
	}
}

// TestTailCall: a direct tail call of the caller's own signature and one
// through a fn value of that type validate, print as tailcall and have no
// successor; an error edge, a destination, a builtin or interface callee, a
// callee of another result or a wrong argument type are M5.
func TestTailCall(t *testing.T) {
	if TermTailCall != TermUnreachable+1 {
		t.Fatal("TermTailCall must be appended after TermUnreachable, so no kind is renumbered")
	}
	f := newFixture()
	b := f.tailBody()
	wantOK(t, f, b, "a direct tail call")
	if s := (&Printer{Types: f.tab, Items: f.items}).term(b, &b.Blocks[0].Term); !strings.HasPrefix(s, "tailcall ") || !strings.Contains(s, "Load(copy _1)") {
		t.Errorf("printed %q", s)
	}
	if succ := b.Successors(0, nil); len(succ) != 0 {
		t.Errorf("successors %v", succ)
	}
	_ = b.CFGChecksum()

	viaValue := func(f *fixture, b *Body, result types.TypeID) {
		ft := f.tab.Fn(types.FnType{Params: []types.FnParam{{Type: types.I64, Into: types.NoParam}}, Result: result, Errors: f.sigs[f.load].Errors})
		fv := local(b, ft)
		b.Blocks[0].Term.Callee = Callee{Kind: CallValue, Value: Copy(LocalPlace(fv))}
	}
	f = newFixture()
	b = f.tailBody()
	viaValue(f, b, types.I32)
	wantOK(t, f, b, "a tail call through a fn value")

	tailViolation(t, "an error edge", func(f *fixture, b *Body) {
		e := local(b, f.tab.ErrorSetType(f.sigs[f.load].Errors))
		b.Blocks[0].Term.HasErr, b.Blocks[0].Term.Err = true, ErrorEdge{Kind: EdgeTry, Block: 0, Error: e}
	})
	tailViolation(t, "a destination", func(f *fixture, b *Body) {
		b.Blocks[0].Term.Dest = LocalPlace(local(b, types.I32))
	})
	tailViolation(t, "a spawn", func(f *fixture, b *Body) { b.Blocks[0].Term.Spawn = 1 })
	tailViolation(t, "a builtin", func(f *fixture, b *Body) {
		b.Blocks[0].Term.Callee = Callee{Kind: CallBuiltin, Builtin: BuiltinEvalArena}
	})
	tailViolation(t, "an interface callee", func(f *fixture, b *Body) {
		b.Blocks[0].Term.Callee = Callee{Kind: CallInterface, Iface: f.nf, SelfType: types.I32}
	})
	tailViolation(t, "no item", func(f *fixture, b *Body) { b.Blocks[0].Term.Callee.Fn.Item = types.NoItem })
	tailViolation(t, "another result", func(f *fixture, b *Body) {
		f.sigs[f.load].Result = types.I64
	})
	tailViolation(t, "another error set", func(f *fixture, b *Body) {
		f.sigs[f.load].Errors = types.EmptyErrors
	})
	tailViolation(t, "an argument of another type", func(f *fixture, b *Body) {
		f.sigs[f.load].Params[0].Type = types.I32
	})
	tailViolation(t, "a fn value of another result", func(f *fixture, b *Body) { viaValue(f, b, types.I64) })
}

// TestTailCallGenericCallee: a tail call of a generic callee gives all
// its generic arguments, as a call does (the shared genericCount, M5), and
// its result is not compared with the body's: with a generic callee the
// declared comparison is check's E0312, so a callee of another result
// validates.
func TestTailCallGenericCallee(t *testing.T) {
	generic := func(f *fixture, b *Body, args ...types.GenericArg) {
		f.sigs[f.load].Generics = []types.GenericParam{{Name: f.in.Intern("T")}}
		b.Blocks[0].Term.Callee.Fn.Args = b.AddGenericArgs(args...)
	}
	f := newFixture()
	b := f.tailBody()
	generic(f, b, types.TypeArg(types.I32))
	wantOK(t, f, b, "a tail call of a generic callee")
	f = newFixture()
	b = f.tailBody()
	generic(f, b, types.TypeArg(types.I32))
	f.sigs[f.load].Result = types.I64
	wantOK(t, f, b, "a generic callee of another result (E0312 is check's)")
	tailViolation(t, "a generic callee without its generic arguments", func(f *fixture, b *Body) { generic(f, b) })
	tailViolation(t, "a generic callee with too many generic arguments", func(f *fixture, b *Body) {
		generic(f, b, types.TypeArg(types.I32), types.TypeArg(types.I64))
	})
}

// TestTailCallFnValueReborrow: through a fn value, a reborrow goes into a
// mut parameter only (M10, the check call shares), and an argument past the
// fn type's parameters is an environment argument (M5).
func TestTailCallFnValueReborrow(t *testing.T) {
	viaValue := func(f *fixture, b *Body, param types.TypeID) LocalID {
		ft := f.tab.Fn(types.FnType{Params: []types.FnParam{{Type: param, Into: types.NoParam}}, Result: types.I32, Errors: f.sigs[f.load].Errors})
		fv := local(b, ft)
		b.Blocks[0].Term.Callee = Callee{Kind: CallValue, Value: Copy(LocalPlace(fv))}
		m := local(b, f.tab.Mut(types.I64))
		b.Args[b.Blocks[0].Term.Args.Start] = Arg{Operand: Copy(LocalPlace(m)), Mode: ArgReborrow}
		return m
	}
	f := newFixture()
	b := f.tailBody()
	viaValue(f, b, f.tab.Mut(types.I64))
	wantOK(t, f, b, "a reborrow into a fn value's mut parameter")

	f = newFixture()
	b = f.tailBody()
	viaValue(f, b, types.I64)
	err := b.Validate(f.env())
	t.Logf("a reborrow into a fn value's i64 parameter: %v", err)
	if err == nil || !strings.Contains(err.Error(), "M10:") || !strings.Contains(err.Error(), "reborrow into a parameter") {
		t.Errorf("a reborrow into a fn value's i64 parameter: want M10, got %v", err)
	}

	tailViolation(t, "a fn value with an extra argument that is no environment", func(f *fixture, b *Body) {
		m := viaValue(f, b, f.tab.Mut(types.I64))
		b.Blocks[0].Term.Args = b.AddArgs(Arg{Operand: Copy(LocalPlace(m)), Mode: ArgReborrow}, Arg{Operand: Copy(LocalPlace(local(b, types.I64))), Mode: ArgCopy})
	})
}
