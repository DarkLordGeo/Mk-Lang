// Why: a const initializer may hold a []T over static data (static slices),
// and the rule must not leak into other positions.
// What: TestStaticSliceInConst and TestArrayLiteralElsewhere.
// How: in a const initializer an array literal whose expected type is []T
// is a []T over static data and `[]` is the empty []T; elsewhere E0301 and
// E0711 are unchanged.

package check

import "testing"

const staticSliceRoute = "struct Route {\n\tpath: str\n\tmethods: []str\n}\n\n"

// TestStaticSliceInConst: the accepting cases. The initializer's own
// expressions, nested constructor arguments, call arguments and `[]`
// included.
func TestStaticSliceInConst(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"top level", "const Xs: []int = [1, 2, 3]\n", ""},
		{"empty", "const Xs: []int = []\n", ""},
		{"nested constructor", staticSliceRoute + "const Routes: []Route = [\n\tRoute(path: \"/\", methods: [\"GET\"]),\n\tRoute(path: \"/h\", methods: []),\n]\n", ""},
		{"constructor field", staticSliceRoute + "const R: Route = Route(path: \"/\", methods: [\"GET\", \"POST\"])\n", ""},
		{"call argument", "Sum(xs: []int): int {\n\tvar n := 0\n\tfor x in xs {\n\t\tn += x\n\t}\n\treturn n\n}\n\nconst S: int = Sum([1, 2, 3])\n", ""},
		{"slice of slices", "const Xss: [][]int = [[1], [], [2, 3]]\n", ""},
		{"array still an array", "const A: [2]int = [1, 2]\n", ""},
		{"element typed by T", "const Xs: []u8 = [1, 255]\n", ""},
	})
}

// TestArrayLiteralElsewhere: the rejecting cases. Outside a const
// initializer an array literal is [N]T (E0301 against []T) and `[]` needs an
// array type (E0711): in a function body, in a var global's initializer,
// and in the body of a fn literal that a const initializer holds (a body an
// evaluation runs). An element of the wrong type is still E0301.
func TestArrayLiteralElsewhere(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"call in a body", "First(xs: []int): int {\n\treturn xs[0]\n}\n\nOne(): int {\n\treturn First([1, 2])\n}\n", "E0301@6:15"},
		{"empty in a body", "F(): int {\n\txs: []int = []\n\treturn xs.Len()\n}\n", "E0711@2:14"},
		{"let in a body", "F(): int {\n\txs: []int = [1]\n\treturn xs.Len()\n}\n", "E0301@2:14"},
		{"fn literal in a const", "Ap(const f: fn(): int): int {\n\treturn f()\n}\n\nFirst(xs: []int): int {\n\treturn xs[0]\n}\n\nconst K: int = Ap(fn(): int { return First([1]) })\n", "E0301@9:44"},
		{"element mismatch", "const Xs: []int = [1, \"a\"]\n", "E0301@1:23"},
		{"mut slice", "const Xs: mut []int = [1]\n", "E0301@1:23 E0408@1:7"},
		{"var global", "var G: []int = [1]\n", "E0301@1:16"},
	})
}
