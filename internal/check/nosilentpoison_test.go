// Why: two fuzz findings in check (FuzzCheck d2354a05193a5dc0,
// FuzzNoSilentPoison e0c9160e3b178120) and the sibling forms beside them
// must stay fixed.
// What: TestFuzzCheck_LocalOutsideBody and TestNoSilentPoison_* (a method
// without a call, member siblings, raw access inside unsafe, universe names).
// How: checks each input and its relatives and compares codes; no Poison
// type may stand without a diagnostic.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/source"
)

// TestFuzzCheck_LocalOutsideBody is FuzzCheck's d2354a05193a5dc0: a local
// declared in an expression that no function body encloses (an `else`
// handler in an array length, a const or global initializer, a struct
// field's type) must draw a diagnostic, never an ICE. The ICE was
// resolve's: walker.declareLocal indexed w.bodies[len(w.bodies)-1] with no
// body frame pushed for these items (internal/resolve/walker.go).
//
// Each case asserts the language's verdict, E0208 aside (the unused handler
// names): `else` on a value that cannot fail is E0307, a handler in a
// type's const argument is resolve's E0705, and a const or global whose
// initializer handles a real failure with a diverging handler
// (`const X = F(1)`) is valid, locals and all.
func TestFuzzCheck_LocalOutsideBody(t *testing.T) {
	const p = "error Bad\n\nP(): Bad!i32 {\n\treturn 3\n}\n\n"
	for _, tc := range []struct{ name, src, want string }{
		{"crasher", "var A:[else{A:0", ""}, // parse errors: any diagnostic, no E0000
		{"const initializer", "const N: i32 = 1 else e { 0 }\n", "E0209@1:27 E0307@1:16"},
		{"global initializer", "var G: i32 = 1 else e { 0 }\n", "E0209@1:25 E0307@1:14"},
		{"global initializer block", "var G: i32 = 1 else { x := 0 }\n", "E0307@1:14"},
		{"struct field length", "struct S {\n\ta: [1 else e { 0 }]u8\n}\n", "E0705@2:6"},
		{"parameter length (has a body frame)", "F(a: [1 else e { 0 }]u8) {\n}\n", "E0705@1:7"},
		{"valid const handler with locals", p + "const N: i32 = P() else e {\n\t_ = e\n\tx := 1\n\t_ = x\n\tPanic(\"no\")\n}\n", ""},
		{"valid global handler with locals", p + "var G: i32 = P() else e {\n\t_ = e\n\tx := 1\n\t_ = x\n\tPanic(\"no\")\n}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, ds := checkNoICE(t, tc.src)
			var kept []*diag.Diagnostic
			for _, d := range ds {
				if d.Code != diag.E0208 {
					kept = append(kept, d)
				}
			}
			if tc.name == "crasher" {
				if len(ds) == 0 {
					t.Errorf("no diagnostic for %q", tc.src)
				}
				return
			}
			if got := codes(w, kept); got != tc.want {
				t.Errorf("diagnostics %q, want %q\n%s", got, tc.want, dump(w, kept))
			}
		})
	}
}

// TestNoSilentPoison_MethodWithoutCall is FuzzNoSilentPoison's
// e0c9160e3b178120 (`A(c:Cell[i32]){(c.Load)}`): a method named without a
// call is E0609 at the member name (Mk has no method values), in every
// value position; namespace form `T.M` stays a fn value.
func TestNoSilentPoison_MethodWithoutCall(t *testing.T) {
	const p = "struct P {\n\tx: i32\n}\n\nP.M(&self): i32 {\n\treturn self.x\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"crasher", "A(c:Cell[i32]){(c.Load)}", "E0609@1:19"},
		{"statement", "A(c: Cell[i32]) {\n\tc.Load\n}\n", "E0609@2:4"},
		{"bound", "A(c: Cell[i32]) {\n\tx := c.Load\n}\n", "E0609@2:9"},
		{"discarded", "A(c: Cell[i32]) {\n\t_ = c.Load\n}\n", "E0609@2:8"},
		{"operands", "A(c: Cell[i32]): bool {\n\treturn c.Load == c.Load\n}\n", "E0609@2:11 E0609@2:21"},
		{"user method", p + "A(q: P) {\n\t_ = q.M\n}\n", "E0609@10:8"},
		{"through a reference", p + "A(q: &P) {\n\t_ = q.M\n}\n", "E0609@10:8"},
		{"namespace form is a fn value", p + "A(): fn(&P): i32 {\n\treturn P.M\n}\n", ""},
		{"called", p + "A(q: P): i32 {\n\treturn q.M()\n}\n", ""},
		// As TestCheckGap_ScopeValueNotACall_Closed in internal/lower: a
		// scope over a method value, `io.Scope` with no call.
		{"scope over a method value", "W() {\n}\n\nA(io: Io) {\n\tscope a := io.Scope {\n\t\ta.go W()\n\t}\n}\n", "E0609@5:16"},
	})
	w := newWorld(t, true)
	b := checkBody(t, w, "E0609@1:19", "A(c:Cell[i32]){(c.Load)}")
	for _, d := range b.bodyDiags(t) {
		if fact(d, "method") != "Load" || len(d.Alternatives) != 2 || !strings.Contains(d.Alternatives[0], "c.Load(..)") {
			t.Errorf("facts %v alternatives %q", d.Facts, d.Alternatives)
		}
	}
}

