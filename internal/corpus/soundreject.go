// Why: a borrow checker that is sound may still reject too much. Running
// rejected programs in the dynamic checker estimates how many of the loan
// rules' rejections were false: a program that runs clean was rejected for
// nothing the seeds could show.
//
// What: the rejection sample (the run record's "t7" section): RejectSeeds,
// RejectReport and RejectRate, RunReject, rejectFill, rejectCut, rejectTally,
// rejectByShape, rejectJudge, writeRejectExamples and rejectMinimise.
//
// How: only programs a loan code (E0501 to E0507, E0602 to E0605) rejects
// count; each runs with Rejected on RejectSeeds seeds, and clean means no
// violation and no Skipped run. The rate is an upper estimate, because the
// seeds miss paths. Clause-restored twins separate a missing clause from
// the rule itself. Units are forgotten window by window so memory stays
// bounded.

package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mk/internal/source"
)

// RejectSeeds is the run seeds per sampled program.
const RejectSeeds = 8

// RejectRate is one rate of the sample: clean over runnable, with its 95
// percent Wilson interval.
type RejectRate struct {
	Clean    int     `json:"clean"`
	Runnable int     `json:"runnable"`
	Rate     float64 `json:"rate"`
	Low      float64 `json:"low"`
	High     float64 `json:"high"`
}

// RejectReport is the t7 section of a rejection-sample run record.
type RejectReport struct {
	Sampled  int `json:"sampled"`   // programs a loan code rejects, run
	MoveOnly int `json:"move_only"` // rejected only by move, init or linear codes, counted apart
	Accepted int `json:"accepted"`  // generated programs the compiler accepted (not sampled)
	Defects  int `json:"defects"`   // generator defects (not sampled)
	// ICE counts the programs and twins whose check crashed (an E0000:
	// a compiler bug, among them a miss of borrow's split-union check),
	// counted apart from the generator's
	// defects, with the first five as "<index> <seed>" (" twin" for a
	// twin's). TestSound_Reject fails on any.
	ICE       int            `json:"ice"`
	ICESeeds  []string       `json:"ice_seeds"`
	Generated int            `json:"generated"` // programs generated to fill the sample
	Skipped   map[string]int `json:"skipped"`   // sampled programs with a Skipped run, by reason: out of the denominator
	// Errored counts the sampled programs with a run that ended in an
	// evaluator error (a panic, fuel, a refusal), by kind: neither a
	// completion nor a Skipped run, so they stay in the denominator and are
	// not clean.
	Errored map[string]int        `json:"errored"`
	Overall RejectRate            `json:"overall"`
	ByCode  map[string]RejectRate `json:"by_code"`
	// Clean names the first clean programs (program index and seed), for the
	// minimised examples.
	Clean []string `json:"clean_examples"`

	// Body is the rate over sampled programs with a body loan code (E0501 to
	// E0507), Boundary over those whose loan codes are all boundary codes
	// (E0602 to E0605).
	Body     RejectRate `json:"body"`
	Boundary RejectRate `json:"boundary"`
	// Twins: each sampled program whose helpers lost a clause to the
	// clause draws is replaced by its clause-restored twin (GenerateSoundTwin).
	// A twin the compiler accepts means the program was rejected for its
	// signature, which misstates its body for every caller; it leaves the
	// adjusted population. A twin a loan code rejects runs as the program
	// would, and counts in its place.
	Twins RejectTwins `json:"twins"`
	// Adjusted is the rate over the sample with every clause restored: the
	// share of the loan rules' own rejections that run clean (values holding
	// loans and into, with a complete signature).
	Adjusted       RejectRate            `json:"adjusted"`
	AdjustedByCode map[string]RejectRate `json:"adjusted_by_code"`
	AdjustedClean  []string              `json:"adjusted_clean_examples"`
}

