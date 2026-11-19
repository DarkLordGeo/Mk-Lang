// Why: the range analysis is worth something only when its facts change the
// code: a proved check must become an operator or cast that cannot trap, and
// an accumulator in a loop needs a bound the plain intervals cannot give.
//
// What: rewrite, which turns every proved checked Add, Sub, Mul and cast into
// its no-wrap or truncating form; rule 8 (accumulators) in accumulate,
// accLoop, counterStep, stepsCut and accBlock; loops, the natural loops.
//
// How: each rewrite site states its proof obligation. Rule 8 bounds s in
// s = s + e by the chains-of-recurrences form {s0,+,e} (Bachmann, Wang and
// Zima, ISSAC 1994) over a loop's counter, on natural loops found from
// dominators (Cooper, Harvey and Kennedy, "A simple, fast dominance
// algorithm", 2001).

package lower

import (
	"cmp"
	"slices"

	"mk/internal/mir"
)

// accSite is a checked add the intervals left unproved, for rule 8.
type accSite struct {
	blk mir.BlockID
	j   int
}

// rewrite replays every reached block under its solved in-state and applies
// what the states prove; it returns the proved bounds checks, (block, ok
// target) each, which it does not fold (rangeCheck and ProvedBounds.Fold
// do). The states are a post-fixpoint, so every interval read here holds on
// every execution that reaches the statement.
func (p *rangePass) rewrite() [][2]mir.BlockID {
	cur := p.cur
	var acc []accSite
	var folds [][2]mir.BlockID // (block, ok target) of each proved bounds check
	for _, blk := range p.rpo {
		if !p.reached[blk] {
			continue
		}
		copy(cur, p.state(blk))
		block := &p.b.Blocks[blk]
		fold := mir.NoBlock
		p.run(blk, cur, func(j int, st []ival) {
			if j == len(block.Stmts) {
				// At the terminator: a bounds check whose condition is [1, 1]
				// can never fail. Obligation: k's interval is exactly {1}.
				if ok := boundsTarget(p.b, block); ok != mir.NoBlock {
					if k := p.idx[block.Term.On.Place.Local]; k >= 0 && st[k] == (ival{1, 1}) {
						fold = ok
					}
				}
				return
			}
			s := &block.Stmts[j]
			if s.Kind != mir.StAssign {
				return
			}
			switch r := &s.Rvalue; r.Kind {
			case mir.RvBinary:
				if r.Bin != mir.Add && r.Bin != mir.Sub && r.Bin != mir.Mul {
					return
				}
				in := intOf(p.tab, r.Type)
				a, aok := p.operand(st, r.A)
				b, bok := p.operand(st, r.B)
				if !in.ok || !aok || !bok {
					return
				}
				// Obligation: the exact result of every pair of operand values
				// lies in the type, so the operation cannot overflow (Add,
				// Sub and Mul alike).
				x, ok := binRange(r.Bin, a.r, b.r, a.in)
				switch {
				case !ok:
				case p.mut == mutSubLow && r.Bin == mir.Sub:
					x.lo = x.lo.add(wInt(1))
				case p.mut == mutMulHigh && r.Bin == mir.Mul:
					x.hi = x.hi.sub(wInt(1))
				}
				if ok && in.fits(x) {
					switch r.Bin {
					case mir.Add:
						r.Bin = mir.AddNoWrap // nsw or nuw by signedness
					case mir.Sub:
						r.Bin = mir.SubNoWrap // nsw or nuw by signedness
					case mir.Mul:
						r.Bin = mir.MulNoWrap
					}
				} else if r.Bin == mir.Add {
					acc = append(acc, accSite{blk, j})
				}
			case mir.RvCast:
				if r.Cast != mir.CastChecked {
					return
				}
				in := intOf(p.tab, r.Type)
				a, ok := p.operand(st, r.A)
				// Obligation: the source interval lies in the target, so the
				// cast cannot trap, and Trunc to an equal or narrower width
				// keeps every such value.
				if in.ok && ok && in.bits <= a.in.bits && in.fits(a.r) {
					r.Cast = mir.CastTrunc
				}
			}
		}, func(mir.BlockID, []ival, []int64) {})
		if fold != mir.NoBlock {
			folds = append(folds, [2]mir.BlockID{blk, fold})
		}
	}
	if len(acc) > 0 {
		p.accumulate(acc)
	}
	// Folded last, by the caller: rule 8 replays blocks and wants their
	// refining switches.
	return folds
}

// loopSet is the natural loops of the reached CFG.
type loopSet struct {
	header  []mir.BlockID
	member  [][]bool
	size    []int
	latches [][]mir.BlockID
	inner   []int32 // block -> innermost loop, or -1
}

