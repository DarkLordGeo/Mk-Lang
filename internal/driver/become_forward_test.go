// Why: `become` tail calls, packed by-value arguments, the become rename
// and the import alias suggestion each had a way to miscompile or mislead;
// these tests pin the fixed behaviour through the driver.
// What: TestBecome_ReadonlyForward, TestBecomeForwardInAnotherPack,
// TestBecome_PackedArgumentPerTarget, TestFixBecome_Holes and the
// TestE0206_ImportAlias* tests.
// How: a tail call that forwards a memory-passed parameter into a callee
// that writes it (release) and its pack key; an 8-byte packed struct that
// System V passes in memory; mk fix's rename followed into format and asm
// holes, each round listed under its trigger code; and E0206's alias
// suggestion for two imports of one last segment.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// becomeVarCallee: G passes its Big on to H, and GT its Pair to HP through
// a fn value; H and HP write their var parameter. K and KT keep their own
// value across the call. Pair is 16 bytes: Win64 passes it in memory,
// System V here in two registers, so a fn value may pass it on (a Big
// through a fn value is E0313, TestBecome_ByvalThroughFnValue).
const becomeVarCallee = `struct Big {
    a: int
    b: int
    c: int
}

struct Pair {
    a: int
    c: int
}

H(var b: Big, n: int): int {
    b.a = 99
    b.c = 77
    return b.a + n
}

G(b: Big, n: int): int {
    become H(b, n)
}

HP(var b: Pair, n: int): int {
    b.a = 99
    b.c = 77
    return b.a + n
}

const Tab: [1]fn(Pair, int): int = [HP]

GT(b: Pair, n: int): int {
    become Tab[0](b, n)
}

K(b: Big, n: int): int {
    r := G(b, n)
    return r * 1000 + b.a * 10 + b.c
}

KT(b: Pair, n: int): int {
    r := GT(b, n)
    return r * 1000 + b.a * 10 + b.c
}

export Main(sys: &System): WriteError!void {
    x := Big(a: 1, b: 2, c: 3)
    k := K(x, 0)
    kt := KT(Pair(a: 1, c: 3), 0)
    try sys.Print("K {k} KT {kt} x.a {a}\n", (k: k, kt: kt, a: x.a))
}
`

