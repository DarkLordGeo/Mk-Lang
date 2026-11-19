// Why: mk build refuses aarch64, so the per-unit crypto tests are the one
// place the aarch64 lowering of these intrinsics reaches LLVM's backend.
// What: the shared helpers of aes_obj_test.go and
// sha256_obj_test.go: objArr, intrObject and objMnemonics.
// How: one function per crypto intrinsic is compiled to an object for
// x86_64 and aarch64, and the object's instructions are read back.

package codegen

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// objIntr is one intrinsic call: its std.raw name, parameter and result
// types (built from the test env).
type objIntr struct {
	name   string
	params func(e *testEnv) []types.TypeID
	result func(e *testEnv) types.TypeID
}

// objArr is [n]elem in e.
func objArr(e *testEnv, n uint64, elem types.TypeID) types.TypeID {
	return e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, n)}, elem)
}

// intrObject emits, for target, one function per intrinsic that passes
// its parameters to the intrinsic and returns the result (so -O2 keeps
// every call), compiles the IR with clang at -O0 and -O2, and returns
// llvm-objdump's disassembly of the -O2 object.
func intrObject(t *testing.T, target types.Target, intrs []objIntr) string {
	t.Helper()
	bin := needLLVM(t)
	e := newTestEnv()
	var bodies []*mir.Body
	var uses []types.ItemID
	for _, in := range intrs {
		ps, r := in.params(e), in.result(e)
		id := e.stdFn("std.raw", "", in.name, 0, ps, r)
		uses = append(uses, id)
		f := e.body(e.fn("Use"+in.name, ps, r, 0))
		var ops []mir.Operand
		for i := range ps {
			ops = append(ops, mir.Copy(place(mir.LocalID(i+1))))
		}
		v := f.call(id, nil, r, ops...)
		f.assign(place(0), use(mir.Copy(place(v))), f.at(21, 1))
		f.ret(f.at(21, 1))
		bodies = append(bodies, f.b)
	}
	u := unitOf(e, bodies)
	for _, id := range uses {
		u.Funcs = append(u.Funcs, Func{Symbol: "-", Key: e.items.Get(id).Key.String(), Item: id, Pack: -1})
	}
	ll := emit(t, e, u, target, types.Release)
	dir := t.TempDir()
	clangC(t, target, dir, "o0", ll, "-O0")
	obj := clangC(t, target, dir, "o2", ll, "-O2")
	return runTool(t, tool(bin, "llvm-objdump"), "-d", "--no-show-raw-insn", obj)
}

// objMnemonics counts each mnemonic of want in a disassembly.
func objMnemonics(dis string, want []string) map[string]int {
	n := map[string]int{}
	for _, ln := range strings.Split(dis, "\n") {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		for _, w := range want {
			if f[1] == w {
				n[w]++
			}
		}
	}
	return n
}
