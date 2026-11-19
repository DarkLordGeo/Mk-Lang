// Why: an undecided bracket over a member, the universe name Target and
// `some(x)` must each get a type; a Poison with no diagnostic there reaches
// lower as a `(check gap)` ICE.
// What: TestUndecidedIndexOnMember, TestTargetIsTargetInfo,
// TestSomeTypesUnderExpected.
// How: checks small bodies and renders the type of every node (nodeTypes);
// an undecided bracket over a member is an index (resolve's ReadUndecided).

package check

import (
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/types"
)

// nodeTypes checks unit name of b and renders the type of every node of
// Go type T as "line:col=type", in source order.
func nodeTypes[T ast.Node](t *testing.T, b *built, name string) (string, *Body) {
	t.Helper()
	body := b.w.c.Check(b.m, b.item(name))
	var out []string
	ast.Inspect(body.Node, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if _, ok := n.(T); ok {
			out = append(out, b.w.lc(n.Span())+"="+b.ts(body.TypeOf(n.ID()), nil))
		}
		return true
	})
	return strings.Join(out, " "), body
}

func TestUndecidedIndexOnMember(t *testing.T) {
	t.Parallel()
	src := "struct H {\n\titems: []i32\n\tbuf: [4]u8\n}\n\n" +
		"H.At(&self, i: int): i32 {\n\treturn self.items[i]\n}\n\n" + // 7
		"H.Set(mut self) {\n\tself.buf[0] = 1\n}\n\n" + // 11
		"Ref(h: &H): &i32 {\n\treturn &h.items[1]\n}\n" // 15
	w := newWorld(t, true)
	b := checkBody(t, w, "", src)
	if got, _ := nodeTypes[*ast.IndexExpr](t, b, "H:At"); got != "7:9=i32" {
		t.Errorf("At: %s", got)
	}
	if got, _ := nodeTypes[*ast.IndexExpr](t, b, "H:Set"); got != "11:2=u8" {
		t.Errorf("Set: %s", got)
	}
	got, body := nodeTypes[*ast.FieldExpr](t, b, "Ref")
	if got != "15:10=[]i32" {
		t.Errorf("Ref: %s", got)
	}
	ast.Inspect(body.Node, func(n ast.Node) bool {
		if fe, ok := n.(*ast.FieldExpr); ok {
			if a := *body.adjust(fe.X.ID()); a.Kind != AdjDeref || a.Deref != 1 {
				t.Errorf("h in h.items: adjust %+v, want AdjDeref 1", a)
			}
		}
		return true
	})
	// The index is checked now: a str index and a str element are E0301.
	checkBody(t, newWorld(t, true), "E0301@7:20",
		"struct H {\n\titems: []i32\n}\n\nH.Bad(&self): i32 {\n\ts := \"x\"\n\treturn self.items[s]\n}\n")
	checkBody(t, newWorld(t, true), "E0301@6:9",
		"struct H {\n\titems: []i32\n}\n\nH.Bad(&self): str {\n\treturn self.items[0]\n}\n")
}

func TestTargetIsTargetInfo(t *testing.T) {
	t.Parallel()
	src := "P(): int {\n\tif Target.arch == Arch.A {\n\t\treturn 1\n\t}\n\treturn 2\n}\n\n" +
		"Q(): TargetInfo {\n\treturn Target\n}\n"
	w := newWorld(t, true)
	b := checkBody(t, w, "", src)
	got, body := nodeTypes[*ast.Ident](t, b, "P")
	if !strings.Contains(got, "2:5=TargetInfo") || !strings.Contains(got, "2:12=<none>") {
		t.Errorf("P idents: %s", got)
	}
	if got, _ := nodeTypes[*ast.FieldExpr](t, b, "P"); !strings.HasPrefix(got, "2:5=Arch") {
		t.Errorf("P fields: %s", got)
	}
	var targetUses int
	ast.Inspect(body.Node, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if u := *body.use(id.ID()); u.Kind == UseBuiltin && Builtin(u.Index) == BuiltinTarget {
				targetUses++
			}
		}
		return true
	})
	if targetUses != 1 {
		t.Errorf("P: %d BuiltinTarget uses, want 1", targetUses)
	}
	targets := func(body *Body) string {
		var fs []string
		for _, r := range body.Reads.Sorted() {
			if r.Kind == types.ReadTarget {
				fs = append(fs, r.Name)
			}
		}
		return strings.Join(fs, ",")
	}
	if got := targets(body); got != "arch" {
		t.Errorf("P reads Target fields %q, want arch", got)
	}
	if got := targets(w.c.Check(b.m, b.item("Q"))); got != "arch,os,pointer_bytes" {
		t.Errorf("Q reads Target fields %q, want all three", got)
	}
	// Target is a value: it has fields, not methods, and is not a type.
	checkBody(t, newWorld(t, true), "E0301@2:9", "P(): int {\n\treturn Target.os\n}\n")
}

func TestSomeTypesUnderExpected(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := checkBody(t, w, "", "Opt[T](x: ?T): ?T {\n\treturn x\n}\n\n"+
		"F(): ?u8 {\n\tvar o: ?i32 = some(1)\n\tg := some(2)\n\th := Opt(some(3))\n\treturn some(4)\n}\n")
	got, _ := nodeTypes[*ast.CallExpr](t, b, "F")
	if got != "6:16=?i32 7:7=?int 8:7=?int 8:11=?int 9:9=?u8" {
		t.Errorf("calls: %s", got)
	}
	got, _ = nodeTypes[*ast.BasicLit](t, b, "F")
	if got != "6:21=i32 7:12=int 8:16=int 9:14=u8" {
		t.Errorf("literals: %s", got)
	}
	runBodyCases(t, []bodyCase{
		{"non-optional", "F() {\n\tc: i32 = some(0)\n}\n", "E0301@2:11"},
		{"result", "F(): str {\n\treturn some(\"x\")\n}\n", "E0301@2:9"},
		{"element once", "F() {\n\tc: ?i32 = some(\"x\")\n}\n", "E0301@2:17"},
		{"no argument", "F(): ?int {\n\treturn some()\n}\n", "E0304@2:9"},
		{"two arguments", "F(): ?int {\n\treturn some(1, 2)\n}\n", "E0304@2:9"},
		{"generic body", "G[T](): ?T {\n\treturn some(1)\n}\n", "E0703@2:14"},
	})
}
