// Why: std.derive's generators are the reference's worked example of gen.
// What: TestDeriveDeclared and TestDeriveEndToEnd.
// How: checks the generator signatures and Eq's body against the reference,
// then builds and runs a program whose gen lines use Eq, Hash and Format.

package std

import (
	"strings"
	"testing"
)

// TestDeriveDeclared: derive/derive.mk exports Eq, Hash and Format with
// the generator signature (g: mut Gen, t: Type), and Eq's text from its
// `var body` line to its last brace is the reference's derive block.
func TestDeriveDeclared(t *testing.T) {
	src := readText(t, "derive/derive.mk")
	for _, n := range []string{"Eq", "Hash", "Format"} {
		if !strings.Contains(src, "\nexport "+n+"(g: mut Gen, t: Type) {\n") {
			t.Errorf("derive/derive.mk does not export %s(g: mut Gen, t: Type)", n)
		}
	}
	sec := referenceSection(t, "## 11. Compile time")
	i := strings.Index(sec, "export Eq(g: mut Gen, t: Type) {\n")
	if i < 0 {
		t.Fatal("the reference's compile-time section has no derive.Eq block")
	}
	ref := sec[i:]
	ref = ref[strings.Index(ref, "    var body := g.Block()\n"):strings.Index(ref, "\n}\n")]
	j := strings.Index(src, "export Eq(g: mut Gen, t: Type) {\n")
	have := src[j:]
	have = have[strings.Index(have, "    var body := g.Block()\n"):strings.Index(have, "\n}\n")]
	if have != ref {
		t.Errorf("derive.Eq's body is not the reference's block:\n%s\nwant:\n%s", have, ref)
	}
}

const deriveProgram = `import std.derive

struct User {
    name: str
    age: int
}

gen derive.Eq(User)
gen derive.Hash(User)
gen derive.Format(User)

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    a := User(name: "ann", age: 3)
    b := User(name: "ann", age: 3)
    c := User(name: "bob", age: 3)
    try a.Format(mut out)
    ab := a.Eq(&b)
    ac := a.Eq(&c)
    try Print(mut out, " {ab} {ac}\n", (ab, ac))
    var m := Map[User, int].New(sys.arena)
    try m.Set(a, 1)
    try m.Set(c, 2)
    try m.Set(b, 5)
    n := m.Len()
    v := m.Get(&b).Must()
    w := *v
    try Print(mut out, "{n} {w}\n", (n, w))
}
`

// TestDeriveEndToEnd: deriveProgram prints `User(name: ann, age: 3)
// true false` and `2 5` (a derived struct as a Map key).
func TestDeriveEndToEnd(t *testing.T) {
	t.Parallel()
	out, status := checkProgram(t, nil, deriveProgram)
	if status != 0 {
		t.Fatalf("mk check: exit %d\n%s", status, out)
	}
	r := runNative(t, buildNative(t, nil, deriveProgram), "")
	wantRun(t, r, 0, "User(name: ann, age: 3) true false\n2 5\n")
}