// TestBecome_ReadonlyForward: G's Big travels Indirect on Win64 and is
// not var, so it would be marked readonly, and in release MemCpyOpt let K pass
// its own b uncopied; G's tail call forwarded that pointer to H, which
// wrote K's b (K 100067). A parameter a tail call passes on is not
// readonly. System V passes Big byval, and G and H are each split into a
// C ABI wrapper and a body the tail call reaches (codegen/tailbody.go):
// Linux must print 99013 too, in dev as well (a musttail call with a byval
// argument wrote over G's return address there).
func TestBecome_ReadonlyForward(t *testing.T) {
	tools := needTools(t)
	dir := writeTree(t, map[string]string{"pkg.mk": fixBecomeManifest, "src/main.mk": becomeVarCallee})
	const want = "K 99013 KT 99013 x.a 1\n"
	targets := []types.Target{HostTarget()}
	if linuxRun() && HostTarget().OS == types.OSWindows {
		targets = append(targets, types.X86_64Linux)
	}
	for _, target := range targets {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(target.Name+"/"+prof.String(), func(t *testing.T) {
				s, err := NewSession(Config{Root: dir, Target: target, Profile: prof, Tools: tools, CacheDir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				r, paths, err := s.Build(filepath.Join(t.TempDir(), "app"+exeSuffix(target)))
				if err != nil || len(r.Report.Diags) > 0 || len(paths) != 1 {
					t.Fatalf("build: %v\n%s", err, r.Report.Text(r.Sources))
				}
				out, errOut, code := runExe(t, target, paths[0], "")
				if code != 0 || out != want {
					t.Fatalf("exit %d, stdout %q, stderr %q; want %q", code, out, errOut, want)
				}
			})
		}
	}
	if !linuxRun() && HostTarget().OS == types.OSWindows {
		t.Logf("x86_64-linux: not run (%s)", linuxOff)
	}
}

// TestBecomeForwardInAnotherPack (the forwarding fix's pack key): lib.G turns its
// plain call of H into a tail call that forwards its Big; app's source is
// unchanged, but app's call of G loses the readonly mark, so app's pack
// key must move with lib's body. Without the tail-forward bits in
// PackFacts app's pack hits its stale memo, which the MK_CACHE_VERIFY
// build reports. Dev: the mark is emitted in every profile.
func TestBecomeForwardInAnotherPack(t *testing.T) {
	tools := needTools(t)
	// call names lib.G in a direct call; value only as a fn value, so app's
	// pack holds G's declaration but no call of it.
	for _, use := range []struct{ name, k string }{
		{"call", "    r := lib.G(b, 0)\n"},
		{"value", "    f: fn(lib.Big, int): int = lib.G\n    r := f(b, 0)\n"},
	} {
		base := pkFiles()
		base["lib/lib.mk"] += "\nexport struct Big {\n    export a: int\n    export b: int\n    export c: int\n}\n\n" +
			"H(var b: Big, n: int): int {\n    b.a = 99\n    return b.a + n\n}\n\n" +
			"export G(b: Big, n: int): int {\n    return H(b, n)\n}\n"
		app := base["app/app.mk"]
		app = strings.Replace(app, "import shape\n", "import shape\n\nK(b: lib.Big): int {\n"+use.k+"    return r + b.a - 100\n}\n", 1)
		app = strings.Replace(app, "lib.Counter - 26", "lib.Counter + K(lib.Big(a: 1, b: 2, c: 3)) - 26", 1)
		base["app/app.mk"] = app
		dir := writeTree(t, base)
		out := t.TempDir()
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(use.name+"/"+prof.String(), func(t *testing.T) {
				pkWrite(t, dir, base)
				cfg := Config{Root: dir, Target: pkTarget(), Profile: prof, Tools: tools, CacheDir: filepath.Join(t.TempDir(), "cache")}
				pkBuild(t, cfg, filepath.Join(out, prof.String()+"-cold.exe"))
				e := pkEdit{name: "tail-forward", file: "lib/lib.mk", old: "    return H(b, n)\n", new: "    become H(b, n)\n"}
				pkWrite(t, dir, e.apply(t, base))
				ed := pkBuild(t, cfg, filepath.Join(out, prof.String()+"-edit.exe"))
				if got := strings.Join(ed.emittedMods(), " "); use.name == "call" && (!strings.Contains(got, "app") || !strings.Contains(got, "lib")) {
					t.Errorf("emitted %v, want lib (G's body) and app (its call of G lost readonly)", got)
				}
				pkVerify(t, cfg, filepath.Join(out, prof.String()+"-verify.exe"), ed.exe)
			})
		}
	}
}

// TestBecome_PackedArgumentPerTarget: the packed P is 8 bytes with
// a u32 at offset 1, and a struct holding it at an odd offset is too:
// Win64 passes them in a register, System V in memory. The rule asks every
// built target's classifier, so a new value of either is E0313 whatever
// the target, where x86_64-linux's codegen raised an internal error. An
// aligned 8-byte struct stays accepted.
func TestBecome_PackedArgumentPerTarget(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"packed", `packed struct P {
    a: u8
    b: u32
    c: u16
    d: u8
}

F(p: P, n: int): int {
    if n == 0 {
        return Cast[int](p.b)
    }
    q := P(a: p.a, b: p.b + 1, c: p.c, d: p.d)
    become F(q, n - 1)
}
`, "E0313"},
		{"nested", `packed struct In {
    a: u8
    b: u32
}

struct Out {
    x: u8
    i: In
    y: u16
}

F(p: Out, n: int): int {
    if n == 0 {
        return Cast[int](p.i.b)
    }
    become F(Out(x: 1, i: In(a: 2, b: 3), y: 4), n - 1)
}
`, "E0313"},
		{"aligned", `struct P {
    a: u32
    b: u16
    c: u16
}

F(p: P, n: int): int {
    if n == 0 {
        return Cast[int](p.a)
    }
    become F(P(a: p.a + 1, b: 0, c: 0), n - 1)
}
`, ""},
	}
	for _, c := range cases {
		for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
			t.Run(c.name+"/"+target.Name, func(t *testing.T) {
				t.Parallel()
				main := c.src + "\nexport Main(sys: &System): WriteError!void {\n    try sys.Print(\"x\\n\")\n}\n"
				dir := writeTree(t, map[string]string{"pkg.mk": fixBecomeManifest, "src/main.mk": main})
				s, err := NewSession(Config{Root: dir, Target: target, CacheDir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				r := s.Check()
				var got []string
				for _, e := range r.Report.Diags {
					got = append(got, e.Diag.Code.String())
				}
				if strings.Join(got, " ") != c.want {
					t.Fatalf("diagnostics %v, want %q\n%s", got, c.want, r.Report.Text(r.Sources))
				}
			})
		}
	}
}

