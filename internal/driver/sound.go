// Why: the soundness runner (internal/corpus) must run the programs the
// checker accepted, and on request the ones it rejected, to see whether a
// verdict matches what the code does at run time.
//
// What: RunSpec, RunResult and CorpusSession.RunUnit, which runs one
// function of a unit CheckUnit has checked; Forget, which drops a finished
// unit's state; soundUnits, the per-unit bookkeeping.
//
// How: RunUnit runs in the plain evaluator or in eval's tagged mode; with
// Tag.Rejected it serves the unit's rejected bodies with their lowered MIR.
// A body that would go through mono stays poisoned, so such a run ends
// Skipped "poisoned" rather than guessing.

package driver

import (
	"strings"
	"sync"

	"mk/internal/diag"
	"mk/internal/eval"
	"mk/internal/types"
)

// The aliases let corpus name eval's types without importing eval, as
// BlockResult = UnitOutcome does.
type (
	TagOptions    = eval.TagOptions
	Violation     = eval.Violation
	ViolationKind = eval.ViolationKind
)

// RunSpec asks CorpusSession.RunUnit to run one function of a unit that
// CheckUnit has already checked in this session.
type RunSpec struct {
	Unit string   // the UnitInput.Name given to CheckUnit
	Fn   string   // a non-generic function of the unit's root module
	Args []uint64 // one per parameter that is not Io or &Arena; each such
	// parameter has an integer type, and the value is truncated to it
	Fuel uint64 // 0 means types.DefaultFuel
	// Tag nil runs the plain evaluator (Eval; a parameter of type Io is then
	// refused as Eval refuses it). Non-nil runs eval's RunTagged with it.
	Tag *TagOptions
}

// RunResult is a run's outcome.
type RunResult struct {
	Value      uint64 // the result's bits as a u64 when the run completed (Err nil)
	Err        *types.EvalError
	Violations []Violation // tagged runs only
	// Skipped is the tagged mode's Skipped, or why RunUnit did not start or
	// could not judge the run:
	//   "no unit"         the name was never checked here, its check had an
	//                     E0000, it was a ParseOnly unit, or it was forgotten;
	//   "no function"     Fn is not a function with a body in the unit's root module;
	//   "generic"         Fn has generic parameters;
	//   "parameter type"  a parameter is neither an integer nor a host parameter
	//                     (Io, &Arena; &Arena only in a tagged run), or len(Args)
	//                     is not the number of such parameters;
	//   "not lowered"     Fn has no MIR, or this session's Through is before
	//                     flow, so stage 7 did not judge the unit (with std not
	//                     flow-clean a rejected body is stored ready and the
	//                     unit's flow diagnostics are not reported, so running
	//                     it would look like running an accepted program);
	//   "rejected"        the unit has diagnostics, and Tag.Rejected is not set
	//                     or one of them is not of phase flow;
	//   "poisoned"        the run ended in poison: it reached a body it
	//                     may not enter, such as a rejected generic body's
	//                     instance under Rejected.
	Skipped string
	Steps   uint64
	Starts  []uint32
}

// The Skipped reasons RunUnit gives itself (the tagged mode adds its own).
const (
	skipNoUnit     = "no unit"
	skipNoFunction = "no function"
	skipGeneric    = "generic"
	skipParamType  = "parameter type"
	skipNotLowered = "not lowered"
	skipRejected   = "rejected"
	skipPoisoned   = "poisoned"
)

// soundUnits is the session's record of each checked unit for RunUnit, and
// the evaluator that serves rejected bodies. The zero value is ready.
type soundUnits struct {
	mu    sync.Mutex
	units map[string]*soundUnit

	rejOnce sync.Once
	rej     *eval.Evaluator // the Rejected runs' evaluator (rejectedProgram)
}

// soundUnit is what RunUnit needs of a unit's check.
type soundUnit struct {
	runnable  bool // checked past parse with no E0000
	diags     bool // the report has a diagnostic
	flowOnly  bool // every diagnostic of the report is of phase flow
	forgotten bool
}

