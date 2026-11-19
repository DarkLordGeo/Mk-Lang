// Why: codegen emits nsw/nuw arithmetic from the no-wrap BinOps, so their
// validation and numbering must hold.
// What: TestSubMulNoWrap, for the BinOps SubNoWrap and MulNoWrap.
// How: validates and prints integer forms, refuses float ones (M4) and checks
// their order after AddNoWrap.

package mir

import (
	"strings"
	"testing"

	"mk/internal/types"
)

// TestSubMulNoWrap: an integer SubNoWrap and MulNoWrap validate and print
// by name, a float one is M4 naming the operator, they come after AddNoWrap
// in that order (iface encodes the numbering), and neither is a compare.
func TestSubMulNoWrap(t *testing.T) {
	if SubNoWrap != AddNoWrap+1 || MulNoWrap != AddNoWrap+2 {
		t.Fatalf("SubNoWrap = %d, MulNoWrap = %d, want %d and %d", SubNoWrap, MulNoWrap, AddNoWrap+1, AddNoWrap+2)
	}
	for _, c := range []struct {
		op   BinOp
		name string
	}{{SubNoWrap, "SubNoWrap"}, {MulNoWrap, "MulNoWrap"}} {
		f := newFixture()
		b := addNoWrapBody(f, types.I64)
		b.Blocks[1].Stmts[0].Rvalue.Bin = c.op
		if err := b.Validate(f.env()); err != nil {
			t.Fatalf("integer %s refused: %v", c.name, err)
		}
		if got := (&Printer{Types: f.tab, Items: f.items}).Print(b); !strings.Contains(got, c.name+"(copy _1, ") {
			t.Errorf("printer does not name %s:\n%s", c.name, got)
		}
		f = newFixture()
		b = addNoWrapBody(f, types.F64)
		b.Blocks[1].Stmts[0].Rvalue.Bin = c.op
		if err := b.Validate(f.env()); err == nil || !strings.Contains(err.Error(), "M4: ") || !strings.Contains(err.Error(), c.name+" on") {
			t.Errorf("float %s: want M4 naming it, got %v", c.name, err)
		}
		if c.op.IsCompare() {
			t.Errorf("%s.IsCompare() is true", c.name)
		}
	}
}
