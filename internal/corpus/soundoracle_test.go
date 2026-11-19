// Why: the tagged mode and the oracle each have subtle cases (an arena's
// Reset, a trapping batch, an entry that takes io) that need tests of their
// own.
// What: TestSB_ArenaResetEndsObjects, TestSB_ArenaResetGeneratedClean,
// TestOracleEvalSeam_FullWithIo, TestOracle_ComparedCounts and
// TestOracle_BisectTrap.
// How: hand-written programs with known stale reads, generated programs
// that call Reset, and batches with a planted native trap.

package corpus

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"mk/internal/driver"
)

// resetProg is one program of TestSB_ArenaResetEndsObjects: its text, the
// codes the compiler gives it, and whether a Rejected run must report a
// lifetime end at the Reset (a stale read) or run clean.
var resetProgs = []struct {
	name  string
	codes []string
	stale bool
	text  string
	// kind is the violation a stale read must give ("": VDead or
	// VDeadScope), removed what its Removed must name ("": the Reset).
	kind, removed string
}{
	// (a) a reference into the arena read after Reset, before any reuse.
	{"a", []string{"E0501"}, true, `export Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var b := a.Sub() else {
        return h
    }
    var d := b.Put(i64(5)) else {
        b.Free()
        return h
    }
    b.Reset()
    h = h *% 31 +% *d
    b.Free()
    h = h *% 31 +% k
    return h
}
`, "", ""},
	// (b) a stale &Cell after Reset, its bytes reused by the next Put: the
	// read would see the new cell's reference.
	{"b", []string{"E0501"}, true, `export Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var b := a.Sub() else {
        return h
    }
    var d := b.Put(Cell.New(&v1)) else {
        b.Free()
        return h
    }
    b.Reset()
    var e := b.Put(Cell.New(&v2)) else {
        b.Free()
        return h
    }
    h = h *% 31 +% *d.Load()
    h = h *% 31 +% *e.Load()
    b.Free()
    h = h *% 31 +% k
    return h
}
`, "", ""},
	// (c) as (b) with plain i64 objects.
	{"c", []string{"E0501"}, true, `export Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var b := a.Sub() else {
        return h
    }
    var d := b.Put(i64(5)) else {
        b.Free()
        return h
    }
    b.Reset()
    var e := b.Put(i64(6)) else {
        b.Free()
        return h
    }
    h = h *% 31 +% *d
    h = h *% 31 +% *e
    b.Free()
    h = h *% 31 +% k
    return h
}
`, "", ""},
	// (d) Reset, then Free, then the stale read: the page is freed, so the
	// read is VDead at the Free's page release, not VDeadScope at the Reset
	// (a dead allocation is checked first).
	{"d", []string{"E0501", "E0503"}, true, `export Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var b := a.Sub() else {
        return h
    }
    var d := b.Put(i64(5)) else {
        b.Free()
        return h
    }
    b.Reset()
    b.Free()
    h = h *% 31 +% *d
    h = h *% 31 +% k
    return h
}
`, "VDead", "removed at std/core/mem_arena.mk:"},
	// The accepted form: every read of the old objects before the Reset,
	// the new ones put and read after it, a cell among them. Clean.
	{"ok", nil, false, `export Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var b := a.Sub() else {
        return h
    }
    var d := b.Put(Cell.New(&v1)) else {
        b.Free()
        return h
    }
    var w := b.Put(i64(7)) else {
        b.Free()
        return h
    }
    h = h *% 31 +% *d.Load()
    h = h *% 31 +% *w
    b.Reset()
    var e := b.Put(Cell.New(&v2)) else {
        b.Free()
        return h
    }
    var x := b.Put(i64(8)) else {
        b.Free()
        return h
    }
    h = h *% 31 +% *e.Load()
    h = h *% 31 +% *x
    b.Reset()
    var y := b.Put(i64(9)) else {
        b.Free()
        return h
    }
    h = h *% 31 +% *y
    b.Free()
    h = h *% 31 +% k
    return h
}
`, "", ""},
}

