// Why: the corpus and the gate trust driver.Available() to say which stages
// run, and a test that cannot run must not pass silently under the gate.
// What: tests of Available(), of the instance-limit walk a corpus unit gets
// once Through reaches types, and of notRun under MK_GATE=1.
// How: compares Available() with the full stage list, checks three units for
// their E0708 lines under Through flow and Through types, and drives notRun
// through a fake testing.TB.

package driver

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/diag"
)

// TestAvailableEveryStage: Available() is every stage, exactly:
// StageInstances is there, so an ok unit's need (corpus okNeed: stages 1 to
// 7b and the instance walk) is wired; StageCodegen and StageNative are there
// because mk build and mk run run them (native.go), whether or not another
// error stops a given program; StageGen (gen.go) and StageIface (the caches)
// are wired too. With every stage wired, the corpus allows no pending unit.
func TestAvailableEveryStage(t *testing.T) {
	want := []Stage{StageManifest, StageLoad, StageParse, StageResolve, StageGen, StageSignatures, StageBodies,
		StageFlow, StageIface, StageInstances, StageCodegen, StageNative}
	got := Available().Stages()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Available() = %v, want %v", got, want)
	}
	for st := StageNone + 1; st < numStages; st++ {
		if !Available().Has(st) {
			t.Errorf("Available() lacks %v; every stage is wired", st)
		}
	}
	okNeed := StageSet(0).With(StageLoad, StageParse, StageResolve, StageSignatures, StageBodies, StageFlow, StageInstances)
	if Available()&okNeed != okNeed {
		t.Errorf("an ok unit's need %v is not wired in %v", okNeed.Stages(), got)
	}
}

// walkUnits are three units for TestCheckUnitWalksThroughTypes, with the
// E0708 line each must report ("" for none) when stage 7's verdict is
// trusted (Through flow) and when it is not (Through types).
var walkUnits = []struct {
	name, src  string
	flow, typs string
}{
	// tests/conformance/codes/E0708.mk.
	{"walk-e0708", nest + "\nStart(): int {\n    return Nest(1, 3)\n}\n", "block.mk:9:", "block.mk:9:"},
	// Finite instantiation: the walk adds nothing.
	{"walk-clean", "One[T: Plain](x: T): int {\n    return 1\n}\n\nTwo(): int {\n    return One(true) + One(2)\n}\n", "", ""},
	// Twice's body is rejected by stage 7 (a linear value closed twice): it
	// is poisoned with Through flow, and with Through types stage 7's
	// silent verdict makes it a leaf too, so neither walk enters it and
	// neither reports its Nest chain. Through flow reports the flow error.
	{"walk-leaf", nest + "\nlinear struct R {\n    v: int\n}\n\nR.Close(self) {\n    R(v: _) := self\n}\n\n" +
		"Twice(r: R): int {\n    r.Close()\n    r.Close()\n    return Nest(1, 3)\n}\n", "", ""},
}

// TestCheckUnitWalksThroughTypes: a corpus unit gets the instance-limit walk
// over its limit roots once Through reaches types. With Through flow, after
// stage 7. With Through types (std has stage-7 diagnostics, world.noFlow),
// stage 7 runs on the unit silently, for borrow's guard decisions, and a
// body it rejects is a leaf. Before this, a Through-types session ran no
// walk, so codes/E0708.mk was judged FAIL "emitted nothing" once
// StageInstances was available. No case is an E0000, and StageInstances is
// never in Skipped.
func TestCheckUnitWalksThroughTypes(t *testing.T) {
	for _, mode := range []string{"flow", "types"} {
		t.Run(mode, func(t *testing.T) {
			c, err := NewCorpusSession(Config{})
			if err != nil {
				t.Fatal(err)
			}
			if c.Through() != diag.PhaseFlow {
				t.Fatalf("Through %v, want flow (std is flow-clean)\n%s", c.Through(), c.Std().Report.Text(c.Std().Sources))
			}
			if mode == "types" {
				// As NewCorpusSession leaves a session whose std has a
				// stage-7 diagnostic, set before any unit runs.
				c.through, c.w.noFlow = diag.PhaseTypes, true
			}
			for _, u := range walkUnits {
				out := c.CheckUnit(UnitInput{Name: u.name, Files: []UnitFile{{"block.mk", u.src}}})
				var e0708, other []string
				for _, e := range out.Report.Diags {
					l := diag.Location(out.Sources, e.Diag.At) + ":" + e.Diag.Code.String()
					if e.Diag.Code == diag.E0708 {
						e0708 = append(e0708, l)
					} else {
						other = append(other, l)
					}
				}
				want := u.flow
				if mode == "types" {
					want = u.typs
				}
				text := out.Report.Text(out.Sources)
				switch {
				case want == "" && len(e0708) != 0:
					t.Errorf("%s: E0708 %v, want none\n%s", u.name, e0708, text)
				case want != "" && (len(e0708) != 1 || !strings.Contains(e0708[0], "/"+want)):
					t.Errorf("%s: E0708 %v, want one at %s\n%s", u.name, e0708, want, text)
				}
				for _, l := range other {
					flowErr := u.name == "walk-leaf" && mode == "flow" && !strings.HasSuffix(l, ":E0000")
					if !flowErr {
						t.Errorf("%s: unexpected %s\n%s", u.name, l, text)
					}
				}
				if u.name == "walk-leaf" && mode == "flow" && len(other) == 0 {
					t.Errorf("%s: no stage-7 diagnostic with Through flow\n%s", u.name, text)
				}
				if out.Skipped.Has(StageInstances) {
					t.Errorf("%s: Skipped %v", u.name, out.Skipped.Stages())
				}
			}
		})
	}
}

// fakeTB records how notRun stopped a test.
type fakeTB struct {
	testing.TB
	skipped, failed string
}

func (f *fakeTB) Helper()                        {}
func (f *fakeTB) Skipf(format string, a ...any)  { f.skipped = fmt.Sprintf(format, a...) }
func (f *fakeTB) Fatalf(format string, a ...any) { f.failed = fmt.Sprintf(format, a...) }

// TestNotRunFailsUnderGate: a test that cannot run here (no LLVM, no runner,
// a known blocker elsewhere) is a SKIP outside the gate and a failure with
// the same text under MK_GATE=1, so MK_GATE=1 go test ./internal/driver
// cannot report ok while its native tests did not run.
func TestNotRunFailsUnderGate(t *testing.T) {
	const why = "BLOCKED: codegen"
	for _, gate := range []string{"", "1"} {
		t.Setenv("MK_GATE", gate)
		f := &fakeTB{}
		notRun(f, "%s", why)
		if gate == "1" {
			if f.skipped != "" || !strings.Contains(f.failed, why) {
				t.Errorf("MK_GATE=1: skipped %q failed %q, want a failure naming %q", f.skipped, f.failed, why)
			}
		} else if f.failed != "" || f.skipped != why {
			t.Errorf("no gate: skipped %q failed %q, want the skip %q", f.skipped, f.failed, why)
		}
	}
}
