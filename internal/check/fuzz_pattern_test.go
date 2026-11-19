// Why: fuzzers found inputs that crashed check or let a Poison type through
// silently; each must stay fixed.
// What: TestFuzzPathPatternNotAPattern, TestFuzzFieldsLoopOneName,
// TestFuzzSpawnOnScopeValue.
// How: checks each kept fuzz input and its relatives and compares codes.

package check

import (
	"testing"

	"mk/internal/diag"
)

// TestFuzzPathPatternNotAPattern is corpus FuzzCheck's
// 54056f522b943918, `import std.net` then `match 0{net.Refused.A{}}`, and
// its relatives: a PathPattern that names no enum variant, error or struct
// (a member path past a variant, a member of a struct, error, primitive,
// type parameter, value or const; a function, interface or primitive with
// payload fields) was a silent Poison, and lower, which reads every accepted
// PathPattern's Use, raised "types.Items.Get: unknown item 0". `T.X` was
// check's own ICE: a RefDeferred keeps no root kind, and its A, a type
// parameter's index, was read as an item. Each is E0301 at the path.
func TestFuzzPathPatternNotAPattern(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"past a variant", "enum E { X }\nA(e: E) {\n\tmatch e {\n\t\tE.X.A {\n\t\t}\n\t}\n}\n", "E0301@4:3"},
		{"past a variant, fields", "enum E { X }\nA(e: E) {\n\tmatch e {\n\t\tE.X.A(b) {\n\t\t\t_ = b\n\t\t}\n\t}\n}\n", "E0301@4:3"},
		{"past a variant, its field", "enum E { X(a: int) }\nA(e: E) {\n\tmatch e {\n\t\tE.X.a {\n\t\t}\n\t}\n}\n", "E0301@4:3"},
		{"past a variant, scrutinee int", "enum E { X }\nA() {\n\tmatch 0 {\n\t\tE.X.A {\n\t\t}\n\t}\n}\n", "E0301@4:3"},
		{"struct member", "struct P {\n\tx: int\n}\nA(p: P) {\n\tmatch p {\n\t\tP.Q {\n\t\t}\n\t}\n}\n", "E0301@6:3"},
		{"struct method", "struct P {\n\tx: int\n}\nP.Q(): int {\n\treturn 1\n}\nA(p: P) {\n\tmatch p {\n\t\tP.Q {\n\t\t}\n\t}\n}\n", "E0301@9:3"},
		{"error member", "error Bad\nA(e: Bad) {\n\tmatch e {\n\t\tBad.Q {\n\t\t}\n\t}\n}\n", "E0301@4:3"},
		{"primitive member", "A(e: int) {\n\tmatch e {\n\t\tint.Q {\n\t\t}\n\t}\n}\n", "E0301@3:3"},
		{"primitive with fields", "A(e: int) {\n\tmatch e {\n\t\tint(x) {\n\t\t\t_ = x\n\t\t}\n\t}\n}\n", "E0301@3:3"},
		{"type parameter member", "A[T](e: T) {\n\tmatch e {\n\t\tT.X {\n\t\t}\n\t}\n}\n", "E0301@3:3"},
		{"value member", "A(e: int) {\n\tmatch e {\n\t\te.X {\n\t\t}\n\t}\n}\n", "E0301@3:3"},
		{"const member", "const C: int = 1\nA(e: int) {\n\tmatch e {\n\t\tC.X {\n\t\t}\n\t}\n}\n", "E0301@4:3"},
		{"function with fields", "F() {\n}\nA(e: int) {\n\tmatch e {\n\t\tF(x) {\n\t\t\t_ = x\n\t\t}\n\t}\n}\n", "E0301@5:3"},
		{"interface with fields", "interface I {\n}\nA(e: int) {\n\tmatch e {\n\t\tI(x) {\n\t\t\t_ = x\n\t\t}\n\t}\n}\n", "E0301@5:3"},
		// Accepted, as before: a variant through its enum, and through an
		// alias of it (`Q.X` is a value too), generic or not.
		{"variant", "enum E { X }\nA(e: E) {\n\tmatch e {\n\t\tE.X {\n\t\t}\n\t}\n}\n", ""},
		{"variant through an alias", "enum E { X }\ntype Q = E\nA(e: E) {\n\tmatch e {\n\t\tQ.X {\n\t\t}\n\t}\n}\n", ""},
		{"generic variant through an alias", "enum E[T] { X(a: T) }\ntype Q = E[int]\nA(e: E[int]) {\n\tmatch e {\n\t\tQ.X(a) {\n\t\t\t_ = a\n\t\t}\n\t}\n}\n", ""},
		{"unknown variant", "enum E { X }\nA(e: E) {\n\tmatch e {\n\t\tE.Y {\n\t\t}\n\t\t_ {\n\t\t}\n\t}\n}\n", "E0203@4:5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, ds := checkNoICE(t, tc.src)
			var kept []*diag.Diagnostic
			for _, d := range ds {
				if d.Code != diag.E0208 && d.Code != diag.E0306 {
					kept = append(kept, d)
				}
			}
			if got := codes(w, kept); got != tc.want {
				t.Errorf("diagnostics %q, want %q\n%s", got, tc.want, dump(w, kept))
			}
		})
	}
	// Through a module: resolve sets the enum segment's own Ref, which
	// pathRoot reads, so `lib.E.X` stays a variant and `lib.E.X.A` is E0301.
	const lib = "export enum E {\n\tX\n\tY(a: int)\n}\n"
	for _, tc := range []struct{ name, src, want string }{
		{"module variant", "import lib\nA(e: lib.E) {\n\tmatch e {\n\t\tlib.E.X {\n\t\t}\n\t\tlib.E.Y(a) {\n\t\t\t_ = a\n\t\t}\n\t}\n}\n", ""},
		{"module past a variant", "import lib\nA(e: lib.E) {\n\tmatch e {\n\t\tlib.E.X.A {\n\t\t}\n\t\t_ {\n\t\t}\n\t}\n}\n", "E0301@4:3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, ds := checkNoICEWith(t, opt{}, tc.src, func(w *world) { w.add("lib", opt{}, lib) })
			var kept []*diag.Diagnostic
			for _, d := range ds {
				if d.Code != diag.E0208 && d.Code != diag.E0306 {
					kept = append(kept, d)
				}
			}
			if got := codes(w, kept); got != tc.want {
				t.Errorf("diagnostics %q, want %q\n%s", got, tc.want, dump(w, kept))
			}
		})
	}
}

