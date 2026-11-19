// Why: std.derive's Eq, Hash and Format must emit correct Mk for enums too.
// What: shapeTarget's hand-written impls, TestDeriveEnumEndToEnd and
// TestSTDMEM_DeriveEmptyEnum.
// How: runs the emitted text natively, then the gen-line version, which must
// behave the same; an enum with no variants must be E0903, never E0905.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shapeDecl is the sample enum of the enum forms: a one-field payload, a
// payload whose fields are named like Format's and Hash's parameters (w, h),
// one named like Eq's (other), and no payload.
const shapeDecl = `enum Shape {
    Circle(r: int)
    Rect(w: int, h: str)
    Tag(other: int)
    Empty
}
`

// shapeTarget is what derive.Eq, derive.Hash and derive.Format emit for
// Shape (derive/derive.mk's enum forms), without gen's header and trace
// comments: the text a person would write. TestDeriveTargetsRun runs it
// natively today; TestDeriveEnumEndToEnd runs the generated version.
const shapeTarget = `impl Eq for Shape {
    Eq(&self, other: &Shape): bool {
        match self {
            .Circle(r: r1) {
                match other {
                    .Circle(r: r2) {
                        if !r1.Eq(r2) {
                            return false
                        }
                        return true
                    }
                    _ {
                        return false
                    }
                }
            }
            .Rect(w: w1, h: h1) {
                match other {
                    .Rect(w: w2, h: h2) {
                        if !w1.Eq(w2) {
                            return false
                        }
                        if !h1.Eq(h2) {
                            return false
                        }
                        return true
                    }
                    _ {
                        return false
                    }
                }
            }
            .Tag(other: other1) {
                match other {
                    .Tag(other: other2) {
                        if !other1.Eq(other2) {
                            return false
                        }
                        return true
                    }
                    _ {
                        return false
                    }
                }
            }
            .Empty {
                match other {
                    .Empty {
                        return true
                    }
                    _ {
                        return false
                    }
                }
            }
        }
    }
}

impl Hash for Shape {
    Hash(&self, h: mut Hasher) {
        match self {
            .Circle(r: r1) {
                h.U64(0)
                r1.Hash(h)
            }
            .Rect(w: w1, h: h1) {
                h.U64(1)
                w1.Hash(h)
                h1.Hash(h)
            }
            .Tag(other: other1) {
                h.U64(2)
                other1.Hash(h)
            }
            .Empty {
                h.U64(3)
            }
        }
    }
}

impl Format for Shape {
    Format(&self, w: mut Writer): WriteError!void {
        match self {
            .Circle(r: r1) {
                try w.Str("Circle")
                try w.Str("(")
                try w.Str("r")
                try w.Str(": ")
                try r1.Format(w)
                try w.Str(")")
            }
            .Rect(w: w1, h: h1) {
                try w.Str("Rect")
                try w.Str("(")
                try w.Str("w")
                try w.Str(": ")
                try w1.Format(w)
                try w.Str(", ")
                try w.Str("h")
                try w.Str(": ")
                try h1.Format(w)
                try w.Str(")")
            }
            .Tag(other: other1) {
                try w.Str("Tag")
                try w.Str("(")
                try w.Str("other")
                try w.Str(": ")
                try other1.Format(w)
                try w.Str(")")
            }
            .Empty {
                try w.Str("Empty")
            }
        }
    }
}
`

// shapeMain uses every derived impl of Shape: Format of each variant, Eq
// within and across variants (equal payloads in different variants), and
// Shape as a Map key.
const shapeMain = `
export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    a := Shape.Rect(w: 2, h: "x")
    b := Shape.Rect(w: 2, h: "x")
    c := Shape.Rect(w: 2, h: "y")
    d := Shape.Circle(r: 2)
    e := Shape.Tag(other: 2)
    z := Shape.Empty
    try a.Format(mut out)
    try out.Str(" ")
    try d.Format(mut out)
    try out.Str(" ")
    try e.Format(mut out)
    try out.Str(" ")
    try z.Format(mut out)
    ab := a.Eq(&b)
    ac := a.Eq(&c)
    de := d.Eq(&e)
    zz := z.Eq(&z)
    try Print(mut out, "\n{ab} {ac} {de} {zz}\n", (ab, ac, de, zz))
    var m := Map[Shape, int].New(sys.arena)
    try m.Set(a, 1)
    try m.Set(c, 2)
    try m.Set(d, 3)
    try m.Set(e, 4)
    try m.Set(z, 5)
    try m.Set(b, 6)
    n := m.Len()
    v := *m.Get(&b).Must()
    w := *m.Get(&e).Must()
    y := *m.Get(&z).Must()
    try Print(mut out, "{n} {v} {w} {y}\n", (n, v, w, y))
}
`

const shapeOut = "Rect(w: 2, h: x) Circle(r: 2) Tag(other: 2) Empty\ntrue false false true\n5 6 4 5\n"

// goldenItems is a gen golden's text without its header and trace comments.
func goldenItems(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "internal", "gen", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var keep []string
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(l, "//") {
			keep = append(keep, l)
		}
	}
	return strings.TrimSpace(strings.Join(keep, "\n")) + "\n"
}