// TestSB_ArenaResetEndsObjects (Reset's `ends self`): Arena.Reset keeps its
// first and current chunks and bumps them from their start, so their pages
// stay live; the tagged mode ends every object they held at the Reset. A read
// through a reference made before the Reset is a lifetime violation whose
// Removed is the Reset's call, whether or not the bytes were reused (a stale
// &Cell would otherwise read the next cell put at its address), and the
// accepted form, which reads only objects put after the last Reset, runs
// clean.
func TestSB_ArenaResetEndsObjects(t *testing.T) {
	s := soundSess(t)
	for _, c := range resetProgs {
		t.Run(c.name, func(t *testing.T) {
			name := fmt.Sprintf("sound/reset%d", soundUnitN.Add(1))
			out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: c.text}}})
			if got := unitCodes(out); !slices.Equal(got, c.codes) {
				t.Fatalf("codes %v, want %v:\n%s", got, c.codes, out.Report.Text(out.Sources))
			}
			// The Reset that ends the objects a stale read uses: the first.
			at := 1 + strings.Count(c.text[:strings.Index(c.text, "b.Reset()")], "\n")
			removed := fmt.Sprintf("removed at sound.mk:%d:", at)
			if c.removed != "" {
				removed = c.removed
			}
			for _, k := range []uint64{0, 1} {
				r := SoundRunOn(s, SoundRunSpec{Unit: name, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, Rejected: c.stale}})
				if r.ErrKind != "" || r.Skipped != "" {
					t.Fatalf("k %d: err %s %s, skipped %q", k, r.ErrKind, r.ErrText, r.Skipped)
				}
				if !c.stale {
					if len(r.Violations) > 0 {
						t.Errorf("k %d: %v, want a clean run", k, r.Violations)
					}
					continue
				}
				if len(r.Violations) == 0 {
					t.Fatalf("k %d: clean run (value %d), want VDead or VDeadScope at the stale read", k, r.Value)
				}
				v := r.Violations[0]
				if k == 0 {
					t.Logf("%s", v.Text)
				}
				if c.kind != "" && v.Kind != c.kind || v.Kind != "VDead" && v.Kind != "VDeadScope" {
					t.Errorf("k %d: %s, want %s", k, v.Text, map[bool]string{true: c.kind, false: "VDead or VDeadScope"}[c.kind != ""])
				}
				if !strings.Contains(v.Text, removed) {
					t.Errorf("k %d: %s, want %q (the Reset)", k, v.Text, removed)
				}
			}
		})
	}
}

// TestSB_ArenaResetGeneratedClean: the generator's own Reset (S42)
// in accepted programs runs clean under the Reset hook: the first 200
// accepted full programs of the default base whose text calls Reset, on 2
// run seeds each, one with FailAlloc as the runner sets it.
func TestSB_ArenaResetGeneratedClean(t *testing.T) {
	s := soundSess(t)
	const want = 200
	type hit struct {
		i    int
		unit string
		seed uint64
	}
	var (
		mu   sync.Mutex
		hits []hit
		next atomic.Int64
		wg   sync.WaitGroup
	)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				mu.Lock()
				done := len(hits) >= want || i >= 20000
				mu.Unlock()
				if done {
					return
				}
				p := GenerateSound(ProfileFull, ProgramSeed(DefaultSoundBase, i))
				if !strings.Contains(p.Text, ".Reset()") {
					continue
				}
				unit := fmt.Sprintf("sound/resetgen/%d", soundUnitN.Add(1))
				out := s.CheckUnit(driver.UnitInput{Name: unit, Files: []driver.UnitFile{{Path: "sound.mk", Text: p.Text}}})
				if c, _ := classifySoundProg(p, out.Report); c == "accepted" {
					mu.Lock()
					hits = append(hits, hit{i, unit, p.Seed})
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if len(hits) < want {
		t.Fatalf("%d accepted programs that call Reset in the first 20,000, want %d", len(hits), want)
	}
	for _, h := range hits {
		for _, k := range RunSeeds(h.seed, 4)[2:] { // run seeds 2 and 3: the second fails allocations (k mod 4)
			r := SoundRunOn(s, SoundRunSpec{Unit: h.unit, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, FailAlloc: soundFailAlloc(k)}})
			if len(r.Violations) > 0 || r.ErrKind != "" {
				t.Errorf("program %d (seed %#x) k %#x: violations %v, err %s %s", h.i, h.seed, k, r.Violations, r.ErrKind, r.ErrText)
			}
		}
	}
	t.Logf("%d accepted programs that call Reset, 2 tagged runs each: clean", len(hits))
}

// TestOracleEvalSeam_FullWithIo: the evaluator half of a full program whose
// entry takes io (and an arena) gives the soundness host's tagged values,
// with no plain run to refuse the Io; the plain run is made for oracle and
// core programs and for any entry that takes only k.
func TestOracleEvalSeam_FullWithIo(t *testing.T) {
	for _, c := range []struct {
		p    SoundProgram
		want bool
	}{
		{SoundProgram{Profile: ProfileOracle, Params: []string{"k"}}, true},
		{SoundProgram{Profile: ProfileCore, Params: []string{"k"}}, true},
		{SoundProgram{Profile: ProfileFull, Params: []string{"k"}}, true},
		{SoundProgram{Profile: ProfileFull, Params: []string{"k", "arena"}}, false},
		{SoundProgram{Profile: ProfileVec, Params: []string{"k", "io"}}, false},
	} {
		if got := oraclePlain(c.p); got != c.want {
			t.Errorf("oraclePlain(%s %v) = %v, want %v", c.p.Profile, c.p.Params, got, c.want)
		}
	}
	s := soundSess(t)
	check := sessChecker(s)
	for i := 0; i < 400; i++ {
		p := GenerateSound(ProfileFull, ProgramSeed(DefaultSoundBase, i))
		if !slices.Contains(p.Params, "io") {
			continue
		}
		out := check(fmt.Sprintf("oracle/fullio/gen/%d", soundUnitN.Add(1)), p.Text)
		if c, _ := classifySoundProg(p, out.Report); c != "accepted" {
			continue
		}
		vals, err := OracleEvalSeam(s, fmt.Sprintf("oracle/fullio/%d", soundUnitN.Add(1)), p, []uint64{1, 2})
		if err != nil {
			t.Fatalf("program %d (seed %#x, params %v): %v", i, p.Seed, p.Params, err)
		}
		if len(vals) != 2 {
			t.Fatalf("program %d: %d values, want 2", i, len(vals))
		}
		t.Logf("program %d (seed %#x, params %v): values %#x %#x", i, p.Seed, p.Params, vals[0], vals[1])
		return
	}
	t.Fatal("no accepted full program of the default base's first 400 takes io")
}

