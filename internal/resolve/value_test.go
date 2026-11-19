// Why: value constructs (if and match as values) and destructured loop
// variables open scopes of their own, which resolve must get right.
// What: TestArmScopes and TestPatternLocals.
// How: small modules through the helpers_test.go harness, checked for E0201
// and E0206.

package resolve

import "testing"

// TestArmScopes: an arm's bindings stay in the arm, and a binding's own
// name is declared after its value (E0201 for x := if c { x } ..).
func TestArmScopes(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, `F(c: bool, o: ?int): int {
    x := if c {
        y := 1
        y
    } else if z := o {
        z
    } else {
        0
    }
    v := if c { v } else { 0 }
    return x + y + v
}
`)
	b.want(t, "E0201@10:17", "E0201@11:16")
}

// TestPatternLocals: the names of a destructured loop variable are body
// locals, and one that shadows a local is E0206.
func TestPatternLocals(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, `F(xs: [](a: int, b: int)): int {
    var n := 0
    for (a, b) in xs {
        n += a + b
    }
    for i, (a: p, b: q) in xs {
        n += i + p + q
    }
    for (n, b) in xs {
        _ = b
    }
    return n + a
}
`)
	b.want(t, "E0206@9:10", "E0208@9:10", "E0201@12:16")
}
