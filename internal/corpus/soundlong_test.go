// Why: the soundness test's real evidence comes from long runs over many
// generated programs, which need switches, records and guards of their own.
// What: TestSound_Throughput, TestSound_Run, TestSound_Reject, the
// split-union guard (soundSplitUnion), and BenchmarkTaggedRunGenerated and
// BenchmarkSoundPipeline.
// How: the long forms run only with MK_SOUND=1, in the background under the
// machine lock, and write run records.
//
// TestSound_Throughput (120 s of profile full with every worker, written
// as a throughput run record) and TestSound_Run (a sized run: every
// violation minimised and recorded under tests/soundness/found/open/, and
// a failure).

package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"mk/internal/borrow"
	"mk/internal/driver"
)

// soundLong skips a long form without MK_SOUND=1.
func soundLong(t testing.TB) {
	t.Helper()
	if os.Getenv("MK_SOUND") != "1" {
		t.Skip("MK_SOUND=1 not set")
	}
}

// soundSplitUnion turns on, for the rest of the test, borrow's check that a
// split root's held set is exactly the union of its fields, at every write
// and every join (the invariant field-level liveness relies on; a miss is
// an internal error, E0000), and logs at the end how many checks it made
// on the units the test checked. A miss fails each caller: TestSound_Run and
// TestSound_Throughput on rep.ICE, TestSound_Reject on t7.ICE
// (rejectFailICE), TestFlowsWitnesses as a witness that misses its verdict.
// With need set (profiles fields and rejectf, which draw S70 to S81; the
// witnesses) a test that checked no split root of its own units fails:
// the check would be on and see nothing. std's checks do not count: the
// guard's compiler leaves out what Start and Recycle check (no worker runs
// then), and from leaves out what was checked before it (a shared
// session). A skipped test is not judged. A mutant's child does not call
// it: a mutant that breaks the invariant must flip its witnesses, not
// crash.
func soundSplitUnion(t testing.TB, need bool) *splitUnionGuard {
	t.Helper()
	g := &splitUnionGuard{}
	prev := borrow.SetSplitUnionCheck(true)
	g.n0 = borrow.SplitUnionChecks()
	t.Cleanup(func() {
		borrow.SetSplitUnionCheck(prev)
		n := borrow.SplitUnionChecks() - g.n0 - g.std
		t.Logf("split-union checks: %d on the test's units (std's %d left out)", n, g.std)
		if need && n == 0 && !t.Skipped() {
			t.Errorf("the split-union check is on, and no split root of the test's units was checked")
		}
	})
	return g
}

// splitUnionGuard is soundSplitUnion's count: the checks before n0, and
// std's (std), are not the test's.
type splitUnionGuard struct {
	n0  uint64
	std uint64
}

// from counts from now: what a shared session checked before is not the
// test's.
func (g *splitUnionGuard) from() { g.n0 = borrow.SplitUnionChecks() }

// compiler is c with std's checks, made by Start and Recycle, left out of
// the count.
func (g *splitUnionGuard) compiler(c SoundCompiler) SoundCompiler {
	return &splitUnionStd{SoundCompiler: c, g: g}
}

// splitUnionStd is guard.compiler's SoundCompiler. RunSound and RunReject
// call Start before any worker and Recycle after every worker has stopped,
// so the counter's change across either is std's alone.
type splitUnionStd struct {
	SoundCompiler
	g *splitUnionGuard
}

func (c *splitUnionStd) Start() error {
	n := borrow.SplitUnionChecks()
	err := c.SoundCompiler.Start()
	c.g.std += borrow.SplitUnionChecks() - n
	return err
}

func (c *splitUnionStd) Recycle() error {
	n := borrow.SplitUnionChecks()
	err := c.SoundCompiler.Recycle()
	c.g.std += borrow.SplitUnionChecks() - n
	return err
}

// rejectFailICE fails a reject run (RejectReport) whose checks crashed (an E0000, a
// split-union miss among them): a compiler bug, never a generator defect.
func rejectFailICE(t testing.TB, t7 *RejectReport) {
	t.Helper()
	if t7.ICE > 0 {
		t.Errorf("%d compiler crashes (E0000) in the reject run, %d of them twins; the first, as program index and seed: %v", t7.ICE, t7.Twins.ICE, t7.ICESeeds)
	}
}

// soundInt reads an integer MK_SOUND_* switch.
func soundInt(t testing.TB, name string, def int) int {
	t.Helper()
	v := soundEnv(name, strconv.Itoa(def))
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("%s=%q: want an integer", name, v)
	}
	return n
}

