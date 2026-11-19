// Why: the miscompile oracle catches code generation bugs by running each
// generated program three ways, and the oracle itself must be shown to work.
// What: TestOracle_BatchShape, TestOracle_Quick, TestOracle_PlantedDifference,
// TestOracle_Long and BenchmarkOracleBatch.
// How: a dev build, a release build and the evaluator must give equal values
// for every program and seed; a planted difference proves the comparison.
//
// TestOracle_Quick is the quick set to run after every codegen change.

package corpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/driver"
)

// soundSess returns the shared soundness session, or skips the test when
// std is not flow-clean in this tree (units are then judged only through an
// earlier phase, so no program can be classified). Call it on the test's
// own goroutine.
func soundSess(t testing.TB) *driver.CorpusSession {
	t.Helper()
	s, err := soundSession()
	if err != nil {
		t.Fatal(err)
	}
	if s.Through() < diag.PhaseFlow {
		t.Skipf("std is not flow-clean in this tree, so units are judged only through %s: %s", s.Through(), firstStdDiag(s))
	}
	return s
}

// sessChecker is a UnitChecker over the shared session.
func sessChecker(s *driver.CorpusSession) UnitChecker {
	return func(unit, text string) driver.UnitOutcome {
		return s.CheckUnit(driver.UnitInput{Name: unit, Files: []driver.UnitFile{{Path: "sound.mk", Text: text}}})
	}
}

// oracleAccepted generates programs of profile p from base until n are
// accepted, checking each on the session, with s run seeds each.
func oracleAccepted(t testing.TB, check UnitChecker, label string, p Profile, base uint64, first, n, s int) ([]SoundProgram, [][]uint64, int) {
	t.Helper()
	var progs []SoundProgram
	var seeds [][]uint64
	i := first
	for ; len(progs) < n; i++ {
		if i-first > 50*n {
			t.Fatalf("%d programs of %s give fewer than %d accepted", i, p, n)
		}
		prog := GenerateSound(p, ProgramSeed(base, i))
		out := check(fmt.Sprintf("oracle/%s/gen/%d", label, i), prog.Text)
		switch c, _ := classifySound(out.Report); c {
		case "accepted":
			progs = append(progs, prog)
			seeds = append(seeds, RunSeeds(prog.Seed, s))
		case "defect", "ice":
			t.Fatalf("program %d of %s (seed %#x) is a generator %s:\n%s", i, p, prog.Seed, c, out.Report.Text(out.Sources))
		}
	}
	return progs, seeds, i
}

