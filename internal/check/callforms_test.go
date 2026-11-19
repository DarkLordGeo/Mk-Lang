// Why: assignment targets, enum and alias calls, namespace-form method
// calls, `E!T` operands and E0804 cascades each let a silent Poison or a
// wrong code through; each must stay fixed.
// What: TestE0301_NotAPlace, TestEnumAndAliasCallsConvert,
// TestNamespaceFormMethodCall, TestE0802_OperatorOperand,
// TestE0804_NoCascade, TestValidSeedsClean.
// How: checks small programs and compares codes, Use and Adjust records,
// and the Poison expressions lower would read.

package check

import (
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/types"
)

// TestE0301_NotAPlace: an assignment's target must be a
// local, a field, an element or a dereference by what it names, not by its
// syntax. A const, a function, a universe name and an item named through a
// module or a type are E0301 `reason: not a place`, one diagnostic each.
func TestE0301_NotAPlace(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"const", "const K: int = 1\n\nG() {\n\tK = 2\n}\n", "E0301@4:2"},
		{"function", "F(x: int) {\n\tF = F\n}\n", "E0301@2:2"},
		{"universe", "H(): int {\n\ttrue = false\n\treturn 0\n}\n", "E0301@2:2"},
		{"compound on a const", "const K: int = 1\n\nG() {\n\tK += 2\n}\n", "E0301@4:2"},
		{"type function", "struct P {\n\tx: int\n}\n\nP.Make(): P {\n\treturn P(x: 0)\n}\n\nG() {\n\tP.Make = P.Make\n}\n", "E0301@10:2"},
		{"variant", "enum C {\n\tR\n\tS\n}\n\nG() {\n\tC.R = C.S\n}\n", "E0301@7:2"},
		{"generic fn value", "Id[T](x: T): T {\n\treturn x\n}\n\nG() {\n\tId[int] = Id[int]\n}\n", "E0301@6:2"},
		{"call", "G(): int {\n\treturn 1\n}\n\nF() {\n\tG() = 2\n}\n", "E0301@6:2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := checkBody(t, w, tc.want, tc.src)
			ds := b.bodyDiags(t)
			if got := fact(ds[0], "reason"); got != "not a place" {
				t.Errorf("reason %q", got)
			}
		})
	}
	t.Run("imported const", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, true)
		w.add("lib", opt{}, "export const C: int = 1\n")
		b := checkBody(t, w, "E0301@4:2", "import lib\n\nG() {\n\tlib.C = 1\n}\n")
		if got := fact(b.bodyDiags(t)[0], "reason"); got != "not a place" {
			t.Errorf("reason %q", got)
		}
	})
	runBodyCases(t, []bodyCase{
		{"places", "struct P {\n\tx: int\n}\n\nG(r: mut int, q: mut P) {\n\tvar x := 0\n\tvar p := P(x: 0)\n\tvar xs := [1, 2]\n\tvar t := (a: 1, b: 2)\n\tx = 1\n\tx += 1\n\tp.x = 1\n\tq.x = 2\n\txs[0] = 1\n\tt.a = 3\n\t*r = 1\n}\n", ""},
		{"unknown name stays silent", "G() {\n\tnope = 1\n}\n", "E0201@2:2"},
		{"element of a Slice implementer", "struct Buf {\n\tb: []u8\n}\n\nimpl Slice[u8] for Buf {\n}\n\nF(b: mut Buf) {\n\tb[0] = 1\n}\n", ""},
	})
}

