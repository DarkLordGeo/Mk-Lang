// Why: generators and compile-time data files read more than a module's
// own text; each read must be checked, cached and rerun exactly when its
// answer changes.
// What: the TestGen_* tests: a function as a gen argument, Type.Methods
// on a struct, Method.Flows, the float and fn splices, the splice table's
// E0301, build.File and Gen.Input with every E0910 reason a Windows host
// can make, a data file edit rerunning its reader, the build profile,
// build.File at run time (E0705), and the HasFlows mk fix round.
// How: checks small packages cold and warm through the driver and compares
// diagnostics, generated text and the reads the cache records.

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

// gen10Check checks files as a package on disk with the gen/ view on and
// returns the result, the root and the view's text.
func gen10Check(t *testing.T, files map[string]string, prof types.Profile) (Result, string, string) {
	t.Helper()
	if _, ok := files["pkg.mk"]; !ok {
		files["pkg.mk"] = manifest(nil)
	}
	dir := writeTree(t, files)
	s, err := NewSession(Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, Profile: prof, GenView: true,
		BuildID: source.HashString("test", "mk")})
	if err != nil {
		t.Fatal(err)
	}
	res := s.Check()
	view, _ := os.ReadFile(filepath.Join(dir, "a", "gen", "x86_64-windows", "gen.mk"))
	return res, dir, string(view)
}

// gen10Lib is a generator module: Bind takes a Method (a function argument)
// and emits its name, receiver, signature and flows; Class takes a Type and
// emits its inherent functions; Splice splices a float and a fn value.
const gen10Lib = `export Describe(m: Method, a: &Arena): str {
    n := m.Name()
    st := m.Receiver() == Receiver.Static
    h := m.HasFlows()
    var s := a.Fmt("{n} static={st} flows={h}", (n, st, h)).Must()
    for f in m.Flows() {
        k := f.Kind() == FlowKind.From
        p := f.Param()
        q := f.Path()
        x := f.Field()
        s = a.Fmt("{s} [from={k} {p} {q} {x}]", (s, k, p, q, x)).Must()
    }
    return s
}

export Bind(g: mut Gen, m: Method) {
    s := Describe(m, g.Arena())
    g.Emit(quote {
        export const Bound: str = $(g.Str(s))
    })
}

export Class(g: mut Gen, t: Type) {
    a := g.Arena()
    var s := ""
    for m in t.Methods() {
        d := Describe(m, a)
        s = a.Fmt("{s}{d};", (s, d)).Must()
    }
    g.Emit(quote {
        export const Members: str = $(g.Str(s))
    })
}

export Twice(x: int): int {
    return x * 2
}

export Splice(g: mut Gen) {
    f := 0.1
    h: f32 = 1.5
    n := -2.5e-8
    tw := Twice
    g.Emit(quote {
        export const F: f64 = $(f)
        export const H: f32 = $(h)
        export const N: f64 = $(n)
        export const T: fn(int): int = $(tw)
    })
}

export Input(g: mut Gen) {
    b := g.Input("data/in.txt")
    n := b.Len()
    g.Emit(quote {
        export const InputLen: int = $(n)
    })
}
`

// TestGen_FunctionArgMethodsAndFlows: gen lux.Bind(Add) gives the
// generator a Method with Receiver Static and the function's declared
// flows; gen lux.Class(Node) gives Node's inherent functions in
// declaration order, private ones included in Node's own module.
func TestGen_FunctionArgMethodsAndFlows(t *testing.T) {
	t.Parallel()
	res, _, view := gen10Check(t, map[string]string{
		"g/g.mk": gen10Lib,
		"a/a.mk": `import g

export struct Node {
    export v: int
    export buf: str
}

export Pick(a: &Node, b: &Node): &Node from a, b {
    return a
}

Node.Len(&self): int {
    return self.v
}

Node.New(v: int): Node {
    return Node(v: v, buf: "")
}

Node.Text(&self): str from self.buf {
    return self.buf
}

gen g.Bind(Pick)
gen g.Class(Node)
`,
	}, types.Dev)
	clean(t, res)
	for _, want := range []string{
		`export const Bound: str = "Pick static=true flows=true [from=true result a ] [from=true result b ]"`,
		`export const Members: str = "Len static=false flows=false;New static=true flows=false;Text static=false flows=true [from=true result self.buf ];"`,
	} {
		if !strings.Contains(view, want) {
			t.Errorf("gen view lacks\n%s\nview:\n%s", want, view)
		}
	}
}

