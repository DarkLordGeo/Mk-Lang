// Why: resolve runs on whatever the parser produced, broken input included,
// so no input may crash or hang it.
// What: FuzzResolve and TestResolveNeverPanics.
// How: parses arbitrary input and resolves it whatever the parse reported;
// the property is no ICE (no E0000, no panic), no hang, and no RefPending
// after Settle.

package resolve

import (
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// resolveText resolves text as module main against the fake prelude, with
// a gen line's module settled, and returns every diagnostic.
func resolveText(t testing.TB, text string, w *world) []*diag.Diagnostic {
	src := w.sm.Add("fuzz.mk", []byte(text))
	f, _ := syntax.ParseFile(src, w.in, syntax.Options{})
	if err := ast.Validate(f, src.Text); err != nil {
		t.Fatalf("parser broke Validate: %v", err)
	}
	mod := &types.Module{Path: "main", Files: []source.FileID{src.ID}}
	id := w.items.AddModule(mod)
	in := &ModuleInput{Module: id, Files: []*ast.File{f}, Prelude: w.core, BinRoot: true, HasGen: func(types.ModuleID) bool { return false }}
	m := w.s.Begin(in)
	info := m.Finish(2)
	ds, gs := m.Settle()
	all := append(append(append([]*diag.Diagnostic(nil), info.Diags...), ds...), gs...)
	for _, fi := range info.Files {
		for i, r := range fi.Refs {
			if r.Kind == RefPending {
				t.Fatalf("node %d is RefPending after Settle", i)
			}
		}
	}
	return all
}

func FuzzResolve(f *testing.F) {
	for _, b := range readDocBlocks(f) {
		f.Add(b.text)
	}
	for _, s := range []string{
		"F() { x := fn() { y := fn() { _ = x } } }\n",
		"struct S[T] { x: T }\nS[T].M[T](self) {}\nimpl Eq for S[int] {}\n",
		"gen a.b(1, -2, &T)\nF(): Nope { return Nope.X[1][2] }\n",
		"F() { match x { A | B { } err(ok) { } Some(x) { } } }\n",
		"enum E { A\n A }\nE.A(self) {}\nerror E\n",
		// A local declared in a function's type-parameter bound, before
		// the function's own frame (a case FuzzCheck found).
		"A[:[else{A:",
		"F[T: [else e { x := 1 }]]() {}\n",
		"interface I {\n\tM[T: [else e { x := 1 }]](&self)\n}\n",
		"struct S {\n}\n\nS.M[T: [else e { x := 1 }]](&self) {}\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		w := newWorld(t, true)
		for _, d := range resolveText(t, text, w) {
			if d.Code == diag.E0000 {
				t.Fatalf("ICE: %s %v", d.Message, d.Facts)
			}
		}
	})
}

// TestResolveNeverPanics runs the fuzz property over the seeds and every
// line prefix of every Mk block in doc/*.md (readDocBlocks), as the
// parser's TestParseNeverPanics does, so a regression shows in go test
// without -fuzz.
func TestResolveNeverPanics(t *testing.T) {
	t.Parallel()
	for _, b := range readDocBlocks(t) {
		text := b.text
		for i := 0; i <= len(text); i++ {
			if i < len(text) && text[i] != '\n' {
				continue
			}
			for _, d := range resolveText(t, text[:i], newWorld(t, true)) {
				if d.Code == diag.E0000 {
					t.Fatalf("%s prefix %d: ICE %s %v", b.name, i, d.Message, d.Facts)
				}
			}
		}
	}
}