// TestOracle_ComparedCounts: the comparison skips a call no execution
// reached after a trap, and the record counts only the calls both native
// executions ran and the programs all of whose calls they ran; a trapped
// call is a difference.
func TestOracle_ComparedCounts(t *testing.T) {
	seeds := [][]uint64{{1, 2}, {3}, {4}}
	mk := func() *NativeRun {
		return &NativeRun{Vals: [][]int64{{10, 20}, {30}, {40}}, Got: [][]bool{{true, true}, {true}, {true}}, Trap: [2]int{-1, -1}}
	}
	dev, rel := mk(), mk()
	dev.Got[1][0], dev.Got[2][0] = false, false
	dev.Trapped = [][]bool{{false, false}, {true}, {false}} // program 2: after the trap, never reached
	rel.Got[2][0] = false
	rel.Trap = [2]int{2, 0}
	progs := []SoundProgram{{Seed: 1}, {Seed: 2}, {Seed: 3}}
	eval := [][]int64{{10, 20}, {30}, {40}}
	if p, c := OracleCompared(seeds, dev, rel); p != 2 || c != 3 {
		t.Errorf("OracleCompared = %d programs, %d calls; want 2 and 3", p, c)
	}
	if diffs := CompareOracle(progs, seeds, eval, dev, rel); len(diffs) != 1 || diffs[0].Program != 1 || diffs[0].Trapped != "dev" {
		t.Fatalf("differences %+v, want program 1 (dev trapped) only: dev never ran program 2", diffs)
	}
	dev.Got[2][0] = true // dev ran program 2; release trapped there
	if p, c := OracleCompared(seeds, dev, rel); p != 3 || c != 4 {
		t.Errorf("OracleCompared = %d programs, %d calls; want 3 and 4", p, c)
	}
	diffs := CompareOracle(progs, seeds, eval, dev, rel)
	if len(diffs) != 2 || diffs[0].Program != 1 || diffs[1].Program != 2 || diffs[1].Trapped != "release" {
		t.Fatalf("differences %+v, want program 1 (dev trapped) and program 2 (release trapped)", diffs)
	}
}

// TestOracle_BisectTrap: a batch whose program 1 traps natively (an Assert
// that fails, in the native text only) exits non-zero; the long set's batch
// bisects it, so the programs after the trap are still built, run and
// compared, program 1's call is the only difference, and the record counts
// every program whose values were compared and every build the bisection
// made.
func TestOracle_BisectTrap(t *testing.T) {
	s := soundSess(t)
	check := sessChecker(s)
	progs, seeds, _ := oracleAccepted(t, check, "bisect", ProfileOracle, OracleQuickBase^0x424953454354, 0, 4, 1)
	native := plantNative(t, progs, 1, "h = h *% 31 +% k\n    Assert(h == h +% 1)\n    return h")
	rec := &OracleRecord{}
	or := oracleLongBatch(t, check, rec, "bisect", progs, seeds, native, driver.HostTarget().Name, false)
	for _, n := range []*NativeRun{or.dev, or.rel} {
		if n.Exit == 0 {
			t.Fatalf("the planted Assert did not trap natively (exit 0)")
		}
		if !n.TrappedAt(1, 0) || n.Got[1][0] {
			t.Errorf("program 1: trapped %v, got %v; want trapped with no line", n.TrappedAt(1, 0), n.Got[1][0])
		}
		for i := range progs {
			if i != 1 && !n.Got[i][0] {
				t.Errorf("program %d: no value: the bisection did not run it", i)
			}
		}
	}
	if len(or.diffs) != 1 || or.diffs[0].Program != 1 || or.diffs[0].Trapped != "dev, release" {
		t.Fatalf("differences %+v, want program 1's trap in dev and release only", or.diffs)
	}
	if rec.Programs != 4 || rec.Calls != 4 || rec.Differences != 1 || rec.Builds <= 2 {
		t.Errorf("record: %d programs, %d calls, %d differences, %d builds; want 4, 4, 1 and more than 2", rec.Programs, rec.Calls, rec.Differences, rec.Builds)
	}
	t.Logf("exit dev %d release %d; bisection builds %d+%d", or.dev.Exit, or.rel.Exit, or.devBisect, or.relBisect)
}
