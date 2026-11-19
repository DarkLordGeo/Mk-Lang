// Why: these defects broke release builds, fields loops and finite-set
// generic clauses, and must stay fixed.
// What: regression tests of a wrapped Main kept out of line, a fields loop
// over a temporary, Vec operators and Reinterpret under listed sets of
// const generics, and gen aliases of error unions.
// How: builds or checks small programs and reads the release IR (through
// mainDefine), the run's output or the diagnostics (diagAt).

package driver

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"mk/internal/types"
)

// mainDefine returns the line that defines Main in the IR of a release
// build of src (codegen writes a function's attributes on it), and the
// whole IR; "" when no pack defines Main.
func mainDefine(t *testing.T, src string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var lls []string
	s, err := NewSession(Config{Root: file, Profile: types.Release, Tools: needTools(t), CacheDir: t.TempDir(),
		TestHooks: TestHooks{OnLL: func(_, ll string) { mu.Lock(); lls = append(lls, ll); mu.Unlock() }}})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(dir, "main"+exeSuffix(s.cfg.Target)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		skipKnownBlockers(t, r)
		t.Fatalf("mk build --release: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
	}
	def := regexp.MustCompile(`(?m)^define [^\n]*@"?mk\.main\.Main"?\([^\n]*`)
	for _, ll := range lls {
		if m := def.FindString(ll); m != "" {
			return m, ll
		}
	}
	return "", strings.Join(lls, "\n")
}

// TestWrappedMainStaysOutOfLine: a Main that can fail is called from
// the driver's report wrapper, in Main's own pack, and release's
// single-call rule inlined it there, so mk.main.Main left the executable
// and std's TestSTDCRYPTO_ConstantTimeDisassembly, which reads the probe's
// Main by symbol, checked nothing. The wrapped Main is noinline and never a
// sole-call target (both are needed: clang rejects noinline with a call
// site's alwaysinline). A Main that cannot fail has no wrapper and keeps
// its attributes.
func TestWrappedMainStaysOutOfLine(t *testing.T) {
	t.Parallel()
	def, ll := mainDefine(t, "error Bad(code: i64)\n\nexport Main(sys: &System): Bad!void {\n    if sys.args.Len() > 3 {\n        fail Bad(code: 7)\n    }\n}\n")
	if def == "" {
		t.Fatalf("no definition of mk.main.Main in the release IR:\n%s", ll)
	}
	if !strings.Contains(def, " noinline ") {
		t.Errorf("the wrapped Main is not noinline:\n%s", def)
	}
	plain, _ := mainDefine(t, "export Main(sys: &System) {\n    if sys.args.Len() > 3 {\n        Assert(false)\n    }\n}\n")
	if plain == "" || strings.Contains(plain, " noinline ") {
		t.Errorf("a Main with no wrapper: want a definition without noinline, got %q", plain)
	}
}

// TestFieldsLoopOverATemporary: a fields loop over a value that is not
// a place, `fields(&Make())`, was lower's "Poison with no diagnostic" ICE
// (check typed the `&` Poison; FuzzNoSilentPoison 9c2443104f774598). It
// builds and runs in both profiles, beside the generic form.
func TestFieldsLoopOverATemporary(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "main.mk")
	text := "struct P {\n    export n: int\n    export s: str\n}\n\nMake(): P {\n    return P(n: 3, s: \"x\")\n}\n\n" +
		"Dump[A: Fields[Format] + Plain](w: mut Writer, args: A): WriteError!void {\n    for name, value in fields(&args) {\n        try w.Str(name)\n        try value.Format(w)\n    }\n}\n\n" +
		"export Main(sys: &System): WriteError!void {\n    var w := sys.Stdout()\n    for name, value in fields(&Make()) {\n        try w.Str(name)\n        try value.Format(mut w)\n    }\n    try Dump(mut w, Make())\n    try w.Str(\"\\n\")\n}\n"
	if err := os.WriteFile(src, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	host := HostTarget()
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		exe := buildProgram(t, src, host, prof, t.TempDir())
		if out, errOut, code := runExe(t, host, exe, ""); code != 0 || out != "n3sxn3sx\n" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q; want exit 0 and %q", prof, code, out, errOut, "n3sxn3sx\n")
		}
	}
}

