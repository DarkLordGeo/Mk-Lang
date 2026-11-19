// Why: lower makes `a && b` and `a || b` control flow, and the range pass's
// join forgets what each path knew: in `for i < n && b[i] != Quote` the body
// loses i < n, and after `if a < 0 || b > s.Len() { Fail() }` the rest loses
// b <= s.Len(). Without this rule those bounds checks stay.
//
// What: rule 12 of the range analysis: splitSetup finds the short-circuit
// joins, splitKeep keeps each predecessor's refined out-state, and splitEmit
// hands each edge of the join's switch the join of the feasible states.
//
// How: lower (expr.go logical) builds a's switch, a block that assigns the
// result r from b, a block that assigns r the short value, and a join that
// switches on r or on a copy or negation of it (`if !(a && b)`, `c := a && b;
// if c`). The join's switch is refined per predecessor path, not from the
// union of their states.

// The rule: for a join block J that ends in `switch k [0: F, _: T]`, k a
// tracked bool whose value in J is r's (k = r, or r through copies and
// negations J makes, each of a whole tracked bool), with no other
// statement of J assigning anything (only scope ends, joins and nops
// besides the chain) and J no widening point, the pass keeps, for each of
// J's predecessors P and each value v of r, P's out-state on the edge to J
// refined by r = v through P's own definitions (rule 5, as at P's own
// switch; plain r = v when P ends in a call, whose destination run writes
// without a definition entry), and its zone refined the same way. The
// edge of J's switch that needs r = v gets the join, over the predecessors
// whose refinement by r = v is feasible, of those states, with the chain's
// values set.
//
// Obligation: every execution that reaches J's switch with r = v came
// through exactly one predecessor P, on P's one edge into J (a block with
// two edges into J makes J no split join), whose out-state on that edge it
// had at P's end; between P's end and J's switch only the chain runs,
// which assigns the chain's bools and nothing else, so every other tracked
// value and every zone variable at the switch is its value at P's end, and
// r is v there. The refinement of P's out-state by r = v holds every such
// state (refine is sound on P's definitions, which ran in P before its
// end). So the join over the feasible predecessors holds every execution on
// the edge. The kept states are a predecessor's out-state as the solve
// last computed it; a change marks J dirty, so at the post-fixpoint each
// one is the out-state under the predecessor's final in-state, and in
// narrowing and the replays each is a sound out-state of a sound in-state.
// J is never a widening point, so no state passes a loop without widening.

package lower

import (
	"slices"

	"mk/internal/mir"
)

// splitJoin is one short-circuit join's kept predecessor states.
type splitJoin struct {
	r     int32 // the bool the chain starts at, assigned in no statement of J
	chain []splitStep
	not   bool          // k is the negation of r (an odd number of negations)
	off   bool          // an out-state came from no predecessor listed: the rule is off here
	preds []mir.BlockID // J's predecessors in the analysed CFG
	have  []bool        // per predecessor: an out-state was kept
	feas  [2][]bool     // per value of r, per predecessor: the refinement is feasible
	st    [2][]ival     // per value of r: preds × tracked
	z     [2][]int64    // per value of r: preds × m², nil without a zone
}

// splitStep is one statement of the chain: dst = src, or dst = !src.
type splitStep struct {
	dst, src int32
	not      bool
}