// TestFixBecome_Holes: a program written before become was a keyword,
// which printed "a 44 / hole 9 / short 9", and one with asm. After one mk
// fix both check clean and print what they printed: the rename's names, then the
// holes `{become}` of a format string (and of the `(become)` shorthand's
// record), a path segment `{r.become}`, and of an asm template that named
// them. `{{become}}` stays text.
// The rounds are listed under their trigger codes, E0102, E0706 and E1104,
// not E0000; a second mk fix changes nothing.
func TestFixBecome_Holes(t *testing.T) {
	tools := needTools(t)
	util := "export struct Rec {\n    export become: int\n    export other: int\n}\n\nexport Make(become: int): Rec {\n    return Rec(become: become, other: 1)\n}\n"
	kfix := `import src.util
import become = src.util

struct S {
    become: int
}

Step(become: int): int {
    return become + 1
}

Fmt(sys: &System, become: int): WriteError!void {
    try sys.Print("hole {become} {{become}}\n", (become: become))
    try sys.Print("short {become}\n", (become))
    try sys.Print("path {r.become}\n", (r: util.Make(5)))
}

export Main(sys: &System): WriteError!void {
    s := S(become: 4)
    r := util.Make(7)
    q := become.Make(8)
    a := Step(s.become) + r.become + q.become
    try sys.Print("a {a}\n", (a: a))
    try Fmt(sys, 9)
}
`
	kasm := `Sp(): u64 {
    var become: u64 = 0
    unsafe "reads the stack pointer" {
        asm("mov {become}, rsp", out(reg) become, options(nomem, nostack, preserves_flags))
    }
    return become
}

export Main(sys: &System): WriteError!void {
    ok := Sp() != 0
    try sys.Print("{ok}\n", (ok: ok))
}
`
	asmManifest := strings.Replace(fixBecomeManifest, "unsafe_modules: []", `unsafe_modules: ["src"]`, 1)
	for _, c := range []struct {
		name  string
		files map[string]string
		want  string
		codes []diag.Code
	}{
		{"format", map[string]string{"pkg.mk": fixBecomeManifest, "src/main.mk": kfix, "src/util/util.mk": util},
			"a 20\nhole 9 {become}\nshort 9\npath 5\n", []diag.Code{diag.E0102, diag.E0706}},
		{"asm", map[string]string{"pkg.mk": asmManifest, "src/main.mk": kasm}, "true\n", []diag.Code{diag.E0102, diag.E1104}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := writeTree(t, c.files)
			cache := t.TempDir()
			session := func() *Session {
				s, err := NewSession(Config{Root: dir, CacheDir: cache, Tools: tools, Target: HostTarget()})
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			res, err := session().Fix(FixOptions{})
			if err != nil || res.Stopped != "clean" {
				t.Fatalf("mk fix: %v %q %+v\n%s", err, res.Stopped, res.Rounds, diagLines(res.Final))
			}
			seen := map[diag.Code]bool{}
			for _, r := range res.Rounds {
				for _, a := range r.Applied {
					if a.Code == diag.E0000 {
						t.Errorf("a round listed under E0000: %+v", a)
					}
					seen[a.Code] = true
				}
			}
			for _, code := range c.codes {
				if !seen[code] {
					t.Errorf("no round under %v: %+v", code, res.Rounds)
				}
			}
			main, _ := os.ReadFile(filepath.Join(dir, "src", "main.mk"))
			if !strings.Contains(string(main), "{{become}}") && c.name == "format" {
				t.Errorf("the escaped `{{become}}` was rewritten:\n%s", main)
			}
			r, paths, err := session().Build(filepath.Join(t.TempDir(), "app"+exeSuffix(HostTarget())))
			if err != nil || len(r.Report.Diags) > 0 || len(paths) != 1 {
				t.Fatalf("build after mk fix: %v\n%s\n%s", err, r.Report.Text(r.Sources), main)
			}
			out, errOut, code := runExe(t, HostTarget(), paths[0], "")
			if code != 0 || out != c.want {
				t.Errorf("exit %d, stdout %q, stderr %q; want %q", code, out, errOut, c.want)
			}
			if res, err := session().Fix(FixOptions{}); err != nil || len(res.Rounds) != 0 {
				t.Errorf("second mk fix: %v %+v", err, res.Rounds)
			}
		})
	}
}

