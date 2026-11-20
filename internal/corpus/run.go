// Why: mk corpus judges every design block (and, with --all, every
// conformance unit) of the repository, in parallel, with results that must
// not depend on scheduling.
//
// What: Options (the mk corpus flags), Run, UnitResult, Summary, Result, and
// ExitStatus (0 clean, 1 a unit failed, 2 an extraction or usage error).
//
// How: Run extracts every unit, judges them on a bounded worker pool with
// every worker under source.Guard, stores results by extraction order, then
// applies the pending allowlist and counts.

package corpus

import (
	"fmt"
	"path"
	"runtime"
	"strings"
	"sync"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
)

// Options is one corpus run. The zero value judges every design block of the
// repository found from the working directory.
type Options struct {
	Root        string // --root; "" means FindRoot("")
	Conformance bool   // --all: also the tests/conformance units
	Only        string // --only: a path.Match pattern over unit keys ("doc/memory.md:*")
	NoPending   bool   // --no-pending: a pending unit not in pending.txt counts as failed
	Build       bool   // --build: also build passing ok units, and run those with a Main
	Jobs        int    // -j; <= 0 means runtime.NumCPU()

	// Driver is the session configuration DriverCompiler uses (target, std
	// directory for tests); Root, Jobs and the rest of a build are ignored.
	Driver driver.Config
	// Compiler replaces DriverCompiler; tests inject one.
	Compiler Compiler
	// allow replaces the embedded pending.txt when non-nil; tests inject
	// one, as they inject a Compiler.
	allow *Allowlist
}

// UnitResult is one judged unit.
type UnitResult struct {
	Unit *Unit
	Judgement
	// Explained is the pending.txt reason of a PENDING unit listed there.
	Explained string
	// Parsed is true when the unit's report holds no parse-group diagnostic
	// and no E0000 (the summary's "parsed").
	Parsed bool
	// Snapshot is Snapshot(Unit, result) for a passing unit that has a
	// SnapshotName (a codes/<code> unit), else nil.
	Snapshot []byte
	// Built, Ran, RunStatus and RunSkipped are the driver's build and run
	// outcome under --build: objects produced, a Main
	// linked and started, its exit status (-1 when killed or not started),
	// and why a unit with a Main was built but not run.
	Built, Ran bool
	RunStatus  int
	RunSkipped string
}

// Summary holds the counts of a run.
type Summary struct {
	Blocks, OK, Parse, Reject int // design blocks by verdict
	Parsed                    int // design blocks that parse
	Units                     int // conformance units judged
	Pass, Fail, Pending       int // over every judged unit
	Skip                      int // units whose // host: names another target
	Explained                 int // pending units listed in pending.txt
	// Under --build: units built, units whose Main ran, and units with a
	// Main built but not run (RunSkipped).
	Built, Ran, RunSkipped int
}

// Result is a whole run.
type Result struct {
	Units   []UnitResult // design blocks in document and line order, then conformance units in path order
	Summary Summary
	Std     StdInfo
	Avail   driver.StageSet
	Build   bool // Options.Build: the report prints the build counts
}

