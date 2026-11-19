// Why: tests, the gen-only rule, contracts and E0807 each have check rules,
// and the gen-only rule must not widen what an incremental build rechecks.
// What: TestTests, TestE0705_GenAtRunTime_Local and _Incremental,
// TestContracts, TestE0807.
// How: checks small modules and compares codes and read sets.

package check

import (
	"testing"

	"mk/internal/diag"
)

func TestTests(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"with sys", "test \"x\" (sys: &System) {\n\ts := sys\n}\n", ""},
		{"without a parameter", "test \"x\" {\n\tx := 1\n}\n", ""},
		{"an int parameter", "test \"x\" (n: int) {\n\tm := n + 1\n}\n", "E0301@1:14"},
		{"System by value", "test \"x\" (sys: System) {\n}\n", "E0301@1:16"},
		{"try is not checked", "error E\n\nF(): E!int {\n\treturn 1\n}\n\ntest \"x\" {\n\tv := try F()\n}\n", ""},
	})
}

const genSrc = "import std.core\n\nGenerate(g: mut core.Gen, t: core.Type) {\n\tg.Emit(Describe(t))\n}\n\nDescribe(t: core.Type): core.Code {\n\treturn quote {\n\t\tx := 1\n\t}\n}\n\n"

func TestE0705_GenAtRunTime_Local(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"generator and helper", genSrc, ""},
		{"a generator calls a generator", genSrc + "H(g: mut core.Gen, t: core.Type) {\n\tGenerate(g, t)\n}\n", ""},
		{"Main calls a generator", genSrc + "Main(p: *core.Gen, q: *core.Type) {\n\tunsafe \"valid\" {\n\t\tGenerate(mut *p, *q)\n\t}\n}\n", "E0705@15:3"},
		{"a run-time function calls a helper", genSrc + "Run(p: *core.Type) {\n\tunsafe \"valid\" {\n\t\tx := Describe(*p)\n\t}\n}\n", "E0705@15:8"},
		{"quote at run time", genSrc + "Run() {\n\tc := quote {\n\t\tx := 1\n\t}\n}\n", "E0705@14:7"},
		{"a generator as a fn value", genSrc + "Run() {\n\tf := Describe\n}\n", "E0705@14:7"},
	})
}

// TestE0705_GenAtRunTime_Incremental: adding a call of the helper H from a
// unit that is not gen-only changes only that unit's diagnostics; H's
// diagnostics and read set are the same.
func TestE0705_GenAtRunTime_Incremental(t *testing.T) {
	const h = "H(t: core.Type): core.Code {\n\treturn quote {\n\t\ty := 2\n\t}\n}\n\n"
	unitOf := func(src string) (string, string, string) {
		w := newWorld(t, true)
		b := w.add("m", opt{}, genSrc+h+src)
		var hd, rd string
		for _, u := range b.m.Units() {
			body := w.c.Check(b.m, u)
			key := w.items.Get(u).Key.String()
			switch key {
			case "m.H":
				hd = codes(w, body.Diags)
				for _, r := range body.Reads.Sorted() {
					rd += r.Key.String() + ";"
				}
			case "m.Run":
				return hd, rd, codes(w, body.Diags)
			}
		}
		return hd, rd, ""
	}
	h1, r1, run1 := unitOf("Run(p: *core.Type) {\n}\n")
	h2, r2, run2 := unitOf("Run(p: *core.Type) {\n\tunsafe \"p is valid\" {\n\t\tc := H(*p)\n\t}\n}\n")
	if h1 != h2 || r1 != r2 {
		t.Fatalf("H changed: %q %q, then %q %q", h1, r1, h2, r2)
	}
	if run1 != "" || run2 != "E0705@21:8" {
		t.Fatalf("Run diagnostics %q then %q", run1, run2)
	}
	_ = diag.E0705
}

func TestContracts(t *testing.T) {
	const span = "struct Span {\n\tlo: int\n\thi: int\n\tinvariant lo <= hi\n}\n\nSpan.Len(&self): int\n\tensures result >= 0\n{\n\treturn self.hi - self.lo\n}\n\nSafeDiv(a: i32, b: i32): i32\n\trequires b != 0\n{\n\treturn a / b\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"the reference's Span test", span + "test \"len\" {\n\tAssert(Span(lo: 1, hi: 4).Len() == 3)\n}\n", ""},
		{"not bool", "F(a: int): int\n\trequires a + 1\n{\n\treturn a\n}\n", "E0301@2:11"},
		{"invariant not bool", "struct S {\n\tn: int\n\tinvariant n\n}\n", "E0301@3:12"},
		{"try in a predicate", "error E\n\nG(): E!bool {\n\treturn true\n}\n\nF(a: int): int\n\trequires try G()\n{\n\treturn a\n}\n", "E0101@8:11"},
		{"a call taking Io", "Ready(io: Io): bool {\n\treturn true\n}\n\nF(io: Io, a: int): int\n\trequires Ready(io)\n{\n\treturn a\n}\n", "E0101@6:11"},
		{"ensures result", "F(a: int): int\n\tensures result > a\n{\n\treturn a + 1\n}\n", ""},
	})
	w := newWorld(t, true)
	b := checkBody(t, w, "E0101@8:11", "error E\n\nG(): E!bool {\n\treturn true\n}\n\nF(a: int): int\n\trequires try G()\n{\n\treturn a\n}\n")
	for _, d := range b.bodyDiags(t) {
		if d.Code == diag.E0101 && d.Phase != diag.PhaseTypes {
			t.Errorf("E0101 in a predicate has phase %v, want types", d.Phase)
		}
	}
}

func TestE0807(t *testing.T) {
	const src = "linear struct H {\n\tn: int\n}\n\nH.Close(self) {\n}\n\nH.Peek(&self): int {\n\treturn self.n\n}\n\nLook(h: &H) {\n}\n\nerror E\n\n"
	runBodyCases(t, []bodyCase{
		{"two values", src + "F(a: H, b: H): E!void {\n\tonerr {\n\t\ta.Close()\n\t\tb.Close()\n\t}\n\tfail E\n}\n", "E0807@18:2"},
		{"one value", src + "F(a: H): E!void {\n\tonerr a.Close()\n\tfail E\n}\n", ""},
		{"borrows do not consume", src + "F(a: H, b: H): E!void {\n\tonerr {\n\t\tLook(&a)\n\t\tn := a.Peek() + a.n\n\t\tb.Close()\n\t}\n\tfail E\n}\n", ""},
		{"plain values", "error E\n\nF(a: int, b: int): E!void {\n\tonerr {\n\t\tx := a + b\n\t}\n\tfail E\n}\n", ""},
		// A local the onerr declares is not consumed
		// from outside, so the binding twin consumes only a.
		{"body-local binding", src + "F(a: H): E!void {\n\tonerr {\n\t\tg := a\n\t\tg.Close()\n\t}\n\tfail E\n}\n", ""},
		{"body-local beside two values", src + "F(a: H, b: H): E!void {\n\tonerr {\n\t\tg := a\n\t\tg.Close()\n\t\tb.Close()\n\t}\n\tfail E\n}\n", "E0807@18:2"},
	})
}
