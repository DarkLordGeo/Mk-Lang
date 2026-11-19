// Why: the tagged evaluator mode is the soundness test's detector of dead
// reads, and it must be shown to fire on unsound code and stay quiet on
// sound code.
// What: TestCalibration, TestSound_ConformanceTagged,
// TestSoundGen_HeaderTaggedClean and TestSound_ErrKindsMatchTypes.
// How: runs model, conformance and header programs in the tagged mode with
// fixed Cond values and seeds.
//
// TestCalibration (the model's calibration programs report an access
// to a dead location), TestSound_ConformanceTagged (every accepted
// conformance unit of model/, rules/ and memory/ runs clean) and
// TestSoundGen_HeaderTaggedClean (each header helper, called from a
// one-line entry, runs clean); and TestSound_ErrKindsMatchTypes
// (SoundRunOn's words for eval's error kinds).

package corpus

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"mk/internal/driver"
	"mk/internal/types"
)

func TestSound_ErrKindsMatchTypes(t *testing.T) {
	for k, w := range map[types.EvalErrorKind]string{types.EvalFuel: "fuel", types.EvalPanic: "panic", types.EvalRefused: "refused", types.EvalStage: "stage", types.EvalCycle: "cycle"} {
		if int(k) >= len(evalErrKinds) || evalErrKinds[k] != w {
			t.Errorf("EvalErrorKind %d spells %q, want %q", k, evalErrKinds[min(int(k), len(evalErrKinds)-1)], w)
		}
	}
}