// splitSetup finds the short-circuit joins: blocks J reached from the
// entry, not a widening point, with at least two predecessors, ending in
// `switch k [0: _, _: _]` on a tracked bool k that J's statements derive
// from a bool r by copies and negations alone.
func (p *rangePass) splitSetup() {
	b := p.b
	for blk := range b.Blocks {
		J := mir.BlockID(blk)
		if p.pos[J] < 0 || p.widen[J] || J == mir.EntryBlock || len(p.predsOf(J)) < 2 {
			continue
		}
		t := &b.Blocks[J].Term
		if t.Kind != mir.TermSwitch || t.On.Kind != mir.OpCopy || t.On.Place.Proj.Len != 0 || t.Cases.Len != 1 || t.Default == mir.NoBlock || b.Cases[t.Cases.Start].Value != 0 {
			continue
		}
		k := p.idx[t.On.Place.Local]
		if k < 0 || p.info[k].bits != 1 {
			continue
		}
		var chain []splitStep
		ok := true
		for j := range b.Blocks[J].Stmts {
			s := &b.Blocks[J].Stmts[j]
			if s.Kind == mir.StAsm {
				ok = false
				break
			}
			if s.Kind != mir.StAssign {
				continue
			}
			st, good := p.splitStepOf(s)
			if !good && p.mut != mutSplitStmt {
				ok = false
				break
			}
			if good {
				chain = append(chain, st)
			}
		}
		if !ok {
			continue
		}
		// Follow k back through the chain to r; each chain bool is assigned
		// once, and every step must be on k's chain.
		r, not, used := k, false, 0
		for i := len(chain) - 1; i >= 0; i-- {
			if chain[i].dst != r {
				continue
			}
			r, not = chain[i].src, not != chain[i].not
			used++
		}
		if used != len(chain) || r == k && len(chain) > 0 {
			continue
		}
		if !p.splitOnce(chain, r) {
			continue
		}
		if p.splitAt == nil {
			p.splitAt = make([]int32, len(b.Blocks))
			for i := range p.splitAt {
				p.splitAt[i] = -1
			}
		}
		preds := append([]mir.BlockID(nil), p.predsOf(J)...)
		if splitTwice(preds) {
			continue // a block with two edges into J: its states are per edge
		}
		p.splitAt[J] = int32(len(p.splits))
		p.splits = append(p.splits, splitJoin{r: r, chain: chain, not: not, preds: preds, have: make([]bool, len(preds))})
	}
}

// splitStepOf is s as a chain step: a whole tracked bool assigned a copy,
// move or negation of a whole tracked bool.
func (p *rangePass) splitStepOf(s *mir.Statement) (splitStep, bool) {
	if s.Place.Proj.Len != 0 {
		return splitStep{}, false
	}
	dst := p.idx[s.Place.Local]
	if dst < 0 || p.info[dst].bits != 1 {
		return splitStep{}, false
	}
	r := &s.Rvalue
	var not bool
	switch {
	case r.Kind == mir.RvUse:
	case r.Kind == mir.RvUnary && r.Un == mir.Not:
		not = true
	default:
		return splitStep{}, false
	}
	src := p.depOf(r.A)
	if src < 0 || p.info[src].bits != 1 || src == dst {
		return splitStep{}, false
	}
	return splitStep{dst: dst, src: src, not: not}, true
}

// splitTwice reports that a block is listed twice among J's predecessors:
// two edges from one block (a switch whose case and default are both J),
// whose out-states may differ, so one kept state per block would lose one.
func splitTwice(preds []mir.BlockID) bool {
	for i, q := range preds {
		if slices.Contains(preds[:i], q) {
			return true
		}
	}
	return false
}

// splitOnce reports that each chain bool is assigned once and that r, the
// chain's start, is assigned by none of it.
func (p *rangePass) splitOnce(chain []splitStep, r int32) bool {
	for i, s := range chain {
		if s.dst == r {
			return false
		}
		for _, o := range chain[:i] {
			if o.dst == s.dst {
				return false
			}
		}
	}
	return true
}

