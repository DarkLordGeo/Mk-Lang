// Why: stages 5 and 6 (signatures, then bodies) are where most of a check's
// time goes, so a module's bodies run in parallel, and same-module consts
// must be evaluated only once the bodies they read exist.
//
// What: build.checkModule (the stages of one module), bodies and bodyAt (the
// parallel unit checks), declEvals (const and global evaluation), lowerable,
// and accounting, which decides which of lower's E0000s an earlier
// diagnostic already explains.
//
// How: signatures first, holding one worker slot; then every unit checked
// and lowered on its own worker under source.Guard, results merged in Units
// order so output never depends on scheduling; then one EvalConst per const
// and global. The module's state is written last: importers start only
// after it.

package driver

import (
	"sync"
	"time"

	checker "mk/internal/check"
	"mk/internal/diag"
	"mk/internal/eval"
	"mk/internal/source"
	"mk/internal/types"
)

// acquire and release take and give back one worker slot of the shared
// bound (Config.Jobs).
func (w *world) acquire() { w.sem <- struct{}{} }
func (w *world) release() { <-w.sem }

// unitResult is one stage-6 slot: the unit's check diagnostics and the
// E0000 of a fault in checking or lowering it.
type unitResult struct {
	diags []*diag.Diagnostic
	ice   *diag.Diagnostic
	flow  []*diag.Diagnostic // stage 7 (borrow)
	// held is lower's E0000 on a body that reads consts with no type,
	// poisonReads those consts (checker.Body.PoisonReads; set whether or
	// not lowering faulted). checkModule reports held only when the consts
	// do not account for it (accounting).
	held        *diag.Diagnostic
	poisonReads []types.ItemID
}

// checkModule is stages 5 and 6 of one module, in this order:
//
//  1. Signatures (holding one worker slot). check hands each early const it
//     checked to Prepare, which lowers it at once, so a stage-5
//     evaluation finds every same-module initializer it may enter.
//  2. Bodies: every unit of Units, and every early const check deferred
//     (Deferred: its type needs a same-module layout), each checked and
//     lowered on its own worker under source.Guard; results in slots merged
//     in Units order. The module holds no slot
//     while they run, so Jobs 1 cannot deadlock.
//  3. Declaration evaluations: one EvalConst per const and global, Units
//     order then Early order, on this goroutine (holding one slot); then
//     the values are written into the Decls.
//
// The module's state is written last: importers start only after it.
func (b *build) checkModule(m *modNode) {
	w := b.w
	s := w.sem5
	if m.info == nil {
		// Stage 3 faulted (an E0000 is in m.diags): nothing to check.
		m.state.Store(modDoneFailed)
		return
	}
	b.onStage(m, StageSignatures, true)
	in := &checker.ModuleInput{Module: m.id, Info: m.info, BinRoot: m.binRoot, Library: m.libBoundary}
	for _, f := range m.files {
		in.Files = append(in.Files, f.ast)
	}
	w.acquire()
	cm := func() *checker.Module {
		defer w.release()
		return s.chk.Signatures(in)
	}()
	m.cmod = cm
	m.cdiags = append(m.cdiags, cm.Diags...)
	m.cdiags = append(m.cdiags, b.testDepUses(m)...) // E0217 (deps_testdeps.go)
	if k := w.cfg.TestHooks.UntypeConst; k != "" {
		ids := cm.Units()
		for _, eb := range cm.Early() {
			ids = append(ids, eb.Item)
		}
		for _, id := range ids {
			if it := w.items.Get(id); it.Key.String() == k {
				if d, ok := it.Decl.(*types.ConstDecl); ok {
					d.Type = types.NoType // a simulated check gap (TestHooks)
				}
			}
		}
	}
	b.onStage(m, StageSignatures, false)

	b.onStage(m, StageBodies, true)
	if w.front != nil {
		// The function prefixes' const and clause parts, built here on the
		// module's worker while the files' ASTs are held, so a unit's
		// lookup and its store (whose encoding runs on the store's goroutine
		// after release dropped the ASTs) read the same parts. Built first
		// at the store, contractNames took its no-AST fallback, and a verify
		// check found every stored entry differing.
		b.moduleConsts(m)
	}
	units := cm.Units()
	early := cm.Early()
	deferred := map[types.ItemID]bool{}
	for _, id := range cm.Deferred() {
		deferred[id] = true
	}
	var late []*checker.Body
	for _, eb := range early {
		if deferred[eb.Item] {
			late = append(late, eb)
		}
	}
	// One answer for the whole module: the deps' states are final here.
	lw := b.lowerable(m)
	res := b.bodies(cm, units, late, lw)
	var held []heldICE
	for _, r := range res {
		m.cdiags = append(m.cdiags, r.diags...)
		if r.ice != nil {
			m.cdiags = append(m.cdiags, r.ice)
		}
		if r.held != nil {
			held = append(held, heldICE{d: r.held, reads: r.poisonReads})
		}
		m.fdiags = append(m.fdiags, r.flow...)
	}
	m.prepMu.Lock()
	if lw {
		// A Prepare that faulted in a module lowerable rejects is a
		// consequence of the earlier diagnostic (lower's "check gap" ICEs
		// on a Poison type), so its E0000 is kept only when lowerable.
		m.cdiags = append(m.cdiags, m.prep...)
		held = append(held, m.prepHeld...)
		m.fdiags = append(m.fdiags, m.prepFlow...)
	}
	m.prepMu.Unlock()
	for _, id := range units {
		if u := m.unit(id); u != nil && u.reused {
			m.reused = append(m.reused, w.items.Get(id).Key)
			continue
		}
		m.checked = append(m.checked, w.items.Get(id).Key)
	}
	b.onStage(m, StageBodies, false)

	reported := b.declEvals(m, cm, units, early)
	m.prepMu.Lock()
	for _, id := range m.prepFailed {
		reported[id] = true
	}
	m.prepMu.Unlock()
	// Lower's E0000s on bodies that read consts with no value are judged
	// once the consts' own evaluations have reported (accounting).
	acct := newAccounting(units, late, early, res, reported)
	for _, h := range held {
		if !acct.covers(h.reads) {
			m.cdiags = append(m.cdiags, h.d)
		}
	}
	if d := w.writeTargetValue(m); d != nil {
		m.cdiags = append(m.cdiags, d)
	}
	switch {
	case m.failed || len(m.cdiags) > 0:
		m.state.Store(modDoneFailed)
	case !lw:
		// No diagnostic of its own, but its bodies were stored poisoned
		// because a dependency is not clean: tainted, never clean, so its
		// importers are not lowerable either.
		m.state.Store(modDoneTainted)
	default:
		m.state.Store(modDoneClean)
	}
}

