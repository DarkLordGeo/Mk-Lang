// Why: a generic function named as a value needs its type arguments, so
// the instance is explicit.
// What: TestE0711_GenericValue and TestE0711_Fix.
// How: an instance named with its type arguments is a fn value; a generic
// named without them is E0711 whatever the expected type, with a fix.

package check

import (
	"testing"

	"mk/internal/diag"
)

// instDecls declares a generic function, a generic struct with a method and
// a Type.Name function, and a generic struct with a generic method; F
// starts on line 21.
const instDecls = "Same[T](x: T): T {\n\treturn x\n}\n\n" +
	"struct Holder[T: Plain] {\n\tv: T\n}\n\nHolder[T].Get(&self): T {\n\treturn self.v\n}\n\n" +
	"Holder[T].Name(x: T): int {\n\treturn 1\n}\n\n" +
	"struct Pair[T] {\n\tv: T\n}\n\n"

// pairMap is a generic method of a generic struct, appended after instDecls
// (F then starts on line 25).
const pairMap = "Pair[T].Map[U](x: T, y: U): U {\n\treturn y\n}\n\n"

func TestE0711_GenericValue(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"instance", instDecls + "F(): i32 {\n\tf := Same[i32]\n\treturn f(4)\n}\n", ""},
		{"instance argument", instDecls + "Ap(f: fn(i32): i32, x: i32): i32 {\n\treturn f(x)\n}\n\nF(): i32 {\n\treturn Ap(Same[i32], 4)\n}\n", ""},
		{"method of an instance", instDecls + "F(h: &Holder[i32]): i32 {\n\tg := Holder[i32].Get\n\treturn g(h)\n}\n", ""},
		{"Type.Name of an instance", instDecls + "F(): int {\n\tg := Holder[i32].Name\n\treturn g(1)\n}\n", ""},
		{"generic method of an instance", instDecls + pairMap + "F(): u8 {\n\tg := Pair[i32].Map[u8]\n\treturn g(1, 2)\n}\n", ""},
		{"bare, no expected type", instDecls + "F() {\n\tf := Same\n}\n", "E0711@22:7"},
		{"bare, expected type", instDecls + "F(): i32 {\n\tf: fn(i32): i32 = Same\n\treturn f(1)\n}\n", "E0711@22:20"},
		{"bare argument", instDecls + "Ap(f: fn(i32): i32, x: i32): i32 {\n\treturn f(x)\n}\n\nF(): i32 {\n\treturn Ap(Same, 4)\n}\n", "E0711@26:12"},
		{"open head", instDecls + "F(h: &Holder[i32]): i32 {\n\tg: fn(&Holder[i32]): i32 = Holder.Get\n\treturn g(h)\n}\n", "E0711@22:29"},
		{"generic method without its argument", instDecls + pairMap + "F(): u8 {\n\tg: fn(i32, u8): u8 = Pair[i32].Map\n\treturn g(1, 2)\n}\n", "E0711@26:33"},
	})
}

// TestE0711_Fix: the fix is proven only when the expected type fixes
// every missing argument; it inserts them after the name they belong to.
func TestE0711_Fix(t *testing.T) {
	for _, tc := range []struct {
		name, src, fix string
	}{
		{"expected", instDecls + "F(): i32 {\n\tf: fn(i32): i32 = Same\n\treturn f(1)\n}\n", "[i32]"},
		{"open head", instDecls + "F(h: &Holder[i32]): i32 {\n\tg: fn(&Holder[i32]): i32 = Holder.Get\n\treturn g(h)\n}\n", "[i32]"},
		{"none", instDecls + "F() {\n\tf := Same\n}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, true)
			b := w.add("m", opt{}, tc.src)
			var hit *diag.Diagnostic
			for _, d := range b.bodyDiags(t) {
				if d.Code == diag.E0711 {
					hit = d
				}
			}
			if hit == nil {
				t.Fatal("no E0711")
			}
			if fact(hit, "param") != "T" {
				t.Fatalf("param %q:\n%s", fact(hit, "param"), dump(w, []*diag.Diagnostic{hit}))
			}
			if tc.fix == "" {
				if len(hit.Fixes) != 0 || len(hit.Alternatives) == 0 {
					t.Fatalf("want alternatives only:\n%s", dump(w, []*diag.Diagnostic{hit}))
				}
				return
			}
			if len(hit.Fixes) != 1 || hit.Fixes[0].Class != diag.Proven || len(hit.Fixes[0].Edits) != 1 || hit.Fixes[0].Edits[0].Text != tc.fix {
				t.Fatalf("want proven fix %q:\n%s", tc.fix, dump(w, []*diag.Diagnostic{hit}))
			}
			if e := hit.Fixes[0].Edits[0]; e.Span.Start != e.Span.End || e.Span.End != hit.At.End {
				t.Fatalf("the fix inserts at %v, want after the name %v", e.Span, hit.At)
			}
		})
	}
}
