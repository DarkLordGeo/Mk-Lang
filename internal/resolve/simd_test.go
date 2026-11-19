// Why: the compiler knows a few std.simd items by stable key, and a build
// without std.simd must still work.
// What: TestKnownSimdItems and TestNoSimdModuleIsInert.
// How: resolves Vec, Mask, Lane and CpuFeatures with and without the module.

package resolve

import (
	"testing"

	"mk/internal/types"
)

// TestKnownSimdItems: Vec, Mask, Lane and CpuFeatures are found in
// std.simd, whatever the prelude path.
func TestKnownSimdItems(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	w.add("std.simd", opt{std: true}, "export interface Lane {\n}\n\nexport struct Vec[T: Lane, const N: int] {\n    lanes: [N]T\n}\n\n"+
		"export struct Mask[T: Lane, const N: int] {\n    lanes: [N]T\n}\n\nexport struct CpuFeatures {\n    bits: [2]u64\n}\n")
	k := w.s.Known()
	if k.Vec != w.item("std.simd.Vec").ID || k.Mask != w.item("std.simd.Mask").ID || k.Lane != w.item("std.simd.Lane").ID ||
		k.CpuFeatures != w.item("std.simd.CpuFeatures").ID {
		t.Errorf("known %+v", k)
	}
}

// TestNoSimdModuleIsInert: with no std.simd module the four fields are
// NoItem and nothing is reported.
func TestNoSimdModuleIsInert(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "F(x: int): int {\n    return x\n}\n")
	if len(b.diags) != 0 {
		t.Fatalf("diagnostics: %v", b.diags)
	}
	k := w.s.Known()
	if k.Vec != types.NoItem || k.Mask != types.NoItem || k.Lane != types.NoItem || k.CpuFeatures != types.NoItem {
		t.Errorf("known %+v", k)
	}
}
