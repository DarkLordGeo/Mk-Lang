// Why: the walk marker, Type.Methods' order and data file reads each let a
// warm check differ from a cold one when their reads were incomplete.
// What: the TestGen_* tests: walk markers that must rewalk or still
// hold, MK_CACHE_VERIFY over walk markers, the methods order read, a failed
// data file read's reason, and the HasFlows round on a mixed line.
// How: each case edits a package and requires a warm check after the edit
// to equal a fresh-cache cold one.

package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

// genfixNest is a generic whose every instance requests the next one: any
// instance of it is an E0708 chain.
const genfixNest = "Nest[T: Plain](x: T, n: int): int {\n    if n == 0 {\n        return 0\n    }\n    return Nest([x], n - 1)\n}\n"

// genfixEdit replaces old by new in dir's file rel, once.
func genfixEdit(t *testing.T, dir, rel, old, new string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s holds no %q", rel, old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// genfixWarmCold checks root cold (clean), applies edit, and returns the
// warm check in the same cache and a cold one in a fresh cache.
func genfixWarmCold(t *testing.T, root string, verify bool, edit func()) (Result, Result) {
	t.Helper()
	cfg := Config{Root: root, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk")}
	clean(t, frontCheck(t, cfg))
	edit()
	wcfg := cfg
	wcfg.CacheVerify = verify
	warm := frontCheck(t, wcfg)
	fresh := cfg
	fresh.CacheDir = t.TempDir()
	return warm, frontCheck(t, fresh)
}

// TestGen_WalkMarkerRewalks: each edit changes what a body the
// walk reached instantiates without touching that body's tokens, and the
// cold check gives E0708. The warm check must give it too, and equal the
// cold --json. b1 to b2c change a dependency body, a private name, an
// import alias and a root's genericity; ps and pc are a private own root
// that stays a root but returns another type, and a private const whose
// value comes from a same-module body the walk never reached.
func TestGen_WalkMarkerRewalks(t *testing.T) {
	t.Parallel()
	app := "import lib\n\nexport Use(): int {\n    return lib.H(1)\n}\n"
	cases := []struct {
		name  string
		files map[string]string
		root  string // the checked package's directory under the tree
		file  string
		old   string
		new   string
	}{
		{
			// A body edit of a non-generic function of a path dependency:
			// a dependency's functions are never limit roots.
			name: "b1_dependency_body",
			files: map[string]string{
				"app/pkg.mk":   manifest(map[string]string{"name": `"app"`, "deps": `[pkg.Dep(name: "util", path: "../util", url: "", hash: "")]`}),
				"util/pkg.mk":  manifest(map[string]string{"name": `"util"`}),
				"app/app.mk":   "import util\n\nexport Use(): int {\n    return util.Foo()\n}\n",
				"util/util.mk": genfixNest + "\nexport Foo(): int {\n    return 1\n}\n",
			},
			root: "app", file: "util/util.mk", old: "    return 1\n", new: "    return Nest(1, 3)\n",
		},
		{
			// A new private generic hides the prelude's Swap a reached
			// generic body calls.
			name: "b2_new_private_name",
			files: map[string]string{
				"pkg.mk":     manifest(nil),
				"lib/lib.mk": "export H[T: Plain](x: T): int {\n    var a := 1\n    var b := 2\n    Swap(mut a, mut b)\n    return a\n}\n",
				"app/app.mk": app,
			},
			file: "lib/lib.mk", old: "    return a\n}\n", new: "    return a\n}\n\n" + genfixNest + "\nSwap[T: Plain](a: mut T, b: mut T) {\n    _ = Nest(1, 3)\n}\n",
		},
		{
			// An import alias of a reached module repointed to another
			// module of the closure.
			name: "b2b_import_alias",
			files: map[string]string{
				"pkg.mk":     manifest(nil),
				"x/a/a.mk":   "export F[T: Plain](x: T): int {\n    return 1\n}\n",
				"x/b/b.mk":   "export F[T: Plain](x: T): int {\n    return Nest(x, 3)\n}\n\n" + genfixNest + "\nexport G(): int {\n    return 2\n}\n",
				"lib/lib.mk": "import u = x.a\nimport x.b\n\nexport H[T: Plain](x: T): int {\n    return u.F(x)\n}\n\nexport K(): int {\n    return b.G()\n}\n",
				"app/app.mk": "import lib\nimport x.a\n\nexport Use(): int {\n    return lib.H(1)\n}\n\nexport Z(): int {\n    return a.F(1)\n}\n",
			},
			file: "lib/lib.mk", old: "import u = x.a\n", new: "import u = x.b\n",
		},
		{
			// A private own root a reached generic calls becomes generic.
			name: "b2c_root_becomes_generic",
			files: map[string]string{
				"pkg.mk":     manifest(nil),
				"lib/lib.mk": genfixNest + "\nFoo(x: int): int {\n    return x\n}\n\nexport H[T: Plain](x: T): int {\n    return Foo(1)\n}\n",
				"app/app.mk": app,
			},
			file: "lib/lib.mk", old: "Foo(x: int): int {\n    return x\n", new: "Foo[T: Plain](x: T): int {\n    return Nest(x, 3)\n",
		},
		{
			// A private own root stays a root but returns another type,
			// whose impl a reached generic then instantiates.
			name: "ps_root_signature",
			files: map[string]string{
				"pkg.mk": manifest(nil),
				"lib/lib.mk": genfixNest + "\ninterface Step {\n    Go(&self): int\n}\n\nstruct A {\n    v: int\n}\n\nstruct W[T] {\n    v: T\n}\n\n" +
					"impl Step for A {\n    Go(&self): int {\n        return self.v\n    }\n}\n\nimpl[T: Plain] Step for W[T] {\n    Go(&self): int {\n        return Nest(self.v, 3)\n    }\n}\n\n" +
					"Run[S: Step + Plain](s: S): int {\n    return s.Go()\n}\n\nMake(): A {\n    return A(v: 1)\n}\n\nexport H[T: Plain](x: T): int {\n    return Run(Make())\n}\n",
				"app/app.mk": app,
			},
			file: "lib/lib.mk", old: "Make(): A {\n    return A(v: 1)\n", new: "Make(): W[int] {\n    return W[int](v: 1)\n",
		},
		{
			// A private const's value is computed by a generic the walk
			// never reaches; the const's own tokens stay.
			name: "pc_const_value",
			files: map[string]string{
				"pkg.mk": manifest(nil),
				"lib/lib.mk": genfixNest + "\nSafe(x: int): int {\n    return x\n}\n\nBad[T: Plain](x: int): int {\n    return Nest(x, 3)\n}\n\n" +
					"Pick[T: Plain](): fn(int): int {\n    return Safe\n}\n\nconst F: fn(int): int = Pick[int]()\n\nexport H[T: Plain](x: T): int {\n    return F(1)\n}\n",
				"app/app.mk": app,
			},
			file: "lib/lib.mk", old: "    return Safe\n", new: "    return Bad[int]\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := writeTree(t, c.files)
			warm, cold := genfixWarmCold(t, filepath.Join(dir, filepath.FromSlash(c.root)), false, func() { genfixEdit(t, dir, c.file, c.old, c.new) })
			if got := codes(cold); !slices.Equal(got, []string{"E0708"}) {
				t.Fatalf("cold codes %q, want [E0708]:\n%s", got, cold.Report.Text(cold.Sources))
			}
			if w, cj := jsonOf(t, warm), jsonOf(t, cold); !bytes.Equal(w, cj) {
				t.Errorf("warm --json %s\ncold --json %s", w, cj)
			}
		})
	}
}

// TestGen_VerifyChecksWalkMarkers: MK_CACHE_VERIFY walks the roots a walk
// marker would skip and reports a marker that hides a diagnostic as E0000
// "cache verify" (not exit 0 on case b1). The markers are made to hold
// whatever their reads (testTrustWalkMarkers), so the edit leaves a stale
// one to find. Not parallel: the switch is a package variable.
func TestGen_VerifyChecksWalkMarkers(t *testing.T) {
	testTrustWalkMarkers = true
	defer func() { testTrustWalkMarkers = false }()
	dir := writeTree(t, map[string]string{
		"pkg.mk":     manifest(nil),
		"lib/lib.mk": genfixNest + "\nFoo(x: int): int {\n    return x\n}\n\nexport H[T: Plain](x: T): int {\n    return Foo(1)\n}\n",
		"app/app.mk": "import lib\n\nexport Use(): int {\n    return lib.H(1)\n}\n",
	})
	warm, cold := genfixWarmCold(t, dir, true, func() {
		genfixEdit(t, dir, "lib/lib.mk", "Foo(x: int): int {\n    return x\n", "Foo[T: Plain](x: T): int {\n    return Nest(x, 3)\n")
	})
	text := warm.Report.Text(warm.Sources)
	if got := codes(warm); !slices.Equal(got, []string{"E0000", "E0708"}) && !slices.Equal(got, []string{"E0708", "E0000"}) ||
		!strings.Contains(text, "cache verify") || !strings.Contains(text, "walk ") {
		t.Errorf("verify codes %q, want the walk's E0000 cache verify beside the cold E0708:\n%s", got, text)
	}
	if got := codes(cold); !slices.Equal(got, []string{"E0708"}) {
		t.Errorf("cold codes %q, want [E0708]", got)
	}
}

// TestGen_MethodsOrderRead: swapping two exported methods of another
// module's struct reruns the gen line that listed them; the warm view equals
// the cold one, and MK_CACHE_VERIFY is clean.
func TestGen_MethodsOrderRead(t *testing.T) {
	t.Parallel()
	node := "export struct Node {\n    export v: int\n}\n\nexport Node.Alpha(&self): int {\n    return self.v\n}\n\nexport Node.Beta(&self): int {\n    return self.v + 1\n}\n"
	swapped := "export struct Node {\n    export v: int\n}\n\nexport Node.Beta(&self): int {\n    return self.v + 1\n}\n\nexport Node.Alpha(&self): int {\n    return self.v\n}\n"
	dir := writeTree(t, map[string]string{
		"pkg.mk": manifest(nil),
		"g/g.mk": "export Class(g: mut Gen, t: Type) {\n    a := g.Arena()\n    var s := \"\"\n    for m in t.Methods() {\n        n := m.Name()\n        s = a.Fmt(\"{s}{n};\", (s, n)).Must()\n    }\n    g.Emit(quote {\n        export const Members: str = $(g.Str(s))\n    })\n}\n",
		"t/t.mk": node,
		"a/a.mk": "import g\nimport t\n\ngen g.Class(t.Node)\n",
	})
	view := filepath.Join(dir, "a", "gen", "x86_64-windows", "gen.mk")
	members := func() string {
		b, err := os.ReadFile(view)
		if err != nil {
			t.Fatal(err)
		}
		for l := range strings.Lines(string(b)) {
			if strings.Contains(l, "Members") {
				return strings.TrimSpace(l)
			}
		}
		t.Fatalf("no Members in the view:\n%s", b)
		return ""
	}
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, GenView: true, BuildID: source.HashString("test", "mk")}
	clean(t, frontCheck(t, cfg))
	if err := os.WriteFile(filepath.Join(dir, "t", "t.mk"), []byte(swapped), 0o644); err != nil {
		t.Fatal(err)
	}
	clean(t, frontCheck(t, cfg))
	warm := members()
	v := cfg
	v.CacheVerify = true
	clean(t, frontCheck(t, v))
	fresh := cfg
	fresh.CacheDir = t.TempDir()
	clean(t, frontCheck(t, fresh))
	if cold := members(); warm != cold || !strings.Contains(cold, `"Beta;Alpha;"`) {
		t.Errorf("warm view %s, cold %s", warm, cold)
	}
}

