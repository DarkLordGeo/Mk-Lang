// Why: soundness is shown by checking many generated programs and running
// every accepted one in the dynamic checker; the run must be bounded in
// memory, reproducible, and the same whatever the scheduling.
//
// What: SoundCompiler (what the runner needs from the driver), its mirror
// types SoundTag, SoundRunSpec and SoundRunResult, SoundConfig, RunSound,
// the run record SoundReport with its sections, and classifySound.
//
// How: workers take program indexes, store each outcome at its index, and
// the report merges slots in index order and cuts at the bound. Sessions are
// recycled every Recycle units so memory stays bounded. corpus may not
// import eval or types, so the runner keeps its own mirrors of
// driver.RunSpec and driver.RunResult. A run seed fixes its allocation
// failures, so a record reproduces.

package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
)

// SoundTag mirrors eval.TagOptions for a run through the seam.
type SoundTag struct {
	Seed      uint64
	Cond      int8
	Rejected  bool
	NoRaces   bool
	Max       int
	FailAlloc uint32
}

// soundFailAlloc is the FailAlloc of run seed k in the runner and its
// records: a run seed that is 3 mod 4 fails each page allocation with
// probability 1/3, so the error edges of try, else and onerr run (onerr and
// exits, linear values on error edges); the rest fail none. A record's run
// seed fixes it, so a record reproduces.
func soundFailAlloc(k uint64) uint32 {
	if k%4 == 3 {
		return 3
	}
	return 0
}

// SoundRunSpec mirrors driver.RunSpec: one function of a checked unit.
type SoundRunSpec struct {
	Unit, Fn string
	Args     []uint64
	Fuel     uint64
	Tag      *SoundTag // nil: the plain evaluator
}

// SoundViolation is one violation as the runner keeps it: the kind's name
// (eval.ViolationKind.String) and the violation's text (Violation.String).
type SoundViolation struct {
	Kind string
	Text string
	Std  bool
}

// SoundRunResult mirrors driver.RunResult. ErrKind is "" when the run
// completed, else "fuel", "panic", "refused", "stage" or "cycle".
type SoundRunResult struct {
	Value      uint64
	ErrKind    string
	ErrText    string
	Violations []SoundViolation
	Skipped    string
	Steps      uint64
	Starts     []uint32
}

// SoundCompiler is what the runner needs from the driver; the default is
// over driver.CorpusSession and its RunUnit, and tests give a fake.
type SoundCompiler interface {
	Start() error
	Check(unit string, text string) driver.UnitOutcome
	RunUnit(spec SoundRunSpec) SoundRunResult
	Forget(units []string)
	Recycle() error // a fresh session, std checked again
}

// SoundConfig is one run of the runner.
type SoundConfig struct {
	Profile  Profile
	Base     uint64        // program i has seed splitmix64(Base + i)
	First    int           // the first index (a soak continues where a run stopped)
	Accepted int           // stop after this many accepted programs (0: no bound)
	Budget   time.Duration // stop after this long (0: no bound); at least one bound is set
	Seeds    int           // run seeds per accepted program (default 4)
	Workers  int           // default runtime.NumCPU() - 1, at least 1
	Recycle  int           // units per CorpusSession (default 5,000)
	Reject   int           // rejected programs to sample; 0 none
	Oracle   int           // every Oracle-th accepted program of an eligible profile also goes to the oracle (0 none)
	Minimise bool          // minimise and record each violation
	Out      string        // the record root, tests/soundness; "" writes nothing
	Label    string        // the run record's name
	// CheckOnly checks the programs and runs none: the smoke test's check
	// half.
	CheckOnly bool
	// Generated stops after this many programs are judged (0: no bound),
	// so a fixed sample is a run of its own (the smoke, the determinism test).
	// In RunReject it replaces Reject as the bound: the sample is the first
	// Generated programs, whatever the sampled count.
	Generated int
	// Stop ends the run at the first violation (MK_SOUND_STOP=1).
	Stop bool
	// Command is the run record's command section.
	Command map[string]string
	// OracleSink is the oracle the run hands its programs to: with Oracle
	// set and a profile the oracle takes (OracleTakes),
	// RunSound calls it once, after the run, with every Oracle-th accepted
	// program of the report in index order, and keeps what it returns as
	// the oracle section's result. nil: the section names the programs only.
	OracleSink func(progs []SoundProgram) (json.RawMessage, error)
}