// note records u's outcome. The first record of a name wins: a repeated
// name's check is an E0000 that says nothing of the first unit.
func (s *soundUnits) note(u UnitInput, out *UnitOutcome) {
	r := &soundUnit{runnable: !u.ParseOnly, flowOnly: true}
	for _, e := range out.Report.Diags {
		r.diags = true
		if e.Diag.Phase == diag.PhaseInternal {
			r.runnable = false
		}
		if e.Diag.Phase != diag.PhaseFlow {
			r.flowOnly = false
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.units == nil {
		s.units = map[string]*soundUnit{}
	}
	if s.units[u.Name] == nil {
		s.units[u.Name] = r
	}
}

// unit is name's record, nil when RunUnit may not run it.
func (s *soundUnits) unit(name string) *soundUnit {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.units[name]
	if r == nil || !r.runnable || r.forgotten {
		return nil
	}
	cp := *r
	return &cp
}

// RunUnit runs r. The name sits beside CheckUnit, apart from Session.Run,
// which runs a built program. With r.Tag.Rejected set, every body of the
// unit's own modules that stage 7 rejected is served with its lowered MIR
// (State BodyReady, OnerrRuns nil), provided every diagnostic of the unit is
// in phase flow; otherwise Skipped is "rejected". std's bodies are served as
// always. A rejected body that eval would run through mono (a generic
// instance, or a caller of a Slice[T] function) stays poisoned, and
// such a run ends Skipped "poisoned". Safe for concurrent use with CheckUnit
// and other RunUnit calls.
//
// An internal error in the evaluator is not caught here: it panics as in
// any evaluation, and the runner's source.Guard turns it into a crash record.
func (c *CorpusSession) RunUnit(r RunSpec) RunResult {
	u := c.sound.unit(r.Unit)
	if u == nil {
		return RunResult{Skipped: skipNoUnit}
	}
	w := c.w
	s := w.sem5
	if s == nil || c.through < diag.PhaseFlow {
		return RunResult{Skipped: skipNotLowered}
	}
	// A unit with a diagnostic of any phase but flow is "rejected" before
	// its items are looked at: an item of a unit that failed before check
	// has no Decl.
	if u.diags && !(r.Tag != nil && r.Tag.Rejected && u.flowOnly) {
		return RunResult{Skipped: skipRejected}
	}
	id, ok := w.items.ByKey(types.ItemKey(r.Unit, r.Fn))
	if !ok {
		return RunResult{Skipped: skipNoFunction}
	}
	it := w.items.Get(id)
	fd, _ := it.Decl.(*types.FuncDecl)
	if it.Kind != types.KindFunc || fd == nil || fd.Bodiless || w.items.Module(it.Module).Path != r.Unit {
		return RunResult{Skipped: skipNoFunction}
	}
	if len(fd.Sig.Generics) > 0 {
		return RunResult{Skipped: skipGeneric}
	}
	args, ok := c.runArgs(&fd.Sig, r.Args, r.Tag != nil)
	if !ok {
		return RunResult{Skipped: skipParamType}
	}
	if e := s.body(id); e == nil || e.mir == nil {
		return RunResult{Skipped: skipNotLowered}
	}
	req := &types.EvalRequest{What: types.EvalCall, Item: id, Args: args, Fuel: r.Fuel,
		Stage: types.StageBodies, Module: it.Module, Site: it.Span, Target: w.cfg.Target}
	ev := s.ev
	if u.diags { // rejected && flowOnly: some body of the unit was rejected
		ev = c.rejectedEvaluator()
	}
	var out RunResult
	var res types.EvalResult
	if r.Tag == nil {
		res = ev.Eval(req)
	} else {
		opt := *r.Tag
		tr := ev.RunTagged(req, &opt)
		res = tr.EvalResult
		out.Violations, out.Skipped, out.Starts = tr.Violations, tr.Skipped, tr.Starts
	}
	out.Err, out.Steps = res.Err, res.Steps
	switch {
	case res.Err == nil && res.Value != types.NoConst:
		out.Value = constBits(w.tab.Consts().Get(res.Value).Bytes)
	case res.Err == nil && len(out.Violations) == 0 && out.Skipped == "":
		out.Skipped = skipPoisoned
	}
	return out
}

// runArgs interns vals as the arguments of sig's parameters, each truncated
// to its integer type. A host parameter (Io, &Arena, as eval's hostParam
// knows them) takes no value: in a tagged run eval's host fills it; in a
// plain run Eval refuses an Io parameter before it counts arguments, and
// an &Arena parameter cannot be given. ok is false for any other parameter
// type and for a count that does not match.
func (c *CorpusSession) runArgs(sig *types.Signature, vals []uint64, tagged bool) ([]types.ConstID, bool) {
	tab := c.w.tab
	k := &c.w.sem5.known
	var out []types.ConstID
	for _, p := range sig.Params {
		t := tab.Get(p.Type)
		switch {
		case t.Kind == types.KNamed && k.Io != types.NoItem && t.Item == k.Io:
			continue
		case t.Kind == types.KRef && k.Arena != types.NoItem && tab.Get(t.Elem).Kind == types.KNamed && tab.Get(t.Elem).Item == k.Arena:
			if !tagged {
				return nil, false
			}
			continue
		case !t.IsInteger():
			return nil, false
		}
		if len(out) >= len(vals) {
			return nil, false
		}
		n := int(t.Bits) / 8
		if t.Kind == types.KIntPtr {
			n = int(c.w.cfg.Target.PointerBytes)
		}
		b := make([]byte, n)
		types.StoreUint(b, n, vals[len(out)])
		out = append(out, tab.Consts().Intern(types.ConstValue{Type: p.Type, Align: uint32(n), Bytes: b}))
	}
	if len(out) != len(vals) {
		return nil, false
	}
	return out, true
}

// constBits reads up to the first 8 bytes of a value as a little-endian
// integer (zero-extended; 0 for a void result).
func constBits(b []byte) uint64 {
	var v uint64
	for i := min(len(b), 8) - 1; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}

// rejectedEvaluator is the Evaluator of Rejected runs, made once: the
// session's configuration (startSemantics) over rejectedProgram. It is a
// second Evaluator so that the session's own, which check shares, never
// sees a rejected body; its const slots and compiled code are its own.
func (c *CorpusSession) rejectedEvaluator() *eval.Evaluator {
	c.sound.rejOnce.Do(func() {
		w := c.w
		s := w.sem5
		// The fields of startSemantics' eval.Config, Program aside.
		c.sound.rej = eval.New(eval.Config{Types: w.tab, Items: w.items, Consts: w.tab.Consts(), Interner: w.in,
			Program: rejectedProgram{&program{w: w, s: s}}, Known: s.knownCopy(), Target: w.cfg.Target, TargetItem: s.known.Target,
			PageAlloc: w.stdItem("std.core", "EvalPageAlloc"), PageFree: w.stdItem("std.core", "EvalPageFree"),
			EvalArena: w.stdItem("std.core", "MakeEvalArena"), Evaluating: w.stdItem("std.core", "Evaluating")})
	})
	return c.sound.rej
}

// rejectedProgram is program, except that a body of a unit module (any
// non-std module of a corpus session: units never import one another) that
// stage 7 rejected is served with its lowered MIR as BodyReady with no onerr
// decisions; eval's Rejected mode ends a run that reaches a guard as Skipped.
// lowerBody stores exactly such a body as {mir, BodyPoisoned}; a body with a
// check diagnostic or an ICE has no MIR. RunUnit uses it only for a unit
// whose every diagnostic is of phase flow, so the module's types are sound.
type rejectedProgram struct{ *program }

func (p rejectedProgram) Body(fn types.ItemID, args []types.GenericArg) eval.Body {
	it := p.w.items.Get(fn)
	if len(args) == 0 && !p.w.items.Module(it.Module).Std {
		if e := p.s.body(fn); e != nil && e.mir != nil && e.state == eval.BodyPoisoned {
			if callsSliceFn(p.w.items, e.mir) {
				// mono makes the body eval runs, and mono serves no
				// rejected body: poison.
				return eval.Body{State: eval.BodyPoisoned}
			}
			return eval.Body{MIR: e.mir, State: eval.BodyReady}
		}
	}
	return p.program.Body(fn, args)
}

// Forget drops what the session keeps for the named units, so a long run
// does not grow without bound; a name may not be reused after it, and
// RunUnit on it is Skipped "no unit". What it drops: the body store's
// entries (MIR, onerr decisions) and the module nodes of the units' modules.
// What it cannot drop: the Items, Types and Consts tables and the source map
// are append-only, mono's instance cache and the evaluators' compiled code
// (keyed by *mir.Body) keep every body that ran. So the runner still starts
// a new session every MK_SOUND_RECYCLE units.
func (c *CorpusSession) Forget(units []string) {
	if len(units) == 0 {
		return
	}
	set := make(map[string]bool, len(units))
	c.sound.mu.Lock()
	for _, n := range units {
		set[n] = true
		if r := c.sound.units[n]; r != nil {
			r.forgotten = true
		}
	}
	c.sound.mu.Unlock()
	s := c.w.sem5
	if s == nil {
		return
	}
	items := c.w.items
	var mods []*types.Module
	for i := 1; i < items.NumModules(); i++ {
		m := items.Module(types.ModuleID(i))
		if !m.Std && ofUnits(m.Path, set) {
			mods = append(mods, m)
		}
	}
	s.mu.Lock()
	for _, m := range mods {
		for _, id := range m.Items {
			delete(s.bodies, id)
		}
		delete(s.mods, m.ID)
	}
	s.mu.Unlock()
}

// ofUnits reports whether module path belongs to a unit in set: the unit's
// root module (the name itself) or a subdirectory's (name/dir,
// unitModulePath). Unit names hold '/' themselves, so every '/' is tried.
func ofUnits(path string, set map[string]bool) bool {
	if set[path] {
		return true
	}
	for i := strings.IndexByte(path, '/'); i >= 0; {
		if set[path[:i]] {
			return true
		}
		j := strings.IndexByte(path[i+1:], '/')
		if j < 0 {
			break
		}
		i += 1 + j
	}
	return false
}