// RejectTwins counts the twins of the sampled programs that dropped a clause.
type RejectTwins struct {
	Dropped  int            `json:"dropped"`  // sampled programs with a dropped clause
	Accepted int            `json:"accepted"` // twin accepted: the missing clause was the whole rejection
	Rejected int            `json:"rejected"` // twin rejected by a loan code: it runs in the program's place
	Move     int            `json:"move"`     // twin rejected only by move, init or linear codes
	Defects  int            `json:"defects"`
	ICE      int            `json:"ice"`     // twin's check crashed (E0000), also counted in RejectReport.ICE
	Skipped  map[string]int `json:"skipped"` // twin rejected by a loan code with a Skipped run
	Errored  map[string]int `json:"errored"` // twin rejected by a loan code with an errored run: in the adjusted population, not clean
}

// bodyCode reports whether c is a body loan code (E0501 to E0507); the
// other loan codes, E0602 to E0605, are the signature-boundary codes.
func bodyCode(c string) bool { return c >= "E0501" && c <= "E0507" }

// wilson is the 95 percent Wilson score interval of k successes in n.
func wilson(k, n int) (float64, float64) {
	if n == 0 {
		return 0, 0
	}
	const z = 1.959963984540054
	p := float64(k) / float64(n)
	d := 1 + z*z/float64(n)
	c := (p + z*z/(2*float64(n))) / d
	h := z * math.Sqrt(p*(1-p)/float64(n)+z*z/(4*float64(n)*float64(n))) / d
	return max(0, c-h), min(1, c+h)
}

func rateOf(clean, runnable int) RejectRate {
	lo, hi := wilson(clean, runnable)
	r := RejectRate{Clean: clean, Runnable: runnable, Low: lo, High: hi}
	if runnable > 0 {
		r.Rate = float64(clean) / float64(runnable)
	}
	return r
}

// loanCode reports whether c is a loan code the sample counts (E0501 to E0507,
// E0602 to E0605).
func loanCode(c string) bool {
	return (c >= "E0501" && c <= "E0507") || (c >= "E0602" && c <= "E0605")
}

// rejectSlot is one generated program's outcome in the sample.
type rejectSlot struct {
	class   string // "sampled", "move", "accepted", "defect", "ice"
	codes   []string
	clean   bool
	skipped string
	errored string // the evaluator error's kind of the run that ended in one
	seed    uint64
	dropped int
	twin    *rejectSlot // the clause-restored twin, when a sampled program dropped a clause
}

// RunReject samples cfg.Reject programs of profile reject: each a
// loan code rejects runs with Rejected and RejectSeeds run seeds, stopping at
// its first violation; clean means every run completed with no violation
// and none was Skipped. The report is written as <Out>/runs/<Label>.json
// with the sample in its t7 section. It stops early when ctx ends.
//
// As RunSound, it takes indexes in windows of cfg.Recycle programs
// (default 5,000): after each window it forgets every unit it checked (a
// program's and its twin's), in batches of 256, and starts a new session
// unless the sample is full, so a sample of any size holds at most one
// window's units.
func RunReject(ctx context.Context, cfg SoundConfig, c SoundCompiler) (*SoundReport, *RejectReport, error) {
	if cfg.Reject <= 0 {
		return nil, nil, fmt.Errorf("sound: RunReject needs Reject > 0")
	}
	cfg = rejectConfig(cfg)
	if err := c.Start(); err != nil {
		return nil, nil, err
	}
	start := time.Now()
	slots, recycles, err := rejectFill(ctx, cfg, c)
	if err != nil {
		return nil, nil, err
	}
	return rejectTally(cfg, c, slots, recycles, start)
}

// rejectConfig is cfg with the rejection sample's defaults: the workers, the window of Recycle
// programs (5,000), and the profile (reject, or reject7 with the m7
// profile's shapes).
func rejectConfig(cfg SoundConfig) SoundConfig {
	if cfg.Workers <= 0 {
		cfg.Workers = soundWorkers()
	}
	if cfg.Recycle <= 0 {
		cfg.Recycle = 5000
	}
	if cfg.Profile != ProfileReject7 && cfg.Profile != ProfileRejectF {
		cfg.Profile = ProfileReject
	}
	return cfg
}