// lowerable reports whether lower can trust the module's checked bodies (lower
// assumes that a body that reaches it was accepted by check): the module has no
// stage-3 or stage-5 diagnostic and every module it depends on is done clean (a
// failed or tainted dependency makes it tainted in turn, so the rule is
// transitive). An earlier diagnostic leaves Poison types that check does not
// report again, and lower raises an ICE on them ("check gap"). The build
// already holds that diagnostic, so a body not lowered is stored poisoned and
// nothing is lost. Inside a lowerable module, every body is lowered; lower's
// E0000 at a read of a const that has no value (checker.Body.PoisonAt) is held,
// and dropped only when that const's own failure is reported (accounting).
func (b *build) lowerable(m *modNode) bool {
	if len(m.diags) > 0 || m.cmod == nil || len(m.cmod.Diags) > 0 {
		return false
	}
	for _, d := range m.deps {
		if d.state.Load() != modDoneClean {
			return false
		}
	}
	return true
}

// accounting says which consts with no value account for lower's E0000
// at a read of them. A const accounts for it when:
//   - its own unit reported in check or lower (E0301);
//   - its Prepare reported in lower or stage 7 (E0402). A const whose value
//     a body asks for is early, so a stage-7 failure is a Prepare one;
//   - its declaration evaluation reported (E0901, E0902; E0909 for every
//     member of the cycle);
//   - or its own initializer reads only such consts (`const B = A`).
//
// Such an E0000 is a consequence of a diagnostic the build reports, so it
// is dropped and the body stays poisoned. Any other const with no
// value is a check gap, and its E0000 is reported: an unrelated diagnostic
// elsewhere in the module does not hide it.
type accounting struct {
	info map[types.ItemID]acctInfo
	memo map[types.ItemID]int8 // 1 accounted, -1 not, 2 being decided
}

type acctInfo struct {
	diags bool           // the const's own check, lower, stage 7 or evaluation reported
	reads []types.ItemID // the consts with no type its initializer reads
}