// OracleHandoff is a run record's oracle section when Oracle is set: the
// accepted programs at accepted index 0, Oracle,
// 2·Oracle, ... of the report, in index order, which the oracle ran; the
// list follows from the record (its base, first and the accepted
// programs), so the record names the count and the first program indexes.
type OracleHandoff struct {
	Every    int             `json:"every"`
	Programs int             `json:"programs"`
	First    []int           `json:"first"` // the first program indexes handed over, at most 20
	Result   json.RawMessage `json:"result,omitempty"`
}

// OracleTakes reports whether the oracle takes accepted programs of profile
// p (oracle and core, and full and its weightings; the reject profiles'
// programs are the rejection sample's).
func OracleTakes(p Profile) bool {
	return p != ProfileReject && p != ProfileReject7 && p != ProfileRejectF
}

// DefaultSoundBase is MK_SOUND_BASE's default, "MkSound1".
const DefaultSoundBase uint64 = 0x4d6b536f756e6431

// hangGuard bounds one check or one run: a hang guard, never a
// timing assertion.
const hangGuard = 60 * time.Second

// soundSlot is one program's outcome, stored at its index.
type soundSlot struct {
	done     bool
	seed     uint64
	class    string // "accepted", "rejected", "defect", "ice", "hang", "cut" (not done)
	codes    []string
	phase    string // a defect's earliest non-flow phase, or "flow" for E0402
	shapes   []uint16
	runs     int
	fuel     int
	panics   int
	skipped  []string
	viol     *ViolationRef
	genDur   time.Duration
	checkDur time.Duration
	runDur   time.Duration
}

// ViolationRef is one violation in the run record.
type ViolationRef struct {
	Record  string `json:"record"`
	Kind    string `json:"kind"`
	Seed    string `json:"seed"`
	Run     string `json:"run"`
	Text    string `json:"text"`
	Std     bool   `json:"std"`
	Program int    `json:"program"`
}

// DefectCount counts one phase's defects with the first five seeds.
type DefectCount struct {
	Count int      `json:"count"`
	Seeds []string `json:"seeds"`
}

// Throughput is the run record's rates.
type Throughput struct {
	GeneratedPerS float64 `json:"generated_per_s"`
	CheckedPerS   float64 `json:"checked_per_s"`
	AcceptedPerS  float64 `json:"accepted_per_s"`
	RunsPerS      float64 `json:"runs_per_s"`
	CheckMSMean   float64 `json:"check_ms_mean"`
	RunMSMean     float64 `json:"run_ms_mean"`
	GenUSMean     float64 `json:"gen_us_mean"`
}

// Machine is the run record's machine section.
type Machine struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	CPUs   int    `json:"cpus"`
	CPU    string `json:"cpu"`
	Go     string `json:"go"`
	Locked bool   `json:"locked"`
}

// SoundReport is the run record, runs/<label>.json. Keys come in
// struct order; map keys sorted (encoding/json).
type SoundReport struct {
	Label          string                  `json:"label"`
	Kind           string                  `json:"kind"`
	GenVersion     int                     `json:"gen_version"`
	Profile        string                  `json:"profile"`
	Base           string                  `json:"base"`
	First          int                     `json:"first"`
	Seeds          int                     `json:"seeds"`
	Generated      int                     `json:"generated"`
	Accepted       int                     `json:"accepted"`
	RejectedFlow   int                     `json:"rejected_flow"`
	RejectedByCode map[string]int          `json:"rejected_by_code"`
	Defects        map[string]*DefectCount `json:"defects"`
	ICE            int                     `json:"ice"`
	ICESeeds       []string                `json:"ice_seeds"` // the first five crashes, "<index> <seed>"
	Fuel           int                     `json:"fuel"`
	Panics         int                     `json:"panics"`
	Skipped        map[string]int          `json:"skipped"`
	Runs           int                     `json:"runs"`
	Violations     []ViolationRef          `json:"violations"`
	Shapes         map[string][2]int       `json:"shapes"`
	WallSeconds    float64                 `json:"wall_seconds"`
	Workers        int                     `json:"workers"`
	Recycles       int                     `json:"recycles"`
	Throughput     Throughput              `json:"throughput"`
	Machine        Machine                 `json:"machine"`
	Build          string                  `json:"build"`
	Command        map[string]string       `json:"command"`
	Stopped        string                  `json:"stopped"`
	T7             json.RawMessage         `json:"t7,omitempty"`
	Mutants        json.RawMessage         `json:"mutants,omitempty"`
	Oracle         json.RawMessage         `json:"oracle,omitempty"`

	// handed is the program indexes for the oracle (soundReport, Oracle).
	handed []int
}