// rejectFill judges programs cfg.First onwards until cfg.Reject are sampled,
// in windows of cfg.Recycle indexes, forgetting every unit of a window (a
// program's and its twin's, in batches of 256) and recycling the session
// between windows. The slots are in index order, so a cut at the
// Reject-th sampled program (rejectCut) is the same whatever the timing; a
// slot never taken has class "". RunReject and TestSoundRejectBreakdown
// both fill through it.
func rejectFill(ctx context.Context, cfg SoundConfig, c SoundCompiler) ([]rejectSlot, int, error) {
	var (
		mu       sync.Mutex
		slots    []rejectSlot
		sampled  atomic.Int64
		next     atomic.Int64
		recycles int
	)
	for {
		lo := next.Load()
		hi := lo + int64(cfg.Recycle)
		var wg sync.WaitGroup
		for range cfg.Workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				source.Guard("t7 worker", source.Span{}, func() {
					for ctx.Err() == nil && sampled.Load() < int64(cfg.Reject) {
						i := next.Add(1) - 1
						if i >= hi {
							next.Add(-1)
							return
						}
						sl := rejectOne(cfg, c, int(i)+cfg.First)
						if sl.class == "sampled" {
							sampled.Add(1)
						}
						mu.Lock()
						for int64(len(slots)) <= i {
							slots = append(slots, rejectSlot{})
						}
						slots[i] = sl
						mu.Unlock()
					}
				})
			}()
		}
		wg.Wait()
		var names []string
		for i := lo; i < min(hi, int64(len(slots))); i++ {
			if slots[i].class == "" {
				continue // never taken: the sample was full
			}
			names = append(names, soundUnitName(cfg.Label, int(i)+cfg.First))
			if slots[i].twin != nil {
				names = append(names, soundUnitName(cfg.Label+"-twin", int(i)+cfg.First))
			}
		}
		for len(names) > 0 {
			n := min(256, len(names))
			c.Forget(names[:n])
			names = names[n:]
		}
		if ctx.Err() != nil || sampled.Load() >= int64(cfg.Reject) {
			break
		}
		if err := c.Recycle(); err != nil {
			return nil, recycles, err
		}
		recycles++
	}
	return slots, recycles, nil
}

// rejectCut is slots up to the Reject-th sampled program in index order, the
// sample the rejection rate counts: the same whatever the timing.
func rejectCut(cfg SoundConfig, slots []rejectSlot) []rejectSlot {
	n := 0
	for i, sl := range slots {
		if sl.class == "" {
			return slots[:i]
		}
		if sl.class == "sampled" {
			n++
			if n == cfg.Reject {
				return slots[:i+1]
			}
		}
	}
	return slots
}