// TestNoSilentPoison_MemberSiblings: the other member forms that typed as
// Poison with no diagnostic beside the method value: a receiverless
// Type.Name function or a variant through a value (called or not), a
// field through its type (called or not), and members reached through a
// raw pointer the member rules do not dereference.
func TestNoSilentPoison_MemberSiblings(t *testing.T) {
	const p = "struct P {\n\tx: i32\n}\n\nP.N(): P {\n\treturn P(x: 1)\n}\n\nP.M(&self): i32 {\n\treturn self.x\n}\n\n"
	const e = "enum E {\n\tA(x: i32)\n\tB\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"receiverless through a value", p + "A(q: P) {\n\t_ = q.N\n}\n", "E0203@14:8"},
		{"receiverless called through a value", p + "A(q: P) {\n\t_ = q.N()\n}\n", "E0203@14:8"},
		{"variant through a value", e + "A(v: E) {\n\t_ = v.B\n}\n", "E0203@7:8"},
		{"variant constructed through a value", e + "A(v: E) {\n\t_ = v.A(1)\n}\n", "E0203@7:8"},
		{"variant through the type", e + "A(): E {\n\treturn E.B\n}\n", ""},
		{"field through the type", p + "A() {\n\t_ = P.x\n}\n", "E0203@14:8"},
		{"field called through the type", p + "A() {\n\t_ = P.x(1)\n}\n", "E0203@14:8"},
		{"method call on a raw pointer", p + "A(q: *P) {\n\t_ = q.M()\n}\n", "E0203@14:8"},
		{"field through a raw pointer", p + "A(q: *P) {\n\t_ = q.x\n}\n", "E1103@14:6"},
		{"index of a raw pointer", "A(q: *[4]u8) {\n\t_ = q[0]\n}\n", "E0308@2:6"},
		{"slice of a raw pointer", "A(q: *[4]u8) {\n\t_ = q[0..1]\n}\n", "E0308@2:6"},
	})
}

// TestNoSilentPoison_RawInsideUnsafe: inside `unsafe` a raw dereference
// draws nothing (E1103 is outside only), so a field through a second raw
// layer must draw its own diagnostic; one layer is a field read.
func TestNoSilentPoison_RawInsideUnsafe(t *testing.T) {
	const p = "struct P {\n\tx: i32\n}\n\n"
	for _, tc := range []bodyCase{
		{"one layer", p + "A(q: *P): i32 {\n\tunsafe \"test\" {\n\t\treturn q.x\n\t}\n}\n", ""},
		{"two layers", p + "A(q: **P): i32 {\n\tunsafe \"test\" {\n\t\treturn q.x\n\t}\n}\n", "E0203@7:12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, true)
			var b *built
			if ie := source.Guard("m", source.Span{}, func() { b = w.add("m", opt{unsafe: true}, tc.src) }); ie != nil {
				t.Fatal(ie)
			}
			ds := append(b.diags(), b.bodyDiags(t)...)
			if got := codes(w, ds); got != tc.want {
				t.Fatalf("diagnostics %q, want %q\n%s", got, tc.want, dump(w, ds))
			}
		})
	}
}

// TestNoSilentPoison_UniverseNames is FuzzNoSilentPoison's d007d34ca9722e17
// (`A(){some}`, found after the first fix): a universe name in a
// form it has no meaning in typed as Poison with no diagnostic. `some`
// named without its call is E0304 with the signature; `fields` anywhere
// but a fields loop's source is E0301 fields-of; a value name or a bound
// called is E0301; SizeOf, AlignOf, Cast and Trunc without their one type
// argument are E0711 (as a generic function nothing fixes T of), with
// another count E0304 type-arguments. `ok` and `err` keep resolve's one
// E0201. The valid forms are the control.
func TestNoSilentPoison_UniverseNames(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"crasher", "A(){some}", "E0304@1:5"},
		{"some bound", "A() {\n\tx := some\n\t_ = x\n}\n", "E0304@2:7"},
		{"some returned", "F(): ?i32 {\n\treturn some\n}\n", "E0304@2:9"},
		{"some called", "F(): ?i32 {\n\treturn some(1)\n}\n", ""},
		{"fields named", "A(){fields}", "E0301@1:5"},
		{"fields called outside a loop", "A(){fields(1)}", "E0301@1:5"},
		{"fields as a loop source without a call", "F() {\n\tfor a in fields {\n\t}\n}\n", "E0301@2:11"},
		{"SizeOf without its type argument", "A(){SizeOf()}", "E0711@1:5"},
		{"SizeOf with a value", "A(){SizeOf(1)}", "E0711@1:5"},
		{"SizeOf with two type arguments", "A(){SizeOf[i32, i32]()}", "E0304@1:5"},
		{"AlignOf without its type argument", "A(){AlignOf()}", "E0711@1:5"},
		{"Cast without its type argument", "A(){Cast(1)}", "E0711@1:5"},
		{"Trunc without its type argument", "A(){Trunc()}", "E0711@1:5"},
		{"Cast with two type arguments", "A(){Cast[i32, u8](1)}", "E0304@1:5"},
		{"true called", "A(){true()}", "E0301@1:5"},
		{"none called", "A(){none()}", "E0301@1:5"},
		{"Target called", "A(){Target()}", "E0301@1:5"},
		{"bound called", "A(){Plain()}", "E0301@1:5"},
		{"Fields called", "A(){Fields[i32]()}", "E0301@1:5"},
		{"ok named", "A(){ok}", "E0201@1:5"},
		{"err called", "A(){err(1)}", "E0201@1:5"},
		{"builtins", "A(x: i32): int {\n\tAssert(true)\n\ty: u8 = Cast[u8](x)\n\t_ = y\n\treturn SizeOf[i32]() + AlignOf[i32]()\n}\n", ""},
	})
}