// TestFuzzFieldsLoopOneName is check FuzzCheck's f1ab159c3ca01cb5,
// `A[A:Fields[FormAt]](Args:A {for A in fields(&Args`: a fields loop with
// one name is the parser's E0101, but check walks the recovered tree, and
// the generic form read the absent second name (a nil *ast.Ident) for its
// hidden generic. The concrete form already tolerated it.
func TestFuzzFieldsLoopOneName(t *testing.T) {
	for _, src := range []string{
		"A[A:Fields[FormAt]](Args:A {for A in fields(&Args",
		"A[T: Fields[Format]](p: T) {\n\tfor v in fields(&p) {\n\t\t_ = v\n\t}\n}\n",
		"struct P {\n\tx: int\n}\nA(p: P) {\n\tfor v in fields(&p) {\n\t\t_ = v\n\t}\n}\n",
	} {
		// checkNoICEWith fails on an ICE; the E0101 is the parser's,
		// counted in built.parsed.
		_, b, _ := checkNoICEWith(t, opt{}, src)
		if b.parsed == 0 {
			t.Errorf("%q: no parse diagnostic for the one-name head", src)
		}
	}
}

// TestFuzzSpawnOnScopeValue is corpus FuzzCheck's c8cfb8114d6ab0ba,
// `A(s:Scope,x:mut i32){s.go Bump(x)}`: `.go` starts a task
// only on the scope binding or a `&Scope` value, else E0301. check took any
// Scope under any references, and lower's spawn then failed the MIR
// validator's rule M9 ("neither a scope local nor a &Scope"), an E0000.
func TestFuzzSpawnOnScopeValue(t *testing.T) {
	const tick = "Tick() {\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"the crasher", "Bump(a: mut i32) {\n}\n\nA(s: Scope, x: mut i32) {\n\ts.go Bump(x)\n}\n", "E0301@5:2"},
		{"Scope parameter", tick + "F(s: Scope) {\n\ts.go Tick()\n}\n", "E0301@5:2"},
		{"mut Scope parameter", tick + "F(s: mut Scope) {\n\ts.go Tick()\n}\n", "E0301@5:2"},
		{"&&Scope parameter", tick + "F(s: &&Scope) {\n\ts.go Tick()\n}\n", "E0301@5:2"},
		{"the binding", tick + "F(io: Io) {\n\tscope s := io.Scope() {\n\t\ts.go Tick()\n\t}\n}\n", ""},
		{"&Scope parameter", tick + "F(s: &Scope) {\n\ts.go Tick()\n}\n", ""},
		{"&s", tick + "F(io: Io) {\n\tscope s := io.Scope() {\n\t\t(&s).go Tick()\n\t}\n}\n", ""},
		{"a &Scope local", tick + "F(io: Io) {\n\tscope s := io.Scope() {\n\t\tr := &s\n\t\tr.go Tick()\n\t}\n}\n", ""},
		{"a &Scope field", tick + "struct H {\n\ts: &Scope\n}\n\nF(h: &H) {\n\th.s.go Tick()\n}\n", ""},
	})
}
