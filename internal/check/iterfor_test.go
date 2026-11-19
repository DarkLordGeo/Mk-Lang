// Why: a for loop over an iterator and its destructured loop variable have
// their own typing rules and refusals.
// What: TestIterElem, TestSliceWins, TestRefusals, TestParamIterator (element type, Slice wins, refusals, parameter
// iterators) and TestIndexFormIsTypesPhase, TestNextCallSiteRules, TestIterMissReadsMembers (phase, Next call rules, misses).
// How: checks loops and compares codes and the recorded Next calls.

package check

import (
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/types"
)

const iterForSrc = "struct Count {\n\tn: int\n}\nCount.Next(mut self): ?int {\n\tif self.n == 0 {\n\t\treturn none\n\t}\n\tself.n -= 1\n\treturn some(self.n)\n}\n" +
	"struct Peek {\n\tn: int\n}\nPeek.Next(&self): ?(a: int, b: int) {\n\treturn none\n}\n"

func TestIterElem(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"mut self", iterForSrc + "F(): int {\n\tvar s := 0\n\tfor x in Count(n: 3) {\n\t\ts += x\n\t}\n\treturn s\n}\n", ""},
		{"shared self and pattern", iterForSrc + "F(): int {\n\tvar s := 0\n\tfor (a, b) in Peek(n: 1) {\n\t\ts += a + b\n\t}\n\treturn s\n}\n", ""},
		{"renamed pattern", iterForSrc + "F(): int {\n\tvar s := 0\n\tfor (a: x, b: y) in Peek(n: 1) {\n\t\ts += x + y\n\t}\n\treturn s\n}\n", ""},
		{"pattern over a non-record", iterForSrc + "F() {\n\tfor (a, b) in Count(n: 1) {\n\t}\n}\n", "E0301@18:6"},
		{"pattern names a missing field", iterForSrc + "F() {\n\tfor (a, c) in Peek(n: 1) {\n\t\t_ = a\n\t}\n}\n", "E0309@18:10"},
		{"slice of records", "F(xs: [](a: int, b: int)): int {\n\tvar s := 0\n\tfor i, (a, b) in xs {\n\t\ts += i + a + b\n\t}\n\treturn s\n}\n", ""},
	})
}

// TestSliceWins: a type that implements Slice[T] and also has a Next
// iterates as a Slice[T]: the index form is allowed, x is the
// element type, and no Next call is recorded on the source.
func TestSliceWins(t *testing.T) {
	const both = "struct Buf {\n\tb: []int\n}\n\nimpl Slice[int] for Buf {\n}\n\nBuf.Next(mut self): ?str {\n\treturn none\n}\n\n"
	const loop = both + "F(s: &Buf): int {\n\tvar n := 0\n\tfor i, x in s {\n\t\tn += i + x\n\t}\n\treturn n\n}\n"
	runBodyCases(t, []bodyCase{
		{"slice", "F(xs: []int): int {\n\tvar s := 0\n\tfor i, x in xs {\n\t\ts += i + x\n\t}\n\treturn s\n}\n", ""},
		{"implementer with Next", loop, ""},
		// x is the element, int, not Next's str.
		{"element type", both + "F(s: &Buf): str {\n\tfor i, x in s {\n\t\treturn x\n\t}\n\treturn \"\"\n}\n", "E0301@14:10"},
	})
	w := newWorld(t, true)
	b := w.add("m", opt{}, loop)
	body := b.unitBodies(t)["F"]
	seen := false
	ast.Inspect(body.Node, func(nd ast.Node) bool {
		h, ok := nd.(*ast.InHead)
		if !ok {
			return true
		}
		seen = true
		if body.Uses != nil {
			if u := body.Uses[body.slot(h.Source.ID())]; u.Kind != 0 || u.Item != types.NoItem {
				t.Errorf("a Next call recorded on the source: %+v", u)
			}
		}
		return true
	})
	if !seen {
		t.Fatal("no loop head")
	}
}

func TestRefusals(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"no Next", "struct Bag {\n\tn: int\n}\nF(b: Bag) {\n\tfor x in b {\n\t\t_ = x\n\t}\n}\n", "E0301@5:11"},
		{"index form", iterForSrc + "F() {\n\tfor i, x in Count(n: 3) {\n\t\t_ = i\n\t\t_ = x\n\t}\n}\n", "E0101@18:6"},
		{"ref source", iterForSrc + "F() {\n\tvar it := Count(n: 3)\n\tfor x in &it {\n\t\t_ = x\n\t}\n}\n", "E0301@19:11"},
		{"wrong Next", "struct W {\n\tn: int\n}\nW.Next(mut self, k: int): ?int {\n\treturn none\n}\nF(w: W) {\n\tfor x in w {\n\t\t_ = x\n\t}\n}\n", "E0301@8:11"},
	})
}

func TestParamIterator(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"bound", "interface Iter {\n\tNext(mut self): ?int\n}\nSum[I: Iter](it: I): int {\n\tvar s := 0\n\tfor x in it {\n\t\ts += x\n\t}\n\treturn s\n}\n", ""},
	})
}