// calibUnits loads the conformance units of the named suites under the
// session-unique prefix, so the same file can be checked by two tests of
// one session.
func calibUnits(t *testing.T, prefix string, suites ...string) ([]Unit, string) {
	t.Helper()
	root := repoRoot(t)
	us, err := Units(root)
	if err != nil {
		t.Fatal(err)
	}
	prelude, err := Prelude(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []Unit
	for _, u := range us {
		for _, s := range suites {
			if strings.HasPrefix(u.Name, conformanceDir+"/"+s+"/") {
				u.Name = prefix + "/" + u.Name
				out = append(out, u)
			}
		}
	}
	return out, prelude
}

// calibSeeds are the four seeds every calibration run takes beside Cond 1
// and -1.
var calibSeeds = []uint64{1, 2, 3, 4}

// calibRuns is the tag options of one calibration entry: Cond 1, Cond -1,
// then calibSeeds with Cond drawn from the seed.
func calibRuns(rejected bool) []SoundTag {
	out := []SoundTag{{Cond: 1, Seed: 1, Rejected: rejected}, {Cond: -1, Seed: 1, Rejected: rejected}}
	for _, s := range calibSeeds {
		out = append(out, SoundTag{Seed: s, Rejected: rejected})
	}
	return out
}

// TestCalibration: each calibration program of the
// model, rejected by the loan rules, run with Rejected and Cond 1, reports
// an access to a dead location (VDead or VDeadScope); R7.Replace, accepted,
// runs clean with Cond 1, -1 and four seeds.
func TestCalibration(t *testing.T) {
	s := soundSess(t)
	units, prelude := calibUnits(t, fmt.Sprintf("sound/calib%d", soundUnitN.Add(1)), "model")
	byFile := map[string]*Unit{}
	for i := range units {
		byFile[filepath.Base(units[i].Name)] = &units[i]
	}
	cases := []struct {
		file, entry string
		// heap: the entry takes &Heap, which the soundness host does not
		// give; RunUnit skips it as "parameter type".
		heap bool
	}{
		{"R6.Hole.mk", "Hole", false},
		{"R11.Slot.mk", "Caller", false},
		{"X.Collapse.Mut.mk", "Caller", false},
		{"X.Collapse.Cell.mk", "Caller", false},
		{"H66.local.mk", "Caller", false},
		{"H72.mk", "Caller", false},
		{"H71.mk", "Bad", true},
	}
	for _, c := range cases {
		u := byFile[c.file]
		if u == nil {
			t.Fatalf("%s: no such model unit", c.file)
		}
		out := s.CheckUnit(ToUnitInput(u, prelude, driver.UnitCheck))
		if len(out.Report.Diags) == 0 {
			t.Errorf("%s: checks clean, want its rejection", c.file)
			continue
		}
		r := SoundRunOn(s, SoundRunSpec{Unit: u.Name, Fn: c.entry, Tag: &SoundTag{Cond: 1, Seed: 1, Rejected: true}})
		switch {
		case c.heap && r.Skipped == "parameter type":
			t.Logf("%s %s: not run, its &Heap parameter has no host value (S-3)", c.file, c.entry)
		case len(r.Violations) == 0:
			t.Errorf("%s %s: no violation (skipped %q, err %s %s), want VDead or VDeadScope", c.file, c.entry, r.Skipped, r.ErrKind, r.ErrText)
		case r.Violations[0].Kind != "VDead" && r.Violations[0].Kind != "VDeadScope":
			t.Errorf("%s %s: %s, want VDead or VDeadScope", c.file, c.entry, r.Violations[0].Text)
		default:
			t.Logf("%s %s: %s", c.file, c.entry, r.Violations[0].Text)
		}
	}
	u := byFile["R7.Replace.mk"]
	if u == nil {
		t.Fatal("R7.Replace.mk: no such model unit")
	}
	if out := s.CheckUnit(ToUnitInput(u, prelude, driver.UnitCheck)); len(out.Report.Diags) > 0 {
		t.Fatalf("R7.Replace.mk: %d diagnostics, want none", len(out.Report.Diags))
	}
	for _, tag := range calibRuns(false) {
		r := SoundRunOn(s, SoundRunSpec{Unit: u.Name, Fn: "Replace", Tag: &tag})
		if len(r.Violations) > 0 || r.Skipped != "" || r.ErrKind != "" {
			t.Errorf("R7.Replace Replace %+v: violations %v, skipped %q, err %s %s; want a clean run", tag, r.Violations, r.Skipped, r.ErrKind, r.ErrText)
		}
	}
}

// topFns finds the candidate entries of a unit's root files: a line that
// starts with a capitalised name and "(" is a function declaration (types
// start with a keyword, methods with Type.Name).
var topFns = regexp.MustCompile(`(?m)^(?:export\s+)?([A-Z][A-Za-z0-9_]*)\(`)

// runEntry runs fn of unit with tag, giving each integer parameter the value
// 1: RunUnit says "parameter type" both for a parameter it cannot fill and
// for a wrong count, so counts 0 to 6 are tried in turn. ok is false when no
// count fits (a parameter that is not an integer, Io or &Arena). fuel 0 is
// types.DefaultFuel.
func runEntry(s *driver.CorpusSession, unit, fn string, tag SoundTag, fuel uint64) (SoundRunResult, bool) {
	for n := 0; n <= 6; n++ {
		args := make([]uint64, n)
		for i := range args {
			args[i] = 1
		}
		r := SoundRunOn(s, SoundRunSpec{Unit: unit, Fn: fn, Args: args, Fuel: fuel, Tag: &tag})
		if r.Skipped != "parameter type" {
			return r, r.Skipped != "no function" && r.Skipped != "generic"
		}
	}
	return SoundRunResult{}, false
}

// TestSound_ConformanceTagged: every function of the root
// module of every accepted unit of model/, rules/ and memory/ whose
// parameters are integers, Io or &Arena runs with Cond 1, -1 and four seeds
// with no violation. Skipped runs and evaluator errors are counted for the
// triage, not judged: these units are written for the checker, not as
// complete programs.
func TestSound_ConformanceTagged(t *testing.T) {
	s := soundSess(t)
	units, prelude := calibUnits(t, fmt.Sprintf("sound/conf%d", soundUnitN.Add(1)), "model", "rules", "memory")
	var nUnits, nFns, nRuns, nClean int
	skips := map[string]int{}
	errs := map[string]int{}
	var examples []string
	for i := range units {
		u := &units[i]
		if u.Verdict != VerdictOK {
			continue
		}
		out := s.CheckUnit(ToUnitInput(u, prelude, driver.UnitCheck))
		if len(out.Report.Diags) > 0 {
			continue // judged by the corpus, not here
		}
		nUnits++
		seen := map[string]bool{}
		for _, f := range u.Files {
			if strings.Contains(f.Path, "/") {
				continue // a subdirectory's module, not the root's
			}
			for _, m := range topFns.FindAllStringSubmatch(f.Text, -1) {
				fn := m[1]
				if seen[fn] {
					continue
				}
				seen[fn] = true
				ran := false
				for _, tag := range calibRuns(false) {
					r, ok := runEntry(s, u.Name, fn, tag, 0)
					if !ok {
						break
					}
					ran = true
					nRuns++
					switch {
					case len(r.Violations) > 0:
						t.Errorf("%s %s (Cond %d, seed %d): %s", u.Name[strings.Index(u.Name, conformanceDir)+len(conformanceDir)+1:], fn, tag.Cond, tag.Seed, r.Violations[0].Text)
					case r.Skipped != "":
						skips[r.Skipped]++
					case r.ErrKind != "":
						key := r.ErrKind + " " + r.ErrText
						if errs[key] == 0 {
							examples = append(examples, fmt.Sprintf("%s: %s %s", key, u.Name[strings.Index(u.Name, conformanceDir)+len(conformanceDir)+1:], fn))
						}
						errs[key]++
					default:
						nClean++
					}
				}
				if ran {
					nFns++
				}
			}
		}
	}
	if nRuns == 0 {
		t.Fatal("no conformance function ran")
	}
	t.Logf("%d accepted units, %d functions, %d runs: %d clean, skipped %s, errors %s", nUnits, nFns, nRuns, nClean, countTable(skips), countTable(errs))
	for _, e := range examples {
		t.Logf("  first of %s", e)
	}
}

// countTable spells a count map in key order.
func countTable(m map[string]int) string {
	if len(m) == 0 {
		return "none"
	}
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var b strings.Builder
	for i, k := range ks {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q %d", k, m[k])
	}
	return b.String()
}

// TestSoundGen_HeaderTaggedClean: each helper of the
// generator's header, called from a one-line entry, runs clean in the
// tagged mode.
func TestSoundGen_HeaderTaggedClean(t *testing.T) {
	s := soundSess(t)
	for _, h := range headerEntries {
		text := soundHeader + "\n" + h.entry + "\n"
		name := fmt.Sprintf("sound/header%d", soundUnitN.Add(1))
		out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: text}}})
		if len(out.Report.Diags) > 0 {
			d := out.Report.Diags[0].Diag
			p := out.Sources.Position(d.At.StartPos())
			t.Errorf("%s: %d diagnostics, first %d:%d %s %s", h.name, len(out.Report.Diags), p.Line, p.Col, d.Code, d.Message)
			continue
		}
		for _, k := range []uint64{0, 1, 7} {
			r := SoundRunOn(s, SoundRunSpec{Unit: name, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k}})
			if len(r.Violations) > 0 || r.Skipped != "" || r.ErrKind != "" {
				t.Errorf("%s (k %d): violations %v, skipped %q, err %s %s; want a clean run", h.name, k, r.Violations, r.Skipped, r.ErrKind, r.ErrText)
			}
		}
	}
}