// rejectTally counts the cut sample into the report and writes it
// when cfg.Out is set.
func rejectTally(cfg SoundConfig, c SoundCompiler, slots []rejectSlot, recycles int, start time.Time) (*SoundReport, *RejectReport, error) {
	slots = rejectCut(cfg, slots)
	t7 := &RejectReport{Skipped: map[string]int{}, Errored: map[string]int{}, ByCode: map[string]RejectRate{}, Clean: []string{}, ICESeeds: []string{},
		AdjustedByCode: map[string]RejectRate{}, AdjustedClean: []string{}, Twins: RejectTwins{Skipped: map[string]int{}, Errored: map[string]int{}}}
	byClean, byRun := map[string]int{}, map[string]int{}
	adjClean, adjRun := map[string]int{}, map[string]int{}
	clean, runnable := 0, 0
	var bodyN, bodyC, bndN, bndC, adjN, adjC int
	// adjust counts one program of the adjusted population.
	adjust := func(i int, sl *rejectSlot, twin bool) {
		adjN++
		if sl.clean {
			adjC++
			if len(t7.AdjustedClean) < 20 {
				tw := ""
				if twin {
					tw = " twin"
				}
				t7.AdjustedClean = append(t7.AdjustedClean, fmt.Sprintf("%d %#016x %s%s", i+cfg.First, sl.seed, strings.Join(sl.codes, ","), tw))
			}
		}
		for _, code := range sl.codes {
			if loanCode(code) {
				adjRun[code]++
				if sl.clean {
					adjClean[code]++
				}
			}
		}
	}
	// crash counts one program or twin whose check gave an E0000.
	crash := func(i int, sl *rejectSlot, twin string) {
		t7.ICE++
		if len(t7.ICESeeds) < 5 {
			t7.ICESeeds = append(t7.ICESeeds, fmt.Sprintf("%d %#016x%s", i+cfg.First, sl.seed, twin))
		}
	}
	for i, sl := range slots {
		t7.Generated++
		switch sl.class {
		case "accepted":
			t7.Accepted++
			continue
		case "defect":
			t7.Defects++
			continue
		case "ice":
			crash(i, &sl, "")
			continue
		case "move":
			t7.MoveOnly++
			continue
		}
		t7.Sampled++
		if tw := sl.twin; tw != nil {
			t7.Twins.Dropped++
			switch {
			case tw.class == "accepted":
				t7.Twins.Accepted++
			case tw.class == "move":
				t7.Twins.Move++
			case tw.class == "ice":
				t7.Twins.ICE++
				crash(i, tw, " twin")
			case tw.class != "sampled":
				t7.Twins.Defects++
			case tw.skipped != "":
				t7.Twins.Rejected++
				t7.Twins.Skipped[tw.skipped]++
			default:
				t7.Twins.Rejected++
				if tw.errored != "" {
					t7.Twins.Errored[tw.errored]++
				}
				adjust(i, tw, true)
			}
		} else if sl.skipped == "" {
			adjust(i, &sl, false)
		}
		if sl.skipped != "" {
			t7.Skipped[sl.skipped]++
			continue
		}
		if sl.errored != "" {
			t7.Errored[sl.errored]++ // runnable, not clean
		}
		runnable++
		if slices.ContainsFunc(sl.codes, bodyCode) {
			bodyN++
			if sl.clean {
				bodyC++
			}
		} else {
			bndN++
			if sl.clean {
				bndC++
			}
		}
		if sl.clean {
			clean++
			if len(t7.Clean) < 20 {
				t7.Clean = append(t7.Clean, fmt.Sprintf("%d %#016x %s", i+cfg.First, sl.seed, strings.Join(sl.codes, ",")))
			}
		}
		for _, code := range sl.codes {
			if !loanCode(code) {
				continue
			}
			byRun[code]++
			if sl.clean {
				byClean[code]++
			}
		}
	}
	t7.Overall = rateOf(clean, runnable)
	for code, n := range byRun {
		t7.ByCode[code] = rateOf(byClean[code], n)
	}
	t7.Body, t7.Boundary, t7.Adjusted = rateOf(bodyC, bodyN), rateOf(bndC, bndN), rateOf(adjC, adjN)
	for code, n := range adjRun {
		t7.AdjustedByCode[code] = rateOf(adjClean[code], n)
	}
	rep := soundReport(cfg, nil, "rejected", recycles, time.Since(start))
	rep.Kind = "t7"
	rep.Generated = t7.Generated
	rep.ICE = t7.ICE
	rep.ICESeeds = t7.ICESeeds
	raw, err := json.Marshal(t7)
	if err != nil {
		return nil, nil, err
	}
	rep.T7 = raw
	if cfg.Out != "" {
		if err := writeReport(cfg.Out, rep); err != nil {
			return rep, t7, err
		}
		if len(t7.Clean)+len(t7.AdjustedClean) > 0 {
			if err := writeRejectExamples(cfg, c, t7); err != nil {
				return rep, t7, err
			}
		}
	}
	return rep, t7, nil
}

