// Why: a checked `s += x` (or `s -= x`) left as the only check in an
// innermost counted loop blocks the loop vectoriser with its early exit.
//
// What: versionReduce, which versions such checked reductions in the
// codegen-only frame of rangeversion.go; reduction finds the shape,
// restartable and liveIn prove a chunk can be rerun, versionReduction
// builds the version.
//
// How: the rewrite runs the loop as pass 1, pure: the sum with the wrapping operator, and
// beside it m |= uint(x) + B (a biased-OR precheck), with B a power of two
// chosen from the whole trip count. Pass 1 runs in chunks of at most
// redChunk iterations, and the precheck decides after each (so a failing
// program's trap is late by at most one chunk, never by the whole loop).
// While m < 2B every term so far lay in [-B, B), so no prefix of the sum
// can overflow and pass 1's state is the checked loop's. When a
// chunk fails, s and the counter are restored to that chunk's start and
// the original loop runs from there, and traps where it would have.
//
// The proof, for a signed 64-bit s. Let N be at least the number of terms,
// the initial s0 and one x per iteration; P = 2^(f+1) with f = floor(log2
// N), so N < P; B = 2^63 / P. For a term v, uint(v) +% B < 2B holds exactly
// when v is in [-B, B): v + B in [0, 2B) does not wrap; v >= B gives v + B
// >= 2B or, wrapped, a negative value (as uint at least 2^63 >= 2B); v < -B
// gives a negative v + B. An OR of values is below a power of two 2B
// exactly when each is. So every term is in [-B, B), and a prefix of j <=
// N terms lies in [-jB, j(B-1)], with jB <= (P-1)·2^63/P = 2^63 - B < 2^63:
// inside the type. For s -= x the terms are s0 and -x, and B -% uint(x) is
// uint(-x) + B. For an unsigned s += x the terms are their own values, and
// all below 2B = 2^64 / P bounds the sum below N·2^64/P < 2^64. m is
// cumulative over the chunks and N counts every term of the whole trip, so
// at each chunk's end the argument covers every prefix so far: the s and
// counter a chunk starts from are the checked loop's after the iterations
// before it, and the original loop rerun from them is the checked loop's
// rest (restartable).

package lower