// TestGen_DataFileFailReason: a failed data file read hashes its reason
// and path, so each change of answer at the path reruns the reader, and the
// warm E0910's reason (or its absence) equals a cold check's.
func TestGen_DataFileFailReason(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"pkg.mk":       manifest(nil),
		"data/keep.md": "",
		"a/a.mk":       "import std.build\n\nexport const D: []u8 = build.File(\"data/in.txt\")\n",
	})
	in := filepath.Join(dir, "data", "in.txt")
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk")}
	reason := func(r Result) string {
		if len(r.Report.Diags) == 0 {
			return ""
		}
		if got := codes(r); !slices.Equal(got, []string{"E0910"}) {
			t.Fatalf("codes %q, want [E0910]:\n%s", got, r.Report.Text(r.Sources))
		}
		return factText(r.Report.Diags[0].Diag, "reason")
	}
	if got := reason(frontCheck(t, cfg)); got != "missing" {
		t.Fatalf("first check: reason %q, want missing", got)
	}
	for _, c := range []struct {
		name   string
		change func() error
		want   string
	}{
		{"missing_to_directory", func() error { return os.Mkdir(in, 0o755) }, "not-regular"},
		{"directory_to_file", func() error {
			if err := os.Remove(in); err != nil {
				return err
			}
			return os.WriteFile(in, []byte("hi"), 0o644)
		}, ""},
		{"file_to_directory", func() error {
			if err := os.Remove(in); err != nil {
				return err
			}
			return os.Mkdir(in, 0o755)
		}, "not-regular"},
		{"directory_to_missing", func() error { return os.Remove(in) }, "missing"},
	} {
		if err := c.change(); err != nil {
			t.Fatal(err)
		}
		warm := reason(frontCheck(t, cfg))
		fresh := cfg
		fresh.CacheDir = t.TempDir()
		cold := reason(frontCheck(t, fresh))
		if warm != cold || cold != c.want {
			t.Errorf("%s: warm reason %q, cold %q, want %q", c.name, warm, cold, c.want)
		}
	}
}

