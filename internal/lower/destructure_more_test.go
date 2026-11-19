// Why: a destructure of a value none of whose leaves is plain must lower:
// `Enc(s: _, hist: _) := self` on a linear view struct of `mut []u8` fields
// is no ICE ("a destructure of a non-plain value with no plain leaf to
// take").
// What: TestDestructureAllWildcardNonPlain,
// TestTracerIndexBeforeItemsMut.
// How: lowers each program and finds the expected MIR statements in its
// text.

package lower

import "testing"

func TestDestructureAllWildcardNonPlain(t *testing.T) {
	l := lowerSrc(t, `linear view struct Enc {
	s: mut []u8
	hist: mut []u8
}

Enc.Abandon(self) {
	Enc(s: _, hist: _) := self
}
`)
	f := l.print("Enc:Abandon")
	// One take of a `_` leaf consumes the temporary, so borrow sees the
	// linear value destructured and its scope end is no E0402.
	wantIn(t, f, "_2 = move _1", "_3 = move _2.0", "scope_end _3")
}

// TestTracerIndexBeforeItemsMut: the index of an implementer runs
// before ItemsMut borrows it (left-to-right evaluation), as over an array.
func TestTracerIndexBeforeItemsMut(t *testing.T) {
	l := lowerSrc(t, `struct Counts {
	slots: List[int]
	slot: int
}

Counts.Pick(&self): int {
	return self.slot
}

Counts.Bump(mut self) {
	self.slots[self.Pick()] += 1
}
`)
	wantIn(t, l.print("Counts:Bump"), "Counts:Pick", "ItemsMut")
}