// soundRunConfig is the SoundConfig the MK_SOUND_* switches give, with the
// label's default <date>-<kind>-<profile>-<base>-<first>.
func soundRunConfig(t testing.TB, kind string, seconds int) SoundConfig {
	t.Helper()
	p, ok := ParseProfile(soundEnv("MK_SOUND_PROFILE", "full"))
	if !ok {
		t.Fatalf("MK_SOUND_PROFILE=%q: unknown profile", os.Getenv("MK_SOUND_PROFILE"))
	}
	base := soundBase(t)
	first := soundInt(t, "MK_SOUND_FIRST", 0)
	secs := soundInt(t, "MK_SOUND_SECONDS", seconds)
	cfg := SoundConfig{
		Profile: p, Base: base, First: first, Budget: time.Duration(secs) * time.Second,
		Accepted: soundInt(t, "MK_SOUND_ACCEPTED", 0), Seeds: soundInt(t, "MK_SOUND_SEEDS", 4),
		Workers: soundInt(t, "MK_SOUND_WORKERS", 0), Recycle: soundInt(t, "MK_SOUND_RECYCLE", 0),
		Minimise: true, Out: soundRoot(t), Stop: os.Getenv("MK_SOUND_STOP") == "1",
		Label: soundEnv("MK_SOUND_LABEL", fmt.Sprintf("%s-%s-%s-%016x-%d", time.Now().Format("2006-01-02"), kind, p, base, first)),
	}
	cfg.Command = map[string]string{"test": t.Name(), "MK_SOUND_SECONDS": strconv.Itoa(secs), "MK_SOUND_BASE": fmt.Sprintf("%#x", base),
		"MK_SOUND_FIRST": strconv.Itoa(first), "MK_SOUND_PROFILE": p.String(), "MK_SOUND_SEEDS": strconv.Itoa(cfg.Seeds)}
	return cfg
}

// logMem logs the runner's memory at the end of a long run (the budget:
// resident at most 4 GiB over a 10-minute run).
func logMem(t testing.TB) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("memory: Sys %d MiB, HeapSys %d MiB, HeapInuse %d MiB, NumGC %d", ms.Sys>>20, ms.HeapSys>>20, ms.HeapInuse>>20, ms.NumGC)
}