// TestIndexFormIsTypesPhase: the E0101 of `for i, x in it` is check's,
// in the types group, so it hides no other type error.
func TestIndexFormIsTypesPhase(t *testing.T) {
	src := iterForSrc + "F() {\n\tfor i, x in Count(n: 3) {\n\t\t_ = i\n\t\t_ = x\n\t}\n}\nG(): int {\n\treturn \"s\"\n}\n"
	runBodyCases(t, []bodyCase{{"with another error", src, "E0101@18:6 E0301@24:9"}})
	w := newWorld(t, true)
	b := w.add("m", opt{}, src)
	n := 0
	for _, d := range b.bodyDiags(t) {
		if d.Code == diag.E0101 {
			n++
			if d.Phase != diag.PhaseTypes {
				t.Errorf("E0101 in phase %v, want types", d.Phase)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d E0101, want 1", n)
	}
}

// TestNextCallSiteRules: the loop calls Next at the source as
// `it.Next()` would, so a Next with a cpu clause the context lacks is E1101
// outside unsafe, exactly as the explicit call is.
func TestNextCallSiteRules(t *testing.T) {
	const wide = "struct W {\n\tn: int\n}\n\nW.Next(mut self): ?int cpu \"avx2\" {\n\treturn none\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"loop", wide + "F() {\n\tfor x in W(n: 1) {\n\t\t_ = x\n\t}\n}\n", "E1101@10:11"},
		{"explicit call", wide + "F() {\n\tvar w := W(n: 1)\n\tfor x := w.Next() {\n\t\t_ = x\n\t}\n}\n", "E1101@11:11"},
		{"loop in unsafe", wide + "F() {\n\tunsafe \"Cpu() reports avx2\" {\n\t\tfor x in W(n: 1) {\n\t\t\t_ = x\n\t\t}\n\t}\n}\n", ""},
		{"loop with the features", wide + "F() cpu \"avx2\" {\n\tfor x in W(n: 1) {\n\t\t_ = x\n\t}\n}\n", ""},
	})
	w := newWorld(t, true)
	b := w.add("m", opt{}, wide+"F() {\n\tfor x in W(n: 1) {\n\t\t_ = x\n\t}\n}\n")
	n := 0
	for _, d := range b.bodyDiags(t) {
		if d.Code == diag.E1101 {
			n++
			if fact(d, "operation") != "cpu-call" {
				t.Errorf("operation %q, want cpu-call", fact(d, "operation"))
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d E1101, want 1", n)
	}
}

// TestIterMissReadsMembers: a `for x in e` whose e's type has no
// usable Next records the member list read of a miss on that type, so a
// dependency that adds Next re-checks the loop in a warm build.
func TestIterMissReadsMembers(t *testing.T) {
	const lib = "export struct Bag {\n\texport n: int\n}\n\nexport struct Odd {\n\texport Next: int\n}\n\n" +
		"export struct Stat {\n\texport n: int\n}\n\nexport Stat.Next(): ?int {\n\treturn none\n}\n"
	for _, tc := range []struct{ name, typ, key string }{
		{"no member", "lib.Bag", "lib.Bag:"},
		{"a field named Next", "lib.Odd", "lib.Odd:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := bodyReads(t, lib, "import lib\n\nF(b: "+tc.typ+") {\n\tfor x in b {\n\t\t_ = x\n\t}\n}\n")
			var all []string
			noItem, exports := false, false
			for _, r := range reads {
				all = append(all, r.Kind.String()+" "+r.Key.String()+" "+r.Name)
				if r.Key.String() == tc.key {
					noItem = noItem || r.Kind == types.ReadNoItem && r.Name == "Next"
					exports = exports || r.Kind == types.ReadExports
				}
			}
			if !noItem || !exports {
				t.Errorf("no NoItem Next and Exports on %s in\n%s", tc.key, strings.Join(all, "\n"))
			}
		})
	}
	t.Run("a bound's interface without Next", func(t *testing.T) {
		reads := bodyReads(t, lib+"\nexport interface It {\n\tLen(&self): int\n}\n", "import lib\n\nF[I: lib.It](it: I) {\n\tfor x in it {\n\t\t_ = x\n\t}\n}\n")
		for _, r := range reads {
			if r.Kind == types.ReadSignature && r.Key.String() == "lib.It" {
				return
			}
		}
		t.Errorf("no signature read of lib.It: %v", reads)
	})
	t.Run("Next without a receiver", func(t *testing.T) {
		reads := bodyReads(t, lib, "import lib\n\nF(b: lib.Stat) {\n\tfor x in b {\n\t\t_ = x\n\t}\n}\n")
		for _, r := range reads {
			if r.Kind == types.ReadSignature && r.Key.String() == "lib.Stat:Next" {
				return
			}
		}
		t.Errorf("no signature read of lib.Stat.Next: %v", reads)
	})
}
