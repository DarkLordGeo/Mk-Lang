// Why: stages 5 and 6 of the per-module pipeline carry the evaluator, the
// stage rule, poison and parallel body checking, and their output must not
// depend on scheduling.
// What: tests of the stage rule (E0908), declaration evaluation after the
// bodies, fuel (E0902), poison of failed modules, the phase rule, parallel
// determinism, unit panics as E0000, dependency scheduling and gen lines.
// How: checks small package trees with different worker counts and compares
// the reports byte for byte.

package driver

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/types"
)

// three is a module source with a function the evaluator can run.
const three = "export Three(): int {\n    return 3\n}\n"

func TestStageRuleE0908(t *testing.T) {
	// A same-module const that calls a same-module function cannot appear in
	// a type (the stage rule): E0908, from check, rendered from
	// the evaluator's EvalStage.
	r := check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"a/a.mk": three + "\nconst N: int = Three()\n\nexport F(): u8 {\n    x: [N]u8 = [0; 3]\n    return x[0]\n}\n",
	}))})
	if got := codes(r); !slices.Equal(got, []string{"E0908"}) {
		t.Fatalf("codes %q, want [E0908]\n%s", got, r.Report.Text(r.Sources))
	}
	d := r.Report.Diags[0].Diag
	if loc := diag.Location(r.Sources, d.At); loc != "a/a.mk:8:9" {
		t.Errorf("E0908 at %s, want a/a.mk:8:9", loc)
	}
	if f, ok := d.FactValue("items"); !ok || len(f.List) != 2 {
		t.Errorf("items fact %v", f)
	}
	// The same const imported from a strictly lower module is final when
	// the importer's stage 5 runs, so the type evaluates.
	clean(t, check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"a/a.mk": three + "\nexport const N: int = Three()\n",
		"b/b.mk": "import a\n\nexport F(): u8 {\n    x: [a.N]u8 = [0; 3]\n    return x[0]\n}\n",
	}))}))
}

// constValue reads a const's value from the last Check of s, by key.
func constValue(t *testing.T, s *Session, key string) (types.ConstID, bool) {
	t.Helper()
	id, ok := s.w.items.ByKey(types.KeyFromString(key))
	if !ok {
		t.Fatalf("no item %s", key)
	}
	d, ok := s.w.items.Get(id).Decl.(*types.ConstDecl)
	if !ok {
		t.Fatalf("%s is not a const: %T", key, s.w.items.Get(id).Decl)
	}
	return d.Value, d.HasValue
}

func TestTableEvaluatedAfterBodies(t *testing.T) {
	// A same-module const that calls a same-module function, used in no
	// type, is evaluated by the driver's declaration evaluation after the
	// module's bodies, and its value is written into the ConstDecl.
	src := "Squares(): [4]i32 {\n    var t: [4]i32 = [0; 4]\n    for i in 0..4 {\n        t[i] = Cast[i32](i * i)\n    }\n" +
		"    return t\n}\n\nconst Table = Squares()\n\nexport const Four: i32 = Table[2]\n"
	s, err := NewSession(Config{Root: writeTree(t, pkgFiles(map[string]string{"a/a.mk": src}))})
	if err != nil {
		t.Fatal(err)
	}
	r := s.Check()
	clean(t, r)
	for _, key := range []string{"a.Table", "a.Four"} {
		v, ok := constValue(t, s, key)
		if !ok {
			t.Errorf("%s has no value", key)
			continue
		}
		t.Logf("%s = %s", key, s.w.tab.ConstString(v))
	}
	if v, ok := constValue(t, s, "a.Four"); ok {
		if got := s.w.tab.ConstString(v); got != "4" {
			t.Errorf("Four = %s, want 4", got)
		}
	}
}

func TestE0902_FuelExhaustedAtDeclaration(t *testing.T) {
	// A const whose initializer loops past its fuel is E0902, reported once,
	// by the declaration evaluation.
	src := "Spin(): int {\n    var n := 0\n    for {\n        n = n +% 1\n    }\n}\n\nconst N: int = Spin() fuel 1000\n"
	r := check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{"a/a.mk": src}))})
	if got := codes(r); !slices.Equal(got, []string{"E0902"}) {
		t.Fatalf("codes %q, want [E0902]\n%s", got, r.Report.Text(r.Sources))
	}
}

func TestNoEvaluationOfFailedModule(t *testing.T) {
	// a has a type error in the body N's initializer calls; b's type reads
	// a.N. a is failed, so b's evaluation is poison: no diagnostic beyond
	// a's own.
	r := check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"a/a.mk": "export Bad(): int {\n    return \"x\"\n}\n\nexport const N: int = Bad()\n",
		"b/b.mk": "import a\n\nexport F(): u8 {\n    x: [a.N]u8 = [0; 3]\n    return x[0]\n}\n",
	}))})
	got := lines(r)
	if len(got) == 0 || slices.ContainsFunc(got, func(l string) bool { return !strings.HasPrefix(l, "a/a.mk:2:") }) {
		t.Errorf("diagnostics %q, want only a's type error at a/a.mk:2\n%s", got, r.Report.Text(r.Sources))
	}
}

// phaseTree has a names error in module n and type errors in modules a and
// b, b importing a.
func phaseTree() map[string]string {
	return pkgFiles(map[string]string{
		"n/n.mk": "export N(): int {\n    return Missing()\n}\n",
		"a/a.mk": "export A(): int {\n    return \"a\"\n}\n\nexport B(): bool {\n    return 1\n}\n",
		"b/b.mk": "import a\n\nexport C(): str {\n    return a.A()\n}\n",
	})
}

