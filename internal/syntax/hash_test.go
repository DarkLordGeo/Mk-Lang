// Why: incremental builds key on item and file hashes, so a hash must change
// with the tokens and ignore trivia; tools parse fragments, not whole files.
// What: tests of ItemHash, FileHash, doc comments and ParseFragment by kind.
// How: hashes edited variants of small sources and compares, and parses
// statement, item, type and expression fragments.

package syntax

import (
	"testing"

	"mk/internal/ast"
	"mk/internal/source"
)

func itemHash(t *testing.T, text string, i int) source.Hash {
	t.Helper()
	r := parse(t, text, Options{AllowBodiless: true})
	if len(r.diags) != 0 {
		t.Fatalf("%v", r.codes())
	}
	return ItemHash(r.file, r.src.Text, r.file.Items[i])
}

func fileHash(t *testing.T, text string) source.Hash {
	t.Helper()
	r := parse(t, text, Options{AllowBodiless: true})
	return FileHash(r.file, r.src.Text)
}

func TestItemHashIgnoresTrivia(t *testing.T) {
	t.Parallel()
	a := itemHash(t, "F(a: int): int {\n    return a + 1\n}\n", 0)
	b := itemHash(t, "/// doc\nF(a: int):   int { // c\n\n    return a+1 // d\n}\n", 0)
	if a != b {
		t.Error("trivia changed the hash")
	}
}

func TestItemHashIgnoresPosition(t *testing.T) {
	t.Parallel()
	a := itemHash(t, "F() {\n}\n", 0)
	b := itemHash(t, "struct S { }\n\n\nF() {\n}\n", 1)
	if a != b {
		t.Error("position changed the hash")
	}
}

func TestItemHashSeesTokens(t *testing.T) {
	t.Parallel()
	base := itemHash(t, "F(a: int): int {\n    x := a + 1\n    return x\n}\n", 0)
	for _, s := range []string{
		"F(a: int): int {\n    x := a - 1\n    return x\n}\n",
		"F(b: int): int {\n    x := b + 1\n    return x\n}\n",
		"F(a: int): int {\n    x := a + 2\n    return x\n}\n",
	} {
		if itemHash(t, s, 0) == base {
			t.Errorf("hash unchanged for %q", s)
		}
	}
	// A line break that adds a Term inside a body changes the hash.
	if itemHash(t, "F() {\n    G()\n    H()\n}\n", 0) == itemHash(t, "F() {\n    G() + H()\n}\n", 0) {
		t.Error("statements against an expression hash the same")
	}
}

func TestItemHashIgnoresContinuation(t *testing.T) {
	t.Parallel()
	a := itemHash(t, "F(a: &A): &A from a { return a }\n", 0)
	b := itemHash(t, "F(a: &A): &A\n    from a\n{ return a }\n", 0)
	if a != b {
		t.Error("continuation changed the hash")
	}
	c := itemHash(t, "F(a: int) requires a > 0 {\n}\n", 0)
	d := itemHash(t, "F(a: int)\n    requires a > 0\n{\n}\n", 0)
	if c != d {
		t.Error("a contract on its own line changed the hash")
	}
}

func TestFileHash(t *testing.T) {
	t.Parallel()
	a := fileHash(t, "F() {\n}\nG() {\n}\n")
	if a != fileHash(t, "// c\nF() {\n}\n\n\nG() { // x\n}\n") {
		t.Error("comments and blank lines changed the file hash")
	}
	if a == fileHash(t, "F() {\n}\nH() {\n}\n") {
		t.Error("a changed token left the file hash")
	}
	if fileHash(t, "F(a: &A): &A from a {\n}\n") != fileHash(t, "F(a: &A): &A\n    from a\n{\n}\n") {
		t.Error("continuation changed the file hash")
	}
}

func TestDocComment(t *testing.T) {
	t.Parallel()
	r := parse(t, "/// a\n/// b\nF() {\n}\n// c\nG() {\n}\n//// d\nH() {\n}\n/// e\n\nI() {\n}\n", Options{})
	want := []string{"a\nb", "", "", "e"}
	for i, w := range want {
		if got := DocComment(r.file, r.src.Text, r.file.Items[i]); got != w {
			t.Errorf("item %d: %q, want %q", i, got, w)
		}
	}
}

func fragment(t *testing.T, text string, kind FragmentKind) (*ast.File, []ast.Node, []string) {
	t.Helper()
	sm := source.NewSourceMap()
	src := sm.Add("gen.mk", []byte(text))
	in := source.NewInterner()
	f, nodes, ds := ParseFragment(src, in, kind, Options{})
	if err := ast.Validate(f, src.Text); err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, d := range ds {
		msgs = append(msgs, d.Code.String()+" "+d.Message)
	}
	return f, nodes, msgs
}

func TestParseFragment_Stmts(t *testing.T) {
	_, nodes, ds := fragment(t, "x := 1\ny := x\n", FragStmts)
	if len(ds) != 0 || len(nodes) != 2 {
		t.Errorf("%d nodes, %v", len(nodes), ds)
	}
}

func TestParseFragment_Items(t *testing.T) {
	_, nodes, ds := fragment(t, "F() {\n}\nstruct S { }\n", FragItems)
	if len(ds) != 0 || len(nodes) != 2 {
		t.Errorf("%d nodes, %v", len(nodes), ds)
	}
}

func TestParseFragment_Type(t *testing.T) {
	_, nodes, ds := fragment(t, "?[]u8", FragType)
	if len(ds) != 0 || len(nodes) != 1 {
		t.Fatalf("%d nodes, %v", len(nodes), ds)
	}
	if _, ok := nodes[0].(*ast.OptionalType); !ok {
		t.Errorf("got %T", nodes[0])
	}
	if _, _, ds := fragment(t, "u8 u8", FragType); len(ds) != 1 {
		t.Errorf("trailing token: %v", ds)
	}
}

func TestParseFragment_Expr(t *testing.T) {
	_, nodes, ds := fragment(t, "a + b", FragExpr)
	if len(ds) != 0 || len(nodes) != 1 {
		t.Fatalf("%d nodes, %v", len(nodes), ds)
	}
	if _, ok := nodes[0].(*ast.BinaryExpr); !ok {
		t.Errorf("got %T", nodes[0])
	}
}

func TestParseFragment_GenRejected(t *testing.T) {
	_, _, ds := fragment(t, "gen derive.Eq(T)\n", FragItems)
	if len(ds) != 1 || ds[0] != "E0101 generated output holds no gen" {
		t.Errorf("%v", ds)
	}
}

func TestParseFragment_StrayBrace(t *testing.T) {
	_, _, ds := fragment(t, "x := 1 }\n", FragStmts)
	if len(ds) != 1 || ds[0] != "E0101 unmatched `}`" {
		t.Errorf("%v", ds)
	}
}