func TestOracle_BatchShape(t *testing.T) {
	var progs []SoundProgram
	var seeds [][]uint64
	for i := range 3 {
		p := GenerateSound(ProfileOracle, ProgramSeed(OracleQuickBase, i))
		progs = append(progs, p)
		seeds = append(seeds, RunSeeds(p.Seed, 2))
	}
	progs[2].Params = []string{"k", "arena", "io"} // the full entry's arguments
	files := OracleBatch(progs, seeds)
	var got strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f.Path, "/p.mk") {
			fmt.Fprintf(&got, "==== %s: %d bytes\n", f.Path, len(f.Text))
			continue
		}
		fmt.Fprintf(&got, "==== %s\n%s", f.Path, f.Text)
	}
	path := filepath.Join("testdata", "oracle_batch.golden")
	if *update {
		if err := os.WriteFile(path, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if string(want) != got.String() {
		t.Errorf("OracleBatch's files differ from %s:\n%s", path, got.String())
	}
	for i, f := range files[1 : len(files)-1] {
		if f.Text != progs[i].Text {
			t.Errorf("%s is not program %d's text unchanged", f.Path, i)
		}
	}
	if i64Lit(1<<63) != "(-9223372036854775807 -% 1)" || i64Lit(1<<64-1) != "-1" {
		t.Errorf("i64Lit: %s %s", i64Lit(1<<63), i64Lit(1<<64-1))
	}
}

// oracleRun is one batch through the three executions on target.
type oracleRun struct {
	progs     []SoundProgram
	seeds     [][]uint64
	eval      [][]int64
	dev, rel  *NativeRun
	diffs     []OracleDiff
	warm      bool
	evalTime  time.Duration
	totalTime time.Duration
	r         *Runner // the batch's runner, for the minimiser
	dir       string
	// devBisect and relBisect are the bisection's builds (OracleBisect);
	// programs and calls what the comparison judged (OracleCompared).
	devBisect, relBisect int
	programs, calls      int
}

// plantNative is progs with program i's text changed for the native half
// only: the end of its Run, "h = h *% 31 +% k\n    return h", becomes to.
func plantNative(t testing.TB, progs []SoundProgram, i int, to string) []SoundProgram {
	t.Helper()
	native := slices.Clone(progs)
	old := native[i].Text
	native[i].Text = strings.Replace(old, "h = h *% 31 +% k\n    return h", to, 1)
	if native[i].Text == old {
		t.Fatal("the plant did not apply")
	}
	return native
}

// runOracleBatch builds the batch of native (the native half's texts; nil
// means progs, and a test plants a change there) in dev and release for
// target, runs both, bisects an execution that exits non-zero
// (OracleBisect), and runs the evaluator half of progs against the dev
// values.
func runOracleBatch(t testing.TB, check UnitChecker, label string, progs []SoundProgram, seeds [][]uint64, native []SoundProgram, target string) *oracleRun {
	t.Helper()
	if native == nil {
		native = progs
	}
	files := OracleBatch(native, seeds)
	t0 := time.Now()
	tools := needLLVM(t)
	cache := OracleCacheDir()
	_, statErr := os.Stat(cache)
	or := &oracleRun{progs: progs, seeds: seeds, warm: statErr == nil}
	root := filepath.Join(os.TempDir(), "mk-oracle")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, label+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := writeUnitFiles(filepath.Join(dir, "b0"), files); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(dir, driver.Config{Tools: tools, CacheDir: cache, CacheBudget: OracleCacheBudget}, filepath.Join(dir, "exe"))
	r.WSLSlots = testWSLSlots
	or.r, or.dir = r, dir
	nseeds := make([]int, len(progs))
	for i := range progs {
		nseeds[i] = len(seeds[i])
	}
	var wg sync.WaitGroup
	var devErr, relErr error
	wg.Add(2)
	native1 := func(profile string, out **NativeRun, builds *int, errp *error) {
		defer wg.Done()
		n, err := OracleNative(r, "b0", nseeds, target, profile)
		if err == nil && n.Exit != 0 {
			*builds, err = OracleBisect(r, "b0", native, seeds, n, target, profile, OracleBisectBuilds)
		}
		*out, *errp = n, err
	}
	go native1("dev", &or.dev, &or.devBisect, &devErr)
	go native1("release", &or.rel, &or.relBisect, &relErr)
	wg.Wait()
	for _, err := range []error{devErr, relErr} {
		if ErrNoRunner(err) {
			t.Skip(err)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t1 := time.Now()
	s := soundSess(t)
	or.eval = make([][]int64, len(progs))
	errs := make([]error, len(progs))
	sem := make(chan struct{}, 8)
	for i := range progs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			// The evaluator half: the tagged run's value, checked by a
			// plain run.
			or.eval[i], errs[i] = OracleEvalSeam(s, fmt.Sprintf("oracle/%s/%s/%d/%d", label, target, i, soundUnitN.Add(1)), progs[i], seeds[i])
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("program %d (seed %#x): %v", i, progs[i].Seed, err)
		}
	}
	or.evalTime = time.Since(t1)
	or.diffs = CompareOracle(progs, seeds, or.eval, or.dev, or.rel)
	or.programs, or.calls = OracleCompared(seeds, or.dev, or.rel)
	or.totalTime = time.Since(t0)
	return or
}

// BenchmarkOracleBatch is the oracle's speed benchmark (target: at least 20
// programs per second, in batches of 128): each iteration generates and
// checks OracleBatchMax accepted programs of profile oracle (base
// OracleQuickBase+1, continuing where the last iteration stopped), 4 seeds
// each, and runs them through a dev build, a release build and the
// evaluator half on the host target. The programs/s metric is the whole
// iteration's rate. Run it alone, under the machine lock, with
// -benchtime Nx (each iteration takes seconds).
// oracleBenchRounds counts BenchmarkOracleBatch's calls in this process.
var oracleBenchRounds atomic.Int64

func BenchmarkOracleBatch(b *testing.B) {
	s := soundSess(b)
	check := sessChecker(s)
	target := driver.HostTarget().Name
	next, total, diffs := 0, 0, 0
	// The testing package calls a benchmark more than once (b.N 1, then
	// the measured N) on the one shared session, which refuses a unit
	// name it has seen: each call names its batches by its own round.
	round := oracleBenchRounds.Add(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var progs []SoundProgram
		var seeds [][]uint64
		progs, seeds, next = oracleAccepted(b, check, fmt.Sprintf("bench/r%d/b%d", round, i), ProfileOracle, OracleQuickBase+1, next, OracleBatchMax, 4)
		or := runOracleBatch(b, check, fmt.Sprintf("bench-r%d-%d", round, i), progs, seeds, nil, target)
		total += or.programs
		diffs += len(or.diffs)
	}
	b.StopTimer()
	if diffs > 0 {
		b.Errorf("%d differences in %d programs: run TestOracle_Long to minimise and record them", diffs, total)
	}
	b.ReportMetric(float64(total)/b.Elapsed().Seconds(), "programs/s")
}

// reportDiffs fails the test once per difference with what a record needs.
func reportDiffs(t *testing.T, or *oracleRun, target string) {
	t.Helper()
	for _, d := range or.diffs {
		t.Errorf("miscompile on %s: program %d seed %#x k %#x: eval %d dev %d release %d (trapped: %q): %s, owner %s\n%s",
			target, d.Program, d.Seed, d.K, d.Eval, d.Dev, d.Release, d.Trapped, d.Presentation, d.Owner, numbered(or.progs[d.Program].Text))
	}
}

// TestOracle_Quick is the quick set: profile oracle, base
// OracleQuickBase, 64 accepted programs, 4 seeds each, one batch: a dev
// build, a release build and the evaluator half, all three equal. Its time
// is logged, never asserted. The Linux leaf cross-builds the same
// batch and runs it in WSL, only with MK_LINUX=1 under the WSL lock.
func TestOracle_Quick(t *testing.T) {
	s := soundSess(t)
	check := sessChecker(s)
	t0 := time.Now()
	progs, seeds, _ := oracleAccepted(t, check, "quick", ProfileOracle, OracleQuickBase, 0, 64, 4)
	gen := time.Since(t0)
	// The GenVersion 8 shapes among the quick set: programs with each.
	var views []string
	for id := sArmDiverge; id <= nShapes; id++ {
		n := 0
		for _, p := range progs {
			if slices.Contains(p.Shapes, uint16(id)) {
				n++
			}
		}
		if n > 0 {
			views = append(views, fmt.Sprintf("S%02d %d", id, n))
		}
	}
	t.Logf("quick set: %d programs; with a view shape: %s", len(progs), strings.Join(views, ", "))
	targets := []string{driver.HostTarget().Name}
	if linuxRun() && targets[0] != "x86_64-linux" {
		targets = append(targets, "x86_64-linux")
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			if target == "x86_64-linux" && driver.HostTarget().Name != target {
				needWSLRun(t, target, true)
			}
			or := runOracleBatch(t, check, "quick", progs, seeds, nil, target)
			reportDiffs(t, or, target)
			t.Logf("quick set on %s: %d programs, %d calls, %d differences; generate and check %v, dev build %d ms, release build %d ms, runs %d+%d ms, evaluator half %v, total %v (oracle cache %s was %s)",
				target, len(progs), 4*len(progs), len(or.diffs), gen.Round(time.Millisecond), or.dev.BuildMS, or.rel.BuildMS, or.dev.RunMS, or.rel.RunMS,
				or.evalTime.Round(time.Millisecond), (gen + or.totalTime).Round(time.Millisecond), OracleCacheDir(), map[bool]string{true: "warm", false: "cold"}[or.warm])
		})
	}
}