// TestE0206_ImportAliasSuggestion: two imports binding util are E0206, which
// offers `import <name> = <path>` for each as alternatives; applying either
// one, with one of the file's two uses naming the new alias, checks clean.
func TestE0206_ImportAliasSuggestion(t *testing.T) {
	files := map[string]string{
		"pkg.mk":          fixBecomeManifest,
		"src/a/util/u.mk": "export One(): int {\n    return 1\n}\n",
		"src/b/util/u.mk": "export One(): int {\n    return 2\n}\n",
		"src/main.mk":     "import src.a.util\nimport src.b.util\n\nexport Main(sys: &System): WriteError!void {\n    try sys.Print(\"{x}\\n\", (x: util.One() + util.One()))\n}\n",
	}
	dir := writeTree(t, files)
	check := func() Result {
		s, err := NewSession(Config{Root: dir, CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		return s.Check()
	}
	r := check()
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0206 {
		t.Fatalf("want one E0206:\n%s", r.Report.Text(r.Sources))
	}
	alts := r.Report.Diags[0].Diag.Alternatives
	want := []string{"import a_util = src.a.util", "import b_util = src.b.util"}
	if strings.Join(alts, "|") != strings.Join(want, "|") {
		t.Fatalf("alternatives %q, want %q", alts, want)
	}
	for i, alt := range want {
		line := []string{"import src.a.util", "import src.b.util"}[i]
		// The alias applied, and one of the file's two uses (one per
		// module, as the two imports meant) naming it.
		text := strings.Replace(files["src/main.mk"], line+"\n", alt+"\n", 1)
		text = strings.Replace(text, "util.One()", strings.Fields(alt)[1]+".One()", 1)
		if err := os.WriteFile(filepath.Join(dir, "src", "main.mk"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if r := check(); len(r.Report.Diags) != 0 {
			t.Errorf("after %q:\n%s", alt, r.Report.Text(r.Sources))
		}
	}
}

// TestE0206_ImportAliasAvoidsEveryBinding: an offered alias is free of every
// import of the file, a later one included, and of the aliases offered
// before it. With `import src.a_util` after the two util imports, `a_util`
// is taken, so src.a.util is offered another name; with three util imports,
// the two E0206 offer no name twice.
func TestE0206_ImportAliasAvoidsEveryBinding(t *testing.T) {
	alts := func(t *testing.T, main string, extra map[string]string) []string {
		t.Helper()
		files := map[string]string{
			"pkg.mk":          fixBecomeManifest,
			"src/a/util/u.mk": "export One(): int {\n    return 1\n}\n",
			"src/b/util/u.mk": "export One(): int {\n    return 2\n}\n",
			"src/main.mk":     main,
		}
		for k, v := range extra {
			files[k] = v
		}
		s, err := NewSession(Config{Root: writeTree(t, files), CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		r := s.Check()
		var out []string
		for _, e := range r.Report.Diags {
			if e.Diag.Code == diag.E0206 {
				out = append(out, e.Diag.Alternatives...)
			}
		}
		return out
	}
	t.Run("later import", func(t *testing.T) {
		got := alts(t, "import src.a.util\nimport src.b.util\nimport src.a_util\n\nexport Main(sys: &System): WriteError!void {\n    try sys.Print(\"{x}\\n\", (x: util.One() + a_util.Two()))\n}\n",
			map[string]string{"src/a_util/u.mk": "export Two(): int {\n    return 2\n}\n"})
		for _, a := range got {
			if strings.HasPrefix(a, "import a_util =") {
				t.Fatalf("alternatives %q offer a_util, which `import src.a_util` binds", got)
			}
		}
		if len(got) != 2 {
			t.Fatalf("alternatives %q, want two", got)
		}
	})
	t.Run("three imports", func(t *testing.T) {
		got := alts(t, "import src.a.util\nimport src.b.util\nimport src.c.util\n\nexport Main(sys: &System): WriteError!void {\n    try sys.Print(\"{x}\\n\", (x: util.One()))\n}\n",
			map[string]string{"src/c/util/u.mk": "export One(): int {\n    return 3\n}\n"})
		seen := map[string]bool{}
		for _, a := range got {
			name := strings.Fields(a)[1]
			if seen[name] {
				t.Fatalf("alternatives %q offer %s twice", got, name)
			}
			seen[name] = true
		}
		if len(got) != 4 {
			t.Fatalf("alternatives %q, want four", got)
		}
	})
}