// headerEntries call each function of the header once from Run, the
// generator's entry form.
var headerEntries = []struct{ name, entry string }{
	{"Put", `Run(var k: i64): i64 {
    a: i64 = 3
    b: i64 = 4
    c := Cell.New(&a)
    Put(&c, &b)
    return *c.Load()
}`},
	{"SetRef", `Run(var k: i64): i64 {
    a: i64 = 3
    b: i64 = 4
    var r := &a
    SetRef(mut r, &b)
    return *r
}`},
	{"Deep.Mix", `Run(var k: i64): i64 {
    a: i64 = 3
    b: i64 = 4
    var r := &b
    var d := Deep(a: &a, b: mut r)
    d.Mix()
    return *d.a
}`},
	{"D2.Mix2", `Run(var k: i64): i64 {
    a: i64 = 3
    b: i64 = 4
    c := Cell.New(&b)
    var e := D2(a: &a, c: &c)
    e.Mix2()
    return *c.Load()
}`},
	{"Id", `Run(var k: i64): i64 {
    a: i64 = 3
    return *Id(&a)
}`},
	{"Pick", `Run(var k: i64): i64 {
    a: i64 = 3
    b: i64 = 4
    return *Pick(k, &a, &b)
}`},
	{"Hold", `Run(var k: i64): i64 {
    a: i64 = 3
    c := Cell.New(&a)
    return *Hold(&c)
}`},
	{"PeekS", `Run(var k: i64): i64 {
    var v: i64 = 3
    var s := Sl(m: mut v)
    var r := PeekS(&s)
    return *r
}`},
	{"Cur", `Run(var k: i64): i64 {
    a: i64 = 3
    var l := Lx(src: &a, pos: 0)
    var r := Cur(mut l)
    return *r
}`},
	{"Src", `Run(var k: i64): i64 {
    a: i64 = 3
    var l := Lx(src: &a, pos: 0)
    var r := Src(&l)
    return *r
}`},
	{"Own", `Run(var k: i64): i64 {
    a: i64 = 3
    var l := Lx(src: &a, pos: 5)
    var r := Own(&l)
    return *r
}`},
	{"Bump", `Run(var k: i64): i64 {
    var x: i64 = 3
    Bump(mut x)
    return x
}`},
	{"Sum", `Run(var k: i64): i64 {
    var xs: [3]i64 = [1, 2, 3]
    var x: i64 = 0
    Sum(xs[..], mut x)
    return x
}`},
	{"Read", `Run(var k: i64): i64 {
    a: i64 = 3
    var x: i64 = 1
    Read(&a, mut x)
    return x
}`},
	{"Spawner", `Run(var k: i64, io: Io): i64 {
    var x: i64 = 3
    scope s := io.Scope() {
        Spawner(&s, mut x)
    }
    return x
}`},
	{"Two", `Run(var k: i64): i64 {
    a: i64 = 3
    var b: i64 = 1
    Two(&a, mut b)
    return b
}`},
	{"Swap2", `Run(var k: i64): i64 {
    a: i64 = 3
    b: i64 = 4
    var p := &a
    var q := &b
    Swap2(mut p, mut q)
    return *p
}`},
	{"Set2", `Run(var k: i64): i64 {
    a: i64 = 3
    b: i64 = 4
    var r := &a
    var d := Deep(a: &a, b: mut r)
    Set2(mut d, &b)
    return *r
}`},
	{"Find", `Run(var k: i64): i64 {
    a: i64 = 3
    if r := Find(k, &a) {
        return *r
    }
    return 0
}`},
}