func TestPhaseRuleSameBytes(t *testing.T) {
	// With a names error in the build, the report holds it alone,
	// byte for byte the same whether stages 5 and 6 ran in the other
	// modules or not, and whatever the worker count.
	dir := writeTree(t, phaseTree())
	var want string
	for _, past := range []bool{false, true} {
		for _, jobs := range []int{1, 2, 8} {
			r := check(t, Config{Root: dir, Jobs: jobs, TestHooks: TestHooks{CheckPastNames: past}})
			got := string(r.Report.WriteJSON(r.Sources))
			if want == "" {
				want = got
				if c := codes(r); !slices.Equal(c, []string{"E0201"}) {
					t.Fatalf("codes %q, want [E0201]", c)
				}
				if r.Reached != StageResolve {
					t.Errorf("reached %v, want resolve: a names error stops before stage 5", r.Reached)
				}
			} else if got != want {
				t.Errorf("past %v jobs %d: report differs:\n%s\n%s", past, jobs, got, want)
			}
		}
	}
}

func TestBodiesInParallelAreDeterministic(t *testing.T) {
	// Every stage-6 error of every module is reported, merged in
	// slot order whatever the completion order.
	files := map[string]string{}
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("export F")
		b.WriteString(strings.Repeat("x", i))
		b.WriteString("(): int {\n    return \"s\"\n}\n\n")
	}
	files["a/a.mk"] = b.String()
	files["b/b.mk"] = "import a\n\nexport G(): bool {\n    return a.F()\n}\n"
	dir := writeTree(t, pkgFiles(files))
	var first string
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(jobs int) {
			defer wg.Done()
			r := check(t, Config{Root: dir, Jobs: jobs})
			if n := len(r.Report.Diags); n != 41 {
				t.Errorf("jobs %d: %d diagnostics, want 41\n%s", jobs, n, r.Report.Text(r.Sources))
			}
			got := string(r.Report.WriteJSON(r.Sources))
			mu.Lock()
			defer mu.Unlock()
			if first == "" {
				first = got
			} else if got != first {
				t.Errorf("jobs %d: report differs", jobs)
			}
		}(i%4 + 1)
	}
	wg.Wait()
}

func TestE0000_UnitPanicBecomesDiagnostic(t *testing.T) {
	// An ICE in one unit's stage 6 is an E0000 naming it (exit 3); the other
	// units of the module still report.
	r := check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"a/a.mk": "export F(): int {\n    return 1\n}\n\nexport G(): int {\n    return \"g\"\n}\n",
	})), TestHooks: TestHooks{PanicIn: "a.F"}})
	got := codes(r)
	if !slices.Contains(got, "E0000") || len(got) != 2 {
		t.Errorf("codes %q, want an E0000 and G's type error\n%s", got, r.Report.Text(r.Sources))
	}
	if ExitStatus(r.Report) != 3 {
		t.Errorf("exit %d, want 3", ExitStatus(r.Report))
	}
}

func TestScheduleByDependencies(t *testing.T) {
	// c imports only a; b is unrelated at a's depth. b's stage 6
	// does not end until c has started stage 5, which a depth barrier (or a
	// barrier between modules' stages 5 and 6) would never allow.
	dir := writeTree(t, pkgFiles(map[string]string{
		"a/a.mk": three,
		"b/b.mk": "export B(): int {\n    return 2\n}\n",
		"z/z.mk": "import a\n\nexport C(): int {\n    return a.Three()\n}\n",
	}))
	cStarted := make(chan struct{})
	var once sync.Once
	timedOut := false
	hook := func(module string, st Stage, begin bool) {
		switch {
		case module == "z" && st == StageSignatures && begin:
			once.Do(func() { close(cStarted) })
		case module == "b" && st == StageBodies && !begin:
			select {
			case <-cStarted:
			case <-time.After(10 * time.Second):
				timedOut = true
			}
		}
	}
	r := check(t, Config{Root: dir, Jobs: 2, TestHooks: TestHooks{OnStage: hook}})
	clean(t, r)
	if timedOut {
		t.Error("z did not start stage 5 while b was in stage 6")
	}
	if r.Reached != StageFlow {
		t.Errorf("reached %v, want flow", r.Reached)
	}
}

func TestOneDiagnosticForConstFailure(t *testing.T) {
	// A const whose initializer panics is reported once, at its own
	// declaration evaluation; a same-module const that reads it, and an
	// importer's type that reads that one, get poison and say nothing
	// through the real driver.
	r := check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"a/a.mk": "Boom(): int {\n    Panic(\"no\")\n}\n\nexport const A: int = Boom()\n\nexport const B: int = A + 1\n",
		"b/b.mk": "import a\n\nexport F(): u8 {\n    x: [a.B]u8 = [0; 3]\n    return x[0]\n}\n",
	}))})
	if got := lines(r); !slices.Equal(got, []string{"a/a.mk:5:14:E0901"}) {
		t.Errorf("diagnostics %q, want one E0901 at A\n%s", got, r.Report.Text(r.Sources))
	}
}

func TestUnitWithGenLineRunsGen(t *testing.T) {
	// Stage 4 runs (gen.go): a unit judged past parse that holds a gen line
	// skips no stage, and neither does one without.
	c, err := NewCorpusSession(Config{StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	out := c.CheckUnit(UnitInput{Name: "gen#1", Files: []UnitFile{{"block.mk", "struct P {\n    x: int\n}\n\ngen Eq(P)\n"}}})
	if out.Skipped.Has(StageGen) {
		t.Errorf("gen unit: Skipped %v", out.Skipped.Stages())
	}
	out = c.CheckUnit(UnitInput{Name: "plain#1", Files: []UnitFile{{"block.mk", three}}})
	if out.Skipped != 0 || len(out.Report.Diags) != 0 {
		t.Errorf("plain unit: Skipped %v, %d diagnostics", out.Skipped.Stages(), len(out.Report.Diags))
	}
}