// TestDeriveTargetsRun: the text the generators emit, written out, is
// correct Mk that does the job. For User it is gen's goldens of the struct
// forms (internal/gen/testdata/derive_{eq,hash,format}.golden, which
// derive/derive.mk's struct forms print); for Shape it is shapeTarget, the
// enum forms. Both run natively: Format, Eq, and a Map key. That the
// generators print this text is TestDeriveEndToEnd's.
func TestDeriveTargetsRun(t *testing.T) {
	t.Parallel()
	user := "struct User {\n    name: str\n    age: int\n}\n\n" +
		goldenItems(t, "derive_eq.golden") + "\n" + goldenItems(t, "derive_hash.golden") + "\n" +
		goldenItems(t, "derive_format.golden") + strings.SplitN(deriveProgram, "gen derive.Format(User)\n", 2)[1]
	t.Run("struct", func(t *testing.T) {
		t.Parallel()
		wantRun(t, runNative(t, buildNative(t, nil, user), ""), 0, "User(name: ann, age: 3) true false\n2 5\n")
	})
	t.Run("enum", func(t *testing.T) {
		t.Parallel()
		wantRun(t, runNative(t, buildNative(t, nil, shapeDecl+"\n"+shapeTarget+shapeMain), ""), 0, shapeOut)
	})
}

// TestDeriveEnumFormsDeclared: derive/derive.mk routes an enum to its
// enum forms in all three generators, and binds payload fields with the
// digit suffixes shapeTarget shows, so the two cannot drift apart unseen.
func TestDeriveEnumFormsDeclared(t *testing.T) {
	src := readText(t, "derive/derive.mk")
	for _, want := range []string{
		"if t.Kind() == TypeKind.Enum {\n        EqEnum(g, t)",
		"if t.Kind() == TypeKind.Enum {\n        HashEnum(g, t)",
		"if t.Kind() == TypeKind.Enum {\n        FormatEnum(g, t)",
		"t.Push(f.Name()).Must()\n    if side == 1 {\n        t.Push(\"1\").Must()\n    } else {\n        t.Push(\"2\").Must()",
		"h.U64($i)",
		"match other {\n                    $inner",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("derive/derive.mk lacks %q", want)
		}
	}
}

// TestDeriveEnumEndToEnd: gen lines over Shape print shapeOut, as the
// handwritten shapeTarget does.
func TestDeriveEnumEndToEnd(t *testing.T) {
	t.Parallel()
	prog := "import std.derive\n\n" + shapeDecl + "\ngen derive.Eq(Shape)\ngen derive.Hash(Shape)\ngen derive.Format(Shape)\n" + shapeMain
	out, status := checkProgram(t, nil, prog)
	if status != 0 {
		t.Fatalf("mk check: exit %d\n%s", status, out)
	}
	wantRun(t, runNative(t, buildNative(t, nil, prog), ""), 0, shapeOut)
}

// TestSTDMEM_DeriveEmptyEnum: derive.Eq, derive.Hash and derive.Format on
// an enum with no variants are each E0903 at the type, naming the generator
// and its gen line; never E0905 generated-invalid, which blames std
// (Hash and Format once emitted an empty `match self`). The enum
// alone checks clean, so the input is reachable.
func TestSTDMEM_DeriveEmptyEnum(t *testing.T) {
	t.Parallel()
	if out, status := checkProgram(t, nil, "enum Never {}\n"); status != 0 || len(diagCodes(out)) != 0 {
		t.Fatalf("enum Never {} alone: exit %d\n%s", status, out)
	}
	prog := "import std.derive\n\nenum Never {}\n\ngen derive.Eq(Never)\ngen derive.Hash(Never)\ngen derive.Format(Never)\n"
	out, _ := checkProgram(t, nil, prog)
	if got := strings.Join(diagCodes(out), ","); got != "3 E0903,3 E0903,3 E0903" {
		t.Errorf("want three E0903 at the type (line 3), got %q\n%s", got, out)
	}
	for _, gen := range []string{"generator: derive.Eq\n    gen: main.mk:5:1", "generator: derive.Hash\n    gen: main.mk:6:1", "generator: derive.Format\n    gen: main.mk:7:1"} {
		if !strings.Contains(out, gen) {
			t.Errorf("no E0903 names %q\n%s", gen, out)
		}
	}
	if strings.Contains(out, "E0905") {
		t.Errorf("a generator emitted invalid code:\n%s", out)
	}
	for _, name := range []string{"derive.Eq:", "derive.Hash:", "derive.Format:"} {
		if !strings.Contains(out, name) {
			t.Errorf("no diagnostic names %s\n%s", name, out)
		}
	}
	// The test sees the bug: without HashEnum's guard, Hash is E0905
	// again, from the generator's empty match.
	src := readText(t, "derive/derive.mk")
	guard := "    if vs.Len() == 0 {\n        g.FailOn(t, \"derive.Hash: an enum with no variants has no values to hash\")\n        return\n    }\n"
	planted := strings.Replace(src, guard, "", 1)
	if planted == src {
		t.Fatal("derive/derive.mk: HashEnum's empty-enum guard not found")
	}
	out, _ = checkProgram(t, map[string]string{"derive/derive.mk": planted}, prog)
	if !strings.Contains(out, "E0905") {
		t.Errorf("without HashEnum's guard, want E0905 (the finding), got\n%s", out)
	}
}