// TestGen_HasFlowsListUseSameLine: the round renames the bool use and
// leaves a list use on the same line.
func TestGen_HasFlowsListUseSameLine(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"pkg.mk": manifest(nil),
		"g/g.mk": "export Check(g: mut Gen, t: Type) {\n    for m in t.Methods() {\n        if !m.Flows() && m.Flows().Len() == 0 {\n            _ = m.Name()\n        }\n    }\n}\n",
		"a/a.mk": "import g\n\ninterface I {\n    Get(&self): int\n}\n\ngen g.Check(I)\n",
	})
	s, err := NewSession(Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{})
	if err != nil || res.Stopped != "clean" {
		t.Fatalf("mk fix: %v, stopped %q:\n%s", err, res.Stopped, diagLines(res.Final))
	}
	out, err := os.ReadFile(filepath.Join(dir, "g", "g.mk"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "if !m.HasFlows() && m.Flows().Len() == 0 {") {
		t.Errorf("fixed text:\n%s", out)
	}
}

// TestGen_WalkMarkerStillHolds: the walk context of a module with a
// test block, a private root with a contract and a const holding a fn
// literal is the same on a warm build, so an unedited check walks nothing
// and a body edit of a root walks only its module (the marker's point).
func TestGen_WalkMarkerStillHolds(t *testing.T) {
	t.Parallel()
	lib := "Pos(x: int): int\n    requires x > 0\n{\n    return x\n}\n\nconst Twice: fn(int): int = fn(x: int): int {\n    return x * 2\n}\n\n" +
		"export F(x: int): int {\n    return Pos(x) + 1\n}\n\nexport H[T: Plain](x: T): int {\n    return Twice(Pos(1))\n}\n\ntest \"pos\" {\n    _ = Pos(1)\n}\n"
	dir := writeTree(t, map[string]string{
		"pkg.mk":     manifest(nil),
		"lib/lib.mk": lib,
		"app/app.mk": "import lib\n\nexport Use(x: int): int {\n    return lib.F(x) + lib.H(x)\n}\n",
	})
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk")}
	clean(t, frontCheck(t, cfg))
	if got := pkgOnly(frontCheck(t, cfg).Trace.ModulesWalked); len(got) != 0 {
		t.Errorf("unedited check walked %q, want none", got)
	}
	genfixEdit(t, dir, "lib/lib.mk", "    return Pos(x) + 1\n", "    return Pos(x) + 2\n")
	warm := frontCheck(t, cfg)
	clean(t, warm)
	if got := pkgOnly(warm.Trace.ModulesWalked); !slices.Equal(got, []string{"lib"}) {
		t.Errorf("root body edit walked %q, want [lib]", got)
	}
}