// loops finds the natural loops: a back edge u -> h has h dominating u; its
// loop is h and every block that reaches u without passing h. Back edges
// to one header form one loop.
func (p *rangePass) loops() *loopSet {
	b := p.b
	nb := len(b.Blocks)
	idom := make([]int32, nb)
	for i := range idom {
		idom[i] = -1
	}
	idom[mir.EntryBlock] = int32(mir.EntryBlock)
	inter := func(x, y int32) int32 {
		for x != y {
			for p.pos[x] > p.pos[y] {
				x = idom[x]
			}
			for p.pos[y] > p.pos[x] {
				y = idom[y]
			}
		}
		return x
	}
	for changed := true; changed; {
		changed = false
		for _, blk := range p.rpo[1:] {
			nd := int32(-1)
			for _, q := range p.predsOf(blk) {
				if idom[q] < 0 {
					continue
				}
				if nd < 0 {
					nd = int32(q)
				} else {
					nd = inter(int32(q), nd)
				}
			}
			if nd >= 0 && idom[blk] != nd {
				idom[blk] = nd
				changed = true
			}
		}
	}
	dom := func(h, u mir.BlockID) bool {
		x := int32(u)
		for {
			if x == int32(h) {
				return true
			}
			if x == int32(mir.EntryBlock) || idom[x] < 0 {
				return false
			}
			x = idom[x]
		}
	}
	ls := &loopSet{inner: make([]int32, nb)}
	at := make([]int32, nb)
	for i := range at {
		at[i] = -1
		ls.inner[i] = -1
	}
	for _, u := range p.rpo {
		for _, h := range p.succsOf(u) {
			if !dom(h, u) {
				continue
			}
			l := at[h]
			if l < 0 {
				l = int32(len(ls.header))
				at[h] = l
				ls.header = append(ls.header, h)
				m := make([]bool, nb)
				m[h] = true
				ls.member = append(ls.member, m)
				ls.size = append(ls.size, 1)
				ls.latches = append(ls.latches, nil)
			}
			ls.latches[l] = append(ls.latches[l], u)
			m := ls.member[l]
			work := []mir.BlockID{u}
			for len(work) > 0 {
				x := work[len(work)-1]
				work = work[:len(work)-1]
				if m[x] {
					continue
				}
				m[x] = true
				ls.size[l]++
				work = append(work, p.predsOf(x)...)
			}
		}
	}
	for blk := range ls.inner {
		for l := range ls.header {
			if ls.member[l][blk] && (ls.inner[blk] < 0 || ls.size[l] < ls.size[ls.inner[blk]]) {
				ls.inner[blk] = int32(l)
			}
		}
	}
	return ls
}

// wholeOf reports whether op reads tracked local l whole.
func wholeOf(op mir.Operand, l mir.LocalID) bool {
	return op.Kind <= mir.OpMove && op.Place.Proj.Len == 0 && op.Place.Local == l
}

// accumulate is rule 8 for the adds of acc, each s = s + e (either order)
// in loop L with counter c. When s is assigned in L only there, from a
// block in no loop nested in L, and c is a counter of L as accLoop states
// it (every assignment a step c = c + k, k >= 0, and a unit step on every
// cycle through the header), then at the add s lies in s_entry
// + (c - c_entry)·[min(a,0), max(b,0)], [a, b] being e's interval there:
// each completed iteration added one value of e or nothing, and c - c_entry
// is at least their number.
//
// Its work is per loop and per block, never per site (per site it would
// rescan L and copy two whole states, which grows with the square of a body
// with many accumulators while the cost cap holds).
// Per loop with a site: one count of assignments over L's blocks, the latch
// scan for c, and the entry state, O(blocks + tracked + statements of L).
// Per block with a site: one replay, O(tracked + statements), whose hook
// keeps only what each site's proof reads, e's interval and c's. Per site:
// O(1). Add and AddNoWrap have one transfer (rvalueOK, binRange), so a
// rewrite here changes no state a later replay computes.
func (p *rangePass) accumulate(acc []accSite) {
	ls := p.loops()
	// acc is in RPO, each block's sites in statement order; the stable sort
	// by innermost loop keeps each block's sites together and in order.
	slices.SortStableFunc(acc, func(x, y accSite) int {
		return cmp.Compare(ls.inner[x.blk], ls.inner[y.blk])
	})
	var r accRun
	for i := 0; i < len(acc); {
		l := ls.inner[acc[i].blk]
		e := i + 1
		for e < len(acc) && ls.inner[acc[e].blk] == l {
			e++
		}
		if l >= 0 {
			p.accLoop(ls, int(l), acc[i:e], &r)
		}
		i = e
	}
}