// newAccounting indexes the module's units (res, in bodies' order: units,
// then late), its early consts' bodies, and the consts and globals whose
// Prepare or declaration evaluation reported (reported).
func newAccounting(units []types.ItemID, late []*checker.Body, early []*checker.Body, res []unitResult, reported map[types.ItemID]bool) *accounting {
	a := &accounting{info: map[types.ItemID]acctInfo{}, memo: map[types.ItemID]int8{}}
	for _, eb := range early {
		a.info[eb.Item] = acctInfo{diags: len(eb.Diags) > 0 || reported[eb.Item], reads: eb.PoisonReads}
	}
	for i, r := range res {
		id := types.NoItem
		if i < len(units) {
			id = units[i]
		} else {
			id = late[i-len(units)].Item
		}
		a.info[id] = acctInfo{diags: len(r.diags) > 0 || r.ice != nil || reported[id], reads: r.poisonReads}
	}
	return a
}

// covers reports whether every const of reads accounts for an E0000.
func (a *accounting) covers(reads []types.ItemID) bool {
	for _, c := range reads {
		if !a.accounted(c) {
			return false
		}
	}
	return len(reads) > 0
}

func (a *accounting) accounted(c types.ItemID) bool {
	switch a.memo[c] {
	case 1:
		return true
	case -1, 2:
		return false
	}
	a.memo[c] = 2
	in, ok := a.info[c]
	got := ok && (in.diags || a.covers(in.reads))
	a.memo[c] = -1
	if got {
		a.memo[c] = 1
	}
	return got
}

// bodies is step 2 of checkModule: units and the deferred early consts,
// in parallel, one slot each. Every unit is checked, so every stage-6 error
// of the module is found; a body is lowered only when lowerable
// said so (sigClean), and its own check is clean.
//
// fn literals run after every other unit: a literal written where a fn
// type is expected takes that type's flows, which its enclosing
// body's check gives its signature, so the literal's own check, lowering
// and borrow pass must not start before that check ends. So units run in
// phases by literal depth: other units first, then the literals they hold,
// then the literals inside those, and so on.
//
// Within a depth, with a front cache, the units that are always checked
// (contract predicates, literals, deferred consts: not reusable) run before
// the units that may be looked up. A lookup validates reads such as a
// callee's signature hash, which names the callee's contract bodies; run
// side by side, a lookup could find such a body not lowered yet, take the
// hash's ICE for a changed read and recheck a function that was unchanged,
// depending on the schedule (TestInvalidation_ClauseTextReaderReused).
func (b *build) bodies(cm *checker.Module, units []types.ItemID, late []*checker.Body, sigClean bool) []unitResult {
	w := b.w
	n := len(units) + len(late)
	res := make([]unitResult, n)
	phase := make([]int, n)
	most := 0
	var m *modNode
	if w.front != nil && sigClean && len(units) > 0 {
		m = w.sem5.module(w.items.Get(units[0]).Module)
	}
	for i := range units {
		depth := 0
		for it := w.items.Get(units[i]); it.Kind == types.KindLiteral && it.Owner != types.NoItem; it = w.items.Get(it.Owner) {
			depth++
		}
		phase[i] = 2 * depth
		if m != nil && m.reusable(w.items.Get(units[i]), w.items) {
			phase[i]++
		}
		most = max(most, phase[i])
	}
	for p := 0; p <= most; p++ {
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			if phase[i] == p {
				b.bodyAt(cm, units, late, sigClean, res, i, &wg)
			}
		}
		wg.Wait()
	}
	return res
}

// bodyAt starts unit i of bodies on its own goroutine under a worker slot.
func (b *build) bodyAt(cm *checker.Module, units []types.ItemID, late []*checker.Body, sigClean bool, res []unitResult, i int, wg *sync.WaitGroup) {
	w := b.w
	s := w.sem5
	var id types.ItemID
	var pre *checker.Body
	if i < len(units) {
		id = units[i]
	} else {
		pre = late[i-len(units)]
		id = pre.Item
	}
	it := w.items.Get(id)
	// The slot is taken before the goroutine starts, as world.parallel does,
	// so at most Jobs unit goroutines live at once, not one per unit of the
	// module. This goroutine holds no slot, so Jobs 1 cannot deadlock.
	w.acquire()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer w.release()
		if ie := source.Guard("check "+it.Key.String(), it.Span, func() { b.body(cm, it, pre, sigClean, &res[i]) }); ie != nil {
			res[i].ice = diag.FromInternal(ie)
			s.store(id, &bodyEntry{state: eval.BodyPoisoned})
		}
	}()
}