// TestGen_MethodsOrderOwnModule: the gen line's own module swaps two
// methods of its own struct: its tokens are its prefix, so no ReadFile is
// needed; the warm view equals the cold one.
func TestGen_MethodsOrderOwnModule(t *testing.T) {
	t.Parallel()
	a := "import g\n\nexport struct Node {\n    export v: int\n}\n\nexport Node.Alpha(&self): int {\n    return self.v\n}\n\nexport Node.Beta(&self): int {\n    return self.v + 1\n}\n\ngen g.Class(Node)\n"
	dir := writeTree(t, map[string]string{
		"pkg.mk": manifest(nil),
		"g/g.mk": "export Class(g: mut Gen, t: Type) {\n    a := g.Arena()\n    var s := \"\"\n    for m in t.Methods() {\n        n := m.Name()\n        s = a.Fmt(\"{s}{n};\", (s, n)).Must()\n    }\n    g.Emit(quote {\n        export const Members: str = $(g.Str(s))\n    })\n}\n",
		"a/a.mk": a,
	})
	view := filepath.Join(dir, "a", "gen", "x86_64-windows", "gen.mk")
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, GenView: true, BuildID: source.HashString("test", "mk")}
	clean(t, frontCheck(t, cfg))
	genfixEdit(t, dir, "a/a.mk", "export Node.Alpha(&self): int {\n    return self.v\n}\n\nexport Node.Beta(&self): int {\n    return self.v + 1\n}\n",
		"export Node.Beta(&self): int {\n    return self.v + 1\n}\n\nexport Node.Alpha(&self): int {\n    return self.v\n}\n")
	clean(t, frontCheck(t, cfg))
	b, err := os.ReadFile(view)
	if err != nil || !strings.Contains(string(b), `"Beta;Alpha;"`) {
		t.Errorf("warm view %q (%v), want Beta;Alpha;", b, err)
	}
}

