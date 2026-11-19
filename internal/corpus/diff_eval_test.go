// Why: the compile-time evaluator and native code must compute the same
// values and panic on the same cases.
// What: TestDiffEvalNative over diff/eval.mk, its evaluator and native halves,
// and tests of the case list and generated sources.
// How: one const per case for the evaluator, one process per case natively,
// and planted mismatches that prove both comparisons can fail.
//
// TestDiffEvalNative's evaluator half runs here: every Case<Name> of
// diff/eval.mk is evaluated by the driver's evaluator, through one const
// per case, and must panic exactly when its name ends in Trap (eval.mk's
// convention). A second check wraps every call in DiffAfter, which fails an
// assert once the call has returned, so every non-Trap const must then
// panic with "assert" and every Trap const with its own panic: the
// evaluator ran every case, none was skipped or folded away. The native
// half builds one program per target and profile and runs each case in a
// process of its own: the value must equal the evaluator's, and a Trap must
// panic with the evaluator's panic code.

package corpus

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/types"
)

func TestDiffEvalNative(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, DiffDir, "eval.mk"))
	if err != nil {
		t.Fatal(err)
	}
	cases, err := EvalCases(DiffDir+"/eval.mk", src)
	if err != nil {
		t.Fatal(err)
	}
	traps := 0
	for _, c := range cases {
		if c.Trap() {
			traps++
		}
	}
	if traps == 0 || traps == len(cases) {
		t.Fatalf("eval.mk has %d cases, %d of them Trap: the check needs both kinds", len(cases), traps)
	}
	host := driver.HostTarget()
	for _, name := range []string{"x86_64-windows", "x86_64-linux"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tgt, _, ok := driver.TargetNamed(name)
			if !ok {
				t.Fatalf("no target %s", name)
			}
			plain := evalPanics(t, src, cases, false, tgt)
			t.Run("eval", func(t *testing.T) {
				after := evalPanics(t, src, cases, true, tgt)
				var codes []string
				for _, c := range cases {
					at := DiffDir + "/eval.mk:" + strconv.Itoa(c.Line)
					p, panicked := plain[c.Name]
					switch {
					case c.Trap() && !panicked:
						t.Errorf("%s: Case%s ends in Trap, but the evaluator does not panic", at, c.Name)
					case c.Trap() && p == "assert":
						t.Errorf("%s: Case%s panics with assert, which eval.mk does not call", at, c.Name)
					case !c.Trap() && panicked:
						t.Errorf("%s: Case%s does not end in Trap, but the evaluator panics: %s", at, c.Name, p)
					}
					// The liveness half: DiffAfter's assert fires only after
					// the call returned, so it separates "ran and returned"
					// from "never ran".
					a, ok := after[c.Name]
					switch {
					case !ok:
						t.Errorf("%s: DiffAfter(Case%s()) does not panic, so the evaluator did not run it", at, c.Name)
					case !c.Trap() && a != "assert":
						t.Errorf("%s: DiffAfter(Case%s()) panics with %s, want assert", at, c.Name, a)
					case c.Trap() && panicked && a != p:
						t.Errorf("%s: DiffAfter(Case%s()) panics with %s, but Case%s() alone with %s", at, c.Name, a, c.Name, p)
					}
					if c.Trap() {
						codes = append(codes, c.Name+"="+p)
					}
				}
				t.Logf("%d cases, %d Trap: each Trap panics in the evaluator and no other does; the panic codes the native half matches: %s", len(cases), traps, strings.Join(codes, ", "))
			})
			if tgt.Name == host.Name || tgt.Name == "x86_64-linux" && host.OS == types.OSWindows {
				t.Run("native", func(t *testing.T) {
					evalNative(t, src, cases, plain, name)
				})
				t.Run("native-planted", func(t *testing.T) {
					evalPlanted(t, src, cases, name)
				})
			}
		})
	}
}

