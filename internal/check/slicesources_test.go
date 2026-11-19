// Why: `Slice[T]`'s namespace functions see `self` as `[]T` or `mut []T`,
// so their automatic result source must read `&Slice[T]` as `[]T`; without
// it `Get` and `GetMut` had no source and callers saw a result with no loan.
// What: TestSliceNamespaceSources.
// How: checks the functions' flows; `contents` of the interface-named self
// type once found nothing, and borrow reported E0602 in
// std/core/core_slice.mk.

package check

import (
	"testing"

	"mk/internal/types"
)

func TestSliceNamespaceSources(t *testing.T) {
	core := fakePrelude() +
		"export Slice[T].Len(&self): int\n" +
		"export Slice[T].Get(&self, i: int): ?&T\n" +
		"export Slice[T].GetMut(mut self, i: int): ?mut T\n" +
		"export Slice[T].First(&self): &T\n" +
		"export Slice[T].Rest(&self, i: int): []T\n" +
		"export Slice[T].Fill(mut self, x: T into self)\n" +
		"export Slice[T].CopyFrom(mut self, src: []T) *src into self\n"
	w := newWorldCore(t, true, core)
	b := w.mods["std.core"]
	// The fake prelude's own diagnostics (its `Type.Name` is an E0601 over
	// a private field) are the baseline; the added lines draw none.
	base := newWorldCore(t, true, fakePrelude())
	if got, want := codes(w, b.diags()), codes(base, base.mods["std.core"].diags()); got != want {
		t.Fatalf("std.core: %s, want %s", got, want)
	}
	for _, tc := range []struct{ name, want string }{
		{"Len", ""},           // not a carrier
		{"Get", "H(self)"},    // self stays borrowed while the result lives
		{"GetMut", "H(self)"}, // hasMut(?mut T)
		{"First", "H(self)"},  // own bytes behind self are T's: ANY
		{"Rest", "H(self)"},   // a slice of the elements
		{"Fill", ""},          // void
		{"CopyFrom", ""},      // void
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := w.items.Get(b.item("Slice:" + tc.name)).Decl.(*types.FuncDecl)
			if !ok {
				t.Fatalf("no FuncDecl")
			}
			fl := d.Sig.Flows
			got := ""
			for i, s := range fl.Sources {
				if i > 0 {
					got += " "
				}
				mode := "H"
				if s.Behind {
					mode = "B"
				}
				got += mode + "(" + w.in.Str(d.Sig.Param(s.Param).Name) + ")"
			}
			if got != tc.want || fl.Declared || fl.Ambiguous {
				t.Errorf("sources %q (declared %v, ambiguous %v), want %q", got, fl.Declared, fl.Ambiguous, tc.want)
			}
		})
	}
	// The written store clauses are kept: `mut Slice[T]` is a store target.
	for name, want := range map[string]types.Store{"Fill": {From: 2, Into: 1}, "CopyFrom": {From: 2, Into: 1}} {
		d := w.items.Get(b.item("Slice:" + name)).Decl.(*types.FuncDecl)
		if name == "Fill" {
			if d.Sig.Params[1].Into != 1 {
				t.Errorf("Fill: x into %d, want self", d.Sig.Params[1].Into)
			}
			continue
		}
		if len(d.Sig.Flows.Stores) != 1 || d.Sig.Flows.Stores[0] != want {
			t.Errorf("%s: stores %+v, want [%+v]", name, d.Sig.Flows.Stores, want)
		}
	}
}
