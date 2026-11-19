// Why: a const value can be a chain of 300,000 relocations, and walking it
// must not overflow the Go stack.
// What: a test that constTakes (constvalue.go) walks a deep const value.
// How: interns the chain, lowers the stack limit to 16 MiB and calls
// constTakes.

package borrow

import (
	"encoding/binary"
	"runtime/debug"
	"testing"

	"mk/internal/types"
)

// TestDeepConstTakes: a 300,000-node list of u64 words, each
// relocated to the node before it, takes no store, and constTakes answers
// under a 16 MiB Go stack, where its recursive walk needed a frame per
// node.
func TestDeepConstTakes(t *testing.T) {
	k := newKit(t)
	ct := k.tab.Consts()
	prev := types.NoConst
	for i := range 300_000 {
		b := make([]byte, 16)
		binary.LittleEndian.PutUint64(b, uint64(i))
		v := types.ConstValue{Type: types.U64, Align: 8, Bytes: b}
		if prev != types.NoConst {
			v.Relocs = []types.Reloc{{Offset: 8, Kind: types.RelocData, Data: prev}}
		}
		if err := v.Canonicalize(8); err != nil {
			t.Fatal(err)
		}
		prev = ct.Intern(v)
	}
	w := walkerOf(k, nil)
	old := debug.SetMaxStack(16 << 20)
	defer debug.SetMaxStack(old)
	if w.constTakes(prev) {
		t.Fatal("constTakes = true, want false (u64 words take no store)")
	}
}