// evalNative builds EvalProgramSource for target in dev and release and
// runs every case in a process of its own. A case that is
// not a Trap must exit 0, so its native value is the evaluator's const, with
// nothing on stdout or stderr. A Trap case must exit 101 with a report that
// names the evaluator's panic code (plain). In release LLVM may fold a case
// whose operands it sees through; dev is where the native operations run.
// Values are compared as EvalProgramSource says, not byte for byte in hex.
func evalNative(t *testing.T, src []byte, cases []EvalCase, plain map[string]string, target string) {
	run := evalRunner(t, src, cases, nil)
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			t.Parallel()
			if _, err := run.Build("eval.mk", target, prof); err != nil {
				t.Fatal(err)
			}
			needWSLRun(t, target, true)
			for _, c := range cases {
				t.Run(c.Name, func(t *testing.T) {
					t.Parallel()
					at := DiffDir + "/eval.mk:" + strconv.Itoa(c.Line)
					got, err := run.Run(ProgramCase{Program: "eval.mk", Args: []string{c.Name}, Timeout: DefaultCaseTimeout}, target, prof)
					switch {
					case ErrNoRunner(err):
						if gateRun() {
							t.Fatal(err)
						}
						t.Skip(err)
					case err != nil:
						t.Fatal(err)
					case got.TimedOut:
						t.Fatalf("%s: Case%s timed out after %v", at, c.Name, DefaultCaseTimeout)
					}
					if c.Trap() {
						want := "panic: " + plain[c.Name] + " at "
						if got.Exit != 101 || !strings.HasPrefix(string(got.Stderr), want) || len(got.Stdout) > 0 {
							t.Errorf("%s: Case%s: native exit %d, stderr %q, stdout %q; want exit 101 and a report starting %q (the evaluator's panic)", at, c.Name, got.Exit, got.Stderr, got.Stdout, want)
						}
						return
					}
					if got.Exit != 0 || len(got.Stderr) > 0 || len(got.Stdout) > 0 {
						t.Errorf("%s: Case%s: native exit %d (%s), stderr %q, stdout %q; want exit 0", at, c.Name, got.Exit, nativeExit[got.Exit], got.Stderr, got.Stdout)
					}
				})
			}
		})
	}
}

// evalPlanted is the native half's liveness check, in dev: every case of a
// type with two or more non-Trap cases is compared with the next such case's
// const (evalPlants). Each run must exit 0 or 1, and for each way of
// comparing (==, the float rule, field by field) that has plants, at least
// one run must exit 1. A Main that cannot tell values apart fails here.
func evalPlanted(t *testing.T, src []byte, cases []EvalCase, target string) {
	plant, kinds := evalPlants(cases)
	if len(plant) == 0 {
		t.Fatal("no type has two non-Trap cases, so nothing can be planted")
	}
	run := evalRunner(t, src, cases, plant)
	if _, err := run.Build("eval.mk", target, "dev"); err != nil {
		t.Fatal(err)
	}
	needWSLRun(t, target, true)
	differs := map[string]int{}
	for _, c := range cases {
		other, ok := plant[c.Name]
		if !ok {
			continue
		}
		got, err := run.Run(ProgramCase{Program: "eval.mk", Args: []string{c.Name}, Timeout: DefaultCaseTimeout}, target, "dev")
		switch {
		case ErrNoRunner(err):
			if gateRun() {
				t.Fatal(err)
			}
			t.Skip(err)
		case err != nil:
			t.Fatal(err)
		case got.TimedOut:
			t.Fatalf("Case%s against Case%s's const timed out", c.Name, other)
		case got.Exit == 1:
			differs[kinds[c.Name]]++
		case got.Exit != 0:
			t.Errorf("Case%s against Case%s's const: exit %d (%s), stderr %q; want 0 or 1", c.Name, other, got.Exit, nativeExit[got.Exit], got.Stderr)
		}
	}
	for _, k := range slices.Compact(slices.Sorted(maps.Values(kinds))) {
		if differs[k] == 0 {
			t.Errorf("no planted comparison by %s found a difference: the native check cannot tell those values apart", k)
		}
	}
	t.Logf("%d planted comparisons; those that found a difference, by way of comparing: %v", len(plant), differs)
}

