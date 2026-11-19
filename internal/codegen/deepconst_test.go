// Why: a deeply nested const value must not overflow Go's stack when emitted.
// What: TestDeepConstEmits: constGlobal emits a deep const value.
// How: const.go's constDataFirst walks the value without deep Go recursion;
// the test builds a deep value and emits it.

package codegen

import (
	"runtime/debug"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestDeepConstEmits: List(): str returns a str whose data is the
// head of a 200,000-node chain of [16]u8 values, each relocated at byte 8
// to the one before it. It emits under a 32 MiB Go stack with one private
// global per node; the recursion through constInit and relocValue needed
// a few frames per node and overflowed (a 2,000,000-node value died in
// constGlobal once mono and borrow walked on explicit stacks).
func TestDeepConstEmits(t *testing.T) {
	const n = 200_000
	e := newTestEnv()
	arr := e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, 16)}, types.U8)
	prev := e.consts.Intern(types.ConstValue{Type: arr, Align: 8, Bytes: make([]byte, 16)})
	for i := 1; i < n; i++ {
		b := make([]byte, 16)
		types.StoreUint(b, 8, uint64(i))
		prev = e.consts.Intern(types.ConstValue{Type: arr, Align: 8, Bytes: b,
			Relocs: []types.Reloc{{Offset: 8, Kind: types.RelocData, Data: prev}}})
	}
	hdr := make([]byte, 16)
	types.StoreUint(hdr[8:], 8, 16)
	c := e.consts.Intern(types.ConstValue{Type: types.Str, Align: 8, Bytes: hdr,
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocData, Data: prev}}})
	f := e.body(e.fn("List", nil, types.Str, 0))
	f.assign(place(0), use(mir.Const(types.Str, c)), f.at(2, 1))
	f.ret(f.at(2, 1))
	old := debug.SetMaxStack(32 << 20)
	defer debug.SetMaxStack(old)
	ll := emit(t, e, unitOf(e, []*mir.Body{f.b}), types.X86_64Linux, types.Dev)
	if got := strings.Count(ll, "\n@mk.c."); got < n {
		t.Fatalf("%d private const globals, want at least %d", got, n)
	}
}
