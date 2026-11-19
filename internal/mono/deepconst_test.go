// Why: mono's literal canonicalization (canon.go constID) must walk a deep
// const value without deep Go recursion, or a large const overflows the stack.
// What: TestDeepConstCanonicalizes and its deepList builder.
// How: interns a 300,000-node linked list and canonicalizes it under a 16 MiB
// Go stack.

package mono

import (
	"encoding/binary"
	"runtime/debug"
	"testing"

	"mk/internal/types"
)

// deepList interns a list of n nodes, each 16 bytes: a u64 and a pointer
// word relocated to the node before it; it returns the head.
func deepList(ct *types.ConstTable, n int) types.ConstID {
	prev := types.NoConst
	for i := range n {
		b := make([]byte, 16)
		binary.LittleEndian.PutUint64(b, uint64(i))
		v := types.ConstValue{Type: types.U64, Align: 8, Bytes: b}
		if prev != types.NoConst {
			v.Relocs = []types.Reloc{{Offset: 8, Kind: types.RelocData, Data: prev}}
		}
		if err := v.Canonicalize(8); err != nil {
			panic(err)
		}
		prev = ct.Intern(v)
	}
	return prev
}

// TestDeepConstCanonicalizes: a 300,000-node list goes through
// constID under a 16 MiB Go stack, and comes back as itself (it names no
// literal). The recursive walk needed a frame per node and overflowed
// (a 2,000,000-node const killed mk in mono.(*literals).constID).
func TestDeepConstCanonicalizes(t *testing.T) {
	w := newWorld()
	head := deepList(w.consts, 300_000)
	old := debug.SetMaxStack(16 << 20)
	defer debug.SetMaxStack(old)
	if got := newLiterals(w).constID(head); got != head {
		t.Fatalf("constID(head) = %d, want %d (no literal in the list)", got, head)
	}
}