// TestEnumAndAliasCallsConvert: a call of an
// enum, or of an alias of anything but a struct, is the conversion `T(x)`
// with the alias expanded, judged like any other, never a silent Poison
// that lower turns into an ICE.
func TestEnumAndAliasCallsConvert(t *testing.T) {
	const decls = "enum Rep: u8 {\n\tA\n\tB\n}\n\nenum C {\n\tR\n}\n\ntype CA = C\n\ntype Byte = u8\n\nerror E1\n\nerror E2\n\ntype LE = E1 | E2\n\ntype Pair = (left: int, right: int)\n\n"
	// decls is 21 lines; a body's first statement is on line 23.
	runBodyCases(t, []bodyCase{
		{"integer to repr enum", decls + "X(x: u8): Rep {\n\treturn Rep(x)\n}\n", "E0303@23:9"},
		{"literal to repr enum", decls + "X(): Rep {\n\treturn Rep(1)\n}\n", "E0303@23:9"},
		{"enum identity", decls + "X(r: Rep): Rep {\n\treturn Rep(r)\n}\n", ""},
		{"enum of nothing", decls + "X(): C {\n\treturn C()\n}\n", "E0304@23:9"},
		{"integer to plain enum", decls + "X(): C {\n\treturn C(1)\n}\n", "E0302@23:9"},
		{"named argument", decls + "X(): C {\n\treturn C(v: 1)\n}\n", "E0304@23:9"},
		{"alias of an enum", decls + "X(): C {\n\treturn CA(1)\n}\n", "E0302@23:9"},
		{"alias of an error set", decls + "X(): LE {\n\treturn LE()\n}\n", "E0304@23:9"},
		{"alias of a record", decls + "X(): Pair {\n\treturn Pair(left: 1, right: 2)\n}\n", "E0304@23:9"},
		{"alias identity", decls + "X(x: u8): int {\n\treturn int(Byte(x))\n}\n", ""},
		{"alias literal in range", decls + "X(): u8 {\n\treturn Byte(255)\n}\n", ""},
		{"alias literal out of range", decls + "X(): u8 {\n\treturn Byte(300)\n}\n", "E0303@23:9"},
		{"alias lossy", decls + "X(x: int): u8 {\n\treturn Byte(x)\n}\n", "E0303@23:9"},
		{"alias lossless", decls + "X(x: u8): u16 {\n\treturn u16(Byte(x))\n}\n", ""},
		{"alias const by value", decls + "const K: int = 7\n\nX(): u8 {\n\treturn Byte(K)\n}\n", ""},
		{"alias const out of range", decls + "const K: int = 700\n\nX(): u8 {\n\treturn Byte(K)\n}\n", "E0303@25:9"},
		{"generic enum bare", "enum Opt[T] {\n\tNone\n\tSome(v: T)\n}\n\nX(): Opt[int] {\n\treturn Opt(1)\n}\n", "E0304@7:9"},
		{"generic enum with arguments", "enum Opt[T] {\n\tNone\n\tSome(v: T)\n}\n\nX(): Opt[int] {\n\treturn Opt[int](1)\n}\n", "E0302@7:9"},
		{"generic alias", "type L[T] = []T\n\nX(xs: []int): []int {\n\treturn L[int](xs)\n}\n", ""},
	})
	// A valid conversion through an alias is typed like `u8(x)`: the call
	// is BuiltinConvert of type u8 and the body has no silent Poison.
	w := newWorld(t, true)
	b := checkBody(t, w, "", decls+"X(x: u8): int {\n\treturn int(Byte(x))\n}\n")
	body := w.c.Check(b.m, b.item("X"))
	if bad := silentPoison(w, body); len(bad) > 0 {
		t.Fatalf("Poison with no diagnostic: %v", bad)
	}
	n := 0
	ast.Inspect(body.Node, func(nd ast.Node) bool {
		if c, ok := nd.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && w.in.Str(id.Name) == "Byte" {
				n++
				use := *body.use(c.ID())
				if use.Kind != UseBuiltin || Builtin(use.Index) != BuiltinConvert || body.TypeOf(c.ID()) != types.U8 {
					t.Errorf("Byte(x): use %+v type %s", use, w.tab.TypeString(body.TypeOf(c.ID()), nil))
				}
			}
		}
		return nd != nil
	})
	if n != 1 {
		t.Fatalf("%d Byte calls", n)
	}
}

// TestNamespaceFormMethodCall: a method called in namespace form takes
// its receiver as an ordinary first argument. The call's Use is UseItem of
// the method; the member node keeps no Use and the type head no Adjust, so
// lower cannot take it for a dot call (whose member node carries the call's
// Use) and read the type name as a receiver.
func TestNamespaceFormMethodCall(t *testing.T) {
	src := "struct Pt {\n\tx: int\n\ty: int\n}\n\nPt.Sum(&self): int {\n\treturn self.x + self.y\n}\n\nPt.Bump(mut self) {\n\tself.x += 1\n}\n\nPt.Get(self): int {\n\treturn self.x\n}\n\n" +
		"interface Named {\n\tName(&self): int\n}\n\nimpl Named for Pt {\n\tName(&self): int {\n\t\treturn self.y\n\t}\n}\n\n" +
		"Z1(): int {\n\tp := Pt(x: 1, y: 2)\n\treturn Pt.Sum(&p)\n}\n\n" +
		"Z2(): int {\n\tvar p := Pt(x: 1, y: 2)\n\tPt.Bump(mut p)\n\treturn p.x\n}\n\n" +
		"Z3(): int {\n\tp := Pt(x: 1, y: 2)\n\treturn Pt.Get(p)\n}\n\n" +
		"Z4(): int {\n\tp := Pt(x: 1, y: 2)\n\treturn Pt.Name(&p)\n}\n\n" +
		"Z5(): int {\n\tp := Pt(x: 1, y: 2)\n\treturn p.Sum()\n}\n"
	w := newWorld(t, true)
	b := checkBody(t, w, "", src)
	for _, unit := range []string{"Z1", "Z2", "Z3", "Z4", "Z5"} {
		body := w.c.Check(b.m, b.item(unit))
		if bad := silentPoison(w, body); len(bad) > 0 {
			t.Errorf("%s: Poison with no diagnostic: %v", unit, bad)
		}
		ast.Inspect(body.Node, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return n != nil
			}
			fe, ok := c.Fun.(*ast.FieldExpr)
			if !ok || w.in.Str(fe.Name.Name) == "Pt" {
				return true
			}
			call, member, adj := *body.use(c.ID()), *body.use(fe.ID()), *body.adjust(fe.X.ID())
			if unit == "Z5" {
				if call.Kind != UseMethod || member != call || adj.Kind != AdjRefShared {
					t.Errorf("dot call: call %+v member %+v adjust %+v", call, member, adj)
				}
				return true
			}
			if call.Kind != UseItem || call.Item == types.NoItem {
				t.Errorf("%s: call use %+v", unit, call)
			}
			if member.Kind != UseNone || adj.Kind != AdjNone {
				t.Errorf("%s: namespace form marked like a dot call: member %+v adjust %+v", unit, member, adj)
			}
			return true
		})
	}
	// An interface has no namespace: `Named.Name(&p)` is E0301, so E0206's
	// ambiguous-member offers only a rename (TestUseSiteAmbiguous).
	runBodyCases(t, []bodyCase{
		{"interface head", src + "\nZ6(): int {\n\tp := Pt(x: 1, y: 2)\n\treturn Named.Name(&p)\n}\n", "E0301@56:9"},
		{"receiver type", src + "\nZ6(): int {\n\tp := Pt(x: 1, y: 2)\n\treturn Pt.Sum(p)\n}\n", "E0301@56:16"},
	})
}

