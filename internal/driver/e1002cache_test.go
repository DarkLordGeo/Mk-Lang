// Why: E1002 on a const's pointee (check.constPointee) reads other modules'
// type declarations; a warm build that misses those reads keeps a stale verdict.
// What: TestE1002_ConstPointeeColdWarm and TestE1002_ErrorPayload, over the
// module builders e1002Module, e1002C7 and e1002ErrModule.
// How: EvalConstReads returns the rule's reads and declEvals merges them
// into the module's, so a warm check redoes the rule. Module m toggles a
// private field of a struct Y in a variant nothing constructs (the walk
// inside a pointee goes by its type), behind a field that stops borrow's
// own walk early (a view, or a record with a `?&Path`), so borrow's reads
// do not cover Y; each step compares warm, MK_CACHE_VERIFY and cold verdicts.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// e1002Module is module m: H points at nothing; its private enum E holds Y
// in variant B, which Make never builds; FIELD is Y's field type.
func e1002Module(extra string) string {
	return `import std.atomic

export struct P {
    v: i64
}

unsafe impl Send for P {}
unsafe impl Share for P {}

struct Y {
    v: FIELD
}

enum E {
    A(x: i64)
    B(y: Y)
}

export struct H {
` + extra + `    export p: P
    e: E
}

export Z(): i64 {
    z := atomic.AtomicInt.New(1)
    return z.Load()
}
`
}

// e1002C7 is case c7's module: H also holds a `?&i64` that is none, and E
// a variant holding a generic G[Cell[i64]] that is Share by assertion.
const e1002C7 = `export struct P {
    v: i64
}

unsafe impl Send for P {}
unsafe impl Share for P {}

struct G[T] {
    r: ?&T
}

unsafe impl[T: Share] Share for G[T] {}

struct Y {
    v: FIELD
}

enum E {
    A(x: i64)
    B(y: Y)
    C(g: G[Cell[i64]])
}

export struct H {
    export p: P
    export q: ?&i64
    e: E
}

export Make(a: &Arena): &H from a {
    h := a.Put(H(p: P(v: 1), q: none, e: E.A(x: 5))) else {
        return Make(a)
    }
    return &*h
}
`

// e1002ErrModule is module m with an error Bad whose payload field is
// FIELD, held by E's variant B, which Make never builds.
const e1002ErrModule = `error Bad(p: FIELD)

enum E {
    A(x: i64)
    B(b: Bad)
}

export struct H {
    export n: i64
    e: E
}

export Make(a: &Arena): &H from a {
    h := a.Put(H(n: 1, e: E.A(x: 5))) else {
        return Make(a)
    }
    return &*h
}

export MakeR(a: &Arena): (a: ?&Path, h: &H) from a {
    return (a: none, h: Make(a))
}
`

