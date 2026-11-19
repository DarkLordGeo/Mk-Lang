// Why: the check package's skeleton (the fake prelude, unit order, body
// tables, the ICE guard) underlies every other test.
// What: TestFakePreludeResolves, TestUnitsOrder,
// TestBodyTablesCoverNodeRange, TestGuardTurnsICEIntoE0000.
// How: builds small worlds with the harness and checks each piece directly.

package check

import (
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/types"
)

const unitsSrc = `struct P { x: int }

P.Get(&self): int {
	return self.x
}

F(x: int): int requires x > 0 {
	return x
}

G() {
	_ = fn(a: int): int { return a }
}

test "t" {
}

const C = 1

var V: int = 2
`

func TestFakePreludeResolves(t *testing.T) {
	w := newWorld(t, true)
	b := w.mods["std.core"]
	if len(b.rdiags) > 0 {
		t.Fatalf("fake prelude: %s", dump(w, b.rdiags))
	}
}

func TestUnitsOrder(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{}, unitsSrc)
	var got []string
	for _, u := range b.m.Units() {
		got = append(got, b.key(u))
	}
	want := []string{"F", "G", "P:Get", `#test("t")`, "C", "V", "F#requires0", "G#fn0"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("Units = %q, want %q", got, want)
	}
	for _, u := range b.m.Units() {
		if w.items.Get(u).Kind == types.KindStruct {
			t.Errorf("struct %s is a unit", b.key(u))
		}
	}
}

func TestBodyTablesCoverNodeRange(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{}, unitsSrc)
	for _, u := range b.m.Units() {
		body := w.c.Check(b.m, u)
		lo, hi := nodeRange(body.Node)
		if body.Base != lo || len(body.Types) != int(hi-lo)+1 {
			t.Errorf("%s: Base %d len %d, want [%d, %d]", b.key(u), body.Base, len(body.Types), lo, hi)
		}
		n := 0
		ast.Inspect(body.Node, func(x ast.Node) bool {
			if x == nil {
				return false
			}
			n++
			_ = body.slot(x.ID()) // ICE when outside the range
			return true
		})
		if n != len(body.Types) {
			t.Errorf("%s: %d nodes in a range of %d: the unit's IDs are not contiguous", b.key(u), n, len(body.Types))
		}
		if body.Diverges.Has(hi+1, body.Base) {
			t.Errorf("%s: bit past the range", b.key(u))
		}
		body.Diverges.set(hi, body.Base)
		if !body.Diverges.Has(hi, body.Base) {
			t.Errorf("%s: set bit not seen", b.key(u))
		}
	}
}

func TestGuardTurnsICEIntoE0000(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{stopBefore: true}, unitsSrc)
	bad := b.item("F")
	var seen []types.ItemID
	w.c.hookItem = func(id types.ItemID) {
		seen = append(seen, id)
		if id == bad {
			panic("boom")
		}
	}
	m := w.c.Signatures(&ModuleInput{Module: b.id, Files: b.files, Info: b.info})
	w.c.hookItem = nil
	var ice []*diag.Diagnostic
	for _, d := range m.Diags {
		if d.Code == diag.E0000 {
			ice = append(ice, d)
		}
	}
	if len(ice) != 1 {
		t.Fatalf("want one E0000, got:\n%s", dump(w, m.Diags))
	}
	if got := fact(ice[0], "where"); got != "m.F" {
		t.Errorf("where = %q, want m.F", got)
	}
	if len(seen) != len(b.info.Items) {
		t.Errorf("items visited %d, want all %d: an ICE stopped the walk", len(seen), len(b.info.Items))
	}
}