// TestE0802_OperatorOperand: an `E!T` value as an
// operator's operand is E0802 at that operand with the `try` fix, and
// nothing else: no E0301 at the other operand adapted to `E!T`, no E0308 on
// the operator. Each unhandled value is one root cause, so `F() == F()`
// has two.
func TestE0802_OperatorOperand(t *testing.T) {
	const decls = "error E1\n\nF(): E1!int {\n\treturn 1\n}\n\nF2(): E1!bool {\n\treturn true\n}\n\n"
	// A body's first statement is on line 12.
	runBodyCases(t, []bodyCase{
		{"left of +", decls + "A(): int {\n\treturn F() + 1\n}\n", "E0802@12:9"},
		{"right of +", decls + "A(): int {\n\treturn 1 + F()\n}\n", "E0802@12:13"},
		{"both of ==", decls + "A(): bool {\n\treturn F() == F()\n}\n", "E0802@12:16 E0802@12:9"},
		{"against a local", decls + "A(x: int): bool {\n\treturn x < F()\n}\n", "E0802@12:13"},
		{"negation", decls + "A(): int {\n\treturn -F()\n}\n", "E0802@12:10"},
		{"negation unexpected", decls + "A() {\n\t_ = -F()\n}\n", "E0802@12:7"},
		{"not", decls + "A(): bool {\n\treturn !F2()\n}\n", "E0802@12:10"},
		{"complement", decls + "A(): int {\n\treturn ~F()\n}\n", "E0802@12:10"},
		{"and", decls + "A(b: bool): bool {\n\treturn b && F2()\n}\n", "E0802@12:14"},
		{"shift", decls + "A(): int {\n\treturn F() << 1\n}\n", "E0802@12:9"},
		{"if condition", decls + "A(): E1!int {\n\tif F() == 1 {\n\t\treturn 1\n\t}\n\treturn 0\n}\n", "E0802@12:5"},
		{"try is the consumer", decls + "A(): E1!int {\n\treturn try F() + 1\n}\n", ""},
		{"try both", decls + "A(): E1!bool {\n\treturn try F() == try F()\n}\n", ""},
	})
	w := newWorld(t, true)
	b := checkBody(t, w, "E0802@12:9", decls+"A(): int {\n\treturn F() + 1\n}\n")
	d := b.bodyDiags(t)[0]
	if len(d.Fixes) != 1 || len(d.Fixes[0].Edits) != 1 || d.Fixes[0].Edits[0].Text != "try " {
		t.Errorf("fix %+v, want the `try ` insertion", d.Fixes)
	}
}

// TestE0804_NoCascade: after E0804 the `try` is Poison, so the binding it
// initializes draws nothing more at its uses.
func TestE0804_NoCascade(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"optional", "T1(o: ?int): int {\n\tx := try o\n\treturn x\n}\n", "E0804@2:7"},
		{"optional in a fallible function", "error E1\n\nT1(o: ?int): E1!int {\n\tx := try o\n\treturn x + 1\n}\n", "E0804@4:7"},
		{"plain", "T1(s: str): int {\n\treturn try s\n}\n", "E0804@2:9"},
	})
	w := newWorld(t, true)
	b := checkBody(t, w, "E0804@2:7", "T1(o: ?int): int {\n\tx := try o\n\treturn x\n}\n")
	if d := b.bodyDiags(t)[0]; d.Code != diag.E0804 || fact(d, "found") != "?int" {
		t.Errorf("%v found %s", d.Code, fact(d, "found"))
	}
}

// TestValidSeedsClean: the valid seeds (reviewSeeds) check with
// no diagnostic and no Poison expression lower would read (silentPoison).
func TestValidSeedsClean(t *testing.T) {
	for i, src := range reviewSeeds {
		w := newWorld(t, true)
		b := checkBody(t, w, "", src)
		for _, id := range b.m.Units() {
			if bad := silentPoison(w, w.c.Check(b.m, id)); len(bad) > 0 {
				t.Errorf("seed %d: Poison with no diagnostic: %v", i, bad)
			}
		}
	}
}
