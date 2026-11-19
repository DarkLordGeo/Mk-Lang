// Why: defects found by dogfood programs must stay fixed: E0306's note on
// open std sets, a hidden prelude name, and the cost of the facts walk.
// What: TestE0306NoteOnStdSet, TestE0306StdWalkStaysInImports,
// TestPackHidingNamesTheDeclaration, TestFetchFacts* (termination
// and polynomial cost of the facts walk).
// How: checks small programs and compares codes, notes and messages; the
// facts tests nest generic constructors deep and bound the time.

package check

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/types"
)

// TestE0306NoteOnStdSet: E0306 for a missing member on a match whose
// scrutinee type is an open set of std (an error set holding an open one, or
// an open enum) carries a note naming the open set, and offers the `_` arm as
// an alternative, never as a proven fix; a set the program declares, and a
// closed std enum, get neither.
func TestE0306NoteOnStdSet(t *testing.T) {
	t.Parallel()
	const note = " may gain members: keep a `_` arm"
	for _, c := range []struct {
		name, src, want string
		std             bool
	}{
		{"std error set", "import std.fs\nCode(e: fs.Error): int {\n\treturn match e {\n\t\tfs.NotFound { 1 }\n\t}\n}\n", "E0306@3:9", true},
		{"std enum", "import std.fs\nCode(k: fs.Kind): int {\n\treturn match k {\n\t\t.File { 1 }\n\t}\n}\n", "E0306@3:9", true},
		{"own set", "error A\nerror B\ntype E = A | B\nCode(e: E): int {\n\treturn match e {\n\t\tA { 1 }\n\t}\n}\n", "E0306@5:9", false},
		{"own set with a prelude error", "error A\nerror B\ntype E = A | B | OutOfMemory\nCode(e: E): int {\n\treturn match e {\n\t\tA { 1 }\n\t\tOutOfMemory { 2 }\n\t}\n}\n", "E0306@5:9", false},
		// A set the program spells from a single std error
		// is closed; one that names a whole open std set is open, since a
		// set that holds every member of an open alias is.
		{"own set with a std member", "import std.fs\nerror Mine\ntype MyErr = fs.NotFound | Mine\nCode(e: MyErr): int {\n\treturn match e {\n\t\tMine { 1 }\n\t}\n}\n", "E0306@5:9", false},
		{"own set naming a std set", "import std.fs\nerror Mine\ntype MyErr = fs.Error | Mine\nCode(e: MyErr): int {\n\treturn match e {\n\t\tMine { 1 }\n\t}\n}\n", "E0306@5:9", true},
		{"closed std enum", "import std.fs\nCode(m: fs.Mode): int {\n\treturn match m {\n\t\t.Read { 1 }\n\t}\n}\n", "E0306@3:9", false},
		{"std set with a _ arm", "import std.fs\nCode(e: fs.Error): int {\n\treturn match e {\n\t\tfs.NotFound { 1 }\n\t\t_ { 2 }\n\t}\n}\n", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, true)
			w.add("std.fs", opt{std: true}, "export error NotFound\nexport error Denied\nexport open type Error = NotFound | Denied\nexport open enum Kind {\n\tFile\n\tDir\n}\nexport enum Mode {\n\tRead\n\tWrite\n}\n")
			b := checkBody(t, w, c.want, c.src)
			for _, d := range b.bodyDiags(t) {
				got := false
				for _, f := range d.Facts {
					if f.Key == "note" && strings.HasSuffix(f.Value.Str, note) {
						got = true
					}
				}
				if got != c.std {
					t.Errorf("note present %v, want %v: %s", got, c.std, d.Message)
				}
				alt := len(d.Alternatives) == 1 && strings.Contains(d.Alternatives[0], "`_ { ... }`")
				if alt != c.std {
					t.Errorf("`_` alternative %v, want %v: %q", alt, c.std, d.Alternatives)
				}
				for _, f := range d.Fixes {
					if f.Class == diag.Proven {
						t.Errorf("E0306 carries a proven fix: %+v", f)
					}
				}
			}
		})
	}
}

// TestPackHidingNamesTheDeclaration: a module function `List` hides
// the prelude type `List` (a module-level name may hide a prelude name, and
// module-level names share one namespace), so `List[int].New(..)`
// and `&List[int]` are refused as before; the diagnostic now names the
// hiding function and where it is declared. A function of a name the
// prelude does not give a type gets no note.
func TestPackHidingNamesTheDeclaration(t *testing.T) {
	t.Parallel()
	const fn = "List(n: int): int {\n\treturn n\n}\n"
	for _, c := range []struct {
		name, src, want string
		note            bool
	}{
		{"value position", fn + "F() {\n\t_ = List[int].New()\n}\n", "E0301@5:11 E0308@5:6", true},
		{"type position", fn + "G(xs: &List[int]): int {\n\treturn 0\n}\n", "E0301@4:8", true},
		{"no prelude type", "Lost(n: int): int {\n\treturn n\n}\nF() {\n\t_ = Lost[1]\n}\n", "E0308@5:6", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, true)
			b := w.add("m", opt{}, c.src)
			var ds []*diag.Diagnostic
			for _, d := range append(b.diags(), b.bodyDiags(t)...) {
				if d.Code != diag.E0208 {
					ds = append(ds, d)
				}
			}
			if got := codes(w, ds); got != c.want {
				t.Fatalf("diagnostics %q, want %q\n%s", got, c.want, dump(w, ds))
			}
			notes := 0
			for _, d := range ds {
				for _, f := range d.Facts {
					if f.Key == "note" && strings.Contains(f.Value.Str, "hides the prelude type `List`") {
						notes++
						if _, ok := d.FactValue("declared_at"); !ok || len(d.Alternatives) != 1 || d.Alternatives[0] != "rename the func `List`" {
							t.Errorf("note without its declaration or rename: %s", dump(w, []*diag.Diagnostic{d}))
						}
					}
				}
			}
			if (notes == 1) != c.note {
				t.Errorf("%d notes, want note %v\n%s", notes, c.note, dump(w, ds))
			}
		})
	}
}

