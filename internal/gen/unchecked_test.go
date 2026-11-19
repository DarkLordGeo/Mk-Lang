// Why: a generator reading an interface method whose parameter type failed
// to check met NoType and ended in an internal compiler error
// (types.Table.Get: unknown type 0) instead of leaving the checker's
// diagnostic alone.
// What: TestUncheckedMethodTypeIsPoison, a regression test for the fuzz
// crasher FuzzCheck/fe47ad58fd7f2a9a.
// How: runs generators over a method with an unchecked parameter type and
// expects the checker's E0201 alone.

package gen

import (
	"strings"
	"testing"

	"mk/internal/diag"
)

// TestUncheckedMethodTypeIsPoison: Method.Params, Param.Type and
// Method.Signature over a parameter type that failed to check poison their
// gen line quietly, so the checker's E0201 is the only error; a line of the
// same module that reads only the method names still runs and emits.
func TestUncheckedMethodTypeIsPoison(t *testing.T) {
	w := newMkWorld(t)
	w.add("refl", false, map[string]string{"refl.mk": `export Kinds(g: mut Gen, t: Type) {
    var n := 0
    for m in t.Methods() {
        for p in m.Params() {
            if p.Type().Kind() == TypeKind.Scalar {
                n += 1
            }
        }
    }
    name := g.Fresh("Kinds")
    g.Emit(quote {
        $name(): int {
            return $n
        }
    })
}

export Sigs(g: mut Gen, t: Type) {
    var body := g.Block()
    for m in t.Methods() {
        body.Add(m.Signature())
    }
    name := g.Fresh("Sigs")
    g.Emit(quote {
        $name(): int {
            return 0
        }
    })
}

export Names(g: mut Gen, t: Type) {
    var n := 0
    for m in t.Methods() {
        n += m.Name().Len()
    }
    name := g.Fresh("Names")
    g.Emit(quote {
        $name(): int {
            return $n
        }
    })
}
`})
	m := w.addLoose("main", false, map[string]string{"main.mk": `import refl

interface Device {
    Send(mut self, a: Nope)
}

gen refl.Kinds(Device)
gen refl.Sigs(Device)
gen refl.Names(Device)
`})
	var codes []string
	for _, d := range m.diags {
		codes = append(codes, d.Code.String())
		if d.Code == diag.E0000 || d.Code == diag.E0901 {
			t.Errorf("want only the checker's error, got %s", w.dump([]*diag.Diagnostic{d}))
		}
	}
	if !strings.Contains(strings.Join(codes, " "), "E0201") {
		t.Errorf("want the checker's E0201 for Nope, got %v:\n%s", codes, w.dump(m.diags))
	}
	if m.gen == nil || len(m.gen.Lines) != 3 {
		t.Fatalf("want 3 gen line results, got %+v", m.gen)
	}
	for i, want := range []bool{true, true, false} {
		lr := m.gen.Lines[i]
		if !lr.Ran || lr.Poisoned != want {
			t.Errorf("line %d: ran %v poisoned %v, want ran, poisoned %v", i, lr.Ran, lr.Poisoned, want)
		}
	}
	if m.gen.AST == nil || len(m.gen.AST.Items) != 1 {
		t.Fatalf("want the Names line's one item generated, got %+v", m.gen.AST)
	}
}