// TestSound_Throughput is the throughput measurement: 120 s of profile
// full with every worker, its rates in the throughput record (which the
// sized runs are sized from).
func TestSound_Throughput(t *testing.T) {
	soundLong(t)
	cfg := soundRunConfig(t, "throughput", 120)
	g := soundSplitUnion(t, fieldsProfile(cfg.Profile))
	cfg.Minimise = false
	rep, err := RunSound(context.Background(), cfg, g.compiler(NewSessionCompiler(driver.Config{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Log(rep.Summary())
	th := rep.Throughput
	t.Logf("throughput: %.1f generated/s, %.1f accepted/s, %.1f runs/s; G %.1f µs, C %.2f ms (per worker), R %.3f ms (per worker); %d workers; an hour holds %.0f accepted",
		th.GeneratedPerS, th.AcceptedPerS, th.RunsPerS, th.GenUSMean, th.CheckMSMean, th.RunMSMean, rep.Workers, th.AcceptedPerS*3600)
	logMem(t)
	for _, v := range rep.Violations {
		t.Errorf("violation: program %d (seed %s, run %s): %s", v.Program, v.Seed, v.Run, v.Text)
	}
	if n := rep.DefectTotal() + rep.ICE; n > 0 {
		t.Errorf("%d defects or crashes: defects %+v, fuel %d, panics %d, skipped %v", n, rep.Defects, rep.Fuel, rep.Panics, rep.Skipped)
	}
}

// TestSound_Run is the sized run (600 s by default): zero
// violations, or each one minimised, recorded under found/open/ and filed.
// With MK_SOUND_ORACLE=n every n-th accepted program also goes to the
// oracle after the run (soundOracleSink), and the
// record's oracle section holds what it found.
func TestSound_Run(t *testing.T) {
	soundLong(t)
	cfg := soundRunConfig(t, "run", 600)
	g := soundSplitUnion(t, fieldsProfile(cfg.Profile))
	if cfg.Oracle = soundInt(t, "MK_SOUND_ORACLE", 0); cfg.Oracle > 0 {
		cfg.Command["MK_SOUND_ORACLE"] = strconv.Itoa(cfg.Oracle)
		cfg.OracleSink = soundOracleSink(t, cfg.Label, cfg.Profile)
	}
	rep, err := RunSound(context.Background(), cfg, g.compiler(NewSessionCompiler(driver.Config{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Oracle) > 0 {
		t.Logf("oracle: %s", rep.Oracle)
	}
	t.Log(rep.Summary())
	t.Logf("rejected by code: %s", codeTable(rep.RejectedByCode))
	t.Logf("skipped: %v; fuel %d; panics %d; defects %+v; recycles %d; stopped %s", rep.Skipped, rep.Fuel, rep.Panics, rep.Defects, rep.Recycles, rep.Stopped)
	logMem(t)
	for _, v := range rep.Violations {
		t.Errorf("violation: program %d (seed %s, run %s): %s; record %s: file it (4.5)", v.Program, v.Seed, v.Run, v.Text, v.Record)
	}
	// A sized run reports its defect share; above 1 percent the
	// generator is fixed before the run's acceptance rate is trusted. A
	// crash (E0000) is a found record of its own and always fails.
	n := rep.DefectTotal()
	share := 100 * float64(n) / float64(max(1, rep.Generated))
	t.Logf("defects: %d (%.4f%% of generated): defects %+v, fuel %d, panics %d, skipped %v", n, share, rep.Defects, rep.Fuel, rep.Panics, rep.Skipped)
	if share > 1 {
		t.Errorf("defect share %.3f%% is above 1 percent (4.2.10): fix the generator first", share)
	}
	if rep.ICE > 0 {
		t.Errorf("%d compiler crashes (E0000): no record is written for them; reproduce each from its program index and seed (the first five, also the record's ice_seeds: %v) and file it as a found record (4.5)", rep.ICE, rep.ICESeeds)
	}
}

// soundOracleSink is the oracle a sized run hands its programs to: the
// programs in batches of OracleBatchMax, 4 run seeds
// each, through the long set's batch (oracleLongBatch: a dev build, a
// release build, the evaluator half, the bisection of a batch
// that traps, each difference minimised and recorded) on the host target.
// Its result is the long set's record over those programs.
func soundOracleSink(t *testing.T, label string, p Profile) func([]SoundProgram) (json.RawMessage, error) {
	return func(progs []SoundProgram) (json.RawMessage, error) {
		s := soundSess(t)
		check := sessChecker(s)
		target := driver.HostTarget().Name
		rec := &OracleRecord{Label: label + "-oracle", Kind: "oracle", GenVersion: GenVersion, Profile: p.String(), Seeds: 4, Target: target,
			Records: []string{}, Machine: soundMachine(), Build: fmt.Sprintf("%x", driver.BuildID())}
		t0 := time.Now()
		var buildMS int64
		for lo := 0; lo < len(progs); lo += OracleBatchMax {
			batch := progs[lo:min(lo+OracleBatchMax, len(progs))]
			seeds := make([][]uint64, len(batch))
			for i, q := range batch {
				seeds[i] = RunSeeds(q.Seed, 4)
			}
			or := oracleLongBatch(t, check, rec, label, batch, seeds, nil, target, true)
			buildMS += or.dev.BuildMS + or.rel.BuildMS
		}
		rec.WallSeconds = time.Since(t0).Seconds()
		if rec.WallSeconds > 0 {
			rec.ProgramsPerS = float64(rec.Programs) / rec.WallSeconds
		}
		if rec.Batches > 0 {
			rec.BuildMSMean = float64(buildMS) / float64(2*rec.Batches)
		}
		return json.Marshal(rec)
	}
}

// BenchmarkTaggedRunGenerated is the tagged-run rate: one tagged run of each of
// the first 100 accepted full programs of the default base, per iteration.
func BenchmarkTaggedRunGenerated(b *testing.B) {
	s := soundSess(b)
	type prog struct {
		unit string
		k    uint64
	}
	var progs []prog
	for i := 0; len(progs) < 100 && i < 2000; i++ {
		p := GenerateSound(ProfileFull, ProgramSeed(DefaultSoundBase, i))
		unit := fmt.Sprintf("sound/bench/%d", soundUnitN.Add(1))
		out := s.CheckUnit(driver.UnitInput{Name: unit, Files: []driver.UnitFile{{Path: "sound.mk", Text: p.Text}}})
		if c, _ := classifySoundProg(p, out.Report); c == "accepted" {
			progs = append(progs, prog{unit, RunSeeds(p.Seed, 1)[0]})
		}
	}
	b.ResetTimer()
	runs := 0
	for range b.N {
		for _, p := range progs {
			r := SoundRunOn(s, SoundRunSpec{Unit: p.unit, Fn: "Run", Args: []uint64{p.k}, Tag: &SoundTag{Seed: p.k}})
			if len(r.Violations) > 0 || r.ErrKind != "" {
				b.Fatalf("%s: %+v", p.unit, r)
			}
			runs++
		}
	}
	b.ReportMetric(float64(b.Elapsed().Microseconds())/1000/float64(max(1, runs)), "ms/run")
}

// BenchmarkSoundPipeline is the per-worker rate: generate, check
// and run (4 seeds) programs of profile full on one goroutine.
func BenchmarkSoundPipeline(b *testing.B) {
	s := soundSess(b)
	c := NewSessionCompiler(driver.Config{}, s)
	accepted := 0
	b.ResetTimer()
	for i := range b.N {
		sl := soundOne(context.Background(), SoundConfig{Profile: ProfileFull, Base: DefaultSoundBase, Seeds: 4, Label: fmt.Sprintf("pipe%d", soundUnitN.Add(1))}, c, i)
		if sl.class == "accepted" {
			accepted++
		}
		if sl.viol != nil {
			b.Fatalf("program %d: %+v", i, sl.viol)
		}
	}
	b.ReportMetric(float64(accepted)/b.Elapsed().Seconds(), "accepted/s")
}

// TestSound_Reject is the false-rejection measurement (MK_SOUND=1):
// MK_SOUND_REJECT programs (default 20,000) a loan code rejects, each run
// with Rejected and 8 seeds; the false-rejection rate with its Wilson
// interval, overall and per code, in the record's t7 section. Above 20
// percent is not a failure here: the per-code table shows which rules to
// look at.
func TestSound_Reject(t *testing.T) {
	soundLong(t)
	cfg := soundRunConfig(t, "t7", 0)
	g := soundSplitUnion(t, fieldsProfile(cfg.Profile))
	cfg.Budget = 0
	cfg.Reject = soundInt(t, "MK_SOUND_REJECT", 20000)
	cfg.Command["MK_SOUND_REJECT"] = strconv.Itoa(cfg.Reject)
	rep, t7, err := RunReject(context.Background(), cfg, g.compiler(NewSessionCompiler(driver.Config{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	o := t7.Overall
	t.Logf("t7: %d generated, %d sampled (%d runnable, of which errored %v; skipped %v), %d move-only, %d accepted, %d defects, %d crashes; clean %d of %d = %.2f%% [%.2f%%, %.2f%%] in %.0f s",
		t7.Generated, t7.Sampled, o.Runnable, t7.Errored, t7.Skipped, t7.MoveOnly, t7.Accepted, t7.Defects, t7.ICE, o.Clean, o.Runnable, 100*o.Rate, 100*o.Low, 100*o.High, rep.WallSeconds)
	rejectFailICE(t, t7)
	for _, code := range sortedKeys(t7.ByCode) {
		r := t7.ByCode[code]
		t.Logf("  %s: clean %d of %d = %.2f%% [%.2f%%, %.2f%%]", code, r.Clean, r.Runnable, 100*r.Rate, 100*r.Low, 100*r.High)
	}
	pr := func(name string, r RejectRate) {
		t.Logf("%s: clean %d of %d = %.2f%% [%.2f%%, %.2f%%]", name, r.Clean, r.Runnable, 100*r.Rate, 100*r.Low, 100*r.High)
	}
	pr("body (a code of E0501 to E0507)", t7.Body)
	pr("boundary only (E0602 to E0605)", t7.Boundary)
	tw := t7.Twins
	t.Logf("twins: %d sampled programs dropped a clause; twin accepted %d, rejected by a loan code %d (skipped %v), move-only %d, defects %d, crashes %d",
		tw.Dropped, tw.Accepted, tw.Rejected, tw.Skipped, tw.Move, tw.Defects, tw.ICE)
	pr("adjusted (every clause restored)", t7.Adjusted)
	for _, code := range sortedKeys(t7.AdjustedByCode) {
		pr("  adjusted "+code, t7.AdjustedByCode[code])
	}
	if o.Rate > 0.20 {
		t.Logf("raw rate above 20 percent: read the per-code table and the minimised examples; adjusted %.2f%%", 100*t7.Adjusted.Rate)
	}
}
