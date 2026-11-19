// Why: a restated clause must mean what the callee's clause means, not only
// read the same, or a caller's own name can stand in for the callee's.
// What: TestRestateByMeaning, TestLoadedCalleeAnswersAlike,
// TestLabelsSpelled.
// How: checks callers whose requires match the demanded text with names
// that resolve elsewhere, from source and from Load, and compares codes.

package check

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// TestRestateByMeaning: a demand to restate a callee's clause is met
// by a caller's requires with the same text and the same meaning of every
// name the substitution keeps. When the text alone decided, a caller's
// own Lim (beside lib's exported Lim), a caller's const generic named Lim,
// or lib's private Lim (directly or through a type generic) met the demand,
// and lib.At or At ran with its clause false, reading past the table in
// release. The honest twins name the callee's item as this file can
// (`lib.Lim`) and are accepted.
func TestRestateByMeaning(t *testing.T) {
	const libExp = "export const Lim: int = 4\n\nexport At[const N: int](): int\n\trequires N >= 0 && N < Lim {\n\treturn N\n}\n\n" +
		"export G[T](): int\n\trequires SizeOf[T]() < Lim {\n\treturn 0\n}\n"
	const libPriv = "const Lim: int = 4\n\nexport At[const N: int](): int\n\trequires N >= 0 && N < Lim {\n\treturn N\n}\n\n" +
		"export G[T](): int\n\trequires SizeOf[T]() < Lim {\n\treturn 0\n}\n"
	const same = "const Lim: int = 4\n\nAt[const N: int](): int\n\trequires N >= 0 && N < Lim {\n\treturn N\n}\n\n"
	for _, tc := range []struct {
		name, lib, src string
		want, clause   string
		cannot         bool // the message says it cannot be judged, with no fix
	}{
		{"t1: the caller's own Lim", libExp, "import lib\n\nconst Lim: int = 100\n\nFwd[const J: int](): int\n\trequires J >= 0 && J < Lim {\n\treturn lib.At[J]()\n}\n",
			"E0706@7:9", "J >= 0 && J < lib.Lim", false},
		{"t1h: lib.Lim as this file names it", libExp, "import lib\n\nconst Lim: int = 100\n\nFwd[const J: int](): int\n\trequires J >= 0 && J < lib.Lim {\n\treturn lib.At[J]()\n}\n",
			"", "", false},
		{"t5h: the caller's generic named Lim, substituted beside a kept Lim", libExp, "import lib\n\nFwd[const Lim: int](): int\n\trequires Lim >= 0 && Lim < lib.Lim {\n\treturn lib.At[Lim]()\n}\n",
			"", "", false},
		{"x61b: lib's private Lim", libPriv, "import lib\n\nconst Lim: int = 100\n\nFwd[const J: int](): int\n\trequires J >= 0 && J < Lim {\n\treturn lib.At[J]()\n}\n",
			"E0706@7:9", "J >= 0 && J < Lim", true},
		{"x61: lib's private Lim through a type generic", libPriv, "import lib\n\nconst Lim: int = 100\n\nK[U](): int\n\trequires SizeOf[U]() < Lim {\n\treturn lib.G[U]()\n}\n",
			"E0706@7:9", "SizeOf[U]() < Lim", true},
		{"x61h: lib.Lim exported, through a type generic", libExp, "import lib\n\nK[U](): int\n\trequires SizeOf[U]() < lib.Lim {\n\treturn lib.G[U]()\n}\n",
			"", "", false},
		{"t4: one module, the caller's generic named Lim", "", same + "Fwd[const J: int, const Lim: int](): int\n\trequires J >= 0 && J < Lim {\n\treturn At[J]()\n}\n",
			"E0706@10:9", "J >= 0 && J < Lim", true},
		{"t4: one module, Lim < Lim with one Lim substituted", "", same + "Fwd[const Lim: int](): int\n\trequires Lim >= 0 && Lim < Lim {\n\treturn At[Lim]()\n}\n",
			"E0706@10:9", "Lim >= 0 && Lim < Lim", true},
		{"t4h: one module, the module's Lim", "", same + "Fwd[const J: int, const L: int](): int\n\trequires J >= 0 && J < Lim {\n\treturn At[J]()\n}\n",
			"", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.withEval()
			if tc.lib != "" {
				w.add("lib", opt{}, tc.lib)
			}
			b := w.add("app", opt{}, tc.src)
			ds := append(b.diags(), b.bodyDiags(t)...)
			if got := codes(w, ds); got != tc.want {
				t.Fatalf("diagnostics %q, want %q\n%s", got, tc.want, dump(w, ds))
			}
			if tc.want == "" {
				return
			}
			d := ds[0]
			if got := fact(d, "clause"); got != tc.clause {
				t.Errorf("clause %q, want %q", got, tc.clause)
			}
			if got := strings.Contains(d.Message, "cannot be judged here"); got != tc.cannot {
				t.Errorf("message %q: cannot be judged %v, want %v", d.Message, got, tc.cannot)
			}
			if tc.cannot && len(d.Fixes) > 0 {
				t.Errorf("a demand no clause can meet offers a fix: %+v", d.Fixes)
			}
			if !tc.cannot && len(d.Fixes) != 1 {
				t.Errorf("want the fix that states the clause, got %+v", d.Fixes)
			}
		})
	}
}

