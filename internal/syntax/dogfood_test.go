// Why: defects found by writing real programs in Mk must stay fixed.
// What: parser regression tests, each named after the dogfood defect it closes.
// How: parses the failing source and checks the tree and diagnostics.

package syntax

import (
	"strings"
	"testing"
)

// TestProcmanDestructureValue: a destructuring binding is a binding,
// so its right side is a value position: a value match or if there parses
// into the DestructureStmt; it was E0101.
func TestProcmanDestructureValue(t *testing.T) {
	for _, body := range []string{
		"(line, col) := match e {\n        .A { (line: 1, col: 2) }\n    }",
		"Point(x, y) := if c { p } else { q }",
		"var (a: x, b: y) := if c { r } else { s }",
	} {
		r := parse(t, "F() {\n    "+body+"\n}\n", Options{})
		if len(r.diags) != 0 {
			t.Fatalf("%q: diagnostics:\n  %s", body, strings.Join(r.codes(), "\n  "))
		}
		got := strings.Join(valueParents(r.file), " ")
		if !strings.HasPrefix(got, "*ast.DestructureStmt<-") {
			t.Errorf("%q: value parents %q", body, got)
		}
	}
}

// TestImgprocCpuBeforeRequires: a requires line after a cpu clause
// is one E0101 naming the order, at the requires; it was "function needs a
// body" at the signature and "expected an item" after it.
func TestImgprocCpuBeforeRequires(t *testing.T) {
	r := parse(t, "F(n: int): int cpu \"x86-64-v3\"\n    requires n > 0\n{\n    return n\n}\n", Options{})
	if len(r.diags) != 1 || !strings.Contains(r.diags[0].Message, "requires and ensures lines come before the cpu") {
		t.Fatalf("diagnostics:\n  %s", strings.Join(r.codes(), "\n  "))
	}
	if ok := parse(t, "F(n: int): int\n    requires n > 0\n    cpu \"x86-64-v3\"\n{\n    return n\n}\n", Options{}); len(ok.diags) != 0 {
		t.Fatalf("the accepted order: %s", strings.Join(ok.codes(), "\n  "))
	}
}