// accRun is rule 8's scratch, allocated once per body: per tracked local,
// its assignments in the current loop and its state on entry to that loop;
// and one block's candidate sites.
type accRun struct {
	cnt   []int32
	steps []int32 // per tracked local: its counter steps in the current loop
	entry []ival
	seen  []bool // stepsCut's search
	cands []accCand
}

// accCand is a site whose static conditions hold, with what the replay
// read at it: e's interval (ok false when e is no integer) and c's.
type accCand struct {
	j  int
	ks int32
	e  mir.Operand
	er opRange
	ok bool
	cS [accMaxCounters]ival // each counter's interval at the site
}

// accMaxCounters is how many counters rule 8 tries per loop.
const accMaxCounters = 4

// accLoop is rule 8 for loop l's sites, sorted by block. A counter is a
// tracked local c whose every assignment in L is a step c = c + k, with k
// an immediate >= 0 and the + checked (Add, so a step that did not trap is
// exact) or proved (AddNoWrap), and such that every path from L's header
// back to it passes a block with a step of k >= 1.
// Then each completed iteration raised c by at least 1 and no statement
// lowered it, so at any point of L, c - c_entry is at least the number of
// completed iterations.
func (p *rangePass) accLoop(ls *loopSet, l int, sites []accSite, r *accRun) {
	b := p.b
	h := ls.header[l]
	member := ls.member[l]
	if r.cnt == nil {
		r.cnt = make([]int32, p.n)
		r.steps = make([]int32, p.n)
		r.entry = make([]ival, p.n)
		r.seen = make([]bool, len(b.Blocks))
	}
	p.countAssigns(member, r.cnt)
	// The steps of each tracked local in L; the candidates in the order
	// their first unit step is found.
	clear(r.steps)
	var cands [accMaxCounters]int32
	nc := 0
	for x := range b.Blocks {
		if !member[x] {
			continue
		}
		for j := range b.Blocks[x].Stmts {
			s := &b.Blocks[x].Stmts[j]
			if s.Kind != mir.StAssign || s.Place.Proj.Len != 0 {
				continue
			}
			k, step := p.counterStep(s)
			if k < 0 {
				continue
			}
			r.steps[k]++
			if step >= 1 && nc < accMaxCounters && !slices.Contains(cands[:nc], k) {
				cands[nc] = k
				nc++
			}
		}
	}
	var kcs [accMaxCounters]int32
	nk := 0
	for _, k := range cands[:nc] {
		// Obligation: every assignment of c in L is a step, and every
		// cycle through the header passes a unit step.
		if (r.steps[k] == r.cnt[k] || p.mut == mutCounterAnyDef) && p.stepsCut(ls, l, k, r.seen) {
			kcs[nk] = k
			nk++
		}
	}
	if nk == 0 || !p.loopEntry(h, member, r.entry) {
		return
	}
	for i := 0; i < len(sites); {
		blk := sites[i].blk
		e := i + 1
		for e < len(sites) && sites[e].blk == blk {
			e++
		}
		p.accBlock(blk, sites[i:e], kcs[:nk], r)
		i = e
	}
}

// counterStep is s as a counter step c = c + k: c's tracked index and k,
// or -1. k is the step's least value, -1 when it may be negative.
func (p *rangePass) counterStep(s *mir.Statement) (int32, int64) {
	c := s.Place.Local
	cr := &s.Rvalue
	k := p.idx[c]
	if k < 0 || cr.Kind != mir.RvBinary || !wholeOf(cr.A, c) || cr.B.Kind != mir.OpImm {
		return -1, -1
	}
	switch cr.Bin {
	case mir.Add, mir.AddNoWrap:
	case mir.AddWrap:
		if p.mut != mutCounterWrap {
			return -1, -1
		}
	default:
		return -1, -1
	}
	v := p.info[k].immValue(cr.B.Bits)
	if v.neg0() {
		return -1, -1
	}
	if v.cmp(wInt(1)) >= 0 {
		return k, 1
	}
	return k, 0
}

// stepsCut reports whether every path from loop l's header back to it
// passes a block of l with a unit step of counter k (mutCounterStep0: any
// step): a search from the header that does not enter such blocks must
// not come back to the header. O(blocks of l).
func (p *rangePass) stepsCut(ls *loopSet, l int, k int32, seen []bool) bool {
	b := p.b
	member := ls.member[l]
	h := ls.header[l]
	cut := func(x mir.BlockID) bool {
		for j := range b.Blocks[x].Stmts {
			s := &b.Blocks[x].Stmts[j]
			if s.Kind != mir.StAssign || s.Place.Proj.Len != 0 {
				continue
			}
			if c, step := p.counterStep(s); c == k && (step >= 1 || p.mut == mutCounterStep0 && step >= 0) {
				return true
			}
		}
		return false
	}
	if cut(h) {
		return true
	}
	clear(seen)
	seen[h] = true
	work := []mir.BlockID{h}
	ok := true
	for len(work) > 0 && ok {
		u := work[len(work)-1]
		work = work[:len(work)-1]
		for _, s := range p.succsOf(u) {
			if !member[s] {
				continue
			}
			if s == h {
				ok = false // a cycle with no unit step
				break
			}
			if seen[s] {
				continue
			}
			seen[s] = true
			if !cut(s) {
				work = append(work, s)
			}
		}
	}
	return ok
}