// TestLoadedCalleeAnswersAlike: the callee's side of the match is read
// from Contract.Text and the item table, so a callee module Load rebuilt
// gives each caller the diagnostics, bodies and read sets a callee checked
// from source does (the warm and --no-cache verdicts agree).
func TestLoadedCalleeAnswersAlike(t *testing.T) {
	for _, tc := range []struct {
		name, lib, app string
		e0706          int    // E0706s in the diagnostics
		has            string // text they hold
	}{
		{"exported", "export const Lim: int = 4\n\nexport At[const N: int](): int\n\trequires N >= 0 && N < Lim {\n\treturn N\n}\n",
			"import lib\n\nconst Lim: int = 100\n\nA[const J: int](): int\n\trequires J >= 0 && J < Lim {\n\treturn lib.At[J]()\n}\n\nB[const J: int](): int\n\trequires J >= 0 && J < lib.Lim {\n\treturn lib.At[J]()\n}\n", 1, "J >= 0 && J < lib2.Lim"},
		{"private", "const Lim: int = 4\n\nexport At[const N: int](): int\n\trequires N >= 0 && N < Lim {\n\treturn N\n}\n",
			"import lib\n\nconst Lim: int = 100\n\nA[const J: int](): int\n\trequires J >= 0 && J < Lim {\n\treturn lib.At[J]()\n}\n", 1, "is private to lib2"},
		{"prelude name", "export At[const N: int](): int\n\trequires N >= 0 && N < Target.pointer_bytes {\n\treturn N\n}\n",
			"import lib\n\nA[const J: int](): int\n\trequires J >= 0 && J < Target.pointer_bytes {\n\treturn lib.At[J]()\n}\n", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := loadWorld(t)
			lib := w.add("lib2", opt{}, strings.ReplaceAll(tc.lib, "lib", "lib2"))
			app := w.add("app", opt{stopBefore: true}, strings.ReplaceAll(tc.app, "lib", "lib2"))
			want := checkApp(t, w, w.c, app)
			c2, _ := loadedChecker(t, w, w.core, w.mods["lib"].id, lib.id)
			got := checkApp(t, w, c2, app)
			if len(got.bodies) != len(want.bodies) {
				t.Fatalf("%d units, want %d", len(got.bodies), len(want.bodies))
			}
			if !bytes.Equal(got.json, want.json) {
				t.Errorf("diagnostics differ:\nSignatures: %s\nLoad:       %s", want.json, got.json)
			}
			for i := range want.bodies {
				if i < len(got.bodies) && !reflect.DeepEqual(got.bodies[i], want.bodies[i]) {
					t.Errorf("unit %s differs after Load", w.items.Get(want.bodies[i].Item).Key)
				}
			}
		})
	}
}

// TestLabelsSpelled: a struct literal's field labels are part of a
// clause's text (exprText), and a label is never read as a name: a callee
// parameter named as the field is not substituted into the label.
func TestLabelsSpelled(t *testing.T) {
	src := "struct P {\n\ta: int\n\tb: int\n}\n\n" + // lines 1-4
		"F(const a: int): int\n\trequires P(a: a, b: 1).a < 4 {\n\treturn a\n}\n\n" + // lines 6-9
		"G[const k: int](): int {\n\treturn F(k)\n}\n" // lines 11-13
	w := newWorld(t, true)
	w.withEval()
	b := checkBody(t, w, "E0706@12:9", src)
	if got := fact(b.bodyDiags(t)[0], "clause"); got != "P(a: k, b: 1).a < 4" {
		t.Fatalf("clause %q, want %q", got, "P(a: k, b: 1).a < 4")
	}
}