// Run extracts, compiles and judges. The error is an extraction, root,
// allowlist or usage error (exit 2); failing units are in the Result.
func Run(opts Options) (*Result, error) {
	root, err := FindRoot(opts.Root)
	if err != nil {
		return nil, err
	}
	if opts.Only != "" {
		if _, err := path.Match(opts.Only, ""); err != nil {
			return nil, fmt.Errorf("--only %q: %v", opts.Only, err)
		}
	}
	blocks, err := DesignBlocks(root)
	if err != nil {
		return nil, err
	}
	var conf []Unit
	if opts.Conformance {
		if conf, err = Units(root); err != nil {
			return nil, err
		}
	}
	var allow Allowlist
	if opts.allow != nil {
		allow = *opts.allow
	} else if allow, err = LoadPending(); err != nil {
		return nil, err
	}
	var units []*Unit
	for i := range blocks {
		u := BlockUnit(&blocks[i])
		if opts.Only != "" {
			if ok, _ := path.Match(opts.Only, u.Key); !ok {
				continue
			}
		}
		units = append(units, u)
	}
	for i := range conf {
		u := &conf[i]
		if opts.Only != "" {
			if ok, _ := path.Match(opts.Only, u.Key); !ok {
				continue
			}
		}
		units = append(units, u)
	}
	comp := opts.Compiler
	if comp == nil {
		comp = DriverCompiler(root, opts.Driver)
	}
	std, err := comp.Start()
	if err != nil {
		return nil, err
	}
	res := &Result{Std: std, Avail: comp.Available(), Build: opts.Build, Units: make([]UnitResult, len(units))}
	mode := driver.UnitCheck
	if opts.Build {
		mode = driver.UnitRun
	}
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	judged := opts.Driver.Target.Name
	if judged == "" {
		judged = driver.HostTarget().Name
	}
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for i, u := range units {
		if u.Meta.Host != "" && u.Meta.Host != judged {
			res.Units[i] = UnitResult{Unit: u, Judgement: Judgement{Status: StatusSkip,
				Reason: "host: holds when judged for " + u.Meta.Host + " only, this run judges " + judged, Expected: u.Expected}}
			continue
		}
		if arch, _, _ := strings.Cut(judged, "-"); u.Meta.Arch != "" && u.Meta.Arch != arch {
			res.Units[i] = UnitResult{Unit: u, Judgement: Judgement{Status: StatusSkip,
				Reason: "arch: holds when judged for " + u.Meta.Arch + " only, this run judges " + judged, Expected: u.Expected}}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, u *Unit) {
			defer wg.Done()
			defer func() { <-sem }()
			if ie := source.Guard("corpus unit "+u.Key, source.Span{}, func() {
				res.Units[i] = judgeOne(comp, u, std, res.Avail, mode)
			}); ie != nil {
				res.Units[i] = UnitResult{Unit: u, Judgement: Judgement{Status: StatusFail,
					Reason: "internal error: " + ie.String(), Expected: u.Expected}}
			}
		}(i, u)
	}
	wg.Wait()
	for i := range res.Units {
		r := &res.Units[i]
		if r.Status == StatusPending {
			if why, ok := allow.Reason(r.Unit.Key); ok {
				r.Explained = why
			} else if opts.NoPending {
				r.Status, r.Reason = StatusFail, "unexplained pending: "+r.Reason
			}
		}
	}
	res.Summary = summarize(res.Units)
	return res, nil
}

// judgeOne compiles and judges one unit: JudgeUnit, then, with --build,
// JudgeBuild.
func judgeOne(comp Compiler, u *Unit, std StdInfo, avail driver.StageSet, mode driver.UnitMode) UnitResult {
	r := comp.Check(u, mode)
	j := JudgeBuild(u, r, mode, JudgeUnit(u, r, std.State, avail))
	parsed := true
	for _, g := range j.Emitted {
		if g.Phase == diag.PhaseParse || g.Code == diag.E0000 {
			parsed = false
		}
	}
	ur := UnitResult{Unit: u, Judgement: j, Parsed: parsed, Built: r.Built, Ran: r.Ran, RunStatus: r.RunStatus, RunSkipped: r.RunSkipped}
	if j.Status == StatusPass && SnapshotName(u) != "" && r.Sources != nil {
		ur.Snapshot = Snapshot(u, r)
	}
	return ur
}

func summarize(us []UnitResult) Summary {
	var s Summary
	for _, r := range us {
		if r.Unit.Block != nil {
			s.Blocks++
			switch r.Unit.Verdict {
			case VerdictOK:
				s.OK++
			case VerdictParse:
				s.Parse++
			case VerdictReject:
				s.Reject++
			}
			if r.Parsed {
				s.Parsed++
			}
		} else {
			s.Units++
		}
		if r.Built {
			s.Built++
		}
		if r.Ran {
			s.Ran++
		}
		if r.RunSkipped != "" {
			s.RunSkipped++
		}
		switch r.Status {
		case StatusPass:
			s.Pass++
		case StatusFail:
			s.Fail++
		case StatusSkip:
			s.Skip++
		case StatusPending:
			s.Pending++
			if r.Explained != "" {
				s.Explained++
			}
		}
	}
	return s
}

// ExitStatus is mk corpus's exit status for a finished run: 0 when nothing
// failed, 1 when a unit failed (an unexplained pending under NoPending has
// become a failure). An extraction or usage error, which Run returns as an
// error, is 2.
func ExitStatus(r *Result) int {
	if r == nil {
		return 2
	}
	if r.Summary.Fail > 0 {
		return 1
	}
	return 0
}
