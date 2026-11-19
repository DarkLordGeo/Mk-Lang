// Why: the soundness runner's bookkeeping (budgets, the oracle hand-over,
// session recycling, error counts) must be right whatever state std is in.
// What: TestSound_BudgetCutNotJudged, TestSound_OracleHandoff,
// TestSound_RejectForgetsAndRecycles, TestSound_RejectErroredNotClean,
// TestSoundRejectCountsICE and TestSound_RunICESeeds.
// How: a scripted SoundCompiler (scriptSound) with no compiler behind it
// gives fixed outcomes per program index.

package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/driver"
)

// scriptSound is a SoundCompiler with no compiler behind it: Check rejects
// a unit (one E0501 in phase flow) when reject says so and accepts it
// otherwise; RunUnit is run's, or a clean run; it counts the Check calls
// since the last Recycle and every unit Forget drops.
type scriptSound struct {
	reject func(unit string) bool
	ice    func(unit string) bool // the check crashes: one E0000, before reject is asked
	run    func(spec SoundRunSpec) SoundRunResult

	mu       sync.Mutex
	checked  []string
	forgot   map[string]bool
	since    int
	maxSince int
	recycles int
}

func (f *scriptSound) Start() error { return nil }

func (f *scriptSound) Check(unit, text string) driver.UnitOutcome {
	f.mu.Lock()
	f.checked = append(f.checked, unit)
	f.since++
	f.maxSince = max(f.maxSince, f.since)
	f.mu.Unlock()
	var out driver.UnitOutcome
	if f.ice != nil && f.ice(unit) {
		out.Report = diag.Report{Phase: diag.PhaseFlow, HasPhase: true,
			Diags: []diag.Emitted{{Diag: &diag.Diagnostic{Code: diag.E0000, Phase: diag.PhaseFlow, Message: "scripted crash"}}}}
		return out
	}
	if f.reject != nil && f.reject(unit) {
		out.Report = diag.Report{Phase: diag.PhaseFlow, HasPhase: true,
			Diags: []diag.Emitted{{Diag: &diag.Diagnostic{Code: diag.E0501, Phase: diag.PhaseFlow, Message: "scripted"}}}}
	}
	return out
}

func (f *scriptSound) RunUnit(spec SoundRunSpec) SoundRunResult {
	if f.run != nil {
		return f.run(spec)
	}
	return SoundRunResult{Value: spec.Args[0]}
}

func (f *scriptSound) Forget(units []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.forgot == nil {
		f.forgot = map[string]bool{}
	}
	for _, u := range units {
		f.forgot[u] = true
	}
}

func (f *scriptSound) Recycle() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recycles++
	f.since = 0
	return nil
}

// unitIndex is i of unit name sound/<label>/<i>.
func unitIndex(unit string) int {
	var i int
	fmt.Sscanf(unit[strings.LastIndexByte(unit, '/')+1:], "%d", &i)
	return i
}

// TestSound_BudgetCutNotJudged: when the bound ends while a program's run
// seeds are in progress, the program is not judged: the report counts only
// programs that ran on every seed, so Runs is Seeds times Accepted.
func TestSound_BudgetCutNotJudged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int64
	f := &scriptSound{run: func(spec SoundRunSpec) SoundRunResult {
		if calls.Add(1) == 9 {
			cancel() // two programs judged on 4 seeds; the third is cut after one run
		}
		return SoundRunResult{Value: spec.Args[0]}
	}}
	cfg := SoundConfig{Profile: ProfileCore, Base: 91, Budget: time.Hour, Seeds: 4, Workers: 1, Label: fmt.Sprintf("cut-%d", soundUnitN.Add(1))}
	rep, err := RunSound(ctx, cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Accepted != 2 || rep.Runs != 4*rep.Accepted || rep.Generated != 2 || rep.Stopped != "budget" {
		t.Fatalf("generated %d, accepted %d, runs %d, stopped %q; want 2, 2, 8 and budget (the cut program not judged)",
			rep.Generated, rep.Accepted, rep.Runs, rep.Stopped)
	}
}