// TestE1002_ErrorPayload: a raw pointer in an error member's payload is
// found by byValueChildren, for a const's pointee (case s22,
// constPointee) and for a const's own bytes (case s23, constPlain); both
// are E1002, since a write through the pointer would fault.
func TestE1002_ErrorPayload(t *testing.T) {
	t.Parallel()
	const u = `import std.raw

export error Bad(p: *int)

export struct EH {
    export e: Bad
}

export MakeE(a: &Arena): &EH from a {
    n := a.Put(5) else {
        return MakeE(a)
    }
    var p: *int = raw.Null[int]()
    unsafe "a pointer into the arena" {
        p = raw.OfMut(n)
    }
    h := EH(e: Bad(p: p))
    x := a.Put(h) else {
        return MakeE(a)
    }
    return &*x
}

export MakeV(a: &Arena): EH from a {
    return *MakeE(a)
}
`
	for _, c := range []struct{ name, main, want string }{
		{"s22-pointee", "import u\n\nconst GE: &u.EH = u.MakeE(EvalArena())\n", "main.mk:3:7:E1002"},
		{"s23-inline", "import u\n\nconst GE: u.EH = u.MakeV(EvalArena())\n", "main.mk:3:7:E1002"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := writeTree(t, map[string]string{
				"pkg.mk":  manifest(map[string]string{"name": `"ep"`, "unsafe_modules": `["u"]`}),
				"main.mk": c.main,
				"u/u.mk":  u,
			})
			if got := strings.Join(baseLines(check(t, Config{Root: dir, CacheDir: t.TempDir()})), " "); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestE1002_ConstPointeeColdWarm: each case's verdict, at each step of Y's
// field type, with one warm cache, with MK_CACHE_VERIFY on that cache, and
// cold.
func TestE1002_ConstPointeeColdWarm(t *testing.T) {
	t.Parallel()
	const refused = "main.mk:3:7:E1002"
	make1 := `
export Make(a: &Arena): &H from a {
    h := a.Put(H(p: P(v: 1), e: E.A(x: 5))) else {
        return Make(a)
    }
    return &*h
}
`
	for _, c := range []struct {
		name   string
		module string
		main   string
		fields []string // Y.v's type at each step
		want   []string
	}{
		// c10: a stale acceptance (P to AtomicInt kept clean warm).
		{"c10-record-path", e1002Module("") + make1 + `
export MakeR(a: &Arena): (a: ?&Path, h: &H) from a {
    return (a: none, h: Make(a))
}
`, "import m\n\nconst GH: (a: ?&Path, h: &m.H) = m.MakeR(EvalArena())\n",
			[]string{"P", "atomic.AtomicInt", "P", "atomic.AtomicInt"}, []string{"", refused, "", refused}},
		// c7: a stale refusal (Cell[i64] to i64 kept E1002 warm).
		{"c7-cell-plain", e1002C7, "import m\n\nconst GH: &m.H = m.Make(EvalArena())\n",
			[]string{"Cell[i64]", "i64", "Cell[i64]", "i64"}, []string{refused, "", refused, ""}},
		// c8: a view (Path) ahead of the enum, which borrow's walk stops at
		// and the value rule accepts.
		{"c8-view-field", e1002Module("    export w: Path\n") + `
export Make(a: &Arena): &H from a {
    w := Path.New(a, "x/y".AsBytes()) else {
        return Make(a)
    }
    h := a.Put(H(w: w, p: P(v: 1), e: E.A(x: 5))) else {
        return Make(a)
    }
    return &*h
}
`, "import m\n\nconst GH: &m.H = m.Make(EvalArena())\n",
			[]string{"P", "atomic.AtomicInt", "P", "atomic.AtomicInt"}, []string{"", refused, "", refused}},
		// An error member's payload field toggled between i64 and a raw
		// pointer, in a variant nothing constructs: the rule reads the
		// error's fields (ReadFields "all" of the member).
		{"error-payload", e1002ErrModule, "import m\n\nconst GH: (a: ?&Path, h: &m.H) = m.MakeR(EvalArena())\n",
			[]string{"i64", "*i64", "i64", "*i64"}, []string{"", refused, "", refused}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{
				"pkg.mk":  manifest(map[string]string{"name": `"cw"`, "unsafe_modules": `["m"]`}),
				"main.mk": c.main + "\nexport Main(sys: &System) {\n    sys.Print(\"hi\\n\", ()) else {}\n}\n",
				"m/m.mk":  strings.Replace(c.module, "FIELD", c.fields[0], 1),
			}
			dir := writeTree(t, files)
			warm := t.TempDir()
			path := filepath.Join(dir, "m", "m.mk")
			for i, f := range c.fields {
				if err := os.WriteFile(path, []byte(strings.Replace(c.module, "FIELD", f, 1)), 0o644); err != nil {
					t.Fatal(err)
				}
				for _, run := range []struct {
					what string
					cfg  Config
				}{
					{"warm", Config{Root: dir, CacheDir: warm}},
					{"verify", Config{Root: dir, CacheDir: warm, CacheVerify: true}},
					{"cold", Config{Root: dir, CacheDir: t.TempDir()}},
				} {
					got := strings.Join(baseLines(check(t, run.cfg)), " ")
					if got != c.want[i] {
						t.Errorf("step %d (Y.v %s), %s: %q, want %q", i+1, f, run.what, got, c.want[i])
					}
				}
			}
		})
	}
}
