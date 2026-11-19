// Why: a generator can take a non-generic function as its argument, and the
// stored generator kinds must keep their meaning.
// What: TestGenFunction.
// How: checks that GenFunction comes after GenLiteral and that the Receiver
// Static is NoReceiver.

package types

import "testing"

// TestGenFunction: GenFunction is appended after GenLiteral, so no
// stored GenDecl changes meaning, and the generator API's Receiver Static is
// NoReceiver, the zero Receiver (std.core's enum order, Static first).
func TestGenFunction(t *testing.T) {
	if GenType != 0 || GenInterface != 1 || GenLiteral != 2 || GenFunction != 3 {
		t.Errorf("gen argument kinds %d %d %d %d", GenType, GenInterface, GenLiteral, GenFunction)
	}
	if NoReceiver != 0 || ByValue != 1 || ByShared != 2 || ByMut != 3 {
		t.Error("Receiver must keep std.core.Receiver's order: Static Value Ref Mut")
	}
}