// diagAt is each diagnostic of r as code@line.
func diagAt(r Result) string {
	var at []string
	for _, e := range r.Report.Diags {
		at = append(at, e.Diag.Code.String()+"@"+strconv.Itoa(int(r.Sources.Position(e.Diag.At.StartPos()).Line)))
	}
	return strings.Join(at, " ")
}

// TestVecOperatorUnderFiniteSet: a Vec operator in a body
// whose clauses list N's values took no part in the finite-set check (the
// operator's call is nil, so finiteRequires was skipped) and asked for the
// general lane clause. It is judged over the set like a method call: clean
// for 16, 32 and 64, E0706 at the operator for a set holding 3.
func TestVecOperatorUnderFiniteSet(t *testing.T) {
	t.Parallel()
	r := checkText(t, `import std.simd

Narrow[const N: int](dst: mut []u8)
    requires N == 16 || N == 32 || N == 64
{
    lane := simd.Vec[u8, N].Iota()
    three := simd.Vec[u8, N].Splat(3)
    a := lane.DivTrunc(three)
    b := ((a +% three) & lane).Shl(1)
    m := b == lane
    m.Select(b, ~lane).CopyTo(dst, 0)
}

Bad[const N: int](x: simd.Vec[u8, N]): simd.Vec[u8, N]
    requires N == 3 || N == 16
{
    return x +% x
}
`)
	if got := diagAt(r); got != "E0706@17" {
		t.Errorf("want only Bad's operator E0706 at line 17, got %q:\n%s", got, diagLines(r))
	}
}

// TestReinterpretUnderListedSet: Reinterpret under a listed
// set of const generics. Reinterpret's clause, `M * SizeOf[U]() == N *
// SizeOf[T]() && M >= 2 && M <= 64`, is judged over a listed N (GammaN's
// shape) and holds for each; a lane type it fails for (Wide, u64 from f32)
// still asks for the clause. An implied set (`M * 2 == N`) is not judged
// here.
func TestReinterpretUnderListedSet(t *testing.T) {
	t.Parallel()
	r := checkText(t, `import std.simd

GammaN[const N: int]()
    requires N == 4 || N == 8 || N == 16
{
    bits := simd.Vec[f32, N].Splat(1.0).Reinterpret[u32, N]()
    _ = bits.Reinterpret[f32, N]()
}

Wide[const N: int]()
    requires N == 4 || N == 8
{
    _ = simd.Vec[f32, N].Splat(1.0).Reinterpret[u64, N]()
}
`)
	if got := diagAt(r); got != "E0706@13" {
		t.Errorf("want only Wide's E0706 at line 13, got %q:\n%s", got, diagLines(r))
	}
}

// TestGenAliasErrorSetUnion: a method result naming an alias of
// a `|` set of errors (`type Failure = Bad | OutOfMemory`, or two user
// errors) had no type in the stage-4 view, so gen dyn.Of emitted nothing
// and every use of the generated name was E0201
// (TestGenAliasErrorSet covers the alias of one error).
func TestGenAliasErrorSetUnion(t *testing.T) {
	t.Parallel()
	for _, set := range []string{"Bad | OutOfMemory", "Bad | Worse", "(Bad | Worse) | OutOfMemory"} {
		body := "error Bad\nerror Worse\n\ntype Failure = " + set + "\n\nMust(): Failure!f64 {\n    return 1.0\n}\n\n"
		src := strings.Replace(shapeDyn(body, "Failure!f64", "try Must()"), "_ = d.Area()", "_ = d.Area() else { 0.0 }", 1)
		if r := checkText(t, src); len(r.Report.Diags) != 0 {
			t.Errorf("type Failure = %s: want clean:\n%s", set, diagLines(r))
		}
	}
}
