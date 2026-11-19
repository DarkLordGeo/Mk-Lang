// Why: one unbound name must give one diagnostic, not a cascade.
// What: TestPoisonOneDiagnostic.
// How: a name resolve could not bind is Poison, and Poison draws no
// diagnostic from check wherever it flows, so an unknown name used five
// times gives only resolve's diagnostics.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
)

func TestPoisonOneDiagnostic(t *testing.T) {
	cases := []struct{ name, src string }{
		{"five uses", "F(): int {\n\ta := nope + 1\n\tb := nope.Len()\n\tc := nope(1, 2)\n\td: u8 = nope\n\tif nope { return nope }\n\treturn a + b + c + int(d)\n}\n"},
		{"operands", "F(x: int): bool {\n\ty := -nope\n\tz := !nope\n\tw := nope * x\n\treturn nope == x && nope < 3 && y == z && w > 0\n}\n"},
		{"arguments", "G(a: int, b: str): int { return a }\nH(a: int) { }\nF(): int {\n\tH(nope)\n\t_ = G(nope, nope)\n\treturn G(nope, \"x\")\n}\n"},
		{"members", "struct P { x: int }\nF(): int {\n\tp := nope.P\n\tq := nope.x.y\n\tr := nope[1]\n\ts := nope[1..2]\n\treturn p.x + q + r + s.Len()\n}\n"},
		{"statements", "F(): int {\n\tfor x in nope { }\n\tfor i in 0..nope { }\n\tfor nope { }\n\tvar v := 0\n\tv = nope\n\tv += nope\n\tmatch nope { _ { } }\n\treturn v\n}\n"},
		{"construction", "struct P {\n\tx: int\n\ty: int\n}\nF(): P {\n\tr := (a: nope, b: 1)\n\tarr := [nope, nope]\n\trep := [nope; 3]\n\treturn P(x: nope, y: r.b + arr[0] + rep[0])\n}\n"},
		{"errors", "error E\nF(): E!int {\n\tx := try nope\n\ty := nope else { return 0 }\n\tfail nope\n}\n"},
		{"types", "F(x: Nope): Nope {\n\ty: Nope = x\n\tz: []Nope = []\n\tw: ?Nope = none\n\treturn y\n}\n"},
		{"casts", "F(): u8 {\n\ta := u8(nope)\n\tb := Cast[u8](nope)\n\tc := Trunc[u8](nope)\n\treturn a + b + c\n}\n"},
		{"match", "enum S {\n\tA\n\tB\n}\nF(s: S): int {\n\tmatch nope {\n\t\t.A { return 1 }\n\t\t.B { return 2 }\n\t}\n\tmatch s {\n\t\t.A { return nope }\n\t\t.B { return 2 }\n\t}\n}\n"},
		{"generic and fields", "Id[T](x: T): T { return x }\nF(): int {\n\ta := Id(nope)\n\tb := Id[int](nope)\n\tfor name, value in fields(&nope) { }\n\treturn a + b\n}\n"},
		{"references", "F(): int {\n\ta := &nope\n\tb := mut nope\n\tc := *nope\n\treturn *a + *b + c\n}\n"},
		{"else and defer", "F(): int {\n\tdefer { nope() }\n\tx := nope.Or(1)\n\ty := nope.Must()\n\treturn x + y\n}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := w.add("m", opt{}, tc.src)
			n := 0
			var other []*diag.Diagnostic
			for _, d := range append(b.diags(), b.bodyDiags(t)...) {
				switch {
				case d.Code == diag.E0208:
				case d.Code == diag.E0201 && containsName(d, "nope", "Nope"):
					n++
				default:
					other = append(other, d)
				}
			}
			if n == 0 {
				t.Fatalf("no E0201 for the unknown name")
			}
			if len(other) > 0 {
				t.Fatalf("diagnostics besides resolve's E0201:\n%s", dump(w, other))
			}
		})
	}
}

func containsName(d *diag.Diagnostic, names ...string) bool {
	for _, n := range names {
		if strings.Contains(d.Message, n) || fact(d, "name") == n {
			return true
		}
	}
	return false
}
