// Why: some conformance units can run only on one host, and a run on another
// host must skip them, never pass them.
// What: TestUnitHostKey, on the `// host:` header key of units.go and run.go.
// How: judges one unit with a fake compiler for two targets.

package corpus

import (
	"strings"
	"testing"

	"mk/internal/driver"
)

// TestUnitHostKey: `// host: x86_64-windows` sets Meta.Host; a run judging
// x86_64-linux reports the unit SKIP (never compiled, never a pass,
// counted as skipped), and a run judging x86_64-windows judges it (the
// fake compiler's report judges PENDING). A host
// that is no target name, or given twice, is an extraction error.
func TestUnitHostKey(t *testing.T) {
	root := confTree(t, map[string]string{
		"codes/E0101.mk": "// verdict: ok\n// host: x86_64-windows\nF() { }\n",
	})
	us, err := Units(root)
	if err != nil || len(us) != 1 || us[0].Meta.Host != "x86_64-windows" {
		t.Fatalf("Units = %+v, %v; want one unit with Host x86_64-windows", us, err)
	}
	for _, c := range []struct {
		target string
		want   Status
		checks int32
	}{{"x86_64-linux", StatusSkip, 0}, {"x86_64-windows", StatusPending, 1}} {
		target, _, _ := driver.TargetNamed(c.target)
		fc := &fakeCompiler{avail: driver.Available()}
		r, err := Run(Options{Root: root, Conformance: true, Only: "tests/conformance/codes/E0101.mk", Compiler: fc, Driver: driver.Config{Target: target}})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Units) != 1 || r.Units[0].Status != c.want || fc.checks.Load() != c.checks {
			t.Fatalf("judging %s: %+v (checks %d); want %s", c.target, r.Units, fc.checks.Load(), c.want)
		}
		if c.want == StatusSkip && (r.Summary.Skip != 1 || r.Summary.Pass != 0 || !strings.Contains(textOf(t, r), "skipped 1")) {
			t.Errorf("judging %s: summary %+v", c.target, r.Summary)
		}
	}
	for _, bad := range []string{"// host: x86_64-mars\n", "// host: x86_64-windows\n// host: x86_64-linux\n"} {
		root := confTree(t, map[string]string{"codes/E0101.mk": "// verdict: ok\n" + bad + "F() { }\n"})
		if _, err := Units(root); err == nil || !strings.Contains(err.Error(), "host") {
			t.Errorf("%q: Units error %v; want a host error", bad, err)
		}
	}
}

func textOf(t *testing.T, r *Result) string {
	var b strings.Builder
	if err := WriteText(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