// TestGen_MethodsOrderTwoFiles: methods declared in two files of the
// type's module come in file path order (check's rule, in stage4_funcs.go's
// Funcs), then position: a.mk's before b.mk's whatever the struct's file.
func TestGen_MethodsOrderTwoFiles(t *testing.T) {
	t.Parallel()
	_, _, view := gen10Check(t, map[string]string{
		"g/g.mk": "export Class(g: mut Gen, t: Type) {\n    a := g.Arena()\n    var s := \"\"\n    for m in t.Methods() {\n        n := m.Name()\n        s = a.Fmt(\"{s}{n};\", (s, n)).Must()\n    }\n    g.Emit(quote {\n        export const Members: str = $(g.Str(s))\n    })\n}\n",
		"t/b.mk": "export struct Node {\n    export v: int\n}\n\nexport Node.Alpha(&self): int {\n    return self.v\n}\n",
		"t/a.mk": "export Node.Zeta(&self): int {\n    return self.v + 1\n}\n\nexport Node.Beta(&self): int {\n    return self.v + 2\n}\n",
		"a/a.mk": "import g\nimport t\n\ngen g.Class(t.Node)\n",
	}, types.Dev)
	if !strings.Contains(view, `"Zeta;Beta;Alpha;"`) {
		t.Errorf("view %q, want Zeta;Beta;Alpha;", view)
	}
}