// rejectGroup is one group of rejectByShape: the raw and the adjusted runnable and
// clean counts, as rejectTally's Overall and Adjusted count them.
type rejectGroup struct{ Raw, RawClean, Adj, AdjClean int }

// rejectByShape splits the cut sample by shape: group "all", "no view shape" or
// "some view shape" (view shapes are the m7 profile's, S55 onwards), and one group per
// such shape id that the sampled program carries. It also returns the program
// indexes it counted, in order. Group "all" equals rejectTally's Overall (raw) and
// Adjusted.
func rejectByShape(cfg SoundConfig, slots []rejectSlot) (map[string]*rejectGroup, []int) {
	groups := map[string]*rejectGroup{}
	var idx []int
	for i, sl := range rejectCut(cfg, slots) {
		if sl.class != "sampled" {
			continue
		}
		idx = append(idx, i+cfg.First)
		p := GenerateSound(cfg.Profile, sl.seed)
		keys := []string{"all", "no view shape"}
		for _, id := range p.Shapes {
			if int(id) >= sArmDiverge {
				keys[1] = "some view shape"
				keys = append(keys, fmt.Sprintf("S%02d", id))
			}
		}
		adj := &sl
		if sl.twin != nil {
			adj = sl.twin
			if adj.class != "sampled" {
				adj = nil
			}
		}
		for _, k := range keys {
			g := groups[k]
			if g == nil {
				g = &rejectGroup{}
				groups[k] = g
			}
			if sl.skipped == "" {
				g.Raw++
				if sl.clean {
					g.RawClean++
				}
			}
			if adj != nil && adj.skipped == "" {
				g.Adj++
				if adj.clean {
					g.AdjClean++
				}
			}
		}
	}
	return groups, idx
}

// rejectOne generates, checks and (when a loan code rejects it) runs program i;
// a sampled program that dropped a clause also gets its twin's outcome.
func rejectOne(cfg SoundConfig, c SoundCompiler, i int) rejectSlot {
	seed := ProgramSeed(cfg.Base, i)
	p := GenerateSound(cfg.Profile, seed)
	sl := rejectJudge(c, p, soundUnitName(cfg.Label, i))
	if sl.class == "sampled" && p.Dropped > 0 {
		tw := rejectJudge(c, GenerateSoundTwin(cfg.Profile, seed), soundUnitName(cfg.Label+"-twin", i))
		sl.twin = &tw
	}
	return sl
}

// rejectJudge checks program p as unit and, when a loan code rejects it, runs
// it with Rejected on RejectSeeds run seeds.
func rejectJudge(c SoundCompiler, p SoundProgram, unit string) rejectSlot {
	seed := p.Seed
	sl := rejectSlot{seed: seed, dropped: p.Dropped}
	var class string
	if !guarded(func() { class, sl.codes = classifySoundProg(p, c.Check(unit, p.Text).Report) }) {
		sl.class = "defect"
		return sl
	}
	switch class {
	case "accepted":
		sl.class = "accepted"
		return sl
	case "ice":
		sl.class = "ice" // a compiler crash, never a generator defect
		return sl
	case "rejected":
	default:
		sl.class = "defect"
		return sl
	}
	if !slices.ContainsFunc(sl.codes, loanCode) {
		sl.class = "move"
		return sl
	}
	sl.class, sl.clean = "sampled", true
	for _, k := range RunSeeds(seed, RejectSeeds) {
		var r SoundRunResult
		if !guarded(func() {
			r = c.RunUnit(SoundRunSpec{Unit: unit, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, Rejected: true}})
		}) {
			sl.skipped = "hang"
			break
		}
		if len(r.Violations) > 0 {
			sl.clean = false
			break
		}
		if r.Skipped != "" {
			sl.skipped = r.Skipped
			break
		}
		if r.ErrKind != "" {
			// A panic, fuel or a refusal is neither a completion nor a
			// Skipped run of the host: the program is not clean, and it
			// stays in the denominator.
			sl.clean, sl.errored = false, r.ErrKind
			break
		}
	}
	return sl
}

