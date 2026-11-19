// Why: a mut self Slice[T] method reached through a shared slice used to slip
// past the checker and crash monomorphization.
// What: tests that a mut self Slice[T] method needs a mut []T, a var array, a
// List or a HeapList, else E0507; that `mut xs[a..b]` exists only on a mut []T
// or an array; and that E0507 for a mut self receiver through a shared step
// is borrow's. tests/conformance/rules/R12_reject_slice_mut_method.mk once
// reached mono as `copy xs` into `mut Slice[T]` and became an ICE there.
// How: hand-built MIR calls of a generic Slice method; an ArgReborrow of a
// []T or &T is not a form lower can choose: Validate rejects it.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// sliceMethod declares a generic Slice.Name[T](self, i: int, j: int) with the
// given receiver mode on self type $0: the shape `Slice[T].Swap(mut
// self, i: int, j: int)`, whose self is check's `mut Slice[$0]`. Validate
// does not compare a generic callee's argument types, as for the real
// call.
func sliceMethod(k *kit, name string, recv types.Receiver) {
	self := k.tab.Param(0)
	switch recv {
	case types.ByShared:
		self = k.tab.Ref(self)
	case types.ByMut:
		self = k.tab.Mut(self)
	}
	g := []types.GenericParam{{Name: k.in.Intern("T")}}
	ps := []types.Param{{Name: k.in.Intern("self"), Type: self}, k.param("i", types.Int), k.param("j", types.Int)}
	s := k.declare("Slice."+name, types.MemberKey("std.core", "Slice", name), recv, g, ps, types.Void, types.EmptyErrors)
	s.OwnGenerics = 1
}

func swapArgs(self mir.Arg) []mir.Arg {
	return []mir.Arg{self, {Operand: imm(types.Int, 0), Mode: mir.ArgCopy}, {Operand: imm(types.Int, 1), Mode: mir.ArgCopy}}
}

// Bad(xs: []i32) { xs.Swap(0, 1) // <- E0507 }, as lower passes it today: the
// []T itself into the mut self receiver. Twins: `&self` (`xs.Peek(0, 1)`) on
// the same []T is clean, and a mut []T reborrowed into Swap is clean. The
// same row holds for a &T copied into a mut self (`swap-on-ref`).
func TestMutSelfOnSharedSliceIsE0507(t *testing.T) {
	for _, tc := range []struct {
		name   string
		recv   types.Receiver
		mutArg bool
		ref    bool
		bad    bool
	}{
		{"swap-on-slice", types.ByMut, false, false, true},
		{"peek-on-slice", types.ByShared, false, false, false},
		{"swap-on-mut-slice", types.ByMut, true, false, false},
		{"swap-on-ref", types.ByMut, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := newKit(t)
			sl, ms := k.tab.Slice(types.I32), k.tab.MutSlice(types.I32)
			sliceMethod(k, "M", tc.recv)
			pt := sl
			switch {
			case tc.mutArg:
				pt = ms
			case tc.ref:
				pt = k.tab.Ref(types.I32)
			}
			m := k.body(k.sig("Bad", []types.Param{k.param("xs", pt)}, types.Void, types.EmptyErrors))
			self := argCopy(m.local(1))
			if tc.mutArg {
				self = argReborrow(m.local(1))
			}
			at := m.callv("Slice.M", []types.TypeID{types.I32}, swapArgs(self)...)
			m.ret(mir.ExitNormal)
			r := m.check(t)
			if !tc.bad {
				m.expect(t, r)
				return
			}
			m.expect(t, r, diag.E0507, at)
			if p := r.Diags[0].Facts; len(p) == 0 {
				t.Fatalf("E0507 has no place fact")
			}
		})
	}
}

// The exclusive receiver lower forms for the same call (mirroring the shared
// view of a &self method on a mut []T):
// `t = mut xs[..]` into a mut []T temporary, then `Swap(move t, 0, 1)`. The
// E0507 is the slice's, once: the call then receives a mut []T. The same row
// is the user's `ys := mut xs[0..]` on `xs: []i32`, which was a
// false accept. Twin: the shared view `xs[..]` is clean.
func TestMutSliceOfSharedSliceIsE0507(t *testing.T) {
	for _, excl := range []bool{true, false} {
		name := "shared"
		if excl {
			name = "exclusive"
		}
		t.Run(name, func(t *testing.T) {
			k := newKit(t)
			sl, ms := k.tab.Slice(types.I32), k.tab.MutSlice(types.I32)
			recv := types.ByShared
			ty, bk := sl, mir.Shared
			if excl {
				recv, ty, bk = types.ByMut, ms, mir.Exclusive
			}
			sliceMethod(k, "M", recv)
			m := k.body(k.sig("Bad", []types.Param{k.param("xs", sl)}, types.Void, types.EmptyErrors))
			v := m.temp(ty)
			at := m.set(v, mir.Rvalue{Kind: mir.RvSlice, Borrow: bk, Place: m.local(1), Type: ty})
			self := argCopy(m.local(v))
			if excl {
				self = argMove(m.local(v))
			}
			m.callv("Slice.M", []types.TypeID{types.I32}, swapArgs(self)...)
			m.end(v)
			m.ret(mir.ExitNormal)
			r := m.check(t)
			if excl {
				m.expect(t, r, diag.E0507, at)
				return
			}
			m.expect(t, r)
		})
	}
}
