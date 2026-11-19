// Why: compile-time evaluation may call a generic, whose body only exists
// once mono instantiates it; a missing instance must never be silent.
// What: TestGenericEvaluationUsesInstance, TestGenericEvaluationInPackage
// and TestMutexGlobalUnits.
// How: evaluates compile-time calls of generics through the driver and
// checks that mono's instance body is used: no E0000, and no StageInstances
// in UnitOutcome.Skipped for a corpus unit.

package driver

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/std"
)

// instStd is the stand-in std with one generic method on a prelude type, so
// a unit can call a generic at compile time.
func instStd(t testing.TB) string {
	files := fakeStdFiles()
	files["core/inst.mk"] = "export Box[T].Of(x: T): T {\n    return x\n}\n"
	return writeTree(t, files)
}

func TestGenericEvaluationUsesInstance(t *testing.T) {
	// A compile-time call of a generic evaluates mono's instance body
	// (mono.Instantiate through program.Body), so nothing is skipped and no
	// E0000 appears.
	c, err := NewCorpusSession(Config{StdDir: instStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Std().Report.Diags) != 0 {
		t.Fatalf("std: %s", c.Std().Report.Text(c.Std().Sources))
	}
	if c.Through() != diag.PhaseFlow {
		t.Fatalf("Through %v, want flow", c.Through())
	}
	for _, tc := range []struct {
		name  string
		files []UnitFile
		codes []string
	}{
		{"const#1", []UnitFile{{"block.mk", "const N: int = Box[int].Of(2)\n"}}, nil},
		{"import#1", []UnitFile{
			{"a/a.mk", "export const N: int = Box[int].Of(2)\n"},
			{"main.mk", "import a\n\nconst M: int = a.N + 1\n"},
		}, nil},
		{"plain#1", []UnitFile{{"block.mk", "const N: int = 1 + 1\n"}}, nil},
		{"error#1", []UnitFile{{"block.mk", "const N: int = Box[int].Of(2)\n\nF(): int {\n    return \"x\"\n}\n"}},
			[]string{"E0301"}},
	} {
		out := c.CheckUnit(UnitInput{Name: tc.name, Files: tc.files})
		if got := codesOf(out.Report); !slices.Equal(got, tc.codes) {
			t.Errorf("%s: codes %q, want %q\n%s", tc.name, got, tc.codes, out.Report.Text(out.Sources))
		}
		if out.Skipped.Has(StageInstances) || out.Skipped.Has(StageGen) {
			t.Errorf("%s: Skipped %v", tc.name, out.Skipped.Stages())
		}
	}
}

func TestGenericEvaluationInPackage(t *testing.T) {
	// The same in a package build: the const gets the instance's value.
	dir := writeTree(t, map[string]string{"a.mk": "const N: int = Box[int].Of(2)\n\nexport Main(sys: &System) {\n}\n"})
	s, err := NewSession(Config{Root: dir, StdDir: instStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	clean(t, s.Check())
	if v, ok := constValue(t, s, "main.N"); !ok || s.w.tab.ConstString(v) != "2" {
		t.Errorf("N: has value %v, %s; want 2", ok, s.w.tab.ConstString(v))
	}
}

// TestMutexGlobalUnits runs the gate's inputs on the embedded std:
// an example in doc/memory.md and the units codes/E0605, model/H13.mut and
// model/R16.Stash initialize a var global with Mutex.New(none), a generic.
// The instance is evaluated: no "E0000 ... eval: no body for
// std.core.Mutex:New", nothing skipped. Each unit's E0605 is stage 7's, so
// it appears exactly when Through reaches flow.
func TestMutexGlobalUnits(t *testing.T) {
	c, err := NewCorpusSession(Config{})
	if err != nil {
		t.Fatal(err)
	}
	prelude, err := fs.ReadFile(std.FS, "corpus/testprelude.mk")
	if err != nil {
		t.Fatal(err)
	}
	ins := []UnitInput{{Name: "doc/memory.md#1488", Files: []UnitFile{{"block.mk",
		"var Latest: Mutex[?&i32] = Mutex.New(none)\n\nStash(io: Io): Canceled!void {\n    n: i32 = 1\n" +
			"    var g := try Latest.Lock(io)\n    defer g.Unlock()\n    g.Store(some(&n)) // <- E0605\n}\n"}}}}
	byName := map[string]confUnit{}
	for _, u := range readConfUnits(t, filepath.Join("..", "..", "tests", "conformance")) {
		byName[u.name] = u
	}
	for _, name := range []string{"codes/E0605", "model/H13.mut", "model/R16.Stash"} {
		u, ok := byName[name]
		if !ok {
			t.Fatalf("%s is no unit of tests/conformance", name)
		}
		in := UnitInput{Name: "tests/conformance/" + u.name, Files: slices.Clone(u.files)}
		if u.prelude {
			in.Prelude = "prelude.mk"
			in.Files = append(in.Files, UnitFile{"prelude.mk", string(prelude)})
			slices.SortFunc(in.Files, func(a, b UnitFile) int { return strings.Compare(a.Path, b.Path) })
		}
		ins = append(ins, in)
	}
	for _, in := range ins {
		out := c.CheckUnit(in)
		if out.Skipped.Has(StageInstances) {
			t.Errorf("%s: Skipped %v", in.Name, out.Skipped.Stages())
		}
		for _, e := range out.Report.Diags {
			if e.Diag.Code == diag.E0000 || e.Diag.Phase < diag.PhaseFlow {
				t.Errorf("%s: %s %s", in.Name, e.Diag.Code, e.Diag.Message)
			}
		}
		t.Logf("%s: Through %v, %d diagnostics", in.Name, out.Through, len(out.Report.Diags))
	}
}