// TestFetchFactsTopDownTerminates: the facts of a type that nests
// one generic struct four deep (std.http's `Client.slots`,
// Pair[Pair[Pair[Pair[Slot]]]]), asked top down with nothing memoized (a
// build whose std came from the cache), end with the answers the bottom-up
// walk gives. The recursion check compared Pair[Pair[Pair[Slot]]] with
// the entry Pair[Pair[Pair[Pair[Slot]]]] by asking for the entry's
// argument, the type itself, and recursed until the stack overflowed.
func TestFetchFactsTopDownTerminates(t *testing.T) {
	t.Parallel()
	for _, slot := range []string{"struct Slot {\n    n: int\n}\n", "struct Slot {\n    n: Cell[int]\n}\n"} {
		w := newWorld(t, true)
		b := w.add("main", opt{}, slot+"struct Pair[T] {\n    a: T\n    b: T\n}\nstruct Client {\n    slots: Pair[Pair[Pair[Pair[Slot]]]]\n}\n")
		b.wantClean(t)
		client := w.tab.Named(b.item("Client"), nil)
		want := w.c.Facts(client)
		w.c.memo.dir.Store(nil)
		if got := w.c.Facts(client); got != want {
			t.Errorf("%q: top-down facts %+v, bottom-up %+v", slot, got, want)
		}
	}
}

// TestFetchFactsWalkPolynomial: the facts walk is
// linear in how deeply one generic constructor nests. The recursion check
// compared a nested instance with the entry above it by walking both
// argument lists afresh, and an answer that met the type being decided was
// never kept, so each level walked the one below it twice: Q nested 20
// deep took minutes. Each shape is checked 24 deep, and its facts are
// asked again top down with nothing memoized.
func TestFetchFactsWalkPolynomial(t *testing.T) {
	t.Parallel()
	nest := func(head, inner string, n int) string {
		for range n {
			inner = head + "[" + inner + "]"
		}
		return inner
	}
	for _, c := range []struct{ name, src string }{
		{"closed", "struct Q[T] {\n    a: T\n}\nUse(x: &" + nest("Q", "int", 24) + "): int {\n    return 0\n}\n"},
		{"generic", "struct P[T] {\n    a: T\n    b: ?&P[T]\n}\nUse[T](x: &" + nest("P", "T", 24) + "): int {\n    return 0\n}\n"},
		{"cell", "struct Q[T] {\n    a: T\n}\nUse(x: &" + nest("Q", "Cell[int]", 24) + "): int {\n    return 0\n}\n"},
		// Under a cycle's root nothing of the nest is memoized until the
		// root is, so only the kept argument facts stop the re-walks.
		{"cycle", "struct Q[T] {\n    a: T\n    c: Cell[int]\n}\nstruct S {\n    next: ?&" + nest("Q", "S", 24) + "\n}\nUse(x: &S): int {\n    return 0\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			done := make(chan string, 1)
			go func() {
				b := w.add("main", opt{}, c.src)
				if ds := append(b.diags(), b.bodyDiags(t)...); len(ds) > 0 {
					done <- dump(w, ds)
					return
				}
				if c.name == "generic" {
					done <- ""
					return
				}
				use := w.items.Get(b.item("Use")).Decl.(*types.FuncDecl)
				x := w.tab.Get(use.Sig.Params[0].Type).Elem
				want := w.c.Facts(x)
				w.c.memo.dir.Store(nil)
				if got := w.c.Facts(x); got != want {
					done <- fmt.Sprintf("top-down facts %+v, bottom-up %+v", got, want)
					return
				}
				if want.Share != (c.name == "closed") {
					done <- fmt.Sprintf("facts %+v: Share must follow the Cell", want)
					return
				}
				done <- ""
			}()
			select {
			case msg := <-done:
				if msg != "" {
					t.Fatal(msg)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("check did not end within 20 s")
			}
		})
	}
}

// TestE0306StdWalkStaysInImports: the std-set test
// behind E0306's note reads only the aliases of the unit's module and its
// imports. A std module the unit does not import, caught in its own stage 5
// (registered, nothing declared yet), keeps its alias undeclared: declaring
// it from the unit wrote that module's state from another goroutine.
func TestE0306StdWalkStaysInImports(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, src, want string }{
		{"own set", "error A\nerror B\ntype E = A | B\nCode(e: E): int {\n\treturn match e {\n\t\tA { 1 }\n\t}\n}\n", "E0306@5:9"},
		{"std set", "import std.fs\nCode(e: fs.Error): int {\n\treturn match e {\n\t\tfs.NotFound { 1 }\n\t}\n}\n", "E0306@3:9"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.add("std.fs", opt{std: true}, "export error NotFound\nexport error Denied\nexport open type Error = NotFound | Denied\n")
			other := w.add("std.other", opt{std: true, stopBefore: true}, "export error Busy\nexport open type Error = Busy\n")
			mid := w.c.newModule(&ModuleInput{Module: other.id, Files: other.files, Info: other.info})
			mid.initDecls()
			alias := w.items.Get(other.item("Error"))
			checkBody(t, w, c.want, c.src)
			if alias.Decl != nil || len(mid.d.state) != 0 {
				t.Fatalf("E0306 declared std.other.Error, outside the unit's imports (state %v)", mid.d.state)
			}
		})
	}
}