// TestGen_FunctionArgRefused: a generic function, a method and a
// function passed where the generator takes a Type are E0301.
func TestGen_FunctionArgRefused(t *testing.T) {
	t.Parallel()
	res, _, _ := gen10Check(t, map[string]string{
		"g/g.mk": gen10Lib,
		"a/a.mk": `import g

struct Node {
    v: int
}

Node.Len(&self): int {
    return self.v
}

Id[T](x: T): T {
    return x
}

Add(a: int, b: int): int {
    return a + b
}

gen g.Bind(Id)
gen g.Bind(Node.Len)
gen g.Class(Add)
`,
	}, types.Dev)
	if got := codes(res); !slices.Equal(got, []string{"E0301", "E0301", "E0301"}) {
		t.Errorf("codes %q, want three E0301:\n%s", got, res.Report.Text(res.Sources))
	}
}

// TestGen_ImportedFunctionFlows: a function of another module reaches
// the generator through its checked signature, with the same flows the
// stage-4 view gives a function of the line's own module (one spelling,
// declared clauses only, per-field clauses with their field).
func TestGen_ImportedFunctionFlows(t *testing.T) {
	t.Parallel()
	src := `export struct Pair {
    export x: str
    export y: str
}

export Split(a: &str, b: &str): Pair from (x: a, y: b) {
    return Pair(x: *a, y: *b)
}

export Keep(p: mut []str, s: str into p) {
}
`
	want := `"Split static=true flows=true [from=true result a x] [from=true result b y]"`
	wantKeep := `"Keep static=true flows=true [from=false p s ]"`
	own, _, ownView := gen10Check(t, map[string]string{
		"g/g.mk": gen10Lib,
		"a/a.mk": "import g\n\n" + src + "\ngen g.Bind(Split)\n",
	}, types.Dev)
	clean(t, own)
	imp, _, impView := gen10Check(t, map[string]string{
		"g/g.mk": gen10Lib,
		"b/b.mk": src,
		"a/a.mk": "import g\nimport b\n\ngen g.Bind(b.Split)\n",
	}, types.Dev)
	clean(t, imp)
	for _, v := range []string{ownView, impView} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %s:\n%s", want, v)
		}
	}
	keep, _, keepView := gen10Check(t, map[string]string{
		"g/g.mk": gen10Lib,
		"b/b.mk": src,
		"a/a.mk": "import g\nimport b\n\ngen g.Bind(b.Keep)\n",
	}, types.Dev)
	clean(t, keep)
	if !strings.Contains(keepView, wantKeep) {
		t.Errorf("view lacks %s:\n%s", wantKeep, keepView)
	}
}

// TestGen_MethodFlowsBothRoutes: Type.Methods on a struct of the line's
// own module (the AST route) and of another module (the checked route)
// give the same functions, receivers and flows: a path through self, an
// into on a parameter, and no inferred source as a From.
func TestGen_MethodFlowsBothRoutes(t *testing.T) {
	t.Parallel()
	src := `export struct Node {
    export v: int
    export buf: str
}

export Node.Text(&self): str from self.buf {
    return self.buf
}

export Node.Plain(&self): &str {
    return &self.buf
}

export Node.Keep(mut self, list: mut []str, s: str into list) {
}
`
	want := `"Text static=false flows=true [from=true result self.buf ];Plain static=false flows=false;Keep static=false flows=true [from=false list s ];"`
	own, _, ownView := gen10Check(t, map[string]string{
		"g/g.mk": gen10Lib,
		"a/a.mk": "import g\n\n" + src + "\ngen g.Class(Node)\n",
	}, types.Dev)
	clean(t, own)
	imp, _, impView := gen10Check(t, map[string]string{
		"g/g.mk": gen10Lib,
		"b/b.mk": src,
		"a/a.mk": "import g\nimport b\n\ngen g.Class(b.Node)\n",
	}, types.Dev)
	clean(t, imp)
	for _, v := range []string{ownView, impView} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %s:\n%s", want, v)
		}
	}
}