// splitKeep keeps the out-state (out, zout) of the block run (p.blk) on its
// edge to the split join s, refined by r = 0 and by r = 1; it reports
// whether what was kept changed. Called where an out-state reaches s: the
// solve's join and narrowing's.
func (p *rangePass) splitKeep(s mir.BlockID, out []ival, zout []int64) bool {
	sj := &p.splits[p.splitAt[s]]
	i := -1
	for n, q := range sj.preds {
		if q == p.blk {
			i = n
			break
		}
	}
	if i < 0 {
		sj.off = true
		return false
	}
	if sj.have[i] && p.mut == mutSplitFirst {
		return false
	}
	zs := p.zs
	n, mm := p.n, 0
	if zs != nil {
		mm = zs.m * zs.m
	}
	if sj.st[0] == nil {
		for v := 0; v < 2; v++ {
			sj.feas[v] = make([]bool, len(sj.preds))
			sj.st[v] = make([]ival, len(sj.preds)*n)
			if zs != nil {
				sj.z[v] = make([]int64, len(sj.preds)*mm)
			}
		}
	}
	if p.sbuf == nil {
		p.sbuf = make([]ival, n)
		if zs != nil {
			p.zbuf = make([]int64, mm)
		}
	}
	// A predecessor ending in a call wrote the call's destination after
	// its statements, with no definition entry: r = v only.
	tk := p.b.Blocks[p.blk].Term.Kind
	plain := tk != mir.TermGoto && tk != mir.TermSwitch
	changed := !sj.have[i]
	sj.have[i] = true
	for v := 0; v < 2; v++ {
		tmp := p.sbuf
		copy(tmp, out)
		var ok bool
		if plain {
			tmp[sj.r] = ival{max(tmp[sj.r].lo, int64(v)), min(tmp[sj.r].hi, int64(v))}
			ok = !tmp[sj.r].empty()
		} else {
			ok = p.refine(tmp, sj.r, ival{int64(v), int64(v)}, 0)
		}
		var zt []int64
		if zs != nil {
			zt = p.zbuf
			copy(zt, zout)
			if ok && !plain {
				// The zone's compare terms are read before rule 5 narrows
				// the state, as at a switch (run).
				if op, ta, tb, small, zok := p.zCompareVia(out, sj.r); zok {
					ok = p.zRefine(zt, op, ta, tb, small, v == 1)
				}
			}
		}
		if sj.feas[v][i] != ok {
			sj.feas[v][i] = ok
			changed = true
		}
		if !ok {
			continue
		}
		dst := sj.st[v][i*n : (i+1)*n]
		for x := range dst {
			if dst[x] != tmp[x] {
				copy(dst, tmp)
				changed = true
				break
			}
		}
		if zs != nil {
			zd := sj.z[v][i*mm : (i+1)*mm]
			for x := range zd {
				if zd[x] != zt[x] {
					copy(zd, zt)
					changed = true
					break
				}
			}
		}
	}
	return changed
}

// zCompareVia is zCompare for a bool defined by copies of a compare's
// result in the block run, each copy's source unchanged since.
func (p *rangePass) zCompareVia(st []ival, k int32) (mir.BinOp, zterm, zterm, bool, bool) {
	for d := 0; d < 8 && p.def[k] >= 0; d++ {
		r := &p.b.Blocks[p.blk].Stmts[p.def[k]].Rvalue
		if r.Kind != mir.RvUse {
			break
		}
		a := p.defA[k]
		if a < 0 || !p.defValid(k) || p.info[a].bits != 1 {
			return 0, zterm{}, zterm{}, false, false
		}
		k = a
	}
	return p.zCompare(st, k)
}

// splitEmit is the switch of split join blk: each edge gets the join of
// the kept predecessor states where r can take its value, with the
// chain's values set; false when blk keeps no state yet (the caller then
// refines as at any switch).
func (p *rangePass) splitEmit(blk mir.BlockID, emit func(mir.BlockID, []ival, []int64)) bool {
	sj := &p.splits[p.splitAt[blk]]
	if sj.off || sj.st[0] == nil || !slices.Contains(sj.have, true) {
		return false
	}
	b := p.b
	t := &b.Blocks[blk].Term
	zs := p.zs
	n, mm := p.n, 0
	if zs != nil {
		mm = zs.m * zs.m
	}
	if p.ebuf == nil {
		p.ebuf = make([]ival, n)
		if zs != nil {
			p.ezbuf = make([]int64, mm)
		}
	}
	for kv := 0; kv < 2; kv++ {
		to := t.Default
		if kv == 0 {
			to = b.Cases[t.Cases.Start].Target
		}
		// r's value on this edge: k's, or its negation.
		v := kv
		if sj.not && p.mut != mutSplitNot {
			v = 1 - kv
		}
		if p.mut == mutSplitFlip {
			v = 1 - v
		}
		dst, zd := p.ebuf, p.ezbuf
		any := false
		for i := range sj.preds {
			if !sj.have[i] || !sj.feas[v][i] {
				continue
			}
			src := sj.st[v][i*n : (i+1)*n]
			if !any {
				copy(dst, src)
				if zs != nil {
					copy(zd, sj.z[v][i*mm:(i+1)*mm])
				}
				any = true
				continue
			}
			for x := range dst {
				dst[x] = joinI(dst[x], src[x])
			}
			if zs != nil {
				zs.join(zd, sj.z[v][i*mm:(i+1)*mm], false)
			}
		}
		if !any {
			continue // no execution reaches this edge
		}
		// The chain runs after the predecessors' ends: r is v, and each
		// chain bool its source's value or its negation.
		for _, s := range sj.chain {
			x := dst[s.src].lo
			if s.not {
				x = 1 - x
			}
			dst[s.dst] = ival{x, x}
		}
		emit(to, dst, zd)
	}
	return true
}
