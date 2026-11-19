// Why: the soundness runner needs a real compiler behind its SoundCompiler
// interface: a corpus session that checks units and runs them in the
// dynamic checker.
//
// What: SessionCompiler (Start, Check, RunUnit, Forget, Recycle, Session),
// NewSessionCompiler, and SoundRunOn, which turns the driver's RunResult
// into the runner's SoundRunResult.
//
// How: the session is over driver.CorpusSession.RunUnit and Forget
// (internal/driver/sound.go). corpus's non-test code imports only driver,
// diag and source, so the evaluator's error and violation kinds arrive here
// as words, never as eval types. Start refuses a std not judged through
// flow.

package corpus

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
)

// SessionCompiler is the default SoundCompiler: a corpus session over
// Config's std, checked once at Start and again at each Recycle. Check and
// RunUnit are safe from many goroutines; Start and Recycle are called with
// no Check or RunUnit in flight (RunSound calls Recycle between batches).
type SessionCompiler struct {
	Config driver.Config

	mu sync.RWMutex
	s  *driver.CorpusSession
}

// NewSessionCompiler returns a SessionCompiler over cfg; s, when not nil,
// is the session Start uses first (a test's shared one), so Start does not
// check std again.
func NewSessionCompiler(cfg driver.Config, s *driver.CorpusSession) *SessionCompiler {
	return &SessionCompiler{Config: cfg, s: s}
}

// Start makes the session, unless one was given, and refuses a std whose
// Through is before flow: then stage 7 does not judge units, and RunUnit
// skips every run as "not lowered".
func (c *SessionCompiler) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.s == nil {
		s, err := driver.NewCorpusSession(c.Config)
		if err != nil {
			return err
		}
		c.s = s
	}
	return sessionFlowClean(c.s)
}

// sessionFlowClean is nil when s judges units through flow, else an error
// naming std's first diagnostic.
func sessionFlowClean(s *driver.CorpusSession) error {
	if s.Through() >= diag.PhaseFlow {
		return nil
	}
	r := s.Std()
	why := "no std diagnostic"
	if len(r.Report.Diags) > 0 {
		d := r.Report.Diags[0].Diag
		p := r.Sources.Position(d.At.StartPos())
		why = fmt.Sprintf("%s:%d:%d: %s %s", p.Path, p.Line, p.Col, d.Code, d.Message)
	}
	return fmt.Errorf("sound: std is not flow-clean, so units are judged only through %s: %s", s.Through(), why)
}

// Session is the current session.
func (c *SessionCompiler) Session() *driver.CorpusSession {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.s
}

// Check checks text as the one-file unit named unit.
func (c *SessionCompiler) Check(unit, text string) driver.UnitOutcome {
	return c.Session().CheckUnit(driver.UnitInput{Name: unit, Files: []driver.UnitFile{{Path: "sound.mk", Text: text}}})
}

// RunUnit runs spec on the current session.
func (c *SessionCompiler) RunUnit(spec SoundRunSpec) SoundRunResult {
	return SoundRunOn(c.Session(), spec)
}

// Forget drops the named units' state.
func (c *SessionCompiler) Forget(units []string) { c.Session().Forget(units) }

// Recycle replaces the session with a fresh one, std checked again.
func (c *SessionCompiler) Recycle() error {
	s, err := driver.NewCorpusSession(c.Config)
	if err != nil {
		return err
	}
	if err := sessionFlowClean(s); err != nil {
		return err
	}
	c.mu.Lock()
	c.s = s
	c.mu.Unlock()
	return nil
}

// evalErrKinds spells types.EvalErrorKind's values (EvalFuel = 1 to
// EvalCycle = 5, internal/types/eval.go) as SoundRunResult.ErrKind words;
// TestSound_ErrKindsMatchTypes pins the order.
var evalErrKinds = [...]string{"", "fuel", "panic", "refused", "stage", "cycle"}

// SoundRunOn is one run through CorpusSession.RunUnit, as the runner
// keeps it: a violation as its kind's name and its text over the session's
// source map, an evaluator error as a word and a message.
func SoundRunOn(s *driver.CorpusSession, spec SoundRunSpec) SoundRunResult {
	rs := driver.RunSpec{Unit: spec.Unit, Fn: spec.Fn, Args: spec.Args, Fuel: spec.Fuel}
	if t := spec.Tag; t != nil {
		rs.Tag = &driver.TagOptions{Seed: t.Seed, Cond: t.Cond, Rejected: t.Rejected, NoRaces: t.NoRaces, Max: t.Max, FailAlloc: t.FailAlloc}
	}
	r := s.RunUnit(rs)
	out := SoundRunResult{Value: r.Value, Skipped: r.Skipped, Steps: r.Steps, Starts: r.Starts}
	if e := r.Err; e != nil && len(r.Violations) == 0 && r.Skipped == "" {
		out.ErrKind = fmt.Sprintf("kind%d", e.Kind)
		if int(e.Kind) < len(evalErrKinds) && e.Kind != 0 {
			out.ErrKind = evalErrKinds[e.Kind]
		}
		out.ErrText = e.Message
		if out.ErrKind == "refused" {
			out.ErrText = e.Refusal.String()
			if e.Message != "" {
				out.ErrText += ": " + e.Message
			}
		}
	}
	if len(r.Violations) > 0 {
		sm := s.Std().Sources
		for i := range r.Violations {
			v := &r.Violations[i]
			out.Violations = append(out.Violations, SoundViolation{Kind: v.Kind.String(), Text: violationText(v, sm), Std: v.Std})
		}
	}
	return out
}

// violationText spells v for a record as eval's Violation.String does, with
// each position's file: a std file by its std path, a unit's by its name.
func violationText(v *driver.Violation, sm *source.SourceMap) string {
	pos := func(sp source.Span) string {
		if sm == nil || !sp.IsValid() {
			return "-"
		}
		p := sm.Position(sp.StartPos())
		file := p.Path
		if !strings.HasPrefix(file, "std/") {
			file = path.Base(filepath.ToSlash(file))
		}
		return fmt.Sprintf("%s:%d:%d", file, p.Line, p.Col)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s at %s", v.Kind, pos(v.At))
	if v.Where != "" {
		fmt.Fprintf(&b, " (%s)", v.Where)
	}
	if v.Made.IsValid() {
		fmt.Fprintf(&b, "; tag made at %s", pos(v.Made))
	}
	if v.Removed.IsValid() {
		fmt.Fprintf(&b, "; removed at %s", pos(v.Removed))
	}
	if v.Task != 0 || v.Other != 0 {
		fmt.Fprintf(&b, "; task %d, other %d", v.Task, v.Other)
	}
	return b.String()
}
