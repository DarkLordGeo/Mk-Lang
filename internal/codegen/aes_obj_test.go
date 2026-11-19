// Why: an intrinsic is only worth having if LLVM's backend emits the
// instruction it stands for.
// What: TestAesIntrinsicsObject: std.raw's AES and carry-less multiply
// intrinsics become aesenc, aesenclast and pclmulqdq on x86_64, and aese with
// aesmc, aese and pmull on aarch64.
// How: the IR is compiled to objects and disassembled; this file stands
// alone with the AES and carry-less multiply unit.

package codegen

import (
	"testing"

	"mk/internal/types"
)

var aesIntrs = []objIntr{
	{"AesRound", func(e *testEnv) []types.TypeID {
		return []types.TypeID{objArr(e, 16, types.U8), objArr(e, 16, types.U8)}
	},
		func(e *testEnv) types.TypeID { return objArr(e, 16, types.U8) }},
	{"AesLastRound", func(e *testEnv) []types.TypeID {
		return []types.TypeID{objArr(e, 16, types.U8), objArr(e, 16, types.U8)}
	},
		func(e *testEnv) types.TypeID { return objArr(e, 16, types.U8) }},
	{"Clmul64", func(e *testEnv) []types.TypeID { return []types.TypeID{types.U64, types.U64} },
		func(e *testEnv) types.TypeID {
			return e.tt.Record([]types.RecordField{{Name: e.in.Intern("hi"), Type: types.U64}, {Name: e.in.Intern("lo"), Type: types.U64}})
		}},
}

// TestAesIntrinsicsObject: each intrinsic, called from a function
// without the feature (so through its helper compiled with it), compiles
// for x86_64-linux, x86_64-windows and aarch64-linux, and the -O2 object
// holds its instructions.
func TestAesIntrinsicsObject(t *testing.T) {
	for _, c := range []struct {
		target types.Target
		want   map[string]int // mnemonic: at least this many
	}{
		{types.X86_64Linux, map[string]int{"aesenc": 1, "aesenclast": 1, "pclmulqdq": 1}},
		{types.X86_64Windows, map[string]int{"aesenc": 1, "aesenclast": 1, "pclmulqdq": 1}},
		{types.Aarch64Linux, map[string]int{"aese": 2, "aesmc": 1, "pmull": 1}},
	} {
		t.Run(c.target.Name, func(t *testing.T) {
			t.Parallel()
			var names []string
			for m := range c.want {
				names = append(names, m)
			}
			got := objMnemonics(intrObject(t, c.target, aesIntrs), names)
			t.Logf("%v", got)
			for m, n := range c.want {
				if got[m] < n {
					t.Errorf("%s: %d %s, want at least %d (all: %v)", c.target.Name, got[m], m, n, got)
				}
			}
		})
	}
}