// TestGen_SpliceFloatAndFn: a float splices as the shortest decimal that
// reads back to its bits (f64 and f32), a negative one with its minus, and
// a fn value as its function's qualified name; the output checks.
func TestGen_SpliceFloatAndFn(t *testing.T) {
	t.Parallel()
	res, _, view := gen10Check(t, map[string]string{
		"g/g.mk": gen10Lib,
		"a/a.mk": "import g\n\ngen g.Splice()\n",
	}, types.Dev)
	clean(t, res)
	for _, want := range []string{"export const F: f64 = 0.1", "export const H: f32 = 1.5", "export const N: f64 = -2.5e-08",
		"export const T: fn(int): int = g.Twice"} {
		if !strings.Contains(view, want) {
			t.Errorf("gen view lacks %q:\n%s", want, view)
		}
	}
}

// TestGen_SpliceOtherOperandE0301: a splice of an operand outside the
// table (an enum value, a slice) is E0301 at the splice, from check, so
// gen's host never meets one (host.go's internal error is unreachable).
func TestGen_SpliceOtherOperandE0301(t *testing.T) {
	t.Parallel()
	res, _, _ := gen10Check(t, map[string]string{
		"g/g.mk": `export Bad(g: mut Gen, t: Type) {
    k := t.Kind()
    xs: ?int = some(1)
    g.Emit(quote {
        export const K: int = $(k)
        export const X: int = $(xs)
    })
}
`,
		"a/a.mk": "import g\n\nstruct S {\n    v: int\n}\n\ngen g.Bad(S)\n",
	}, types.Dev)
	got := codes(res)
	if !slices.Equal(got, []string{"E0301", "E0301"}) {
		t.Fatalf("codes %q, want two E0301:\n%s", got, res.Report.Text(res.Sources))
	}
	for _, e := range res.Report.Diags {
		if !strings.Contains(e.Diag.Message, "a splice is") {
			t.Errorf("message %q", e.Diag.Message)
		}
	}
}

// TestGen_DataFiles: build.File in a const initializer and Gen.Input in a
// generator read under the package root of the reading module,
// not the module's directory.
func TestGen_DataFiles(t *testing.T) {
	t.Parallel()
	res, _, view := gen10Check(t, map[string]string{
		"g/g.mk":      gen10Lib,
		"data/in.txt": "hello",
		"a/a.mk": `import g
import std.build

const D: []u8 = build.File("data/in.txt")
const N: int = 10 / (D.Len() - 4)

gen g.Input()
`,
	}, types.Dev)
	clean(t, res)
	if !strings.Contains(view, "export const InputLen: int = 5") {
		t.Errorf("view:\n%s", view)
	}
}

