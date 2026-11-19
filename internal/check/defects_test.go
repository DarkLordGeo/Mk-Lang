// Why: defects found by dogfood programs must stay fixed.
// What: TestChatDiscardElseValue,
// TestPackSameNameErrorsFromTwoModules, TestE0305_LoopHeadNoRest.
// How: each test checks the program shape that showed the defect and
// compares codes and messages; tests are named after the defect.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
)

// TestChatDiscardElseValue: under `_ =`, an else handler on
// a fallible or optional value may end in a value of the success type (the
// else is then a value construct, discarded), fall through with none, or
// not fall through; one that does both is E0803. A value there was E0209,
// whose fix wrote `_ = -1` inside the handler. The first case is
// the conformance unit D418_accept_discard_else_value word for word.
func TestChatDiscardElseValue(t *testing.T) {
	const decls = "error Bad\nerror Gone\ntype E = Bad | Gone\nLoad(i: int): E!int {\n\tif i == 0 {\n\t\tfail Bad\n\t}\n\treturn i\n}\nTwo(n: int): Bad!void {\n\tif n == 0 {\n\t\tfail Bad\n\t}\n}\n"
	runBodyCases(t, []bodyCase{
		{"conformance unit", decls + "Run(w: ?int) {\n\t_ = w else { 1 }\n\t_ = Load(0) else { 0 }\n\t_ = Load(0) else e {\n\t\t_ = e\n\t\t0\n\t}\n}\n", ""},
		{"match arms yield", decls + "Run() {\n\t_ = Load(3) else e {\n\t\tmatch e {\n\t\t\tBad { -1 }\n\t\t\tGone { -2 }\n\t\t}\n\t}\n}\n", ""},
		{"ignore form", decls + "Run(w: ?int) {\n\t_ = w else { }\n\t_ = Load(1) else { }\n\t_ = Load(1) else e {\n\t\tmatch e {\n\t\t\tBad { return }\n\t\t\tGone { }\n\t\t}\n\t}\n}\n", ""},
		{"unhandled last line", decls + "Run() {\n\t_ = Load(1) else {\n\t\tTwo(1)\n\t}\n}\n", "E0802@17:3"},
		{"value and none", decls + "Run(c: bool) {\n\t_ = Load(1) else {\n\t\tif c { 1 } else { }\n\t}\n}\n", "E0803@16:14"},
		{"statement keeps E0209", decls + "Run() {\n\tLoad(1) else {\n\t\t1\n\t}\n}\n", "E0209@16:2 E0209@17:3"},
	})
}

// TestPackSameNameErrorsFromTwoModules: a.Cut and b.Cut in one set
// are two constructors to exhaustiveness, found by item; a match naming
// both was E0306 "does not cover Cut", and one naming only a.Cut still is.
func TestPackSameNameErrorsFromTwoModules(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, arms, want string }{
		{"both", "\t\ta.Cut(n) { n }\n\t\tb.Cut { 2 }\n", ""},
		{"one", "\t\ta.Cut(n) { n }\n", "E0306@5:9"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, true)
			w.add("a", opt{}, "export error Cut(n: int)\n")
			w.add("b", opt{}, "export error Cut\n")
			checkBody(t, w, c.want, "import a\nimport b\ntype Problem = a.Cut | b.Cut\nCode(e: Problem): int {\n\treturn match e {\n"+c.arms+"\t}\n}\n")
		})
	}
}

// TestE0305_LoopHeadNoRest: E0305 on a for head's destructured variable
// does not suggest `..`, which the for head refuses (E0101); a
// destructuring binding still does.
func TestE0305_LoopHeadNoRest(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := checkBody(t, w, "E0305@2:6", "F(ps: [](x: int, y: int)) {\n\tfor (x) in ps {\n\t\t_ = x\n\t}\n}\n")
	for _, d := range b.bodyDiags(t) {
		if len(d.Alternatives) != 0 || strings.Contains(d.Message, "..") {
			t.Errorf("E0305 in a for head suggests `..`: %s", dump(w, []*diag.Diagnostic{d}))
		}
	}
}