// DefectTotal is every generator defect of the run.
func (r *SoundReport) DefectTotal() int {
	n := r.Fuel + r.Panics
	for _, d := range r.Defects {
		n += d.Count
	}
	for why, c := range r.Skipped {
		if why != "would block" {
			n += c
		}
	}
	return n
}

// Summary is the run's one line.
func (r *SoundReport) Summary() string {
	pct := 0.0
	if r.Generated > 0 {
		pct = 100 * float64(r.Accepted) / float64(r.Generated)
	}
	return fmt.Sprintf("sound: %s base %s first %d: generated %d accepted %d (%.1f%%) runs %d violations %d defects %d in %.1f s: %.1f accepted/s on %s/%s %d cpus (%s)",
		r.Profile, r.Base, r.First, r.Generated, r.Accepted, pct, r.Runs, len(r.Violations), r.DefectTotal(), r.WallSeconds, r.Throughput.AcceptedPerS, r.Machine.OS, r.Machine.Arch, r.Machine.CPUs, r.Machine.CPU)
}

// soundWorkers is SoundConfig.Workers' default.
func soundWorkers() int { return max(1, runtime.NumCPU()-1) }

// RunSound runs cfg and returns its report, which it also writes as
// <Out>/runs/<Label>.json when Out is set. It stops early when ctx
// ends. The report merges outcomes in index order and cuts at the bound,
// so it is the same whatever the timing, except wall_seconds, throughput
// and the minimiser's time-bounded passes.
func RunSound(ctx context.Context, cfg SoundConfig, c SoundCompiler) (*SoundReport, error) {
	if cfg.Accepted == 0 && cfg.Budget == 0 && cfg.Generated == 0 {
		return nil, fmt.Errorf("sound: a run needs a bound: Accepted, Budget or Generated")
	}
	if cfg.Seeds <= 0 {
		cfg.Seeds = 4
	}
	if cfg.Workers <= 0 {
		cfg.Workers = soundWorkers()
	}
	if cfg.Recycle <= 0 {
		cfg.Recycle = 5000
	}
	if cfg.Label == "" {
		cfg.Label = fmt.Sprintf("%s-%s-%016x-%d", time.Now().Format("2006-01-02"), cfg.Profile, cfg.Base, cfg.First)
	}
	if err := c.Start(); err != nil {
		return nil, err
	}
	start := time.Now()
	if cfg.Budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Budget)
		defer cancel()
	}
	var (
		mu       sync.Mutex
		slots    []soundSlot
		accepted atomic.Int64
		next     atomic.Int64
		stopped  atomic.Value
		recycles int
	)
	stopped.Store("")
	stop := func(why string) { stopped.CompareAndSwap("", why) }
	limit := int64(1<<62 - 1)
	if cfg.Generated > 0 {
		limit = int64(cfg.Generated)
	}
	for batch := int64(0); stopped.Load() == ""; batch++ {
		lo := next.Load()
		hi := min(lo+int64(cfg.Recycle), limit)
		if lo >= hi {
			stop("generated")
			break
		}
		var wg sync.WaitGroup
		for range cfg.Workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if ie := source.Guard("sound worker", source.Span{}, func() {
					for {
						if ctx.Err() != nil {
							stop("budget")
							return
						}
						if cfg.Accepted > 0 && accepted.Load() >= int64(cfg.Accepted) {
							stop("accepted")
							return
						}
						i := next.Add(1) - 1
						if i >= hi {
							next.Add(-1)
							return
						}
						sl := soundOne(ctx, cfg, c, int(i)+cfg.First)
						if sl.class == "accepted" {
							accepted.Add(1)
						}
						if sl.viol != nil && cfg.Stop {
							stop("violation")
						}
						mu.Lock()
						for int64(len(slots)) <= i {
							slots = append(slots, soundSlot{})
						}
						slots[i] = sl
						mu.Unlock()
					}
				}); ie != nil {
					stop("internal error: " + ie.String())
				}
			}()
		}
		wg.Wait()
		if next.Load() >= hi && hi == limit {
			stop("generated")
		}
		var names []string
		for i := lo; i < min(hi, int64(len(slots))); i++ {
			names = append(names, soundUnitName(cfg.Label, int(i)+cfg.First))
		}
		for len(names) > 0 {
			n := min(256, len(names))
			c.Forget(names[:n])
			names = names[n:]
		}
		if stopped.Load() == "" {
			if err := c.Recycle(); err != nil {
				return nil, err
			}
			recycles++
		}
	}
	wall := time.Since(start)
	rep := soundReport(cfg, slots, stopped.Load().(string), recycles, wall)
	if cfg.Oracle > 0 && OracleTakes(cfg.Profile) {
		if err := handOracle(cfg, rep); err != nil {
			return rep, err
		}
	}
	if cfg.Out != "" {
		if err := writeReport(cfg.Out, rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// handOracle writes rep's oracle section: the programs
// soundReport chose, regenerated from their seeds (GenerateSound is
// deterministic), given to cfg.OracleSink when it is set.
func handOracle(cfg SoundConfig, rep *SoundReport) error {
	h := OracleHandoff{Every: cfg.Oracle, Programs: len(rep.handed), First: []int{}}
	progs := make([]SoundProgram, len(rep.handed))
	for k, i := range rep.handed {
		progs[k] = GenerateSound(cfg.Profile, ProgramSeed(cfg.Base, i))
		if len(h.First) < 20 {
			h.First = append(h.First, i)
		}
	}
	if cfg.OracleSink != nil {
		res, err := cfg.OracleSink(progs)
		if err != nil {
			return err
		}
		h.Result = res
	}
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	rep.Oracle = raw
	return nil
}

// soundUnitName is program i's unit name in run label.
func soundUnitName(label string, i int) string { return fmt.Sprintf("sound/%s/%d", label, i) }

// guarded runs f with the hang guard; ok is false when it did not return
// within hangGuard (the goroutine is then abandoned, and the run goes on).
func guarded(f func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		source.Guard("sound check or run", source.Span{}, f)
	}()
	t := time.NewTimer(hangGuard)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

// soundOne generates, checks and runs the program at index i.
func soundOne(ctx context.Context, cfg SoundConfig, c SoundCompiler, i int) soundSlot {
	t0 := time.Now()
	seed := ProgramSeed(cfg.Base, i)
	p := GenerateSound(cfg.Profile, seed)
	sl := soundSlot{done: true, seed: seed, shapes: p.Shapes, genDur: time.Since(t0)}
	unit := soundUnitName(cfg.Label, i)
	t1 := time.Now()
	var out driver.UnitOutcome
	if !guarded(func() { out = c.Check(unit, p.Text) }) {
		sl.class, sl.phase = "hang", "check"
		return sl
	}
	sl.checkDur = time.Since(t1)
	sl.class, sl.codes = classifySoundProg(p, out.Report)
	if sl.class == "defect" {
		sl.phase = defectPhase(out.Report)
	}
	if sl.class != "accepted" || cfg.CheckOnly {
		return sl
	}
	t2 := time.Now()
	for _, k := range RunSeeds(seed, cfg.Seeds) {
		if ctx.Err() != nil {
			// The bound ended before every run seed ran: the program is not
			// judged, so its slot is not done and the report cuts there, as
			// at an index never taken.
			sl.done, sl.class = false, "cut"
			break
		}
		var r SoundRunResult
		if !guarded(func() {
			r = c.RunUnit(SoundRunSpec{Unit: unit, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, FailAlloc: soundFailAlloc(k)}})
		}) {
			sl.class, sl.phase = "hang", "run"
			break
		}
		sl.runs++
		switch r.ErrKind {
		case "fuel":
			sl.fuel++
		case "":
		default:
			sl.panics++
		}
		if r.Skipped != "" {
			sl.skipped = append(sl.skipped, r.Skipped)
		}
		if len(r.Violations) > 0 {
			v := r.Violations[0]
			sl.viol = &ViolationRef{Kind: v.Kind, Seed: fmt.Sprintf("%#016x", seed), Run: fmt.Sprintf("%#016x", k), Text: v.Text, Std: v.Std, Program: i}
			if cfg.Minimise || cfg.Out != "" {
				sl.viol.Record = recordViolation(cfg, c, p, k, v)
			}
			break // the first violation stops the program's runs
		}
	}
	sl.runDur = time.Since(t2)
	return sl
}

// defectPhase is the earliest phase of a defect's diagnostics outside flow.
func defectPhase(r diag.Report) string {
	best := diag.PhaseFlow
	for _, e := range r.Diags {
		if e.Diag.Phase < best {
			best = e.Diag.Phase
		}
	}
	return best.String()
}

// soundReport merges the slots in index order and cuts the run at
// its bound: after the Accepted-th accepted program, or at the first index
// not done.
func soundReport(cfg SoundConfig, slots []soundSlot, stopped string, recycles int, wall time.Duration) *SoundReport {
	rep := &SoundReport{
		Label: cfg.Label, Kind: "run", GenVersion: GenVersion, Profile: cfg.Profile.String(),
		Base: fmt.Sprintf("%#016x", cfg.Base), First: cfg.First, Seeds: cfg.Seeds,
		RejectedByCode: map[string]int{}, Defects: map[string]*DefectCount{}, ICESeeds: []string{}, Skipped: map[string]int{},
		Violations: []ViolationRef{}, Shapes: map[string][2]int{}, Workers: cfg.Workers, Recycles: recycles,
		WallSeconds: wall.Seconds(), Machine: soundMachine(), Build: fmt.Sprintf("%x", driver.BuildID()),
		Command: cfg.Command, Stopped: stopped,
	}
	if cfg.CheckOnly {
		rep.Seeds = 0
	}
	if rep.Command == nil {
		rep.Command = map[string]string{}
	}
	for _, k := range []string{"MK_STD", "MK_MACHINE", "MK_MACHINE_LOCKED"} {
		if v := os.Getenv(k); v != "" {
			rep.Command[k] = v // a run over another std reproduces only with it
		}
	}
	var gen, check, run time.Duration
	accepted := 0
	for i, sl := range slots {
		if !sl.done {
			break
		}
		if cfg.Accepted > 0 && accepted >= cfg.Accepted {
			break
		}
		rep.Generated++
		gen += sl.genDur
		check += sl.checkDur
		run += sl.runDur
		switch sl.class {
		case "accepted":
			if cfg.Oracle > 0 && accepted%cfg.Oracle == 0 {
				rep.handed = append(rep.handed, i+cfg.First) // in index order
			}
			accepted++
			rep.Accepted++
		case "rejected":
			rep.RejectedFlow++
			for _, c := range sl.codes {
				rep.RejectedByCode[c]++
			}
		case "ice":
			rep.ICE++
			if len(rep.ICESeeds) < 5 {
				rep.ICESeeds = append(rep.ICESeeds, fmt.Sprintf("%d %#016x", i+cfg.First, sl.seed))
			}
		case "defect", "hang":
			ph := sl.phase
			if sl.class == "hang" {
				ph = "hang " + ph
			}
			d := rep.Defects[ph]
			if d == nil {
				d = &DefectCount{Seeds: []string{}}
				rep.Defects[ph] = d
			}
			d.Count++
			if len(d.Seeds) < 5 {
				d.Seeds = append(d.Seeds, fmt.Sprintf("%#016x", sl.seed))
			}
		}
		for _, id := range sl.shapes {
			k := fmt.Sprintf("S%02d", id)
			v := rep.Shapes[k]
			switch sl.class {
			case "accepted":
				v[0]++
			case "rejected":
				v[1]++
			}
			rep.Shapes[k] = v
		}
		rep.Runs += sl.runs
		rep.Fuel += sl.fuel
		rep.Panics += sl.panics
		for _, s := range sl.skipped {
			rep.Skipped[s]++
		}
		if sl.viol != nil {
			rep.Violations = append(rep.Violations, *sl.viol)
		}
	}
	if s := wall.Seconds(); s > 0 {
		rep.Throughput.GeneratedPerS = float64(rep.Generated) / s
		rep.Throughput.CheckedPerS = float64(rep.Generated) / s
		rep.Throughput.AcceptedPerS = float64(rep.Accepted) / s
		rep.Throughput.RunsPerS = float64(rep.Runs) / s
	}
	if rep.Generated > 0 {
		rep.Throughput.CheckMSMean = float64(check.Microseconds()) / 1000 / float64(rep.Generated)
		rep.Throughput.GenUSMean = float64(gen.Nanoseconds()) / 1000 / float64(rep.Generated)
	}
	if rep.Runs > 0 {
		rep.Throughput.RunMSMean = float64(run.Microseconds()) / 1000 / float64(rep.Runs)
	}
	return rep
}

func soundMachine() Machine {
	cpu := os.Getenv("MK_MACHINE")
	if cpu == "" {
		cpu = "unknown"
	}
	return Machine{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), CPU: cpu, Go: runtime.Version(), Locked: os.Getenv("MK_MACHINE_LOCKED") == "1"}
}

