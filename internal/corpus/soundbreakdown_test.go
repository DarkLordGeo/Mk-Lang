// Why: TestSoundRejectBreakdown must count the same programs as RunReject,
// whatever the worker timing, without holding every unit in memory.
// What: TestBreakdownIndexOrder and TestBreakdownForgets, on the
// scripted compiler rejectScript.
// How: a compiler whose outcomes depend on the program index alone, with
// slow runs that make workers finish out of order.

package corpus

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

// rejectScript is a scripted SoundCompiler whose outcomes depend on the
// program index alone: two in three are rejected by a loan code, one in
// five sampled ones has a violation, and some runs are slow, so workers
// finish out of index order.
func rejectScript() *scriptSound {
	return &scriptSound{
		reject: func(u string) bool { return unitIndex(u)%3 != 0 },
		run: func(spec SoundRunSpec) SoundRunResult {
			i := unitIndex(spec.Unit)
			if i%7 == 1 {
				time.Sleep(2 * time.Millisecond)
			}
			if i%5 == 0 {
				return SoundRunResult{Violations: []SoundViolation{{Kind: "VRemovedTag", Text: "scripted"}}}
			}
			return SoundRunResult{Value: spec.Args[0]}
		},
	}
}

// TestBreakdownIndexOrder: the breakdown counts the first
// Reject sampled programs by index, whatever the workers' timing: 1 and 8
// workers give the same index set and the same group tallies, the set is
// the first 50 sampled indexes, and group "all" equals RunReject's Overall
// and Adjusted on the same configuration.
func TestBreakdownIndexOrder(t *testing.T) {
	var sets [][]int
	var tallies []string
	for _, w := range []int{1, 8} {
		f := rejectScript()
		cfg := rejectConfig(SoundConfig{Profile: ProfileReject7, Base: DefaultSoundBase, Reject: 50, Recycle: 16, Workers: w, Label: fmt.Sprintf("t7bd-%d", soundUnitN.Add(1))})
		slots, _, err := rejectFill(context.Background(), cfg, f)
		if err != nil {
			t.Fatal(err)
		}
		groups, idx := rejectByShape(cfg, slots)
		if len(idx) != 50 {
			t.Fatalf("workers %d: %d sampled programs counted, want 50", w, len(idx))
		}
		var want []int
		for i := 0; len(want) < 50; i++ {
			if i%3 != 0 {
				want = append(want, i)
			}
		}
		if !slices.Equal(idx, want) {
			t.Errorf("workers %d: counted %v, want the first 50 sampled indexes %v", w, idx, want)
		}
		var b string
		for _, k := range sortedKeys(groups) {
			b += fmt.Sprintf("%s %+v; ", k, *groups[k])
		}
		sets, tallies = append(sets, idx), append(tallies, b)
		_, t7, err := RunReject(context.Background(), cfg, rejectScript())
		if err != nil {
			t.Fatal(err)
		}
		all := groups["all"]
		if all.Raw != t7.Overall.Runnable || all.RawClean != t7.Overall.Clean || all.Adj != t7.Adjusted.Runnable || all.AdjClean != t7.Adjusted.Clean {
			t.Errorf("workers %d: group all %+v, RunReject overall %+v adjusted %+v", w, *all, t7.Overall, t7.Adjusted)
		}
	}
	if !slices.Equal(sets[0], sets[1]) || tallies[0] != tallies[1] {
		t.Fatalf("1 and 8 workers differ:\n%s\n%s", tallies[0], tallies[1])
	}
	t.Log(tallies[0])
}

// TestBreakdownForgets: the breakdown's fill forgets every
// unit it checks and recycles between windows, so no session holds more
// than one window's units (a program's and its twin's).
func TestBreakdownForgets(t *testing.T) {
	f := rejectScript()
	cfg := rejectConfig(SoundConfig{Profile: ProfileReject7, Base: DefaultSoundBase, Reject: 60, Recycle: 12, Workers: 4, Label: fmt.Sprintf("t7bdf-%d", soundUnitN.Add(1))})
	_, recycles, err := rejectFill(context.Background(), cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	if f.maxSince > 2*cfg.Recycle {
		t.Errorf("%d checks in one session, want at most %d", f.maxSince, 2*cfg.Recycle)
	}
	if recycles < 6 || f.recycles != recycles {
		t.Errorf("recycles %d (compiler %d), want at least 6 for 90 programs in windows of 12", recycles, f.recycles)
	}
	for _, u := range f.checked {
		if !f.forgot[u] {
			t.Errorf("unit %s was checked and never forgotten", u)
		}
	}
}