// TestOracle_PlantedDifference: a batch whose native half runs a different
// text for one program (its Run folds k with 37 in place of 31) must show
// that program, on every seed, as "eval differs; dev = release", and
// nothing else.
func TestOracle_PlantedDifference(t *testing.T) {
	s := soundSess(t)
	check := sessChecker(s)
	progs, seeds, _ := oracleAccepted(t, check, "planted", ProfileOracle, OracleQuickBase^0x504c414e54, 0, 6, 2)
	// The planted program is the first from the fourth on whose Run has no
	// early return (GenVersion 8's diverging arms and else blocks return
	// h), so the planted ending runs on every seed.
	planted := -1
	for i := 3; i < len(progs) && planted < 0; i++ {
		if _, run, _ := strings.Cut(progs[i].Text, "export Run("); strings.Count(run, "return h") == 1 {
			planted = i
		}
	}
	if planted < 0 {
		t.Fatal("no program from the fourth on runs to its ending on every path")
	}
	native := plantNative(t, progs, planted, "h = h *% 37 +% k\n    return h")
	or := runOracleBatch(t, check, "planted", progs, seeds, native, driver.HostTarget().Name)
	if len(or.diffs) != len(seeds[planted]) {
		t.Fatalf("%d differences, want %d (program %d's seeds): %+v", len(or.diffs), len(seeds[planted]), planted, or.diffs)
	}
	for _, d := range or.diffs {
		if d.Program != planted || d.Presentation != "eval differs; dev = release" || d.Owner != "borrow" {
			t.Errorf("difference %+v, want program %d, eval differs; dev = release, owner soundness", d, planted)
		}
		// The evaluator's value was read bit by bit: it is the unplanted
		// program's, so dev = 31*x + k and eval = ... differ only there.
		if d.Eval == d.Dev {
			t.Errorf("program %d seed %d: the evaluator's value read bit by bit equals dev's", d.Program, d.SeedIndex)
		}
	}
}