// writeReport writes rep as <out>/runs/<label>.json.
func writeReport(out string, rep *SoundReport) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(out, "runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, rep.Label+".json"), append(b, '\n'), 0o644)
}

// SoundRecordKeys is the run record's top-level keys in order, for
// TestSound_RunRecordSchema.
var SoundRecordKeys = []string{"label", "kind", "gen_version", "profile", "base", "first", "seeds",
	"generated", "accepted", "rejected_flow", "rejected_by_code", "defects", "ice", "ice_seeds", "fuel", "panics",
	"skipped", "runs", "violations", "shapes", "wall_seconds", "workers", "recycles", "throughput",
	"machine", "build", "command", "stopped"}

// classifySound sorts a checked program: "ice" (an E0000),
// "defect" (a diagnostic outside phase flow, or a flow code the generator's
// own rules forbid: E0402), "rejected" (every diagnostic in phase
// flow) or "accepted" (none). codes are the distinct codes, sorted.
func classifySound(r diag.Report) (string, []string) { return classifySoundLeak(r, false, false) }

// classifySoundProg is classifySound for generated program p: an E0402 is
// a rejection by the linear-value rule when S37 left a sub-arena unfreed on
// purpose (p.Leak), and a generator defect otherwise. A types-group E0609
// or E0705 is a capture restriction rejecting a form S47 wrote on purpose when
// p.CapMisuse (GenVersion 7), and a defect otherwise.
func classifySoundProg(p SoundProgram, r diag.Report) (string, []string) {
	return classifySoundLeak(r, p.Leak, p.CapMisuse)
}

func classifySoundLeak(r diag.Report, leak, capMisuse bool) (string, []string) {
	var codes []string
	class := "accepted"
	for _, e := range r.Diags {
		d := e.Diag
		c := d.Code.String()
		if !slices.Contains(codes, c) {
			codes = append(codes, c)
		}
		switch {
		case d.Code == diag.E0000:
			class = "ice"
		case class == "ice":
		case capMisuse && (d.Code == diag.E0609 || d.Code == diag.E0705):
			if class == "accepted" {
				class = "rejected"
			}
		case d.Phase != diag.PhaseFlow || d.Code == diag.E0402 && !leak:
			class = "defect"
		case class == "accepted":
			class = "rejected"
		}
	}
	slices.Sort(codes)
	return class, codes
}

// sortedKeys returns m's keys sorted.
func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// codeTable spells a code count map as "E0501 3, E0502 1".
func codeTable(m map[string]int) string {
	var parts []string
	for _, k := range sortedKeys(m) {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}