import (
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// redMaxLoopOps is the most statements plus terminators of a loop this
// rewrite versions.
const redMaxLoopOps = 64

// redChunk is the most iterations pass 1 runs before the precheck decides.
// A power of two, so a vectorised chunk has no remainder; at
// a few instructions per iteration the per-chunk test is noise, and a
// failing loop's trap waits for at most this many iterations.
const redChunk = 1 << 12

// redLoop is a loop this rewrite can version: l, and its accumulator statement.
type redLoop struct {
	l   *vloop
	acc point
	s   mir.LocalID
	sub bool
	sty types.TypeID
	sgn bool
}

// versionReduce versions every qualifying loop of b, pass 1 in
// chunks of chunk iterations.
func versionReduce(cfg *Config, b *mir.Body, mut versionMut, chunk uint64) (*mir.Body, int) {
	x := newVctx(cfg, b, mut)
	x.chunk = chunk
	var cands []redLoop
	for _, l := range countedLoops(x.g, x.tab) {
		if r, ok := x.reduction(l); ok {
			cands = append(cands, r)
		}
	}
	if len(cands) == 0 {
		return b, 0
	}
	live := newLiveIn(x, cands)
	done := 0
	for _, r := range cands {
		if done == verMaxLoops {
			break
		}
		if !x.restartable(r, live) {
			continue
		}
		x.versionReduction(r)
		done++
	}
	if x.out == nil {
		return b, 0
	}
	return x.out, done
}

// reduction checks the rewrite's shape on l: the loop leaves only by its header
// (no check is left: a bounds check would be an edge to a panic block),
// every statement writes a whole local nothing references, by an operation
// that cannot trap, except the one accumulator `s = Add(copy s, x)` or
// `s = Sub(copy s, x)` (checked) of a 64-bit integer s, Sub only signed,
// which is s's only definition and only read in the loop.
func (x *vctx) reduction(l *vloop) (redLoop, bool) {
	b := x.b
	if l.ops > redMaxLoopOps || l.small || !x.hiInvariant(l) || x.addr[l.c] {
		return redLoop{}, false
	}
	r := redLoop{l: l, acc: point{mir.NoBlock, 0}}
	for _, m := range l.members {
		for _, s := range x.g.succ[m] {
			if !l.in[s] && !(m == l.h && s == l.exit) {
				return redLoop{}, false
			}
		}
		blk := &b.Blocks[m]
		if t := &blk.Term; t.Kind == mir.TermSwitch && x.derefRead(&mir.Rvalue{Kind: mir.RvUse, A: t.On}) {
			return redLoop{}, false
		}
		for j := range blk.Stmts {
			st := &blk.Stmts[j]
			switch st.Kind {
			case mir.StScopeEnd, mir.StNop:
				continue
			case mir.StAssign:
			default:
				return redLoop{}, false
			}
			if st.Place.Proj.Len != 0 || x.addr[st.Place.Local] {
				return redLoop{}, false
			}
			rv := &st.Rvalue
			// Pass 1 runs iterations the original may never reach (past an
			// overflow): its loads must be valid whatever the iteration, so
			// none goes through a pointer or reference (a *T in unsafe code
			// carries no proof); a slice index's bounds check is proved.
			if x.derefRead(rv) {
				return redLoop{}, false
			}
			if rv.Kind == mir.RvBinary && (rv.Bin == mir.Add || rv.Bin == mir.Sub) && x.intType(rv.Type) {
				in := intOf(x.tab, rv.Type)
				s := st.Place.Local
				if r.acc.blk != mir.NoBlock || in.bits != 64 || !in.signed && rv.Bin == mir.Sub ||
					!wholeOf(rv.A, s) || rv.A.Kind != mir.OpCopy || b.Locals[s].Type != rv.Type || s == l.c {
					return redLoop{}, false
				}
				r.acc, r.s, r.sub, r.sty, r.sgn = point{m, j}, s, rv.Bin == mir.Sub, rv.Type, in.signed
				continue
			}
			if !x.noTrap(rv) {
				return redLoop{}, false
			}
		}
	}
	if r.acc.blk == mir.NoBlock || l.defs[r.s] != 1 || x.reads(l, r.s) != 1 && x.mut != vmutReadsAny {
		return redLoop{}, false
	}
	return r, true
}

// derefRead reports whether r reads a place through a deref step.
func (x *vctx) derefRead(r *mir.Rvalue) bool {
	deref := func(pl mir.Place) bool {
		for _, pr := range x.b.Proj(pl) {
			if pr.Kind == mir.ProjDeref {
				return true
			}
		}
		return false
	}
	op := func(o mir.Operand) bool { return (o.Kind == mir.OpCopy || o.Kind == mir.OpMove) && deref(o.Place) }
	switch r.Kind {
	case mir.RvUse, mir.RvUnary, mir.RvCast:
		return op(r.A)
	case mir.RvBinary:
		return op(r.A) || op(r.B)
	case mir.RvLen, mir.RvDiscriminant:
		return deref(r.Place)
	}
	return false
}

// intType reports whether t is an integer type.
func (x *vctx) intType(t types.TypeID) bool {
	in := intOf(x.tab, t)
	return in.ok && t != types.Bool
}

// noTrap reports whether evaluating r can never trap: a use, a length, a
// compare, wrapping or proved integer arithmetic, bitwise operators, a
// shift by an immediate below the width, a / or % by an immediate that is
// positive in the type, any float operation, Not and BitNot, and the casts
// that never trap. A use's place was checked by a check proved before or
// guarded by rangeversion.go's version, whose proof no iteration's s enters.
//
// Why pass 1's iterations past a failed term are safe. Past an
// overflow the original never runs on, so the range pass's facts need not
// hold there: after a checked add whose exact range lies wholly outside the
// type the interval is empty, and the zone may keep a bound such as s <= 0
// that only the unreachable state breaks. Pass 1 does reach that state, up
// to the end of its chunk. It is harmless only because nothing such a fact
// could prove reaches anything observable: s is read once, by its own add
// (reduction's reads rule, so no compare, index or operand reads it), every
// other local the loop writes is dead at the header (restartable), and the
// loop makes no store, call or asm (reduction, vloop.shape). Loosening any
// of these makes it a hole: vmutReadsAny and vmutAnyLive are the mutants,
// caught by pvEdge's red-reads and safe-live.
func (x *vctx) noTrap(r *mir.Rvalue) bool {
	switch r.Kind {
	case mir.RvUse, mir.RvLen, mir.RvDiscriminant:
		return true
	case mir.RvUnary:
		return r.Un != mir.Neg || x.tab.Get(r.Type).Kind == types.KFloat
	case mir.RvCast:
		return r.Cast == mir.CastLossless || r.Cast == mir.CastTrunc
	case mir.RvBinary:
		if r.Bin.IsCompare() || x.tab.Get(r.Type).Kind == types.KFloat {
			return true
		}
		in := intOf(x.tab, r.Type)
		if !in.ok {
			return false
		}
		switch r.Bin {
		case mir.AddWrap, mir.SubWrap, mir.MulWrap, mir.AddNoWrap, mir.SubNoWrap, mir.MulNoWrap,
			mir.BitAnd, mir.BitOr, mir.BitXor:
			return true
		case mir.Shl, mir.Shr:
			return r.B.Kind == mir.OpImm && r.B.Bits < uint64(in.bits)
		case mir.Div, mir.Rem:
			if r.B.Kind != mir.OpImm || r.B.Bits == 0 {
				return false
			}
			mask := uint64(1)<<(in.bits-1)<<1 - 1 // the type's bits, 2^bits - 1
			if in.signed {
				return r.B.Bits&mask < uint64(1)<<(in.bits-1)
			}
			return r.B.Bits&mask != 0
		}
	}
	return false
}

// reads counts the reads of local v in l: operands, index locals of
// places, and the places of lengths and borrows.
func (x *vctx) reads(l *vloop, v mir.LocalID) int {
	n := 0
	var buf []mir.LocalID
	for _, m := range l.members {
		buf = blockUses(x.b, &x.b.Blocks[m], buf[:0], -1)
		for _, u := range buf {
			if u == v {
				n++
			}
		}
	}
	return n
}

// placeReads appends the locals a read of pl uses: its root and every index.
func placeReads(b *mir.Body, pl mir.Place, buf []mir.LocalID) []mir.LocalID {
	buf = append(buf, pl.Local)
	for _, pr := range b.Proj(pl) {
		if pr.Kind == mir.ProjIndex {
			buf = append(buf, mir.LocalID(pr.A))
		}
	}
	return buf
}

func operandReads(b *mir.Body, o mir.Operand, buf []mir.LocalID) []mir.LocalID {
	if o.Kind == mir.OpCopy || o.Kind == mir.OpMove {
		return placeReads(b, o.Place, buf)
	}
	return buf
}

func rvalueReads(b *mir.Body, r *mir.Rvalue, buf []mir.LocalID) []mir.LocalID {
	switch r.Kind {
	case mir.RvUse, mir.RvUnary, mir.RvCast, mir.RvRepeat:
		buf = operandReads(b, r.A, buf)
	case mir.RvBinary:
		buf = operandReads(b, r.A, buf)
		buf = operandReads(b, r.B, buf)
	case mir.RvRef, mir.RvLen, mir.RvDiscriminant:
		buf = placeReads(b, r.Place, buf)
	case mir.RvSlice:
		buf = placeReads(b, r.Place, buf)
		if r.HasLo {
			buf = operandReads(b, r.A, buf)
		}
		if r.HasHi {
			buf = operandReads(b, r.B, buf)
		}
	case mir.RvAggregate:
		for _, o := range b.Operands[r.Ops.Start:r.Ops.End()] {
			buf = operandReads(b, o, buf)
		}
	}
	return buf
}

// blockUses appends the reads of statements [0, upto) of blk (upto < 0:
// every statement and the terminator), statement by statement in order;
// a write through a projection reads its root and indices.
func blockUses(b *mir.Body, blk *mir.Block, buf []mir.LocalID, upto int) []mir.LocalID {
	for j := range blk.Stmts {
		if upto >= 0 && j >= upto {
			return buf
		}
		buf = stmtReads(b, &blk.Stmts[j], buf)
	}
	return termReads(b, &blk.Term, buf)
}

func stmtReads(b *mir.Body, st *mir.Statement, buf []mir.LocalID) []mir.LocalID {
	switch st.Kind {
	case mir.StAssign:
		buf = rvalueReads(b, &st.Rvalue, buf)
		if st.Place.Proj.Len != 0 {
			buf = placeReads(b, st.Place, buf)
		}
	case mir.StJoin:
		buf = append(buf, st.Local)
	case mir.StAsm:
		for _, op := range b.Asms[st.Asm].Operands {
			buf = operandReads(b, op.In, buf)
			if op.Out.Local != mir.NoLocal {
				buf = placeReads(b, op.Out, buf)
			}
		}
	}
	return buf
}

func termReads(b *mir.Body, t *mir.Terminator, buf []mir.LocalID) []mir.LocalID {
	switch t.Kind {
	case mir.TermSwitch:
		buf = operandReads(b, t.On, buf)
	case mir.TermCall, mir.TermTailCall:
		for _, a := range b.Args[t.Args.Start:t.Args.End()] {
			buf = operandReads(b, a.Operand, buf)
		}
		if t.Callee.Kind == mir.CallValue {
			buf = operandReads(b, t.Callee.Value, buf)
		}
		if t.Spawn != mir.NoLocal {
			buf = append(buf, t.Spawn)
		}
		if t.Dest.Proj.Len != 0 {
			buf = placeReads(b, t.Dest, buf)
		}
	case mir.TermOnerrGuard:
		buf = append(buf, t.Value)
	case mir.TermReturn:
		buf = append(buf, 0)
	case mir.TermPanic:
		if t.HasMsg {
			buf = operandReads(b, t.Msg, buf)
		}
	}
	return buf
}

// liveIn is, for each block, which of a set of locals are live on entry
// (backward liveness: a read before any whole write of the local in the
// block, or live at a successor and not written whole in the block). The
// set is the locals the candidate loops write other than their s and
// counter, the only ones restartable asks about (all of the
// body's locals made it blocks × locals / 64 words per sweep).
type liveIn struct {
	words int
	idx   []int32    // by local: its bit, or -1 outside the set
	in    [][]uint64 // by block
}

func newLiveIn(x *vctx, cands []redLoop) *liveIn {
	b := x.b
	nb := len(b.Blocks)
	lv := &liveIn{idx: make([]int32, len(b.Locals)), in: make([][]uint64, nb)}
	for i := range lv.idx {
		lv.idx[i] = -1
	}
	k := int32(0)
	for _, r := range cands {
		for v, n := range r.l.defs {
			if n != 0 && mir.LocalID(v) != r.s && mir.LocalID(v) != r.l.c && lv.idx[v] < 0 {
				lv.idx[v] = k
				k++
			}
		}
	}
	w := (int(k) + 63) / 64
	lv.words = w
	use := make([][]uint64, nb)
	def := make([][]uint64, nb)
	var buf []mir.LocalID
	for i := range b.Blocks {
		use[i], def[i], lv.in[i] = make([]uint64, w), make([]uint64, w), make([]uint64, w)
		if w == 0 {
			continue
		}
		blk := &b.Blocks[i]
		mark := func(v mir.LocalID) {
			if q := lv.idx[v]; q >= 0 && def[i][q/64]&(1<<(q%64)) == 0 {
				use[i][q/64] |= 1 << (q % 64)
			}
		}
		for j := range blk.Stmts {
			st := &blk.Stmts[j]
			buf = stmtReads(b, st, buf[:0])
			for _, v := range buf {
				mark(v)
			}
			if st.Kind == mir.StAssign && st.Place.Proj.Len == 0 {
				if q := lv.idx[st.Place.Local]; q >= 0 {
					def[i][q/64] |= 1 << (q % 64)
				}
			}
		}
		buf = termReads(b, &blk.Term, buf[:0])
		for _, v := range buf {
			mark(v)
		}
		// A call's whole destination is written after its reads; it is
		// never in a loop this rewrite versions, so it is left a use-free def
		// only conservatively: not killed.
	}
	if w == 0 {
		return lv
	}
	out := make([]uint64, w)
	for changed := true; changed; {
		changed = false
		for k := len(x.g.rpo) - 1; k >= 0; k-- {
			i := x.g.rpo[k]
			clear(out)
			for _, s := range x.g.succ[i] {
				for q := range out {
					out[q] |= lv.in[s][q]
				}
			}
			for q := range out {
				nw := use[i][q] | out[q]&^def[i][q]
				if nw != lv.in[i][q] {
					lv.in[i][q] = nw
					changed = true
				}
			}
		}
	}
	return lv
}

// has reports whether v, a local of the set, is live on entry to blk.
func (lv *liveIn) has(blk mir.BlockID, v mir.LocalID) bool {
	q := lv.idx[v]
	if q < 0 {
		source.ICEf(source.Span{}, "lower: liveness asked about local %d outside its set", v)
	}
	return lv.in[blk][q/64]&(1<<(q%64)) != 0
}

// restartable reports whether rerunning l from its header after a chunk of
// pass 1, with s and c restored to that chunk's start, starts from the
// state the checked loop had there: pass 1 writes only whole locals that
// nothing references (reduction), so it changes no memory, and each local
// the loop writes other than s and c is dead on entry to the header: every
// path from it writes the local before reading it, pass 1's value
// included.
func (x *vctx) restartable(r redLoop, lv *liveIn) bool {
	if x.mut == vmutAnyLive {
		return true
	}
	l := r.l
	for v, n := range l.defs {
		if n == 0 || mir.LocalID(v) == r.s || mir.LocalID(v) == l.c {
			continue
		}
		if lv.has(l.h, mir.LocalID(v)) {
			return false
		}
	}
	return true
}

// versionReduction builds the version of r.l, with K = x.chunk:
//
//	pre:  goto V
//	V:    s0 = s; trip = uint(hi) -% uint(c)
//	      switch trip < 2^62 [0: h (the original loop), _: next]
//	      N = trip + 1; smear N's bits right; P = N' + 1; B = 2^63 / P
//	      lim = B + B; m = uint(s0) +% B (unsigned: uint(s0))
//	      goto E
//	E:    t = Lt(copy c, hi); switch t [0: exit, _: C] (the header's test)
//	C:    sk = s; ck = c; rem = uint(hi) -% uint(c)
//	      switch K < rem [0: he = hi, _: he = c +% K]; goto h'
//	h'..: the loop's copy, its header testing c < he, its accumulator
//	      `s = s +% xv` and m |= uint(xv) +% B (s -= x: B -% uint(xv);
//	      unsigned: uint(xv)), its exit to D
//	D:    switch m < lim [0: R, _: E]
//	R:    s = sk; c = ck; goto h
//
// trip is the iteration count when hi > c (hi - c in [1, 2^64) is exact
// as uint), and when hi <= c the loop runs none and any N >= 1 bounds the
// one term; trip < 2^62 keeps N <= 2^62, so P <= 2^63 does not wrap and B
// >= 1. In C, c < hi (E), so rem = hi - c is exact, and c + K < hi when K
// < rem: no chunk runs past hi, and he is c's type. Every operator here
// wraps or compares; the one / has a divisor in [2, 2^63]. Pass 1 cannot
// trap (reduction), and each chunk ends (at most K iterations of a counted
// loop with one step of one). m is not reset between chunks, so at D it
// covers s0 and every term so far, and when it fails, the original reruns
// from the chunk's start (restartable), whose s and c are the checked
// loop's there by the file's proof.
func (x *vctx) versionReduction(r redLoop) {
	l := r.l
	b := x.b
	span := b.Blocks[l.h].Term.Span
	u := types.Uint
	vb := x.newBlock()
	x.retarget(l.pre, l.h, vb)
	e := &gen{x: x, blk: vb, span: span}
	sv := mir.Copy(mir.LocalPlace(r.s))
	cv := mir.Copy(mir.LocalPlace(l.c))
	s0 := e.set(r.sty, mir.Rvalue{Kind: mir.RvUse, A: sv, Type: r.sty})
	trip := e.bin(mir.SubWrap, u, e.toUint(l.hi), e.toUint(cv))
	e.test(e.bin(mir.Lt, u, trip, mir.Imm(u, 1<<62)), false, l.h)
	tn := trip
	if x.mut == vmutTripShort {
		tn = e.bin(mir.Shr, u, trip, mir.Imm(u, 1))
	}
	nn := e.bin(mir.AddWrap, u, tn, mir.Imm(u, 1))
	for _, sh := range []uint64{1, 2, 4, 8, 16, 32} {
		nn = e.bin(mir.BitOr, u, nn, e.bin(mir.Shr, u, nn, mir.Imm(u, sh)))
	}
	p := e.bin(mir.AddWrap, u, nn, mir.Imm(u, 1))
	bias := e.bin(mir.Div, u, mir.Imm(u, 1<<63), p)
	lim := e.bin(mir.AddWrap, u, bias, bias)
	if x.mut == vmutLimitHigh {
		lim = e.bin(mir.AddWrap, u, lim, lim)
	}
	mv := x.temp(u, span)
	var m0 mir.Operand
	switch {
	case x.mut == vmutNoInit:
		m0 = mir.Imm(u, 0)
	case r.sgn:
		m0 = e.bin(mir.AddWrap, u, e.asUint(s0, r.sgn), bias)
	default:
		m0 = e.asUint(s0, r.sgn)
	}
	e.assign(mv, u, mir.Rvalue{Kind: mir.RvUse, A: m0, Type: u})

	// E: the header's own test, so the loop's t and its exit are the
	// original's when no chunk is left.
	eb := x.newBlock()
	x.out.Blocks[e.blk].Term = mir.Terminator{Kind: mir.TermGoto, Target: eb, Span: span}
	cg := &gen{x: x, blk: eb, span: span}
	*x.stmts(eb) = append(*x.stmts(eb), b.Blocks[l.h].Stmts[0])
	cg.test(mir.Copy(mir.LocalPlace(l.t)), false, l.exit)

	// C: the chunk's start, its restore point and its end he.
	sk := cg.set(r.sty, mir.Rvalue{Kind: mir.RvUse, A: sv, Type: r.sty})
	ck := cg.set(l.ty, mir.Rvalue{Kind: mir.RvUse, A: cv, Type: l.ty})
	he := x.temp(l.ty, span)
	cut := cg.bin(mir.AddWrap, l.ty, cv, mir.Imm(l.ty, x.chunk))
	cp := x.copyLoop(l)
	hd := cp[l.h]
	switch x.mut {
	case vmutNoChunk:
		cg.assign(he, l.ty, mir.Rvalue{Kind: mir.RvUse, A: l.hi, Type: l.ty})
	case vmutChunkPastHi:
		cg.assign(he, l.ty, mir.Rvalue{Kind: mir.RvUse, A: cut, Type: l.ty})
	default:
		rem := cg.bin(mir.SubWrap, u, cg.toUint(l.hi), cg.toUint(cv))
		kb := x.newBlock()
		cg.test(cg.bin(mir.Lt, u, mir.Imm(u, x.chunk), rem), false, kb)
		cg.assign(he, l.ty, mir.Rvalue{Kind: mir.RvUse, A: cut, Type: l.ty})
		kg := &gen{x: x, blk: kb, span: span}
		kg.assign(he, l.ty, mir.Rvalue{Kind: mir.RvUse, A: l.hi, Type: l.ty})
		x.out.Blocks[kb].Term = mir.Terminator{Kind: mir.TermGoto, Target: hd, Span: span}
	}
	x.out.Blocks[cg.blk].Term = mir.Terminator{Kind: mir.TermGoto, Target: hd, Span: span}
	// The copy's header tests c < he: its statements are the copy's own
	// (copyLoop clones them).
	x.out.Blocks[hd].Stmts[0].Rvalue.B = mir.Copy(mir.LocalPlace(he))

	// The accumulator in the copy.
	ab := cp[r.acc.blk]
	old := x.out.Blocks[ab].Stmts[r.acc.j]
	f := &gen{x: x, blk: ab, span: old.Span}
	xv := x.temp(r.sty, old.Span)
	op := mir.AddWrap
	if r.sub {
		op = mir.SubWrap
	}
	seq := []mir.Statement{
		{Kind: mir.StAssign, Place: mir.LocalPlace(xv), Rvalue: mir.Rvalue{Kind: mir.RvUse, A: old.Rvalue.B, Type: r.sty}, Span: old.Span},
		{Kind: mir.StAssign, Place: old.Place, Rvalue: mir.Rvalue{Kind: mir.RvBinary, Bin: op, A: sv, B: mir.Copy(mir.LocalPlace(xv)), Type: r.sty}, Span: old.Span},
	}
	tail := x.out.Blocks[ab].Stmts[r.acc.j+1:]
	x.out.Blocks[ab].Stmts = append(append([]mir.Statement(nil), x.out.Blocks[ab].Stmts[:r.acc.j]...), seq...)
	tu := f.asUint(mir.Copy(mir.LocalPlace(xv)), r.sgn)
	bt := tu
	switch {
	case r.sgn && r.sub:
		bt = f.bin(mir.SubWrap, u, bias, tu)
	case r.sgn:
		bt = f.bin(mir.AddWrap, u, tu, bias)
	}
	f.assign(mv, u, mir.Rvalue{Kind: mir.RvBinary, Bin: mir.BitOr, A: mir.Copy(mir.LocalPlace(mv)), B: bt, Type: u})
	x.out.Blocks[ab].Stmts = append(x.out.Blocks[ab].Stmts, tail...)

	// D, the decision after each chunk, and R, the fallback.
	db := x.newBlock()
	x.retarget(hd, l.exit, db)
	d := &gen{x: x, blk: db, span: span}
	rb := x.newBlock()
	d.test(d.bin(mir.Lt, u, mir.Copy(mir.LocalPlace(mv)), lim), false, rb)
	x.out.Blocks[d.blk].Term = mir.Terminator{Kind: mir.TermGoto, Target: eb, Span: span}
	rg := &gen{x: x, blk: rb, span: span}
	switch x.mut {
	case vmutNoRestore:
	case vmutRestoreFirst:
		rg.assign(r.s, r.sty, mir.Rvalue{Kind: mir.RvUse, A: s0, Type: r.sty})
	default:
		rg.assign(r.s, r.sty, mir.Rvalue{Kind: mir.RvUse, A: sk, Type: r.sty})
	}
	rg.assign(l.c, l.ty, mir.Rvalue{Kind: mir.RvUse, A: ck, Type: l.ty})
	x.out.Blocks[rb].Term = mir.Terminator{Kind: mir.TermGoto, Target: l.h, Span: span}
}

// assign emits `v = r` into the current block.
func (e *gen) assign(v mir.LocalID, t types.TypeID, r mir.Rvalue) {
	st := e.x.stmts(e.blk)
	*st = append(*st, mir.Statement{Kind: mir.StAssign, Place: mir.LocalPlace(v), Rvalue: r, Span: e.span})
}

// asUint is a 64-bit integer's bits as uint: Trunc for a signed one (as
// lower's toUint), Lossless for an unsigned one.
func (e *gen) asUint(a mir.Operand, signed bool) mir.Operand {
	if e.x.out.OperandType(a) == types.Uint {
		return a // lower's toUint: a uint is its own
	}
	k := mir.CastLossless
	if signed {
		k = mir.CastTrunc
	}
	return e.set(types.Uint, mir.Rvalue{Kind: mir.RvCast, Cast: k, A: a, Type: types.Uint})
}