// body checks one unit (pre nil) or takes a deferred early const's checked
// body (pre), lowers it and stores its MIR. Its caller, bodies, holds one
// worker slot for it.
func (b *build) body(cm *checker.Module, it *types.Item, pre *checker.Body, sigClean bool, r *unitResult) {
	w := b.w
	s := w.sem5
	m := s.module(it.Module)
	body := pre
	var took time.Duration
	if body == nil {
		if hook := w.cfg.TestHooks.PanicIn; hook != "" && hook == it.Key.String() {
			source.ICEf(it.Span, "test hook: panic in %s", hook)
		}
		// Per-function reuse (funccache.go): only over a lowerable
		// module, as every stored result was computed over one.
		if sigClean && m != nil && b.funcLookup(m, it, r) {
			return
		}
		start := w.clock()
		body = s.chk.Check(cm, it.ID)
		took = w.since(start)
		r.diags = body.Diags
	}
	r.poisonReads = body.PoisonReads
	if !sigClean {
		s.store(it.ID, &bodyEntry{state: eval.BodyPoisoned})
		w.noteSlow(it, took)
		return
	}
	l := w.lowerBody(body)
	r.ice = l.ice
	r.flow = l.flow
	r.held = l.held
	s.store(it.ID, &l.entry)
	if m != nil {
		m.noteUnit(it.ID, &unitRec{reads: m.unitReads(it, &body.Reads, &l.reads), diags: body.Diags, flow: l.flow})
	}
	w.noteSlow(it, took+l.flowTook)
}

// declEvals is step 3 of checkModule: the declaration evaluation of every
// const and global of the module (EvalConst, at StageBodies, its failure
// rendered at the declaration: E0901, E0902, E0909), then the values written
// into ConstDecl and GlobalDecl on this goroutine, after every evaluation of
// the module. It returns the items whose evaluation reported a diagnostic
// (accounting).
func (b *build) declEvals(m *modNode, cm *checker.Module, units []types.ItemID, early []*checker.Body) map[types.ItemID]bool {
	w := b.w
	s := w.sem5
	var items []types.ItemID
	for _, id := range units {
		if k := w.items.Get(id).Kind; k == types.KindConst || k == types.KindGlobal {
			items = append(items, id)
		}
	}
	for _, eb := range early {
		items = append(items, eb.Item)
	}
	vals := make([]types.ConstID, len(items))
	failed := map[types.ItemID]bool{}
	w.acquire()
	for i, id := range items {
		it := w.items.Get(id)
		if ie := source.Guard("evaluate "+it.Key.String(), it.Span, func() {
			v, ds, rs := s.chk.EvalConstReads(cm, id)
			if rs != nil && rs.Len() > 0 {
				m.readsMu.Lock()
				m.reads.Merge(rs)
				m.readsMu.Unlock()
			}
			vals[i] = v
			m.cdiags = append(m.cdiags, ds...)
			failed[id] = failed[id] || len(ds) > 0
		}); ie != nil {
			m.cdiags = append(m.cdiags, diag.FromInternal(ie))
			failed[id] = true
		}
	}
	w.release()
	// E0909 is reported once per cycle, at its least-key const; every
	// member failed.
	for _, d := range m.cdiags {
		if d.Code != diag.E0909 {
			continue
		}
		if f, ok := d.FactValue("cycle"); ok {
			for _, n := range f.List {
				for _, id := range items {
					if w.in.Str(w.items.Get(id).Name) == n.Str {
						failed[id] = true
					}
				}
			}
		}
	}
	for i, id := range items {
		if vals[i] == types.NoConst {
			continue
		}
		switch d := w.items.Get(id).Decl.(type) {
		case *types.ConstDecl:
			d.Value, d.HasValue = vals[i], true
		case *types.GlobalDecl:
			d.Value, d.HasValue = vals[i], true
		}
	}
	return failed
}

// onStage calls TestHooks.OnStage, when set, at a stage's start and end in
// a module (TestScheduleByDependencies). It is called with no worker slot
// held.
func (b *build) onStage(m *modNode, st Stage, begin bool) {
	if h := b.w.cfg.TestHooks.OnStage; h != nil {
		h(m.path, st, begin)
	}
}