// writeRejectExamples writes the first clean programs' texts under
// <Out>/runs/<Label>-t7/ (examples, not found records).
func writeRejectExamples(cfg SoundConfig, c SoundCompiler, t7 *RejectReport) error {
	dir := filepath.Join(cfg.Out, "runs", cfg.Label+"-t7")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, e := range t7.Clean {
		var i int
		var seed uint64
		if _, err := fmt.Sscanf(e, "%d %v", &i, &seed); err != nil {
			continue
		}
		p := GenerateSound(cfg.Profile, seed)
		head := fmt.Sprintf("// t7 clean: program %d, gen %d "+cfg.Profile.String()+" seed %#016x, codes %s\n", i, GenVersion, seed, strings.Fields(e)[2])
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.mk", i)), []byte(head+p.Text), 0o644); err != nil {
			return err
		}
	}
	for _, e := range t7.AdjustedClean {
		var i int
		var seed uint64
		if _, err := fmt.Sscanf(e, "%d %v", &i, &seed); err != nil {
			continue
		}
		f := strings.Fields(e)
		p, what := GenerateSound(cfg.Profile, seed), "program"
		if len(f) > 3 && f[3] == "twin" {
			p, what = GenerateSoundTwin(cfg.Profile, seed), "twin"
		}
		head := fmt.Sprintf("// t7 adjusted clean: %s %d, gen %d "+cfg.Profile.String()+" seed %#016x, codes %s\n", what, i, GenVersion, seed, f[2])
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("adj-%d.mk", i)), []byte(head+p.Text), 0o644); err != nil {
			return err
		}
		if !cfg.Minimise {
			continue
		}
		// The minimised example: still rejected by the example's
		// first loan code, still clean on every run seed.
		code := strings.Split(f[2], ",")[0]
		for _, cd := range strings.Split(f[2], ",") {
			if loanCode(cd) {
				code = cd
				break
			}
		}
		min, st := rejectMinimise(cfg, c, p, code)
		head = fmt.Sprintf("// t7 adjusted clean, minimised (%d lines to %d, %d candidates): %s %d, gen %d reject seed %#016x, kept %s\n",
			st.FromLines, st.ToLines, st.Candidates, what, i, GenVersion, seed, code)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("adj-%d.min.mk", i)), []byte(head+min), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// rejectMinimise shrinks clean rejected program p, keeping a text that code
// still rejects (with no diagnostic outside flow) and that runs clean
// with Rejected on p's RejectSeeds run seeds.
func rejectMinimise(cfg SoundConfig, c SoundCompiler, p SoundProgram, code string) (string, MinStats) {
	var n atomic.Int64
	seeds := RunSeeds(p.Seed, RejectSeeds)
	// Every candidate's unit is forgotten afterwards, as the sample's are.
	var mu sync.Mutex
	var units []string
	defer func() {
		for len(units) > 0 {
			k := min(256, len(units))
			c.Forget(units[:k])
			units = units[k:]
		}
	}()
	keep := func(t string) bool {
		unit := fmt.Sprintf("sound/%s/t7min/%x/%d", cfg.Label, p.Seed, n.Add(1))
		mu.Lock()
		units = append(units, unit)
		mu.Unlock()
		class, codes := classifySound(c.Check(unit, t).Report)
		if class != "rejected" || !slices.Contains(codes, code) {
			return false
		}
		for _, k := range seeds {
			r := c.RunUnit(SoundRunSpec{Unit: unit, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, Rejected: true}})
			if len(r.Violations) > 0 || r.Skipped != "" || r.ErrKind != "" {
				return false
			}
		}
		return true
	}
	return MinimiseStats(p.Text, keep, MinBudget{Candidates: 400, Time: 30 * time.Second, Workers: min(cfg.Workers, 8)})
}
