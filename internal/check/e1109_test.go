// Why: an export extern of a library is called from C, so every parameter
// and result needs a C form.
// What: TestE1109.
// How: in a module of a library package, an export extern's parameter or
// result with no C form is E1109; a C-form one (an integer, str, a raw
// pointer, a reference to a C-form struct) is not; a function that can fail
// is refused at its result; outside a library package nothing is judged.
package check

import "testing"

func TestE1109(t *testing.T) {
	const ok = "export struct P {\n\texport x: i32\n}\n\nexport extern A(a: i32, s: str, p: *P, r: &P, m: mut P): u64 {\n\treturn 0\n}\n\nexport extern B(): void {\n}\n"
	const bad = "export enum Mode {\n\tA\n}\n\nexport error E\n\nexport extern S(xs: []i32): i32 {\n\treturn 0\n}\n\nexport extern O(): ?i32 {\n\treturn none\n}\n\nexport extern M(m: Mode): i32 {\n\treturn 0\n}\n\nexport extern F(): E!i32 {\n\treturn 1\n}\n"
	for _, c := range []struct {
		name, src string
		lib       bool
		want      string
	}{
		{"C forms", ok, true, ""},
		{"no C form", bad, true, "E1109@11:20 E1109@15:17 E1109@19:20 E1109@7:17"},
		{"no library", bad, false, ""},
		// Const-parameter export externs too (a generic or variadic
		// one is the parser's E0101 before check; one
		// predicate, CFormFunc, shared with the driver's header writer).
		{"const parameter", "export extern K(const n: i32): i32 {\n\treturn n\n}\n", true, "E1109@1:17"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, true)
			b := w.add("m", opt{library: c.lib}, c.src)
			ds := append(b.m.Diags, b.bodyDiags(t)...)
			if got := codes(w, ds); got != c.want {
				t.Errorf("diagnostics %q, want %q\n%s", got, c.want, dump(w, ds))
			}
		})
	}
}
