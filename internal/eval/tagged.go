// Why: the soundness tests run programs under a Stacked Borrows model and
// need a public entry for it that leaves plain evaluation, its flat fast
// path and its speed untouched.
//
// What: the tagged mode's API: TagOptions, ViolationKind, Violation,
// TaggedResult and RunTagged, with taggedRequest, startTagged, violation and
// skip.
//
// How: a tagged run uses a compiled form of its own, every body on the
// general path, and charges exactly the plain mode's fuel and steps. It
// stops after Max violations, or as Skipped on an operation it cannot judge
// (a TermOnerrGuard with no decision under Rejected). Conflicts are found
// lazily, except a call's own loans, which are checked eagerly. These are
// eval's own types, not contract types.

package eval

import (
	"fmt"
	"strings"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// TagOptions turns on the tagged mode for one request. The zero value is the
// runner's default except where a field says otherwise.
type TagOptions struct {
	// Seed chooses the run's schedule: where each spawned task starts
	// and, for the test prelude's Cond() when Cond is 0, each
	// result. The same request and the same Seed give the same run.
	Seed uint64
	// Cond forces the test prelude's Cond(): 1 always true, -1 always
	// false, 0 drawn from Seed (the calibration programs force true).
	Cond int8
	// Rejected runs bodies that stage 7 rejected (the calibration
	// programs). A TermOnerrGuard reached with no borrow
	// decision then ends the run as Skipped, never an ICE.
	Rejected bool
	// NoRaces turns off the interior race check (on by default).
	NoRaces bool
	// Max is the number of violations to collect before the run stops; 0
	// means 1 (the runner stops at the first).
	Max int
	// FailAlloc, when n > 0, makes each page allocation of the run after the
	// entry started fail (the page source returns null, so the arena or heap
	// reports OutOfMemory) with probability 1/n, drawn from a stream of its
	// own derived from Seed: the error edges of try, else and onerr run. The
	// host's own setup (the &Arena parameter) never fails.
	FailAlloc uint32
}

// ViolationKind is what a tagged run found.
type ViolationKind uint8

const (
	// VDead: a read or write of a location whose allocation is dead (a
	// returned frame, a freed page, an ended arena).
	VDead ViolationKind = iota + 1
	// VDeadScope: the location is a local whose scope ended (StScopeEnd),
	// or a later activation of it, so the tag was made in an earlier one.
	VDeadScope
	// VRemovedTag: a use of a tag that a conflicting access removed from
	// the location's stack: the loan had ended.
	VRemovedTag
	// VSharedWrite: a write through a shared tag on frozen bytes.
	VSharedWrite
	// VProtected: an access that would remove a tag a live call holds (a
	// reference parameter's loan): a conflict with a live loan.
	VProtected
	// VUninit: a read of a local's bytes that were never written, or were
	// moved out.
	VUninit
	// VLinear: a linear value still owned when its local's scope or its
	// frame ends.
	VLinear
	// VRace: two accesses of interior memory from tasks that no
	// happens-before edge orders, at least one a write.
	VRace
	// VFacts: the tagged mode's own type walk finds interior bytes in a type
	// whose shape says Freeze, the fact codegen's `noalias readonly` rests
	// on.
	VFacts
)

var violationNames = [...]string{"", "VDead", "VDeadScope", "VRemovedTag", "VSharedWrite", "VProtected", "VUninit", "VLinear", "VRace", "VFacts"}

// String is the kind's name as records spell it ("VRemovedTag").
func (k ViolationKind) String() string {
	if int(k) < len(violationNames) && k != 0 {
		return violationNames[k]
	}
	return fmt.Sprintf("V%d", uint8(k))
}

// ParseViolationKind is String's inverse; ok is false for an unknown name.
func ParseViolationKind(s string) (ViolationKind, bool) {
	for i, n := range violationNames {
		if i > 0 && n == s {
			return ViolationKind(i), true
		}
	}
	return 0, false
}

// Violation is one finding. Spans are source spans of the session's map.
type Violation struct {
	Kind    ViolationKind
	At      source.Span  // the access
	In      types.ItemID // the body that made it
	Std     bool         // In is a std item: std's own unsafe code, or an access std makes for the program
	Tag     uint32       // the tag used (0 for an owner access)
	Made    source.Span  // where Tag was created (the borrow, slice, reborrow or entry retag)
	Removed source.Span  // the access that removed Tag (VRemovedTag), or that ended the location
	Holder  types.ItemID // VProtected: the call whose parameter holds the protected tag
	Where   string       // the location in Mk words: "local x of Run", "element 2 of xs", "heap page"
	Off     uint32       // byte offset in the allocation, and length of the access
	Len     uint32
	Task    uint32 // the task that accessed (0 the request's own), and for VRace the other
	Other   uint32
	Trace   []types.ItemID // the call chain, outermost first, as EvalError.Trace
	Spans   []source.Span
}

// TaggedResult is a tagged run's outcome. EvalResult is as Eval's: Value
// when the run completed, Err for a panic, refusal, fuel or stage stop.
type TaggedResult struct {
	types.EvalResult
	Violations []Violation // in the order found; at most Max
	// Skipped says why the run could not be judged (a refused operation
	// outside the host's list, a blocked lock, an onerr guard with no
	// decision under Rejected); "" when it was.
	Skipped string
	// Starts is the schedule taken: for each spawned task in spawn order,
	// the yield point it started at, for the record.
	Starts []uint32
}

// The kinds of tagged stop (stop.tagged).
const (
	tagNone uint8 = iota
	tagViolation
	tagSkipped
)

// RunTagged runs req in the tagged mode. req.What is EvalCall. A parameter
// of type Io receives the soundness host's Io, a parameter of type
// &Arena the arena Config.EvalArena makes; req.Args gives every other
// parameter, in order. Safe for concurrent use, as Eval is.
//
// The run charges exactly the plain mode's steps and gives the same
// Value whenever it completes with no violation; it runs every body through
// the general path (a compiled form of its own), never the flat one.
// A violation stops the run without storing any const slot outcome, so the
// session's slots are what plain evaluations left (the Evaluator is shared
// with check).
func (e *Evaluator) RunTagged(req *types.EvalRequest, opt *TagOptions) TaggedResult {
	m := e.newMachine(req, nil)
	m.tg = newTagState(opt)
	if it, ok := m.items().ByKey(arenaResetKey); ok {
		m.tg.resetItem = it
	}
	m.mem.tagged = true
	defer m.finish()
	return m.taggedRequest()
}

// taggedRequest is callRequest in the tagged mode.
func (m *Machine) taggedRequest() TaggedResult {
	req := m.req
	it := m.items().Get(req.Item)
	if req.What != types.EvalCall || (it.Kind != types.KindFunc && it.Kind != types.KindLiteral) {
		source.ICEf(req.Site, "eval: RunTagged of %s %s (want an EvalCall of a function)", it.Kind, it.Key)
	}
	fd, _ := it.Decl.(*types.FuncDecl)
	if fd == nil || fd.Bodiless {
		source.ICEf(req.Site, "eval: RunTagged of %s, which has no body", it.Key)
	}
	var out TaggedResult
	one := func(e *types.EvalError) TaggedResult {
		e.Items, e.Trace, e.At = []types.ItemID{it.ID}, []source.Span{req.Site}, req.Site
		out.Err = e
		return out
	}
	if req.Stage == types.StageSignatures && it.Module == req.Module {
		return one(&types.EvalError{Kind: types.EvalStage})
	}
	fuel := req.Fuel
	if fuel == 0 {
		fuel = types.DefaultFuel
	}
	m.segs = append(m.segs, segment{fuel: fuel})
	st := m.guard(func() { m.startTagged(it, fd) })
	if st == nil {
		st = m.runLoop(0)
	}
	if st == nil && len(m.globals) > 0 {
		// The run completed and its segment is gone: a stop in the
		// globals' ending (taghost.go) reports the completed run's steps; a
		// stop other than a violation makes the run Skipped, never an ICE.
		if st := m.guard(m.tgGlobalsEnd); st != nil {
			out.Violations, out.Starts, out.Steps = m.tg.viol, m.tg.starts(), m.result.steps
			switch {
			case st.tagged == tagViolation:
			case st.tagged == tagSkipped:
				out.Skipped = st.skip
			default:
				out.Skipped = "globals ending"
			}
			return out
		}
	}
	out.Violations = m.tg.viol
	out.Starts = m.tg.starts()
	if st != nil {
		switch st.tagged {
		case tagViolation:
			out.Steps = m.segs[0].steps
			return out
		case tagSkipped:
			out.Skipped = st.skip
			out.Steps = m.segs[0].steps
			return out
		}
		out.Err = m.abort(st)
		out.Reads.Merge(&m.segs[0].reads)
		out.Steps = m.segs[0].steps
		return out
	}
	out.Value, out.Steps = m.result.value, m.result.steps
	out.Reads.Merge(&m.result.reads)
	return out
}

// startTagged builds the request's outermost frame as startCall does, then
// the host's parameters (Io, &Arena) and the entry retag of every reference
// parameter.
func (m *Machine) startTagged(it *types.Item, fd *types.FuncDecl) {
	req := m.req
	m.entering, m.enteringAt = it.ID, req.Site
	body := m.cfg.Program.Body(it.ID, req.GenericArgs)
	m.bodyState(it.ID, &body)
	m.bodyReads(it.Key, &body)
	cd, ce := m.enter(body.MIR, req.Site)
	f := m.pushFrame(cd, ce, it.ID, body.OnerrRuns, req.Site, 0)
	m.entering = types.NoItem
	k := 0
	for i := 0; i < int(cd.body.ParamCount); i++ {
		id := mir.LocalID(i + 1)
		if m.hostParam(f, id) {
			f = &m.frames[0] // the arena's frame may have moved the slice
			continue
		}
		dst := f.local(id)
		if k >= len(req.Args) {
			source.ICEf(req.Site, "eval: RunTagged of %s: %d arguments for its non-host parameters", it.Key, len(req.Args))
		}
		a := req.Args[k]
		k++
		if a == types.NoConst {
			source.ICEf(req.Site, "eval: argument %d of %s is NoConst", i, it.Key)
		}
		m.copyValue(dst, loc{a: m.constAlloc(a), ti: m.ti(m.cfg.Consts.Get(a).Type)})
	}
	if k != len(req.Args) {
		source.ICEf(req.Site, "eval: RunTagged of %s: %d arguments for %d non-host parameters", it.Key, len(req.Args), k)
	}
	m.tgEnter(f)
	m.tg.armed = true
}

// violation records one finding at the running statement and stops the run
// once Max are recorded.
func (m *Machine) violation(v Violation) {
	tg := m.tg
	if n := len(m.frames); n > 0 {
		f := &m.frames[n-1]
		if !v.At.IsValid() {
			v.At = f.curSpan()
		}
		if v.In == types.NoItem {
			v.In = f.item
		}
	} else if !v.At.IsValid() {
		v.At = m.req.Site
	}
	if v.In != types.NoItem {
		if it := m.items().Get(v.In); it != nil {
			if md := m.items().Module(it.Module); md != nil {
				v.Std = md.Std
			}
		}
	}
	v.Trace, v.Spans = m.chain(0)
	tg.viol = append(tg.viol, v)
	if len(tg.viol) >= tg.opt.Max {
		panic(&stop{err: &types.EvalError{Kind: types.EvalPanic, Message: "tagged: " + v.Kind.String()}, tagged: tagViolation})
	}
}

// skip ends the run as Skipped (a refused operation, or an onerr guard with
// no decision).
func (m *Machine) skip(why string) {
	panic(&stop{err: &types.EvalError{Kind: types.EvalRefused, Message: why}, tagged: tagSkipped, skip: why})
}

// String spells a violation for a record: kind, where, the three spans.
func (v *Violation) String(sm *source.SourceMap) string {
	var b strings.Builder
	pos := func(s source.Span) string {
		if sm == nil || !s.IsValid() {
			return "-"
		}
		p := sm.Position(s.StartPos())
		return p.LineCol.String()
	}
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
	return b.String()
}
