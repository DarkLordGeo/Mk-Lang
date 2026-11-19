// Why: inlay type hints and local hovers must show only types the text proves.
// What: TestLSPInlayTypeHints, TestLSPHoverLocalKind, TestLSPSemanticTokensUnique, TestInferHelpers.
// How: opens inferFixture in a scripted server session and compares hints,
// hovers and tokens; the pure inference helpers are tested directly.

package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

const inferFixture = `struct Guarded {
    m: Mutex[int]
}

struct Pt {
    x: int
    y: int
}

Pt.Norm(&self): int {
    sx := self.x
    return sx + self.y
}

Pt.Twin(&self): Pt {
    return Pt(x: self.x, y: self.y)
}

MakePt(n: int): Pt {
    return Pt(x: n, y: n)
}

HoldG(g: Guarded) {
}

Count(s: str): int {
    return s.Len()
}

Size(xs: &List[int]): int {
    k := xs.Len()
    return k
}

export Main(sys: &System) {
    a := 1
    f := 2.5
    s := "hi"
    c := 'c'
    p := MakePt(3)
    n := p.Norm()
    q := p
    w := p.x
    t := p.Twin()
    r := &p
    l := Count("abc")
    z := a + 1
    v := Pt(x: 1, y: 2)
    _ = f
    _ = s
    _ = c
    _ = n
    _ = q
    _ = w
    _ = t
    _ = r
    _ = l
    _ = z
    _ = v
}
`

func openInferFixture(t *testing.T) (*client, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.mk")
	must(t, writeFile(path, inferFixture))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, inferFixture)
	c.diagnostics(uri)
	return c, uri
}

// TestLSPInlayTypeHints: `: T` after each `x :=` whose type the text
// proves (a literal, a call's declared result, a method's through the
// receiver's type, `Self` as the receiver type, a std method's, a field, a copy of
// a local, `&` of one), and an operator's from the checker (exact.go);
// none for a construction that spells its type.
func TestLSPInlayTypeHints(t *testing.T) {
	c, uri := openInferFixture(t)
	var hints []inlayHint
	c.result(c.call("textDocument/inlayHint", map[string]any{"textDocument": map[string]any{"uri": uri},
		"range": map[string]any{"start": pos(0, 0), "end": pos(1000, 0)}}), &hints)
	got := map[Position]string{}
	for _, h := range hints {
		if h.Kind == inlayType {
			got[h.Position] = h.Label
		}
	}
	for _, tc := range []struct{ decl, want string }{
		{"a := 1", ": int"},
		{"f := 2.5", ": f64"},
		{`s := "hi"`, ": str"},
		{"c := 'c'", ": char"},
		{"p := MakePt", ": Pt"},
		{"n := p.Norm", ": int"},
		{"q := p", ": Pt"},
		{"w := p.x", ": int"},
		{"t := p.Twin", ": Pt"},
		{"r := &p", ": &Pt"},
		{"l := Count", ": int"},
		{"k := xs.Len", ": int"},
		{"sx := self.x", ": int"},
		{"z := a + 1", ": int"}, // the checker's (exact.go); the text refuses an operator
		{"v := Pt(", ""},
	} {
		line, col := lineOf(t, inferFixture, tc.decl)
		if g := got[Position{line, col + len(strings.Fields(tc.decl)[0])}]; g != tc.want {
			t.Errorf("%q: hint %q, want %q (all: %+v)", tc.decl, g, tc.want, hints)
		}
	}
	c.shutdown()
}

// TestLSPHoverLocalKind: a local's hover shows its declared or inferred
// type, and the type's kind from `mk query kind` when it is a bare named
// type.
func TestLSPHoverLocalKind(t *testing.T) {
	c, uri := openInferFixture(t)
	for _, tc := range []struct {
		sub  string
		want []string
	}{
		{"g: Guarded", []string{"(param) g: Guarded", "Guarded is unique", "field m: Mutex[int] is unique"}},
		{"q := p", []string{"(local) q: Pt", "Pt is plain"}},
		{"n := p.Norm", []string{"(local) n: int"}},
		{"r := &p", []string{"(local) r: &Pt"}},
	} {
		line, col := lineOf(t, inferFixture, tc.sub)
		var h hover
		c.result(c.call("textDocument/hover", at(uri, line, col)), &h)
		for _, w := range tc.want {
			if !strings.Contains(h.Contents.Value, w) {
				t.Errorf("hover on %q: %s, want %q", tc.sub, h.Contents.Value, w)
			}
		}
		if tc.sub == "r := &p" && strings.Contains(h.Contents.Value, " is ") {
			t.Errorf("hover on %q: %s, want no kind for a reference", tc.sub, h.Contents.Value)
		}
	}
	c.shutdown()
}

// TestLSPSemanticTokensUnique: a type `mk query kind` calls unique carries
// the unique modifier, a plain one none; the answer is cached, so a second
// request gives the same tokens.
func TestLSPSemanticTokensUnique(t *testing.T) {
	c, uri := openInferFixture(t)
	for round := 0; round < 2; round++ {
		var st semanticTokens
		c.result(c.call("textDocument/semanticTokens/full", doc(uri)), &st)
		toks := decodeTokens(st.Data)
		check := func(sub string, off int, mods, not int) {
			t.Helper()
			line, col := lineOf(t, inferFixture, sub)
			got, ok := toks[[2]int{line, col + off}]
			if !ok || got[1] != int(classType) || got[2]&mods != mods || got[2]&not != 0 {
				t.Errorf("round %d %q+%d: %v (ok %v), want a type with modifiers %b and not %b", round, sub, off, got, ok, mods, not)
			}
		}
		check("struct Guarded", 7, modDeclaration|modUnique, 0)
		check("g: Guarded", 3, modUnique, 0)
		check("struct Pt", 7, modDeclaration, modUnique|modLinear)
	}
	c.shutdown()
}

// TestInferHelpers: the pure pieces of the inference.
func TestInferHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"Alloc!&mut List[T]": "List",
		"?Pt":                "Pt",
		"m.T":                "m.T",
		"int":                "int",
	} {
		if got := baseType(in); got != want {
			t.Errorf("baseType(%q) = %q, want %q", in, got, want)
		}
	}
	if got := typeText([]string{"&", "mut", "Map", "[", "str", ",", "int", "]"}); got != "&mut Map[str, int]" {
		t.Errorf("typeText: %q", got)
	}
	if got := typeText([]string{"E", "!", "fn", "(", "int", ")", ":", "bool"}); got != "E!fn(int): bool" {
		t.Errorf("typeText: %q", got)
	}
	if got := replaceWord("Self!?Self", "Self", "Pt"); got != "Pt!?Pt" {
		t.Errorf("replaceWord: %q", got)
	}
	if namesWord("Selfish", "Self") || !namesWord("[]Self", "Self") {
		t.Error("namesWord")
	}
}
