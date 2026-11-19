// Why: an intrinsic is only worth having if LLVM's backend emits the
// instruction it stands for.
// What: TestShaIntrinsicsObject: std.raw's SHA-256 intrinsics become
// sha256rnds2, sha256msg1 and sha256msg2 on x86_64, and sha256h, sha256h2,
// sha256su0 and sha256su1 on aarch64.
// How: intrObject compiles one function per intrinsic to an object and
// objMnemonics counts the instructions; this file stands alone with the
// SHA-256 unit.

package codegen

import (
	"testing"

	"mk/internal/types"
)

var shaIntrs = []objIntr{
	{"Sha256Rounds4", func(e *testEnv) []types.TypeID {
		return []types.TypeID{objArr(e, 8, types.U32), objArr(e, 4, types.U32)}
	},
		func(e *testEnv) types.TypeID { return objArr(e, 8, types.U32) }},
	{"Sha256Schedule4", func(e *testEnv) []types.TypeID {
		w := objArr(e, 4, types.U32)
		return []types.TypeID{w, w, w, w}
	}, func(e *testEnv) types.TypeID { return objArr(e, 4, types.U32) }},
}

// TestShaIntrinsicsObject: each intrinsic, called from a function
// without the feature (so through its helper compiled with it), compiles
// for x86_64-linux, x86_64-windows and aarch64-linux (sha2), and the -O2
// object holds its instructions.
func TestShaIntrinsicsObject(t *testing.T) {
	for _, c := range []struct {
		target types.Target
		want   map[string]int
	}{
		{types.X86_64Linux, map[string]int{"sha256rnds2": 2, "sha256msg1": 1, "sha256msg2": 1}},
		{types.X86_64Windows, map[string]int{"sha256rnds2": 2, "sha256msg1": 1, "sha256msg2": 1}},
		{types.Aarch64Linux, map[string]int{"sha256h": 1, "sha256h2": 1, "sha256su0": 1, "sha256su1": 1}},
	} {
		t.Run(c.target.Name, func(t *testing.T) {
			t.Parallel()
			var names []string
			for m := range c.want {
				names = append(names, m)
			}
			got := objMnemonics(intrObject(t, c.target, shaIntrs), names)
			t.Logf("%v", got)
			for m, n := range c.want {
				if got[m] < n {
					t.Errorf("%s: %d %s, want at least %d (all: %v)", c.target.Name, got[m], m, n, got)
				}
			}
		})
	}
}
