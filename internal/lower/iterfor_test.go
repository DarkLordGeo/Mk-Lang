// Why: for over an iterator lowers to a hidden var and a Next call per
// iteration, and its temporaries must end at the right point.
// What: TestForIterShape, TestSourceTempEnds,
// TestForPatternSlice (the destructured loop variable).
// How: lowers small loops and checks the MIR text.

package lower

import "testing"

const iterLowerSrc = "struct Count {\n\tn: int\n}\nCount.Next(mut self): ?int {\n\tif self.n == 0 {\n\t\treturn none\n\t}\n\tself.n -= 1\n\treturn some(self.n)\n}\n" +
	"struct Pairs {\n\ti: int\n}\nPairs.Next(mut self): ?(a: int, b: int) {\n\tif self.i >= 3 {\n\t\treturn none\n\t}\n\tself.i += 1\n\treturn some((a: self.i, b: self.i * 10))\n}\n"

// TestForIterShape: the hidden var, Next on a mut borrow of it in the
// header, a switch on the discriminant, the payload bound in the body.
func TestForIterShape(t *testing.T) {
	l := lowerSrc(t, iterLowerSrc+"F(): int {\n\tvar s := 0\n\tfor x in Count(n: 3) {\n\t\ts += x\n\t}\n\treturn s\n}\n")
	f := l.print("F")
	wantIn(t, f, "= m.Count(int 3)", "goto", "= mut _", "call m.Count:Next(", "Discriminant(", "switch", "= copy (", "goto")
	if len(l.body("F").Loops) != 1 {
		t.Errorf("loops: %+v", l.body("F").Loops)
	}
}

// TestSourceTempEnds: the source's other temporaries end before the
// header; the hidden var lives to the loop's exit.
func TestSourceTempEnds(t *testing.T) {
	l := lowerSrc(t, iterLowerSrc+"Make(n: int): Count {\n\treturn Count(n: n)\n}\nF(): int {\n\tvar s := 0\n\tfor x in Make(2 + 1) {\n\t\ts += x\n\t}\n\treturn s\n}\n")
	// The source's call result is the hidden var itself: no copy, and it
	// is borrowed by each Next call in the header; the source's other
	// temporary (2 + 1) ends before the header.
	f := l.print("F")
	wantIn(t, f, "_3 = call m.Make(copy _2)", "scope_end _2", "goto bb2", "_4 = mut _3", "call m.Count:Next(move _4)", "scope_end _3")
}

// TestForPatternSlice: a destructured element of a slice is copied and
// bound field by field; over an iterator the payload is destructured.
func TestForPatternSlice(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      int64
	}{
		{"iterator", iterLowerSrc + "H(): int {\n\tvar s := 0\n\tfor x in Count(n: 4) {\n\t\ts += x\n\t}\n\tfor (a, b) in Pairs(i: 0) {\n\t\ts += a + b\n\t}\n\treturn s\n}\n", 6 + 66},
		{"slice", "H(): int {\n\txs := [(a: 1, b: 2), (a: 3, b: 4)]\n\tvar s := 0\n\tfor i, (a, b: y) in xs {\n\t\ts += i * 100 + a + y\n\t}\n\tfor (a, b) in xs {\n\t\ts += a * b\n\t}\n\treturn s\n}\n", 110 + 14},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := evalH(t, c.src)
			if err != nil || got != c.want {
				t.Errorf("got %d %v, want %d", got, err, c.want)
			}
		})
	}
}