// countAssigns sets cnt to, per tracked local, its assignments in the
// member blocks: assigns (projected ones too) and call destinations.
func (p *rangePass) countAssigns(member []bool, cnt []int32) {
	clear(cnt)
	add := func(l mir.LocalID) {
		if int(l) < len(p.idx) {
			if k := p.idx[l]; k >= 0 {
				cnt[k]++
			}
		}
	}
	b := p.b
	for x := range b.Blocks {
		if !member[x] {
			continue
		}
		for k := range b.Blocks[x].Stmts {
			if s := &b.Blocks[x].Stmts[k]; s.Kind == mir.StAssign {
				add(s.Place.Local)
			}
		}
		if t := &b.Blocks[x].Term; isCall(t) { // a tail call's Dest is _0
			add(t.Dest.Local)
			if t.HasErr {
				add(t.Err.Error)
			}
		}
	}
}

// loopEntry sets entry to the join of the states on the edges into header
// h from outside the loop; false when no such edge is reached.
func (p *rangePass) loopEntry(h mir.BlockID, member []bool, entry []ival) bool {
	have := false
	for _, q := range p.predsOf(h) {
		if member[q] || !p.reached[q] {
			continue
		}
		copy(p.cur, p.state(q))
		p.run(q, p.cur, nil, func(s mir.BlockID, out []ival, _ []int64) {
			if s != h {
				return
			}
			if !have {
				copy(entry, out)
				have = true
				return
			}
			for i := range entry {
				entry[i] = joinI(entry[i], out[i])
			}
		})
	}
	return have
}

// accBlock is rule 8 for the sites of one block of the loop whose
// counters are kcs and whose entry state is r.entry. A site may be in any
// block of the loop that no nested loop holds, the latch included: c at
// the site is c_entry plus at least the completed iterations, wherever
// the site lies against the steps.
func (p *rangePass) accBlock(blk mir.BlockID, sites []accSite, kcs []int32, r *accRun) {
	b := p.b
	cands := r.cands[:0]
	for _, a := range sites {
		st := &b.Blocks[blk].Stmts[a.j]
		if st.Place.Proj.Len != 0 {
			continue
		}
		sl := st.Place.Local
		ks := p.idx[sl]
		// s's only assignment in L is the add (which counted itself).
		if ks < 0 || r.cnt[ks] != 1 {
			continue
		}
		var e mir.Operand
		switch rv := &st.Rvalue; {
		case wholeOf(rv.A, sl) && !wholeOf(rv.B, sl):
			e = rv.B
		case wholeOf(rv.B, sl) && !wholeOf(rv.A, sl):
			e = rv.A
		default:
			continue
		}
		cands = append(cands, accCand{j: a.j, ks: ks, e: e})
	}
	r.cands = cands
	if len(cands) == 0 {
		return
	}
	next := 0
	copy(p.cur, p.state(blk))
	p.run(blk, p.cur, func(j int, st []ival) {
		if next < len(cands) && cands[next].j == j {
			c := &cands[next]
			c.er, c.ok = p.operand(st, c.e)
			for i, kc := range kcs {
				c.cS[i] = st[kc]
			}
			next++
		}
	}, func(mir.BlockID, []ival, []int64) {})
	for i := range cands {
		c := &cands[i]
		if !c.ok {
			continue
		}
		in := p.info[c.ks]
		sE := in.load(r.entry[c.ks])
		for n, kc := range kcs {
			ci := p.info[kc]
			cE := ci.load(r.entry[kc])
			cS := ci.load(c.cS[n])
			iter := cS.hi.sub(cE.lo)
			if p.mut == mutAccumIter {
				iter = iter.sub(wInt(1))
			}
			if iter.neg0() {
				iter = wInt(0)
			}
			lo, hi := wMin(c.er.r.lo, wInt(0)), wMax(c.er.r.hi, wInt(0))
			before := wi{sE.lo.add(iter.mul(lo)), sE.hi.add(iter.mul(hi))}
			after := wi{before.lo.add(c.er.r.lo), before.hi.add(c.er.r.hi)}
			// Obligation: every value s + e can take at the add lies in the
			// type, with the completed iterations at most c - c_entry.
			if in.fits(after) {
				b.Blocks[blk].Stmts[c.j].Rvalue.Bin = mir.AddNoWrap
				break
			}
		}
	}
}