// TestGen_DataFileReasons: each refusal is E0910 with its reason: path
// (`..`, absolute, a backslash), missing, not-regular (a directory),
// too-large (64 MiB and one byte); a link is linux.sh's (Windows needs a
// privilege to make one), and is skipped here when no link can be made.
func TestGen_DataFileReasons(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ path, reason string }{
		{"../x.txt", "path"}, {"/x.txt", "path"}, {`data\\in.txt`, "path"}, {"data/./in.txt", "path"}, {"", "path"},
		{"data/none.txt", "missing"}, {"data", "not-regular"}, {"big.bin", "too-large"}, {"link.txt", "link"}, {"linkdir/in.txt", "link"},
	} {
		t.Run(c.reason+"_"+strings.NewReplacer("/", "_", "\\", "_", ".", "_").Replace(c.path), func(t *testing.T) {
			files := map[string]string{
				"pkg.mk":      manifest(nil),
				"data/in.txt": "hello",
				"a/a.mk":      "import std.build\n\nconst D: []u8 = build.File(\"" + c.path + "\")\n",
			}
			dir := writeTree(t, files)
			switch c.reason {
			case "too-large":
				f, err := os.Create(filepath.Join(dir, "big.bin"))
				if err != nil {
					t.Fatal(err)
				}
				if err := f.Truncate(types.DataFileLimit + 1); err != nil {
					t.Fatal(err)
				}
				f.Close()
			case "link":
				if err := os.Symlink(filepath.Join(dir, "data", "in.txt"), filepath.Join(dir, "link.txt")); err != nil {
					t.Skipf("no symlink on this host (%v): linux.sh runs it", err)
				}
				if err := os.Symlink(filepath.Join(dir, "data"), filepath.Join(dir, "linkdir")); err != nil {
					t.Skipf("no directory symlink on this host (%v): linux.sh runs it", err)
				}
			}
			s, err := NewSession(Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows})
			if err != nil {
				t.Fatal(err)
			}
			res := s.Check()
			if got := codes(res); !slices.Equal(got, []string{"E0910"}) {
				t.Fatalf("codes %q, want [E0910]:\n%s", got, res.Report.Text(res.Sources))
			}
			d := res.Report.Diags[0].Diag
			if f := factText(d, "reason"); f != c.reason {
				t.Errorf("reason %q, want %q", f, c.reason)
			}
			if f := factText(d, "path"); f != strings.ReplaceAll(c.path, `\\`, `\`) {
				t.Errorf("path %q, want %q", f, c.path)
			}
		})
	}
}

// TestGen_DataFileEditReruns: an edit of a data file reruns exactly the
// module that read it on a warm check, and its new bytes reach the const
// (here, a length of 4 divides by zero: E0901); a module that did not read
// it hits. A file that was missing and appears reruns its reader too.
func TestGen_DataFileEditReruns(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"pkg.mk":       manifest(nil),
		"data/in.txt":  "hello",
		"a/a.mk":       "import std.build\n\nconst D: []u8 = build.File(\"data/in.txt\")\nexport const N: int = 10 / (D.Len() - 4)\n",
		"b/b.mk":       "export const M: int = 1\n",
		"e/e.mk":       "import std.build\n\nconst E: []u8 = build.File(\"data/later.txt\")\n",
		"d/d.mk":       "import a\n\nexport const P: int = a.N\n",
		"data/x/y.txt": "",
	}
	dir := writeTree(t, files)
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk")}
	cold := frontCheck(t, cfg)
	if got := codes(cold); !slices.Equal(got, []string{"E0910"}) {
		t.Fatalf("cold codes %q, want e's E0910:\n%s", got, cold.Report.Text(cold.Sources))
	}
	same := frontCheck(t, cfg)
	if got := pkgOnly(same.Trace.ModulesResolved); len(got) != 0 {
		t.Errorf("unedited warm check resolved %q, want none", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "in.txt"), []byte("four"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "later.txt"), []byte("now"), 0o644); err != nil {
		t.Fatal(err)
	}
	warm := frontCheck(t, cfg)
	if got := pkgOnly(warm.Trace.ModulesResolved); !slices.Contains(got, "a") || !slices.Contains(got, "e") || slices.Contains(got, "b") {
		t.Errorf("after the edits resolved %q, want a and e, not b", got)
	}
	if got := codes(warm); !slices.Equal(got, []string{"E0901"}) {
		t.Errorf("warm codes %q, want a's E0901 (the new length):\n%s", got, warm.Report.Text(warm.Sources))
	}
	fresh := cfg
	fresh.CacheDir = t.TempDir()
	if w, c := jsonOf(t, warm), jsonOf(t, frontCheck(t, fresh)); !bytes.Equal(w, c) {
		t.Errorf("warm --json %s, cold %s", w, c)
	}
}

// TestGen_Profile: build.Profile is the driver's profile, Dev, Release,
// Test or Checked, as ProfileKind's variant of that name.
func TestGen_Profile(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		p    types.Profile
		want string
	}{{types.Dev, "Dev"}, {types.Release, "Release"}, {types.TestProfile, "Test"}, {types.Checked, "Checked"}} {
		src := "import std.build\nimport h\n\nconst K: int = 1 / h.Zero(build.Profile == build.ProfileKind." + c.want + ")\n"
		res, _, _ := gen10Check(t, map[string]string{"a/a.mk": src,
			"h/h.mk": "export Zero(b: bool): int {\n    if b {\n        return 0\n    }\n    return 1\n}\n"}, c.p)
		if got := codes(res); !slices.Equal(got, []string{"E0901"}) {
			t.Errorf("%v: codes %q, want E0901 (Profile is %s):\n%s", c.p, got, c.want, res.Report.Text(res.Sources))
		}
	}
}

// TestGen_BuildFileAtRunTime: build.File outside a const initializer or a
// generator is E0705, as a call and as a fn value; never an internal error
// or a link failure.
func TestGen_BuildFileAtRunTime(t *testing.T) {
	t.Parallel()
	res, _, _ := gen10Check(t, map[string]string{
		"data/in.txt": "hello",
		"a/a.mk": `import std.build

export Read(): int {
    return build.File("data/in.txt").Len()
}

export Value(): fn(str): []u8 {
    return build.File
}
`,
	}, types.Dev)
	if got := codes(res); !slices.Equal(got, []string{"E0705", "E0705"}) {
		t.Errorf("codes %q, want two E0705:\n%s", got, res.Report.Text(res.Sources))
	}
}

// TestGen_HasFlowsFix: mk fix renames a Flows() call used as a bool to
// HasFlows() and leaves the list form alone.
func TestGen_HasFlowsFix(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"pkg.mk": manifest(nil),
		"g/g.mk": `export Check(g: mut Gen, t: Type) {
    for m in t.Methods() {
        if m.Flows() {
            g.Fail("flows")
        }
        if m.Generic() || m.Flows() {
            g.Fail("generic or flows")
        }
        if !m.Flows() {
            _ = m.Name()
        }
        b := m.Flows()
        if b {
            g.Fail("bound")
        }
        for f in m.Flows() {
            _ = f.Param()
        }
    }
}
`,
		"a/a.mk": "import g\n\ninterface I {\n    Get(&self): int\n}\n\ngen g.Check(I)\n",
	}
	dir := writeTree(t, files)
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
	if !strings.Contains(string(out), "if m.HasFlows() {") || !strings.Contains(string(out), "for f in m.Flows() {") ||
		!strings.Contains(string(out), "m.Generic() || m.HasFlows()") || !strings.Contains(string(out), "if !m.HasFlows()") ||
		!strings.Contains(string(out), "b := m.HasFlows()") {
		t.Errorf("fixed text:\n%s", out)
	}
}

// TestGen_FrameLimitFollowsFuel: an evaluation's frames may hold a quarter
// of its stated fuel in bytes when that is more than 1 MiB: a recursion
// 50,000 deep hits the stack limit under the default fuel and evaluates
// under a stated fuel of 40 million.
func TestGen_FrameLimitFollowsFuel(t *testing.T) {
	t.Parallel()
	h := "export Down(n: int): int {\n    if n == 0 {\n        return 0\n    }\n    return Down(n - 1) + 1\n}\n"
	deep, _, _ := gen10Check(t, map[string]string{"h/h.mk": h, "a/a.mk": "import h\n\nconst A: int = h.Down(50_000)\n"}, types.Dev)
	if got := codes(deep); !slices.Equal(got, []string{"E0901"}) || !strings.Contains(deep.Report.Text(deep.Sources), "stack limit") {
		t.Errorf("default fuel: codes %q, want the stack limit's E0901:\n%s", got, deep.Report.Text(deep.Sources))
	}
	fueled, _, _ := gen10Check(t, map[string]string{"h/h.mk": h, "a/a.mk": "import h\n\nconst A: int = h.Down(50_000) fuel 40_000_000\n"}, types.Dev)
	clean(t, fueled)
}

// TestGen_FunctionArgReadsSignatureOnly: a gen line that names a
// function records a read of its signature and none of its body (a public
// fact never depends on a body), so an edit of the body cannot rerun the
// line and an edit of the signature does. This pins the read set the cache
// compares; TestGen_SignatureReadReruns is the end-to-end rerun test.
func TestGen_FunctionArgReadsSignatureOnly(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"pkg.mk": manifest(nil),
		"g/g.mk": gen10Lib,
		"b/b.mk": "export Add(a: int, b: int): int {\n    return a + b\n}\n",
		"a/a.mk": "import g\nimport b\n\ngen g.Bind(b.Add)\n",
		"d/d.mk": "import g\n\nLocal(x: int): int {\n    return x\n}\n\ngen g.Bind(Local)\n",
	})
	s, err := NewSession(Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows})
	if err != nil {
		t.Fatal(err)
	}
	clean(t, s.Check())
	var lines int
	for _, m := range s.pkgB.mods {
		want := map[string]string{"a": "b.Add", "d": "d.Local"}[m.path]
		if want == "" || m.gen == nil {
			continue
		}
		for i, lr := range m.gen.out.Lines {
			lines++
			sig := false
			for _, r := range lr.Reads.Sorted() {
				if r.Key.String() == want && r.Kind == types.ReadSignature {
					sig = true
				}
				if r.Key.String() == want && r.Kind == types.ReadBody {
					t.Errorf("line %d reads the body of %s", i, want)
				}
			}
			if !sig {
				t.Errorf("line %d records no signature read of %s: %v", i, want, lr.Reads.Sorted())
			}
		}
	}
	if lines != 2 {
		t.Fatalf("%d gen lines ran in a and d, want 2", lines)
	}
}

// TestGen_GenInputFailure: a failed Gen.Input read is E0910 at the gen
// line with its path and reason (the host call keeps them).
func TestGen_GenInputFailure(t *testing.T) {
	t.Parallel()
	res, _, _ := gen10Check(t, map[string]string{
		"g/g.mk": gen10Lib,
		"a/a.mk": "import g\n\ngen g.Input()\n",
	}, types.Dev)
	if got := codes(res); !slices.Equal(got, []string{"E0910"}) {
		t.Fatalf("codes %q, want [E0910]:\n%s", got, res.Report.Text(res.Sources))
	}
	d := res.Report.Diags[0].Diag
	if factText(d, "reason") != "missing" || factText(d, "path") != "data/in.txt" || res.Sources.Text(d.At) != "gen g.Input()" {
		t.Errorf("E0910 at %q, facts %v", res.Sources.Text(d.At), d.Facts)
	}
}

// TestGen_GenericSplice: a generic helper that splices its type parameter
// checks; an instance whose type is in the splice table splices, and one
// outside it is E0301 at the splice, never an internal error.
func TestGen_GenericSplice(t *testing.T) {
	t.Parallel()
	lib := `Lit[T](x: T): Code {
    return quote { $(x) }
}

export Good(g: mut Gen) {
    n := Lit(7)
    b := Lit(true)
    g.Emit(quote {
        export const N: int = $(n)
        export const B: bool = $(b)
    })
}

export Bad(g: mut Gen, t: Type) {
    k := Lit(t.Kind())
    g.Emit(quote {
        export const K: int = $(k)
    })
}
`
	res, _, view := gen10Check(t, map[string]string{"g/g.mk": lib, "a/a.mk": "import g\n\ngen g.Good()\n"}, types.Dev)
	clean(t, res)
	if !strings.Contains(view, "export const N: int = 7") || !strings.Contains(view, "export const B: bool = true") {
		t.Errorf("view:\n%s", view)
	}
	bad, _, _ := gen10Check(t, map[string]string{"g/g.mk": lib, "a/a.mk": "import g\n\nstruct S {\n    v: int\n}\n\ngen g.Bad(S)\n"}, types.Dev)
	if got := codes(bad); !slices.Equal(got, []string{"E0301"}) {
		t.Fatalf("codes %q, want [E0301]:\n%s", got, bad.Report.Text(bad.Sources))
	}
	if d := bad.Report.Diags[0].Diag; !strings.Contains(d.Message, "TypeKind") {
		t.Errorf("message %q", d.Message)
	}
}

// TestGen_CachedE0910Hits: a module whose const reads a missing data file
// is stored with its E0910 and hits on an unedited warm check, the
// diagnostic the same as cold.
func TestGen_CachedE0910Hits(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"pkg.mk": manifest(nil),
		"a/a.mk": "import std.build\n\nconst D: []u8 = build.File(\"data/none.txt\")\n",
	})
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk")}
	cold := frontCheck(t, cfg)
	if got := codes(cold); !slices.Equal(got, []string{"E0910"}) {
		t.Fatalf("cold codes %q", got)
	}
	warm := frontCheck(t, cfg)
	if got := pkgOnly(warm.Trace.ModulesResolved); len(got) != 0 {
		t.Errorf("warm check resolved %q, want a from the cache (not stored: %q)", got, warm.Trace.NotStored)
	}
	if w, c := jsonOf(t, warm), jsonOf(t, cold); !bytes.Equal(w, c) {
		t.Errorf("warm --json %s, cold %s", w, c)
	}
}

// TestGen_SignatureReadReruns: a generator that read a function's
// signature does not rerun when the function's body changes, and reruns when
// its signature does. Each warm check equals a cold one.
func TestGen_SignatureReadReruns(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"pkg.mk": manifest(nil),
		"g/g.mk": gen10Lib,
		"b/b.mk": "export Add(a: int, b: int): int {\n    return a + b\n}\n",
		"a/a.mk": "import g\nimport b\n\ngen g.Bind(b.Add)\n",
	}
	dir := writeTree(t, files)
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk")}
	clean(t, frontCheck(t, cfg))
	genKey := types.GenKey("a", 0).String()
	for _, c := range []struct {
		name, text string
		rerun      bool
	}{
		{"body", "export Add(a: int, b: int): int {\n    return b + a\n}\n", false},
		{"signature", "export Add(a: int, c: int): int {\n    return a + c\n}\n", true},
	} {
		if err := os.WriteFile(filepath.Join(dir, "b", "b.mk"), []byte(c.text), 0o644); err != nil {
			t.Fatal(err)
		}
		warm := frontCheck(t, cfg)
		clean(t, warm)
		ran := slices.Contains(pkgItems(warm.Trace.GeneratorRuns), genKey)
		missed := slices.Contains(pkgOnly(warm.Trace.ModulesResolved), "a")
		if ran != c.rerun || missed != c.rerun {
			t.Errorf("%s edit: generator ran %v, a missed %v, want %v (resolved %q)", c.name, ran, missed, c.rerun, warm.Trace.ModulesResolved)
		}
		fresh := cfg
		fresh.CacheDir = t.TempDir()
		if w, cj := jsonOf(t, warm), jsonOf(t, frontCheck(t, fresh)); !bytes.Equal(w, cj) {
			t.Errorf("%s edit: warm --json %s, cold %s", c.name, w, cj)
		}
	}
}

// TestGen_WalkMarkerReads: the walk marker is keyed on what
// the walk reads, so an edit of a non-generic body (a root of its own
// module, walked with it) rewalks only the edited module, whether app's
// roots reach it or not, and an edit of a generic body app's roots
// instantiate rewalks app too. Each warm check equals a cold one.
func TestGen_WalkMarkerReads(t *testing.T) {
	t.Parallel()
	lib := "export F(x: int): int {\n    return x + 1\n}\n\nexport G(x: int): int {\n    return x + 2\n}\n\nexport H[T](x: T): T {\n    return x\n}\n"
	dir := writeTree(t, map[string]string{
		"pkg.mk":     manifest(nil),
		"lib/lib.mk": lib,
		"app/app.mk": "import lib\n\nexport Use(x: int): int {\n    return lib.G(lib.H(x))\n}\n",
	})
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk")}
	cold := frontCheck(t, cfg)
	clean(t, cold)
	if got := pkgOnly(frontCheck(t, cfg).Trace.ModulesWalked); len(got) != 0 {
		t.Errorf("unedited check walked %q, want none", got)
	}
	for _, c := range []struct {
		name, old, new string
		walked         []string
	}{
		{"unreached", "return x + 1", "return x + 3", []string{"lib"}},
		{"reached root", "return x + 2", "return x + 4", []string{"lib"}},
		{"generic", "    return x\n}\n", "    y := x\n    return y\n}\n", []string{"app", "lib"}},
	} {
		lib = strings.Replace(lib, c.old, c.new, 1)
		if err := os.WriteFile(filepath.Join(dir, "lib", "lib.mk"), []byte(lib), 0o644); err != nil {
			t.Fatal(err)
		}
		warm := frontCheck(t, cfg)
		clean(t, warm)
		if got := pkgOnly(warm.Trace.ModulesWalked); !slices.Equal(got, c.walked) {
			t.Errorf("%s body edit walked %q, want %q", c.name, got, c.walked)
		}
		fresh := cfg
		fresh.CacheDir = t.TempDir()
		if w, cj := jsonOf(t, warm), jsonOf(t, frontCheck(t, fresh)); !bytes.Equal(w, cj) {
			t.Errorf("%s: warm --json %s, cold %s", c.name, w, cj)
		}
	}
}

// TestGen_GenRefBindingsAndPins: a stored gen module misses when what its
// generated names mean changes with no edit of its own text: the binding a
// generator's quote names (its module's import line), and the type a gen
// argument pins. Each warm check equals a cold one.
func TestGen_GenRefBindingsAndPins(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"pkg.mk":         manifest(nil),
		"x/user/user.mk": "export struct User {\n    export n: int\n}\n",
		"y/user/user.mk": "export struct User {\n    export m: int\n}\n",
		"g/g.mk":         "import u = x.user\n\nexport Make(g: mut Gen) {\n    g.Emit(quote {\n        export Get(v: u.User): int {\n            return v.n\n        }\n    })\n}\n\nexport Pin(g: mut Gen, t: Type) {\n    g.Emit(quote {\n        export Id(v: $(t.Name())): $(t.Name()) {\n            return v\n        }\n    })\n}\n",
		"a/a.mk":         "import g\n\ngen g.Make()\n",
		"b/b.mk":         "import g\nimport xu = x.user\nimport yu = y.user\n\nexport Both(a: xu.User, b: yu.User) {\n}\n\ngen g.Pin(xu.User)\n",
	}
	dir := writeTree(t, files)
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk")}
	clean(t, frontCheck(t, cfg))
	same := frontCheck(t, cfg)
	if got := pkgOnly(same.Trace.ModulesResolved); len(got) != 0 {
		t.Fatalf("unedited check resolved %q, want none", got)
	}
	for _, c := range []struct {
		file, old, new, missed string
		codes                  bool
	}{
		// g's binding now names y.user, whose User has no n: a must miss
		// and report it, though a's text did not change.
		{"g/g.mk", "import u = x.user", "import u = y.user", "a", true},
		// The pinned argument moves to the other user.User.
		{"b/b.mk", "gen g.Pin(xu.User)", "gen g.Pin(yu.User)", "b", false},
	} {
		p := filepath.Join(dir, filepath.FromSlash(c.file))
		old, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(old), c.old, c.new, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		warm := frontCheck(t, cfg)
		if got := pkgOnly(warm.Trace.ModulesResolved); !slices.Contains(got, c.missed) {
			t.Errorf("%s edit: resolved %q, want %s", c.file, got, c.missed)
		}
		if c.codes == (len(warm.Report.Diags) == 0) {
			t.Errorf("%s edit: diagnostics %q", c.file, codes(warm))
		}
		fresh := cfg
		fresh.CacheDir = t.TempDir()
		if w, cj := jsonOf(t, warm), jsonOf(t, frontCheck(t, fresh)); !bytes.Equal(w, cj) {
			t.Errorf("%s edit: warm --json %s, cold %s", c.file, w, cj)
		}
		if err := os.WriteFile(p, old, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestGen_GenRefInputAndView: a stored gen module whose generator read a
// data file through Gen.Input misses when the file changes, and a hit
// still reports a hand edit of its gen/ view (E0904).
func TestGen_GenRefInputAndView(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"pkg.mk":      manifest(nil),
		"g/g.mk":      gen10Lib,
		"data/in.txt": "hello",
		"a/a.mk":      "import g\n\ngen g.Input()\n",
	})
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk"), GenView: true}
	clean(t, frontCheck(t, cfg))
	if got := pkgOnly(frontCheck(t, cfg).Trace.ModulesResolved); len(got) != 0 {
		t.Fatalf("unedited check resolved %q, want none", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "in.txt"), []byte("hello, world"), 0o644); err != nil {
		t.Fatal(err)
	}
	warm := frontCheck(t, cfg)
	clean(t, warm)
	if got := pkgOnly(warm.Trace.ModulesResolved); !slices.Contains(got, "a") {
		t.Errorf("data edit resolved %q, want a", got)
	}
	view := filepath.Join(dir, "a", "gen", "x86_64-windows", "gen.mk")
	b, err := os.ReadFile(view)
	if err != nil || !strings.Contains(string(b), "InputLen: int = 12") {
		t.Fatalf("view %q (%v)", b, err)
	}
	// A hit, then a hand edit of the view: E0904.
	if got := pkgOnly(frontCheck(t, cfg).Trace.ModulesResolved); len(got) != 0 {
		t.Fatalf("second unedited check resolved %q", got)
	}
	if err := os.WriteFile(view, append([]byte("// a hand edit\n"), b...), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := codes(frontCheck(t, cfg)); !slices.Equal(got, []string{"E0904"}) {
		t.Errorf("hand-edited view on a hit: %q, want [E0904]", got)
	}
}