// evalPlants maps every non-Trap case of a type with two or more of them to
// the next one of that type, cyclically, and gives each planted case's way
// of comparing: evalSameKind's, or "struct".
func evalPlants(cases []EvalCase) (plant, kinds map[string]string) {
	byType := map[string][]string{}
	var types []string
	for _, c := range cases {
		if c.Trap() {
			continue
		}
		if byType[c.Type] == nil {
			types = append(types, c.Type)
		}
		byType[c.Type] = append(byType[c.Type], c.Name)
	}
	plant, kinds = map[string]string{}, map[string]string{}
	for _, typ := range types {
		names := byType[typ]
		if len(names) < 2 {
			continue
		}
		kind := evalSameKind[typ]
		if kind == "" {
			kind = "struct"
		}
		for i, n := range names {
			plant[n] = names[(i+1)%len(names)]
			kinds[n] = kind
		}
	}
	return plant, kinds
}

// evalRunner writes EvalProgramSource(plant) as eval.mk in a directory of
// its own and returns a runner for it.
func evalRunner(t *testing.T, src []byte, cases []EvalCase, plant map[string]string) *Runner {
	t.Helper()
	gen, err := EvalProgramSource(DiffDir+"/eval.mk", src, cases, plant)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "eval.mk"), gen, 0o644); err != nil {
		t.Fatal(err)
	}
	return testRunner(t, dir, needLLVM(t))
}

// nativeExit names EvalProgramSource's exit statuses.
var nativeExit = map[int]string{
	0:   "the native value is the evaluator's",
	1:   "the native value is not the evaluator's",
	2:   "the program has no such case",
	3:   "a Trap case returned",
	101: "the native code panicked",
}

