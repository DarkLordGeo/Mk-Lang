// Why: the soundness tools run accepted and rejected units through
// CorpusSession.RunUnit, and every case it skips must say why, or a hole in
// the checker hides behind a skipped run.
// What: tests of each Skipped case of RunUnit, a plain and a tagged run of a
// small unit, a rejected unit run under Rejected, Forget, and runs over the
// stand-in and the embedded std.
// How: checks one-file units with soundCheck in a soundSession and compares
// the RunResult's fields.

package driver

import (
	"sync"
	"testing"

	"mk/internal/diag"
	"mk/internal/eval"
	"mk/internal/types"
)

// soundBlock is an accepted unit: a run through a shared reference, a
// truncated u8 parameter, a generic, a str parameter and a struct.
const soundBlock = `export Run(k: int): int {
    var n := k
    a := &n
    return *a + 1
}

Low(x: u8): u8 {
    return x
}

Two(a: int, b: int): int {
    return a * 10 + b
}

Same[T](x: T): T {
    return x
}

Len(s: str): int {
    return s.Len()
}

struct Box {
    v: int
}
`

// soundFrozen is rejected by stage 7 alone (E0502): it writes n while a's
// shared loan on n is live.
const soundFrozen = `export Run(k: int): int {
    var n := k
    a := &n
    n = 2
    return *a
}
`

// soundSession is a session over the stand-in std, which is flow-clean.
func soundSession(t *testing.T) *CorpusSession {
	t.Helper()
	c, err := NewCorpusSession(Config{StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	if c.Through() != diag.PhaseFlow {
		t.Fatalf("stand-in std: Through %v, want flow: %s", c.Through(), c.Std().Report.Text(c.Std().Sources))
	}
	return c
}

// soundCheck checks a one-file unit and returns its report.
func soundCheck(t *testing.T, c *CorpusSession, name, text string) UnitOutcome {
	t.Helper()
	return c.CheckUnit(UnitInput{Name: name, Files: []UnitFile{{"block.mk", text}}})
}

func wantSkipped(t *testing.T, what string, r RunResult, want string) {
	t.Helper()
	if r.Skipped != want {
		t.Errorf("%s: Skipped %q (err %+v), want %q", what, r.Skipped, r.Err, want)
	}
}

func TestSound_RunUnitPlainAndTagged(t *testing.T) {
	c := soundSession(t)
	if o := soundCheck(t, c, "sound/plain", soundBlock); len(o.Report.Diags) != 0 {
		t.Fatalf("diagnostics: %s", o.Report.Text(o.Sources))
	}
	plain := c.RunUnit(RunSpec{Unit: "sound/plain", Fn: "Run", Args: []uint64{41}})
	if plain.Err != nil || plain.Skipped != "" || plain.Value != 42 || plain.Steps == 0 {
		t.Fatalf("plain run: %+v", plain)
	}
	tagged := c.RunUnit(RunSpec{Unit: "sound/plain", Fn: "Run", Args: []uint64{41}, Tag: &TagOptions{Seed: 7}})
	if tagged.Err != nil || tagged.Skipped != "" || len(tagged.Violations) != 0 || tagged.Value != 42 {
		t.Fatalf("tagged run: %+v", tagged)
	}
	// 4.1.13: the tagged mode charges exactly the plain mode's steps.
	if tagged.Steps != plain.Steps {
		t.Errorf("steps: tagged %d, plain %d", tagged.Steps, plain.Steps)
	}
	// Each value is truncated to its parameter's type; two parameters in order.
	if r := c.RunUnit(RunSpec{Unit: "sound/plain", Fn: "Low", Args: []uint64{0x1ff}}); r.Err != nil || r.Value != 0xff {
		t.Errorf("Low(0x1ff): %+v", r)
	}
	if r := c.RunUnit(RunSpec{Unit: "sound/plain", Fn: "Two", Args: []uint64{4, 2}, Tag: &TagOptions{}}); r.Err != nil || r.Value != 42 {
		t.Errorf("Two(4, 2): %+v", r)
	}
	// Fuel: a run that cannot finish in one step stops with an error.
	if r := c.RunUnit(RunSpec{Unit: "sound/plain", Fn: "Run", Args: []uint64{1}, Fuel: 1}); r.Err == nil {
		t.Errorf("fuel 1: %+v", r)
	}
	// Concurrent runs of one unit agree.
	var wg sync.WaitGroup
	outs := make([]RunResult, 8)
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var tag *TagOptions
			if i%2 == 1 {
				tag = &TagOptions{Seed: uint64(i)}
			}
			outs[i] = c.RunUnit(RunSpec{Unit: "sound/plain", Fn: "Run", Args: []uint64{uint64(i)}, Tag: tag})
		}(i)
	}
	wg.Wait()
	for i, r := range outs {
		if r.Err != nil || r.Skipped != "" || r.Value != uint64(i)+1 {
			t.Errorf("concurrent run %d: %+v", i, r)
		}
	}
}