// soundEnv reads an MK_SOUND_* switch.
func soundEnv(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// soundBase is MK_SOUND_BASE, default DefaultSoundBase.
func soundBase(t testing.TB) uint64 {
	var b uint64
	if _, err := fmt.Sscanf(soundEnv("MK_SOUND_BASE", fmt.Sprintf("%#x", DefaultSoundBase)), "0x%x", &b); err != nil {
		t.Fatalf("MK_SOUND_BASE: want 0x<hex>: %v", err)
	}
	return b
}

// OracleRecord is the long set's run record, runs/<label>-oracle.json.
type OracleRecord struct {
	Label        string            `json:"label"`
	Kind         string            `json:"kind"`
	GenVersion   int               `json:"gen_version"`
	Profile      string            `json:"profile"`
	Base         string            `json:"base"`
	First        int               `json:"first"`
	Next         int               `json:"next"`
	Seeds        int               `json:"seeds"`
	Target       string            `json:"target"`
	Programs     int               `json:"programs"`
	Calls        int               `json:"calls"`
	Batches      int               `json:"batches"`
	Builds       int               `json:"builds"`
	Differences  int               `json:"differences"`
	Records      []string          `json:"records"`
	WallSeconds  float64           `json:"wall_seconds"`
	ProgramsPerS float64           `json:"programs_per_s"`
	BuildMSMean  float64           `json:"build_ms_mean"`
	Machine      Machine           `json:"machine"`
	Build        string            `json:"build"`
	Command      map[string]string `json:"command"`
}

// TestOracle_Long is the long set (MK_SOUND=1): batches of 128
// accepted oracle-profile programs, 4 seeds each, for MK_SOUND_SECONDS
// (default 600), on the host target; each difference minimised and
// recorded under tests/soundness/miscompile/open/, and the run record
// written as runs/<label>-oracle.json. Run it in the background under the
// machine lock (go run ./tools/gate lock -- go test -run '^TestOracle_Long$'
// -timeout 0 ./internal/corpus).
func TestOracle_Long(t *testing.T) {
	if os.Getenv("MK_SOUND") != "1" {
		t.Skip("MK_SOUND=1 not set")
	}
	s := soundSess(t)
	check := sessChecker(s)
	secs := 600
	fmt.Sscanf(soundEnv("MK_SOUND_SECONDS", "600"), "%d", &secs)
	base := soundBase(t)
	first := 0
	fmt.Sscanf(soundEnv("MK_SOUND_FIRST", "0"), "%d", &first)
	target := driver.HostTarget().Name
	label := soundEnv("MK_SOUND_LABEL", fmt.Sprintf("%s-oracle-%016x-%d", time.Now().Format("2006-01-02"), base, first))
	rec := &OracleRecord{Label: label, Kind: "oracle", GenVersion: GenVersion, Profile: ProfileOracle.String(), Base: fmt.Sprintf("%#016x", base),
		First: first, Seeds: 4, Target: target, Records: []string{}, Machine: soundMachine(), Build: fmt.Sprintf("%x", driver.BuildID()),
		Command: map[string]string{"test": "TestOracle_Long", "MK_SOUND_SECONDS": fmt.Sprint(secs), "MK_SOUND_BASE": fmt.Sprintf("%#x", base), "MK_SOUND_FIRST": fmt.Sprint(first), "MK_STD": os.Getenv("MK_STD"), "MK_MACHINE": os.Getenv("MK_MACHINE")}}
	t0 := time.Now()
	deadline := t0.Add(time.Duration(secs) * time.Second)
	next := first
	var buildMS int64
	for time.Now().Before(deadline) {
		var progs []SoundProgram
		var seeds [][]uint64
		progs, seeds, next = oracleAccepted(t, check, fmt.Sprintf("%s/b%d", label, rec.Batches), ProfileOracle, base, next, OracleBatchMax, 4)
		or := oracleLongBatch(t, check, rec, label, progs, seeds, nil, target, true)
		buildMS += or.dev.BuildMS + or.rel.BuildMS
	}
	rec.Next = next
	rec.WallSeconds = time.Since(t0).Seconds()
	rec.ProgramsPerS = float64(rec.Programs) / rec.WallSeconds
	if rec.Builds > 0 {
		rec.BuildMSMean = float64(buildMS) / float64(rec.Builds)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(soundRoot(t), "runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, label+".json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("oracle long set on %s: %d programs, %d calls, %d batches, %d differences in %.0f s: %.1f programs/s (base %#x, first %d, next %d)",
		target, rec.Programs, rec.Calls, rec.Batches, rec.Differences, rec.WallSeconds, rec.ProgramsPerS, base, first, next)
}

// oracleLongBatch is one batch of the long set: progs (natively
// native, nil for progs; a test plants a trap there) through runOracleBatch,
// which bisects a batch that exits non-zero; the record counts the
// builds, the bisection's included, and only the programs and calls the
// comparison judged (OracleCompared). With minimise each difference is
// minimised and written under tests/soundness/miscompile/open/, one record
// per program, and fails the test.
func oracleLongBatch(t testing.TB, check UnitChecker, rec *OracleRecord, label string, progs []SoundProgram, seeds [][]uint64, native []SoundProgram, target string, minimise bool) *oracleRun {
	t.Helper()
	or := runOracleBatch(t, check, fmt.Sprintf("long%d", rec.Batches), progs, seeds, native, target)
	rec.Batches++
	rec.Builds += 2 + or.devBisect + or.relBisect
	rec.Programs += or.programs
	rec.Calls += or.calls
	rec.Differences += len(or.diffs)
	done := map[int]bool{}
	for _, d := range or.diffs {
		if done[d.Program] || !minimise {
			continue // one record per program
		}
		done[d.Program] = true
		p := progs[d.Program]
		text, st := OracleMinimise(check, or.r, or.dir, fmt.Sprintf("%s/m%d", label, d.Program), p, d, target)
		path, err := WriteMiscompile(soundRoot(t), MiscompileOf(d, p, target, label, text, st))
		if err != nil {
			t.Fatal(err)
		}
		rec.Records = append(rec.Records, filepath.ToSlash(path))
		t.Errorf("miscompile: %s (program seed %#x k %#x, %s, owner %s): file it with its owner", path, d.Seed, d.K, d.Presentation, d.Owner)
	}
	t.Logf("batch %d: %d programs, %d compared, %d differences (dev build %d ms, release build %d ms, bisection builds %d+%d)",
		rec.Batches, len(progs), or.programs, len(or.diffs), or.dev.BuildMS, or.rel.BuildMS, or.devBisect, or.relBisect)
	return or
}