// TestSound_OracleHandoff: a run with Oracle hands the accepted programs at
// accepted index 0, Oracle, 2·Oracle, ... to the oracle, in index order,
// whatever the workers, and its record's oracle section names them; the
// reject profile hands none.
func TestSound_OracleHandoff(t *testing.T) {
	const ran = `{"ran":true}`
	for _, w := range []int{1, 4} {
		f := &scriptSound{reject: func(unit string) bool { return unitIndex(unit)%3 == 0 }}
		var got []SoundProgram
		cfg := SoundConfig{Profile: ProfileFull, Base: 92, Generated: 40, Seeds: 1, Workers: w, Oracle: 4, Label: fmt.Sprintf("hand-%d", soundUnitN.Add(1)),
			OracleSink: func(progs []SoundProgram) (json.RawMessage, error) {
				got = progs
				return json.RawMessage(ran), nil
			}}
		rep, err := RunSound(context.Background(), cfg, f)
		if err != nil {
			t.Fatal(err)
		}
		var want []int
		accepted := 0
		for i := range 40 {
			if i%3 != 0 {
				if accepted%4 == 0 {
					want = append(want, i)
				}
				accepted++
			}
		}
		if rep.Accepted != accepted || len(got) != len(want) {
			t.Fatalf("workers %d: accepted %d, %d handed; want %d and %d", w, rep.Accepted, len(got), accepted, len(want))
		}
		for k, i := range want {
			if got[k].Seed != ProgramSeed(92, i) || got[k].Profile != ProfileFull {
				t.Errorf("workers %d: handed program %d has seed %#x, want program %d's %#x", w, k, got[k].Seed, i, ProgramSeed(92, i))
			}
		}
		var h OracleHandoff
		if err := json.Unmarshal(rep.Oracle, &h); err != nil {
			t.Fatalf("oracle section %s: %v", rep.Oracle, err)
		}
		if h.Every != 4 || h.Programs != len(want) || !slices.Equal(h.First, want) || string(h.Result) != ran {
			t.Errorf("workers %d: oracle section %s, want every 4, programs %v", w, rep.Oracle, want)
		}
	}
	cfg := SoundConfig{Profile: ProfileReject, Base: 92, Generated: 10, Seeds: 1, Workers: 1, Oracle: 1, Label: fmt.Sprintf("hand-%d", soundUnitN.Add(1))}
	rep, err := RunSound(context.Background(), cfg, &scriptSound{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Oracle) != 0 {
		t.Errorf("profile reject: oracle section %s, want none", rep.Oracle)
	}
}

// TestSound_RejectForgetsAndRecycles: the false-rejection run takes its
// programs in windows of Recycle, forgets every unit it
// checked (a program's and its twin's), and starts a new session between
// windows, so a session never holds more than one window's units.
func TestSound_RejectForgetsAndRecycles(t *testing.T) {
	f := &scriptSound{reject: func(string) bool { return true }}
	cfg := SoundConfig{Base: DefaultSoundBase, Reject: 50, Recycle: 10, Workers: 2, Label: fmt.Sprintf("t7win-%d", soundUnitN.Add(1))}
	rep, t7, err := RunReject(context.Background(), cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	if t7.Sampled != 50 {
		t.Fatalf("sampled %d, want 50", t7.Sampled)
	}
	if f.maxSince > 2*cfg.Recycle {
		t.Errorf("%d checks in one session, want at most %d (a window of %d programs and their twins)", f.maxSince, 2*cfg.Recycle, cfg.Recycle)
	}
	if f.recycles < 4 || rep.Recycles != f.recycles {
		t.Errorf("recycles %d (record %d), want at least 4 for 50 programs in windows of 10", f.recycles, rep.Recycles)
	}
	twins := 0
	for _, u := range f.checked {
		if !f.forgot[u] {
			t.Errorf("unit %s was checked and never forgotten", u)
		}
		if strings.Contains(u, "-twin/") {
			twins++
		}
	}
	t.Logf("%d units checked (%d twins), %d recycles, at most %d checks in a session", len(f.checked), twins, f.recycles, f.maxSince)
}

// TestSound_RejectErroredNotClean: a sampled program whose run ends in an
// evaluator error (a panic) is not clean and stays in the denominator, and
// the t7 section counts it apart from the Skipped runs.
func TestSound_RejectErroredNotClean(t *testing.T) {
	label := fmt.Sprintf("t7err-%d", soundUnitN.Add(1))
	f := &scriptSound{reject: func(string) bool { return true }, run: func(spec SoundRunSpec) SoundRunResult {
		if spec.Unit == soundUnitName(label, 3) {
			return SoundRunResult{ErrKind: "panic", ErrText: "index out of range"}
		}
		return SoundRunResult{Value: spec.Args[0]}
	}}
	cfg := SoundConfig{Base: DefaultSoundBase, Reject: 10, Workers: 1, Label: label}
	_, t7, err := RunReject(context.Background(), cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	if o := t7.Overall; o.Runnable != 10 || o.Clean != 9 || t7.Errored["panic"] != 1 || len(t7.Skipped) != 0 {
		t.Fatalf("overall %+v, errored %v, skipped %v; want 9 clean of 10 runnable, one panic, none skipped", o, t7.Errored, t7.Skipped)
	}
}

// tbErrors is a testing.TB that records Errorf instead of failing.
type tbErrors struct {
	testing.TB
	errs []string
}

func (r *tbErrors) Helper() {}
func (r *tbErrors) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

// TestSoundRejectCountsICE: in the false-rejection run a check
// that crashes (an E0000, such as a miss of borrow's split-union check) is
// counted apart from the generator's defects, a twin's too, with the
// first five program indexes and seeds; rejectFailICE, which TestSound_Reject
// calls, fails on it, and passes a run with none.
func TestSoundRejectCountsICE(t *testing.T) {
	crash := map[int]bool{2: true, 9: true}
	f := &scriptSound{
		reject: func(string) bool { return true },
		ice: func(unit string) bool {
			return strings.Contains(unit, "-twin/") || crash[unitIndex(unit)]
		},
	}
	cfg := SoundConfig{Base: DefaultSoundBase, Reject: 40, Workers: 1, Label: fmt.Sprintf("t7ice-%d", soundUnitN.Add(1))}
	rep, t7, err := RunReject(context.Background(), cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	tw := t7.Twins
	if t7.Defects != 0 || tw.Defects != 0 || tw.Dropped == 0 || tw.ICE != tw.Dropped || t7.ICE != len(crash)+tw.ICE || rep.ICE != t7.ICE {
		t.Fatalf("defects %d, twins %+v, ice %d, record ice %d; want no defect, every dropped twin a crash (at least one) and %d crashes besides", t7.Defects, tw, t7.ICE, rep.ICE, len(crash))
	}
	want := fmt.Sprintf("2 %#016x", ProgramSeed(cfg.Base, 2))
	if len(t7.ICESeeds) != min(5, t7.ICE) || !slices.Contains(t7.ICESeeds, want) || !slices.Equal(rep.ICESeeds, t7.ICESeeds) {
		t.Fatalf("ice seeds %v (record %v); want the first %d, %q among them", t7.ICESeeds, rep.ICESeeds, min(5, t7.ICE), want)
	}
	r := &tbErrors{TB: t}
	rejectFailICE(r, t7)
	if len(r.errs) != 1 {
		t.Fatalf("rejectFailICE reported %q; want one failure", r.errs)
	}
	r = &tbErrors{TB: t}
	rejectFailICE(r, &RejectReport{})
	if len(r.errs) != 0 {
		t.Fatalf("rejectFailICE on a run with no crash reported %q", r.errs)
	}
}

// TestSound_RunICESeeds: a sized run's record keeps the first five crashes
// as program index and seed (ice_seeds), which TestSound_Run's failure
// names.
func TestSound_RunICESeeds(t *testing.T) {
	f := &scriptSound{ice: func(unit string) bool { return unitIndex(unit)%2 == 1 }}
	cfg := SoundConfig{Profile: ProfileCore, Base: 91, Generated: 20, Seeds: 1, Workers: 1, Label: fmt.Sprintf("runice-%d", soundUnitN.Add(1))}
	rep, err := RunSound(context.Background(), cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for i := 1; len(want) < 5; i += 2 {
		want = append(want, fmt.Sprintf("%d %#016x", i, ProgramSeed(cfg.Base, i)))
	}
	if rep.ICE != 10 || rep.DefectTotal() != 0 || !slices.Equal(rep.ICESeeds, want) {
		t.Fatalf("ice %d, defects %d, ice seeds %v; want 10, 0 and %v", rep.ICE, rep.DefectTotal(), rep.ICESeeds, want)
	}
}