func TestSound_RunUnitSkipped(t *testing.T) {
	c := soundSession(t)
	if o := soundCheck(t, c, "sound/a", soundBlock); len(o.Report.Diags) != 0 {
		t.Fatalf("diagnostics: %s", o.Report.Text(o.Sources))
	}
	// "no unit": a name never checked, a ParseOnly unit.
	wantSkipped(t, "unknown unit", c.RunUnit(RunSpec{Unit: "sound/none", Fn: "Run", Args: []uint64{1}}), skipNoUnit)
	c.CheckUnit(UnitInput{Name: "sound/parse", ParseOnly: true, Files: []UnitFile{{"block.mk", soundBlock}}})
	wantSkipped(t, "parse-only unit", c.RunUnit(RunSpec{Unit: "sound/parse", Fn: "Run", Args: []uint64{1}}), skipNoUnit)
	// A repeated name is an E0000 for the second check; the first unit's
	// record stands (first wins).
	if o := soundCheck(t, c, "sound/a", soundBlock); len(o.Report.Diags) != 1 || o.Report.Diags[0].Diag.Code != diag.E0000 {
		t.Fatalf("repeated name: %s", o.Report.Text(o.Sources))
	}
	if r := c.RunUnit(RunSpec{Unit: "sound/a", Fn: "Run", Args: []uint64{1}}); r.Skipped != "" || r.Value != 2 {
		t.Errorf("after a repeated name: %+v", r)
	}
	// "no function": no such item, an item that is not a function.
	wantSkipped(t, "missing function", c.RunUnit(RunSpec{Unit: "sound/a", Fn: "Nope"}), skipNoFunction)
	wantSkipped(t, "a struct", c.RunUnit(RunSpec{Unit: "sound/a", Fn: "Box"}), skipNoFunction)
	// "generic".
	wantSkipped(t, "generic", c.RunUnit(RunSpec{Unit: "sound/a", Fn: "Same", Args: []uint64{1}}), skipGeneric)
	// "parameter type": a str parameter; too few and too many values.
	wantSkipped(t, "str parameter", c.RunUnit(RunSpec{Unit: "sound/a", Fn: "Len", Args: []uint64{1}}), skipParamType)
	wantSkipped(t, "too few", c.RunUnit(RunSpec{Unit: "sound/a", Fn: "Two", Args: []uint64{1}}), skipParamType)
	wantSkipped(t, "too many", c.RunUnit(RunSpec{Unit: "sound/a", Fn: "Run", Args: []uint64{1, 2}}), skipParamType)
	// "rejected": a unit stage 7 rejects, run plain or tagged without
	// Rejected; a unit with a types diagnostic even under Rejected.
	o := soundCheck(t, c, "sound/frozen", soundFrozen)
	if len(o.Report.Diags) == 0 {
		t.Fatalf("frozen: no diagnostic")
	}
	for _, e := range o.Report.Diags {
		if e.Diag.Phase != diag.PhaseFlow {
			t.Fatalf("frozen: a %v diagnostic: %s", e.Diag.Phase, o.Report.Text(o.Sources))
		}
	}
	wantSkipped(t, "rejected, plain", c.RunUnit(RunSpec{Unit: "sound/frozen", Fn: "Run", Args: []uint64{1}}), skipRejected)
	wantSkipped(t, "rejected, tagged", c.RunUnit(RunSpec{Unit: "sound/frozen", Fn: "Run", Args: []uint64{1}, Tag: &TagOptions{}}), skipRejected)
	o = soundCheck(t, c, "sound/types", "export Run(k: int): int {\n    return Missing()\n}\n")
	if len(o.Report.Diags) == 0 {
		t.Fatalf("types: no diagnostic")
	}
	wantSkipped(t, "types error under Rejected", c.RunUnit(RunSpec{Unit: "sound/types", Fn: "Run", Args: []uint64{1}, Tag: &TagOptions{Rejected: true}}), skipRejected)
	// "not lowered": a session judged only through parse (no std.core).
	m1, err := NewCorpusSession(Config{StdDir: m1Std(t)})
	if err != nil {
		t.Fatal(err)
	}
	m1.CheckUnit(UnitInput{Name: "sound/m1", Files: []UnitFile{{"block.mk", soundBlock}}})
	wantSkipped(t, "through parse", m1.RunUnit(RunSpec{Unit: "sound/m1", Fn: "Run", Args: []uint64{1}}), skipNotLowered)
}

