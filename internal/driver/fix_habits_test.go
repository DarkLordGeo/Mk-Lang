// Why: mk fix applies only proven fixes, round after round, until nothing
// proven is left; a fix that merely compiles must be reported, never
// applied.
// What: TestFixHabitPrograms over the habit programs in testdata/fix,
// TestFixNeverAppliesCompiles and TestFixAppliesE0711.
// How: copies each program, runs mk fix once to its fixed point, and
// compares the chain of rounds and the final report.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
)

// fixCopy copies testdata/fix/<name> into a temporary directory and runs
// one mk fix over it.
func fixCopy(t *testing.T, name string, dry bool) (*FixResult, string, string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "fix", name))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, src, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: p, CacheDir: filepath.Join(dir, ".cache")})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{DryRun: dry})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	return res, string(src), string(after)
}

func chain(res *FixResult) string {
	var b []string
	for i, r := range res.Rounds {
		for _, a := range r.Applied {
			b = append(b, itoaQ(i+1)+" "+r.Phase+" "+a.Code.String())
		}
	}
	return strings.Join(b, ", ")
}

// TestFixHabitPrograms: one run reaches the fixed point on each program (no
// proven fix is left in the final report). habits.mk loses let, mut and the
// semicolons in the parse round and is recased in the types round; what is
// left is E0802, whose fix is compiles (adding try changes the program) and
// is reported, not applied. closure.mk is valid (a capturing literal as a
// const fn argument), so it is clean with no round. from.mk
// gets E0601's minimal clause `from a, b` (proven on a private function)
// in one round, then clean.
// borrow.mk gets E0501's `from self.text` clause on the callee (proven on a
// private method) in one round, then clean. notry.mk ends at E0802 like habits.mk.
func TestFixHabitPrograms(t *testing.T) {
	for _, name := range []string{"habits.mk", "closure.mk", "notry.mk", "borrow.mk", "from.mk"} {
		t.Run(name, func(t *testing.T) {
			res, before, after := fixCopy(t, name, false)
			if res.Stopped != "clean" && res.Stopped != "no proven fix" {
				t.Fatalf("stopped: %s (chain %s)", res.Stopped, chain(res))
			}
			for _, e := range res.Final.Report.Diags {
				for _, f := range e.Diag.Fixes {
					if f.Class == diag.Proven && len(f.Edits) > 0 {
						t.Errorf("a proven fix is left: %v %s", e.Diag.Code, f.Why)
					}
				}
			}
			var left []string
			for _, e := range res.Final.Report.Diags {
				left = append(left, e.Diag.Code.String())
			}
			t.Logf("chain: [%s]; left: %v", chain(res), left)
			if name == "closure.mk" && (res.Stopped != "clean" || len(res.Rounds) != 0 || after != before) {
				t.Errorf("closure.mk: stopped %s after %d rounds, want clean with none", res.Stopped, len(res.Rounds))
			}
			if name == "from.mk" && (res.Stopped != "clean" || chain(res) != "1 types E0601" || !strings.Contains(after, "from a, b")) {
				t.Errorf("from.mk: stopped %s, chain [%s], file:\n%s", res.Stopped, chain(res), after)
			}
			if name == "borrow.mk" && (res.Stopped != "clean" || chain(res) != "1 flow E0501" || !strings.Contains(after, "): []u8 from self.text")) {
				t.Errorf("borrow.mk: stopped %s, chain [%s], file:\n%s", res.Stopped, chain(res), after)
			}
			if name != "habits.mk" {
				if len(res.Rounds) == 0 && after != before {
					t.Errorf("no round, but the file changed")
				}
				return
			}
			if got := chain(res); got != "1 parse E1207, 1 parse E1206, 2 types E0207, 2 types E0207" {
				t.Errorf("chain %s", got)
			}
			for _, want := range []string{"    var xs := List[i32].New(sys.arena)\n", "    xs.Push(1)\n", "(n: xs.Len())"} {
				if !strings.Contains(after, want) {
					t.Errorf("fixed file lacks %q:\n%s", want, after)
				}
			}
			if strings.Contains(after, ";") || len(left) != 1 || left[0] != "E0802" {
				t.Errorf("left %v, file:\n%s", left, after)
			}
		})
	}
}

// TestFixNeverAppliesCompiles: E0802's add-try fix is compiles; mk fix
// offers it and leaves the file byte for byte as it was. A dry run writes
// nothing either.
func TestFixNeverAppliesCompiles(t *testing.T) {
	res, before, after := fixCopy(t, "notry.mk", false)
	if after != before || len(res.Files) != 0 || len(res.Rounds) != 0 {
		t.Fatalf("notry.mk changed: rounds %d files %v", len(res.Rounds), res.Files)
	}
	d := res.Final.Report.Diags
	if len(d) != 1 || d[0].Diag.Code != diag.E0802 || len(d[0].Diag.Fixes) == 0 || d[0].Diag.Fixes[0].Class != diag.Compiles {
		t.Fatalf("final: %s", res.Final.Report.Text(res.Final.Sources))
	}
	res, before, after = fixCopy(t, "habits.mk", true)
	if after != before || len(res.Rounds) != 2 || len(res.Files) != 1 {
		t.Fatalf("dry run: rounds %d files %v changed %v", len(res.Rounds), res.Files, after != before)
	}
}

// TestFixAppliesE0711: a generic function named as a value whose expected fn
// type fixes every type argument gets them written (E0711's proven
// fix). One round, then clean.
func TestFixAppliesE0711(t *testing.T) {
	res, _, after := fixCopy(t, "generic.mk", false)
	if res.Stopped != "clean" || chain(res) != "1 types E0711" {
		t.Fatalf("stopped %s, chain %s", res.Stopped, chain(res))
	}
	if !strings.Contains(after, "f: fn(int): int = Id[int]\n") {
		t.Errorf("fixed file:\n%s", after)
	}
}