// evalPanics checks eval.mk with one const per case on target tgt
// (EvalCheckSource) and returns, per case whose const drew an E0901 panic,
// the panic code (facts.panic). An E0901 that is not a panic (a refusal),
// two at one const, an E0000 or any other diagnostic in the unit fails the
// test, std's included: both targets have a std.sys, and std is
// flow-clean.
func evalPanics(t *testing.T, src []byte, cases []EvalCase, after bool, tgt types.Target) map[string]string {
	t.Helper()
	gen, at := EvalCheckSource(src, cases, after)
	file := filepath.Join(t.TempDir(), "eval.mk")
	if err := os.WriteFile(file, gen, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := driver.NewSession(driver.Config{Root: file, Target: tgt, NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	res := s.Check()
	got := map[string]string{}
	for _, e := range res.Report.Diags {
		d := e.Diag
		pos := res.Sources.Position(d.At.StartPos())
		inUnit := filepath.Clean(pos.Path) == filepath.Clean(file)
		switch {
		case d.Code == diag.E0000:
			t.Fatalf("internal error: %s: %s", pos.Path, d.Message)
		case inUnit && d.Code == diag.E0901:
			c, ok := at[int(pos.Line)]
			if !ok {
				t.Fatalf("E0901 at line %d, which holds no case's const: %s", pos.Line, d.Message)
			}
			f, ok := d.FactValue("panic")
			if !ok || f.Kind != diag.FactText {
				t.Fatalf("Case%s: E0901 with no panic fact: %s", c.Name, d.Message)
			}
			if prev, dup := got[c.Name]; dup {
				t.Fatalf("Case%s: two E0901 at its const (%s, then %s)", c.Name, prev, f.Str)
			}
			got[c.Name] = f.Str
		default:
			t.Fatalf("unexpected diagnostic on %s (after %v): %s:%d:%d: %s %s", tgt.Name, after, pos.Path, pos.Line, pos.Col, d.Code, d.Message)
		}
	}
	return got
}

func TestEvalCases(t *testing.T) {
	src := "Max(): i64 {\n    return 1\n}\nCaseAddWrap(): i64 {\n    return Max() +% 1\n}\nCaseAddTrap(): Rgb {\n}\n"
	cases, err := EvalCases("e", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if want := []EvalCase{{"AddWrap", "i64", 4}, {"AddTrap", "Rgb", 7}}; !slices.Equal(cases, want) {
		t.Fatalf("cases %+v, want %+v", cases, want)
	}
	if cases[0].Trap() || !cases[1].Trap() {
		t.Fatalf("Trap(): %v %v", cases[0].Trap(), cases[1].Trap())
	}
	for in, want := range map[string]string{
		"CaseA(x: int): int {\n":             "e:1: CaseA takes parameters",
		"CaseA() {\n":                        "e:1: CaseA has no result type",
		"Casea(): int {\n":                   "is not an upper-case case name",
		"CaseA(): int\n":                     "e:1: want",
		"CaseA(): int {\nCaseA(): u8 {\n":    "e:2: CaseA is also on line 1",
		"Max(): int {\n    return 1\n}\n":    "e: no Case<Name> function",
		"// CaseA(): int {\nCaseB(): u8 {\n": "",
	} {
		_, err := EvalCases("e", []byte(in))
		if want == "" {
			if err != nil {
				t.Errorf("%q: %v", in, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want %q", in, err, want)
		}
	}
}

func TestEvalCheckSource(t *testing.T) {
	src := []byte("CaseA(): i64 {\n    return 1\n}")
	cases := []EvalCase{{Name: "A", Type: "i64"}, {Name: "BTrap", Type: "Two"}}
	for _, after := range []bool{false, true} {
		gen, at := EvalCheckSource(src, cases, after)
		lines := strings.Split(string(gen), "\n")
		if len(at) != 2 {
			t.Fatalf("after %v: %d consts placed, want 2", after, len(at))
		}
		for n, c := range at {
			call := "Case" + c.Name + "()"
			if after {
				call = "DiffAfter(" + call + ")"
			}
			if want := "const DiffCase" + c.Name + ": " + c.Type + " = " + call; lines[n-1] != want {
				t.Errorf("after %v: line %d is %q, want %q", after, n, lines[n-1], want)
			}
		}
		if has := strings.Contains(string(gen), "DiffAfter[T](v: T): T {"); has != after {
			t.Errorf("after %v: DiffAfter declared %v", after, has)
		}
	}
}

func TestEvalProgramSource(t *testing.T) {
	src := []byte("struct P {\n    x: u8\n    y: f32\n}\n\nCaseA(): i64 {\n    return 1\n}\nCaseB(): i64 {\n    return 2\n}\nCaseCTrap(): u8 {\n    return 0\n}\nCaseD(): P {\n    return P(x: 1, y: 0.5)\n}")
	cases, err := EvalCases("e", src)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := EvalProgramSource("e", src, cases, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := string(gen)
	for _, want := range []string{
		"const DiffWantA: i64 = CaseA()\n",
		"const DiffWantD: P = CaseD()\n",
		"DiffSameI64(a: i64, b: i64): bool {\n    return a == b\n}",
		"DiffSameF32(a: f32, b: f32): bool {\n    if a != a {",
		"DiffSameP(a: P, b: P): bool {\n    return DiffSameU8(a.x, b.x) && DiffSameF32(a.y, b.y)\n}",
		"export Main(sys: &System): int {\n    name := sys.args[1]\n",
		"    if name == \"A\" {\n        if DiffSameI64(CaseA(), DiffWantA) {\n            return 0\n        }\n        return 1\n    }\n",
		"    if name == \"CTrap\" {\n        got := CaseCTrap()\n",
		"    return 2\n}\n",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("generated source lacks %q:\n%s", want, g)
		}
	}
	if strings.Contains(g, "DiffWantCTrap") || strings.Count(g, "DiffSameI64(a:") != 1 {
		t.Errorf("a Trap case has a const, or a type has two DiffSame functions:\n%s", g)
	}
	planted, err := EvalProgramSource("e", src, cases, map[string]string{"A": "B"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(planted), "if DiffSameI64(CaseA(), DiffWantB) {") {
		t.Errorf("planted A against B not generated:\n%s", planted)
	}
	for msg, plant := range map[string]map[string]string{
		"is not a non-Trap case of type i64": {"A": "D"},
		"planted \"CTrap\" is not":           {"A": "CTrap"},
		"planted case \"CTrap\" is not":      {"CTrap": "A"},
	} {
		if _, err := EvalProgramSource("e", src, cases, plant); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("plant %v: error %v, want %q", plant, err, msg)
		}
	}
	bad := []EvalCase{{Name: "S", Type: "[]u8", Line: 3}}
	if _, err := EvalProgramSource("e", src, bad, nil); err == nil || !strings.Contains(err.Error(), "e:3: CaseS: type []u8 is not a scalar, str or a plain struct") {
		t.Errorf("slice case: %v", err)
	}
	if plant, kinds := evalPlants(cases); len(plant) != 2 || plant["A"] != "B" || plant["B"] != "A" || kinds["A"] != "eq" {
		t.Errorf("evalPlants = %v %v, want A and B planted on each other by eq", plant, kinds)
	}
}