// TestSound_RunUnitRejected: under Rejected the rejected body runs; the run
// either completes or finds the removed loan, and is never Skipped
// "rejected". The plain evaluator still sees the body poisoned.
func TestSound_RunUnitRejected(t *testing.T) {
	c := soundSession(t)
	soundCheck(t, c, "sound/frozen", soundFrozen)
	r := c.RunUnit(RunSpec{Unit: "sound/frozen", Fn: "Run", Args: []uint64{1}, Tag: &TagOptions{Rejected: true}})
	if r.Skipped != "" {
		t.Fatalf("Rejected run skipped %q (err %+v)", r.Skipped, r.Err)
	}
	if len(r.Violations) == 0 && (r.Err != nil || r.Value != 2) {
		t.Fatalf("Rejected run: neither a violation nor a completed run: %+v", r)
	}
	for _, v := range r.Violations {
		t.Logf("violation: %s", v.String(c.w.sm))
		if v.Std {
			t.Errorf("violation in std: %+v", v)
		}
	}
	// The session's own evaluator keeps serving the body poisoned (check
	// shares it): a plain Eval through it is poison.
	id, ok := c.w.items.ByKey(types.ItemKey("sound/frozen", "Run"))
	if !ok {
		t.Fatal("no item")
	}
	if b := (&program{w: c.w, s: c.w.sem5}).Body(id, nil); b.State != eval.BodyPoisoned {
		t.Errorf("session program serves the rejected body: state %d", b.State)
	}
}

func TestSound_Forget(t *testing.T) {
	c := soundSession(t)
	for _, n := range []string{"sound/f#1", "sound/f#2"} {
		if o := c.CheckUnit(UnitInput{Name: n, Files: []UnitFile{
			{"block.mk", "import util\n\n" + soundBlock + "\nUse(): int {\n    return util.U()\n}\n"},
			{"util/u.mk", "export U(): int {\n    return 1\n}\n"},
		}}); len(o.Report.Diags) != 0 {
			t.Fatalf("%s: %s", n, o.Report.Text(o.Sources))
		}
	}
	keep := c.RunUnit(RunSpec{Unit: "sound/f#2", Fn: "Use"})
	if keep.Skipped != "" || keep.Value != 1 {
		t.Fatalf("before Forget: %+v", keep)
	}
	run, _ := c.w.items.ByKey(types.ItemKey("sound/f#1", "Run"))
	u, _ := c.w.items.ByKey(types.ItemKey("sound/f#1/util", "U"))
	if c.w.sem5.body(run) == nil || c.w.sem5.body(u) == nil {
		t.Fatal("no bodies before Forget")
	}
	c.Forget([]string{"sound/f#1"})
	c.Forget(nil)
	wantSkipped(t, "forgotten", c.RunUnit(RunSpec{Unit: "sound/f#1", Fn: "Run", Args: []uint64{1}}), skipNoUnit)
	if c.w.sem5.body(run) != nil || c.w.sem5.body(u) != nil {
		t.Error("Forget kept the unit's bodies")
	}
	if r := c.RunUnit(RunSpec{Unit: "sound/f#2", Fn: "Use"}); r.Skipped != "" || r.Value != 1 {
		t.Errorf("another unit after Forget: %+v", r)
	}
}

func TestSound_OfUnits(t *testing.T) {
	set := map[string]bool{"doc/reference.md#17": true, "u": true}
	for path, want := range map[string]bool{
		"doc/reference.md#17":                true,
		"doc/reference.md#17/util":           true,
		"doc/reference.md#17/a/b":            true,
		"doc/reference.md#1":                 false,
		"doc/reference.md#170":               false,
		"design":                             false,
		"u/x":                                true,
		"ux":                                 false,
		"tests/conformance/codes/E0205/main": false,
	} {
		if got := ofUnits(path, set); got != want {
			t.Errorf("ofUnits(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestSound_ConstBits(t *testing.T) {
	for _, c := range []struct {
		b    []byte
		want uint64
	}{{nil, 0}, {[]byte{0xff}, 0xff}, {[]byte{1, 2}, 0x0201}, {[]byte{1, 0, 0, 0, 0, 0, 0, 0x80, 9}, 0x8000000000000001}} {
		if got := constBits(c.b); got != c.want {
			t.Errorf("constBits(%x) = %#x, want %#x", c.b, got, c.want)
		}
	}
}

// TestSound_EmbeddedStd runs over the embedded std. While std is not
// flow-clean, Through is before flow and every run is "not lowered" (a
// rejected body would be stored ready); once it is, the unit runs.
func TestSound_EmbeddedStd(t *testing.T) {
	c, err := NewCorpusSession(Config{})
	if err != nil {
		t.Fatal(err)
	}
	soundCheck(t, c, "sound/std", soundBlock)
	r := c.RunUnit(RunSpec{Unit: "sound/std", Fn: "Run", Args: []uint64{41}, Tag: &TagOptions{}})
	if c.Through() < diag.PhaseFlow {
		t.Logf("embedded std: Through %v", c.Through())
		wantSkipped(t, "Through before flow", r, skipNotLowered)
		return
	}
	if r.Skipped != "" || r.Err != nil || len(r.Violations) != 0 || r.Value != 42 {
		t.Errorf("embedded std: %+v", r)
	}
}
