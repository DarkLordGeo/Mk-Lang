// Why: a direct call must name its function, and producers rely on the zero
// values of fields added to the contract types.
// What: TestDirectCallNamesAnItem and TestZeroValues.
// How: clears CallDirect's Fn.Item and expects invariant M5; reads the unset
// fields of Bound, Contract and Module.

package mir

import (
	"strings"
	"testing"

	"mk/internal/types"
)

// TestDirectCallNamesAnItem: X.FnCover's Good validates, and the same
// body with its CallDirect's Fn.Item cleared is an M5 violation, with and
// without Env.Callee (the check does not depend on a signature lookup).
func TestDirectCallNamesAnItem(t *testing.T) {
	h1 := []types.Source{{Param: 1}}
	b, env, _ := fnCover(nil, h1, false)
	if err := b.Validate(env); err != nil {
		t.Fatalf("well-formed body refused: %v", err)
	}
	for _, withCallee := range []bool{true, false} {
		b, env, _ := fnCover(nil, h1, false)
		if !withCallee {
			env.Callee = nil
		}
		b.Blocks[0].Term.Callee.Fn.Item = types.NoItem
		err := b.Validate(env)
		if err == nil || !strings.Contains(err.Error(), "M5: ") || !strings.Contains(err.Error(), "direct call of no item") {
			t.Errorf("callee lookup %v: want M5 direct call of no item, got %v", withCallee, err)
		}
	}
}

// TestZeroValues pins what the later-added fields mean when a producer
// leaves them unset: a Bound's Inner is BoundInterface (an interface bound),
// a Contract has no Text, a Module no Imports.
func TestZeroValues(t *testing.T) {
	if types.BoundInterface != 0 {
		t.Fatalf("BoundInterface is %d; Bound.Inner's zero value must mean an interface", types.BoundInterface)
	}
	var b types.Bound
	var c types.Contract
	var m types.Module
	if b.Inner != types.BoundInterface || c.Text != "" || m.Imports != nil {
		t.Fatalf("zero values: Inner %d, Text %q, Imports %v", b.Inner, c.Text, m.Imports)
	}
	f := types.Bound{Kind: types.BoundFields, Inner: types.BoundPlain}
	if f.Item != types.NoItem || f.Inner == types.BoundFields {
		t.Fatalf("Fields[Plain] is %+v", f)
	}
}
