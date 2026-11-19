// Why: release --pgo= (or a default.mkprof) feeds a recorded profile back
// to LLVM, so hot code is laid out, inlined and optimized for how the
// program really runs.
//
// What: lookupProfile, summary, pgoAttrs, entryCount, edgeCounts, weights,
// callAttrs and profileSummary.
//
// How: each member is matched by symbol, CFG checksum and counter count;
// with data it gets its function_entry_count, branch weights on every
// counted terminator, and hot (plus inlinehint when small) or cold from the
// whole profile's summary. A stale record gives no data and lists the
// function in Report.Stale.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/pgo"
)

// inlineOps is the size bound of a hot inline candidate: statements plus
// terminators.
const inlineOps = 64

// lookupProfile fills p.counts from the configured profile, or records the
// function as stale when the profile knows its key under another checksum
// or counter count (Lookup's stale cases). A key the profile never saw is
// not stale: the function is new, or was never run.
func (g *packGen) lookupProfile(f *Func, p *fnPGO) {
	counts, ok := g.cfg.PGO.Lookup(f.Symbol, pgoChecksum(f.Body), p.plan.n)
	if ok {
		p.counts = counts
		return
	}
	if g.cfg.PGO.HasKey(f.Symbol) {
		g.stale = append(g.stale, f.Symbol)
	}
}

// summary is the profile's summary, computed once per pack over the whole
// profile (never the matched subset).
func (g *packGen) summary() *pgo.Summary {
	if g.pgoSum == nil {
		g.pgoSum = g.cfg.PGO.Summarize()
	}
	return g.pgoSum
}

// pgoAttrs adds hot (and inlinehint for a body of at most inlineOps
// operations) or cold to a member with profile data.
func (g *packGen) pgoAttrs(fi int, a llvm.Attrs) llvm.Attrs {
	p := g.pgoOf(fi)
	if p == nil || p.counts == nil {
		return a
	}
	s, c0 := g.summary(), p.counts[0]
	switch {
	case s.Hot(c0):
		a = a.Flag("hot")
		if bodyOps(g.u.Funcs[fi].Body) <= inlineOps {
			a = a.Flag("inlinehint")
		}
	case s.Cold(c0):
		a = a.Flag("cold")
	}
	return a
}

// entryCount attaches function_entry_count to a member with profile data.
func (g *packGen) entryCount(fi int, fn *llvm.Function) {
	if p := g.pgoOf(fi); p != nil && p.counts != nil {
		fn.SetProf(g.m.Metadata().EntryCount(p.counts[0]))
	}
}

// edgeCounts are the recorded counts of the current block's k counted
// edges, nil without profile data.
func (fg *fnGen) edgeCounts(k int) []uint64 {
	p := fg.pgo
	if p == nil || p.counts == nil || p.plan.base[fg.bi] < 0 {
		return nil
	}
	b := int(p.plan.base[fg.bi])
	if b+k > len(p.counts) {
		return nil // never: the plan sized counts; kept so no index passes n
	}
	return p.counts[b : b+k]
}

// weights is the !prof attachment of counts in LLVM's successor order, none
// when there is no data or every count is 0.
func (fg *fnGen) weights(counts []uint64) []llvm.Attach {
	if counts == nil {
		return nil
	}
	ws := pgo.Weights(counts)
	if ws == nil {
		return nil
	}
	return []llvm.Attach{llvm.Prof(fg.g.m.Metadata().BranchWeights(ws...))}
}

// bodyOps counts statements plus terminators.
func bodyOps(b *mir.Body) int {
	n := 0
	for i := range b.Blocks {
		n += len(b.Blocks[i].Stmts) + 1
	}
	return n
}

// callAttrs are the function attributes a call of fn repeats: the
// definition's, without its profile attributes, which describe the callee's
// body, not the call site.
func callAttrs(fn *llvm.Function) llvm.Attrs {
	return fn.Sig().FnAttrs.Without("hot", "cold", "inlinehint")
}

// profileSummary adds LLVM's ProfileSummary module flag (format InstrProf)
// of the whole profile, so LLVM's profile-summary analysis reads the entry
// counts and weights (hot call sites get the inliner's hot threshold, cold
// code is optimized for size). Every pack of the build gets the same flag,
// as ThinLTO's module-flag merge (behaviour Error) requires; a profile with
// no counts adds none.
func (g *packGen) profileSummary() {
	if g.cfg.PGO == nil {
		return
	}
	s := g.cfg.PGO.LLVM()
	if s.TotalCount == 0 {
		return
	}
	m := g.m
	mt := m.Metadata()
	i32, i64 := m.I(32), m.I(64)
	kv := func(k string, v uint64) llvm.MDOp {
		return llvm.MDRef(mt.Tuple(llvm.MDStr(k), llvm.MDVal(m.ConstInt(i64, v))))
	}
	var rows []llvm.MDOp
	for _, e := range s.Detailed {
		rows = append(rows, llvm.MDRef(mt.Tuple(llvm.MDVal(m.ConstInt(i32, uint64(e.Cutoff))),
			llvm.MDVal(m.ConstInt(i64, e.MinCount)), llvm.MDVal(m.ConstInt(i32, min(e.NumCount, 1<<31-1))))))
	}
	top := mt.Tuple(llvm.MDRef(mt.Tuple(llvm.MDStr("ProfileFormat"), llvm.MDStr("InstrProf"))),
		kv("TotalCount", s.TotalCount), kv("MaxCount", s.MaxCount), kv("MaxInternalCount", s.MaxInternalCount),
		kv("MaxFunctionCount", s.MaxFunctionCount), kv("NumCounts", s.NumCounts), kv("NumFunctions", s.NumFunctions),
		llvm.MDRef(mt.Tuple(llvm.MDStr("DetailedSummary"), llvm.MDRef(mt.Tuple(rows...)))))
	mt.ModuleFlagMD(llvm.FlagError, "ProfileSummary", top)
}
