// Why: the range analysis needs, for each operand and rvalue, the interval of
// values it can hold, and for slices their lengths and, where it can know
// them, their contents.
//
// What: operand and rvalue (transfer rules 1 to 4), lenRange and maxLen
// (slice lengths, rule 6), and contents, initOf, zeroFilled and
// updateContents (the contents of slice and array locals, rule 7), and
// callResult (the result ranges of std's counting intrinsics, rule 11).
//
// How: contents are flow-insensitive: they are tracked only for a slice or
// array local whose every use is an element read or store, as the union of
// its initial contents and every value stored into it.

package lower

import (
	"math"

	"mk/internal/mir"
	"mk/internal/types"
)

// opRange is an operand's interval in its type.
type opRange struct {
	r  wi
	in intInfo
}

// operand is the interval of an integer or bool operand: an immediate, a
// tracked local, an element of a content local, or its type's range.
func (p *rangePass) operand(st []ival, op mir.Operand) (opRange, bool) {
	if op.Kind <= mir.OpMove && op.Place.Proj.Len == 0 {
		// A tracked local: its type's facts are info's (the hot path).
		if k := p.idx[op.Place.Local]; k >= 0 {
			return opRange{p.info[k].load(st[k]), p.info[k]}, true
		}
	}
	t := p.b.OperandType(op)
	in := intOf(p.tab, t)
	if !in.ok {
		return opRange{}, false
	}
	switch op.Kind {
	case mir.OpImm:
		return opRange{one(in.immValue(op.Bits)), in}, true
	case mir.OpCopy, mir.OpMove:
		if op.Place.Proj.Len == 0 {
			if k := p.idx[op.Place.Local]; k >= 0 {
				return opRange{in.load(st[k]), in}, true
			}
		} else if c := p.cidx[op.Place.Local]; c >= 0 && op.Place.Proj.Len == 1 && p.b.Proj(op.Place)[0].Kind == mir.ProjIndex {
			return opRange{p.cinfo[c].load(p.cval[c]), in}, true
		}
	}
	return opRange{in.full(), in}, true
}

// rvalue is the interval of r in the destination type in.
func (p *rangePass) rvalue(st []ival, r *mir.Rvalue, in intInfo) ival {
	v, ok := p.rvalueOK(st, r, in)
	if !ok {
		return in.store(in.full())
	}
	return v
}

// rvalueOK is rvalue's work: false means the type's whole range, built
// only then (the hot path never needs it).
func (p *rangePass) rvalueOK(st []ival, r *mir.Rvalue, in intInfo) (ival, bool) {
	switch r.Kind {
	case mir.RvUse:
		if a, ok := p.operand(st, r.A); ok {
			return in.store(a.r), true
		}
	case mir.RvBinary:
		a, aok := p.operand(st, r.A)
		b, bok := p.operand(st, r.B)
		if !aok || !bok {
			return ival{}, false
		}
		if r.Bin.IsCompare() {
			switch cmpRange(r.Bin, a.r, b.r) {
			case 1:
				return ival{1, 1}, true
			case 0:
				return ival{0, 0}, true
			}
			if p.zs != nil {
				// Rules 9 and 10: the zone decides what the intervals do not.
				switch p.zCmp(st, r) {
				case 1:
					return ival{1, 1}, true
				case 0:
					return ival{0, 0}, true
				}
			}
			return ival{0, 1}, true
		}
		if r.Bin == mir.BitAnd && p.mut == mutMaskMinusOne {
			if x, ok := binRange(r.Bin, a.r, b.r, a.in); ok {
				return in.store(wi{x.lo, x.hi.sub(wInt(1))}), true
			}
		}
		x, ok := binRange(r.Bin, a.r, b.r, a.in)
		if !ok {
			return ival{}, false
		}
		if r.Bin == mir.Shr && p.mut == mutShrLow {
			x.hi = x.hi.sub(wInt(1))
		}
		switch r.Bin {
		case mir.AddWrap, mir.SubWrap, mir.MulWrap, mir.Shl:
			// Wrapping (and << dropping bits): exact only when it fits.
			if !in.fits(x) {
				return ival{}, false
			}
		}
		if r.Bin == mir.Mul && p.mut == mutMulTrapHalf {
			x.hi = wMin(x.hi, in.max.shr(1))
		}
		// A checked + - * that did not trap, / % past their zero and MIN/-1
		// traps, AddNoWrap, SubNoWrap and MulNoWrap (their producer's
		// proof), the bitwise operators and >>: the exact value, which lies
		// in the type (rule 4).
		return in.store(x), true
	case mir.RvUnary:
		a, ok := p.operand(st, r.A)
		if !ok {
			return ival{}, false
		}
		switch r.Un {
		case mir.Neg:
			return in.store(wi{a.r.hi.neg(), a.r.lo.neg()}), true
		case mir.Not:
			if in.bits == 1 {
				return in.store(wi{wInt(1).sub(a.r.hi), wInt(1).sub(a.r.lo)}), true
			}
		case mir.BitNot:
			if in.signed {
				m1 := wInt(-1)
				return in.store(wi{m1.sub(a.r.hi), m1.sub(a.r.lo)}), true
			}
			return in.store(wi{in.max.sub(a.r.hi), in.max.sub(a.r.lo)}), true
		}
	case mir.RvCast:
		a, ok := p.operand(st, r.A)
		if !ok {
			return ival{}, false
		}
		switch r.Cast {
		case mir.CastLossless, mir.CastChecked:
			// Lossless never changes the value; a Cast that did not trap
			// left it in the target (rule 3).
			return in.store(a.r), true
		case mir.CastTrunc:
			if in.fits(a.r) {
				return in.store(a.r), true
			}
		}
	case mir.RvLen:
		l := p.lenRange(r.Place)
		if p.mut == mutLenShort {
			l = wi{l.lo.sub(wInt(1)), l.hi.sub(wInt(1))}
		}
		return in.store(l), true
	}
	return ival{}, false
}

// lenRange is rule 6: an array's length exactly, else [0, MaxLen].
func (p *rangePass) lenRange(pl mir.Place) wi {
	t := p.tab.Get(p.b.PlaceType(pl))
	switch t.Kind {
	case types.KArray:
		if t.Len.Kind == types.CAValue {
			v := p.cfg.Consts.Get(t.Len.Value)
			n := types.LoadUint(v.Bytes, len(v.Bytes))
			if n <= math.MaxInt64 {
				return one(wInt(int64(n)))
			}
		}
	case types.KSlice, types.KMutSlice:
		return wi{wInt(0), wInt(maxLen(p.tab, t.Elem))}
	case types.KStr:
		return wi{wInt(0), wInt(maxLen(p.tab, types.U8))}
	}
	return wi{wInt(0), wInt(math.MaxInt64)}
}

// maxLen is the most elements a slice of elem can have: no object spans
// more than 2^56 bytes, so 2^56 over
// the element's size for the scalar elements whose size the type spells,
// else int's range, which every slice meets.
func maxLen(tab *types.Table, elem types.TypeID) int64 {
	size := int64(0)
	switch ty := tab.Get(elem); {
	case elem == types.Bool:
		size = 1
	case ty.IsInteger():
		size = 8
		if ty.Kind == types.KInt {
			size = int64(ty.Bits) / 8
		}
	case ty.Kind == types.KFloat:
		size = int64(ty.Bits) / 8
	}
	if size <= 0 {
		return math.MaxInt64
	}
	return (1 << 56) / size
}

// contents finds the content locals (rule 7): a non-addressed local of type
// []T, mut []T or [N]T with T an integer, whose every occurrence is an
// element read xs[i], an element store xs[i] = v, a Len, a scope end, or a
// whole-local definition whose initial contents the rule knows.
func (p *rangePass) contents(addressed []bool) {
	b := p.b
	nl := len(b.Locals)
	p.cidx = make([]int32, nl)
	any := false
	for l := range b.Locals {
		p.cidx[l] = -1
		switch t := p.tab.Get(b.Locals[l].Type); t.Kind {
		case types.KSlice, types.KMutSlice, types.KArray:
			any = any || intOf(p.tab, t.Elem).ok
		}
	}
	if !any {
		return // most bodies: no slice or array of integers
	}
	cand := make([]bool, nl)
	elemOf := make([]types.TypeID, nl)
	ncand := 0
	for l := range b.Locals {
		p.cidx[l] = -1
		t := p.tab.Get(b.Locals[l].Type)
		if addressed[l] || l == int(mir.ReturnLocal) || b.Locals[l].Kind == mir.LocalParam {
			continue
		}
		switch t.Kind {
		case types.KSlice, types.KMutSlice, types.KArray:
			if intOf(p.tab, t.Elem).ok {
				cand[l], elemOf[l] = true, t.Elem
				ncand++
			}
		}
	}
	if ncand == 0 {
		return // a slice parameter only: never a content local
	}
	// defs[l]: the whole-local definitions of l, as (block, stmt); stmt -1
	// is a call destination.
	type site struct {
		blk  mir.BlockID
		stmt int
	}
	defs := make([][]site, nl)
	kill := func(l mir.LocalID) {
		if int(l) < nl {
			cand[l] = false
		}
	}
	// readOp checks an operand: an element read of a candidate is allowed.
	readOp := func(op mir.Operand, elemOK bool) {
		if op.Kind > mir.OpMove {
			return
		}
		pl := op.Place
		if pl.Proj.Len == 0 {
			kill(pl.Local)
			return
		}
		pr := b.Proj(pl)
		if !(elemOK && len(pr) == 1 && pr[0].Kind == mir.ProjIndex) {
			kill(pl.Local)
		}
	}
	for i := range b.Blocks {
		blk := &b.Blocks[i]
		for j := range blk.Stmts {
			s := &blk.Stmts[j]
			switch s.Kind {
			case mir.StAssign:
				pl := s.Place
				if pl.Proj.Len == 0 {
					if cand[pl.Local] { // only a candidate's are read below
						defs[pl.Local] = append(defs[pl.Local], site{mir.BlockID(i), j})
					}
				} else if pr := b.Proj(pl); !(len(pr) == 1 && pr[0].Kind == mir.ProjIndex) {
					kill(pl.Local)
				}
				r := &s.Rvalue
				switch r.Kind {
				case mir.RvLen:
				case mir.RvRef, mir.RvSlice, mir.RvDiscriminant:
					kill(r.Place.Local)
				case mir.RvUse:
					// A whole-local copy or move of a candidate ends it as one:
					// the two locals would share its elements. The destination's
					// own contents are initOf's (the Arena.Slice chain).
					readOp(r.A, true)
				case mir.RvAggregate:
					for _, op := range b.Operands[r.Ops.Start:r.Ops.End()] {
						readOp(op, true)
					}
				default:
					readOp(r.A, true)
					readOp(r.B, true)
				}
			case mir.StAsm:
				for _, op := range b.Asms[s.Asm].Operands {
					switch op.Kind {
					case mir.AsmIn, mir.AsmInOut, mir.AsmInLateOut:
						readOp(op.In, false)
					}
				}
			}
		}
		t := &blk.Term
		switch t.Kind {
		case mir.TermSwitch:
			readOp(t.On, true)
		case mir.TermTailCall:
			// Reads its arguments and defines _0, which is never a
			// content candidate.
			for _, a := range b.Args[t.Args.Start:t.Args.End()] {
				readOp(a.Operand, false)
			}
			if t.Callee.Kind == mir.CallValue {
				readOp(t.Callee.Value, false)
			}
		case mir.TermCall:
			for _, a := range b.Args[t.Args.Start:t.Args.End()] {
				readOp(a.Operand, false)
			}
			if t.Callee.Kind == mir.CallValue {
				readOp(t.Callee.Value, false)
			}
			if t.Dest.Proj.Len == 0 {
				if cand[t.Dest.Local] {
					defs[t.Dest.Local] = append(defs[t.Dest.Local], site{mir.BlockID(i), -1})
				}
			} else {
				kill(t.Dest.Local)
			}
		case mir.TermPanic:
			if t.HasMsg {
				readOp(t.Msg, false)
			}
		}
	}
	// Initial contents from the definitions.
	for l := range b.Locals {
		if !cand[l] {
			continue
		}
		ein := intOf(p.tab, elemOf[l])
		init := wi{}
		ok := len(defs[l]) > 0
		for n, d := range defs[l] {
			r, known := p.initOf(mir.LocalID(l), d.blk, d.stmt)
			if !known {
				r = ein.full()
			}
			if n == 0 {
				init = r
			} else {
				init = wi{wMin(init.lo, r.lo), wMax(init.hi, r.hi)}
			}
		}
		if !ok {
			continue
		}
		c := int32(len(p.locs))
		p.cidx[l] = c
		p.locs = append(p.locs, mir.LocalID(l))
		p.cinfo = append(p.cinfo, ein)
		v := ein.store(init)
		p.cinit = append(p.cinit, v)
		p.cval = append(p.cval, v)
	}
}

// initOf is the initial contents one definition gives: an array repeat or
// literal of immediates, or the zero-filled result of std.core's
// Arena.Slice, assigned directly or moved from the call's
// destination temporary.
func (p *rangePass) initOf(l mir.LocalID, blk mir.BlockID, stmt int) (wi, bool) {
	b := p.b
	ein := intOf(p.tab, p.tab.Get(b.Locals[l].Type).Elem)
	if stmt < 0 {
		return wi{wInt(0), wInt(0)}, p.zeroFilled(&b.Blocks[blk].Term)
	}
	r := &b.Blocks[blk].Stmts[stmt].Rvalue
	switch r.Kind {
	case mir.RvRepeat:
		if r.A.Kind == mir.OpImm {
			return one(ein.immValue(r.A.Bits)), true
		}
	case mir.RvAggregate:
		if r.Agg != mir.AggArray {
			return wi{}, false
		}
		ops := b.Operands[r.Ops.Start:r.Ops.End()]
		if len(ops) == 0 {
			return wi{}, false
		}
		var out wi
		for n, op := range ops {
			if op.Kind != mir.OpImm {
				return wi{}, false
			}
			v := ein.immValue(op.Bits)
			if n == 0 {
				out = one(v)
			} else {
				out = wi{wMin(out.lo, v), wMax(out.hi, v)}
			}
		}
		return out, true
	case mir.RvUse:
		// xs = move t, t the destination of one Arena.Slice call and used
		// nowhere else (a candidate's read of t would have killed it).
		if r.A.Kind != mir.OpMove || r.A.Place.Proj.Len != 0 {
			return wi{}, false
		}
		t := r.A.Place.Local
		if src := p.callDefOf(t); src != nil && p.zeroFilled(src) && p.tab.Get(b.Locals[t].Type).Kind == types.KMutSlice && p.usedOnlyOnce(t) {
			return wi{wInt(0), wInt(0)}, true
		}
	}
	return wi{}, false
}

// callDefOf is the call terminator whose destination is local t, when that
// is t's only definition.
func (p *rangePass) callDefOf(t mir.LocalID) *mir.Terminator {
	var found *mir.Terminator
	for i := range p.b.Blocks {
		blk := &p.b.Blocks[i]
		for j := range blk.Stmts {
			if s := &blk.Stmts[j]; s.Kind == mir.StAssign && s.Place.Local == t {
				return nil
			}
		}
		if tm := &blk.Term; isCall(tm) && tm.Dest.Local == t {
			if found != nil || tm.Dest.Proj.Len != 0 {
				return nil
			}
			found = tm
		}
	}
	return found
}

// usedOnlyOnce reports that t is read by exactly one operand in the body.
func (p *rangePass) usedOnlyOnce(t mir.LocalID) bool {
	n := 0
	see := func(op mir.Operand) {
		if op.Kind <= mir.OpMove && op.Place.Local == t {
			n++
		}
	}
	b := p.b
	for i := range b.Blocks {
		blk := &b.Blocks[i]
		for j := range blk.Stmts {
			s := &blk.Stmts[j]
			if s.Kind != mir.StAssign {
				continue
			}
			r := &s.Rvalue
			see(r.A)
			see(r.B)
			if r.Kind == mir.RvRef || r.Kind == mir.RvSlice || r.Kind == mir.RvLen || r.Kind == mir.RvDiscriminant {
				if r.Place.Local == t {
					n++
				}
			}
			if r.Kind == mir.RvAggregate {
				for _, op := range b.Operands[r.Ops.Start:r.Ops.End()] {
					see(op)
				}
			}
		}
		tm := &blk.Term
		switch tm.Kind {
		case mir.TermSwitch:
			see(tm.On)
		case mir.TermCall, mir.TermTailCall:
			for _, a := range b.Args[tm.Args.Start:tm.Args.End()] {
				see(a.Operand)
			}
		}
	}
	return n == 1
}

// zeroFilled reports a direct call of std.core's Arena.Slice, whose result
// is n zero elements (Arena.Slice zero-fills). Arena.SliceAligned zero-fills
// too and is not matched here, so its result's contents are not known to be
// zero.
func (p *rangePass) zeroFilled(t *mir.Terminator) bool {
	if !isCall(t) || t.Callee.Kind != mir.CallDirect || t.Callee.Fn.Item == types.NoItem {
		return false
	}
	return p.cfg.Items.Get(t.Callee.Fn.Item).Key.String() == "std.core.Arena:Slice"
}

// simdMaxLanes is the greatest lane count std.simd's lane-count clause
// admits (`requires N == 2 || N == 4 || ... || N == 64`, std/simd/vec.mk).
const simdMaxLanes = 64

// callResult is rule 11: the interval of a call's integer result, in its
// destination's type in. It is in's whole range except for a call of one
// of the std intrinsics below, whose result range their declaration
// states. The call must be exactly the intrinsic: TermCall's direct form
// (not a fn value, an interface call or a builtin), not spawned and
// infallible, its item a FuncDecl with Intrinsic (a bodiless function of
// a std module, check's sig.go), named by its key. That is the test codegen
// (call.go: CallDirect, fd.Intrinsic, then intrinsicCall by key) and eval
// (intrinsic.go: fd.Intrinsic, then by key) use to run it, so a user
// function, a method of the same name on another type, an fn value naming
// the intrinsic and any call with a body are all whole-range. Each range:
//   - std.simd.Mask:CountSet, `Mask[T, N].CountSet(self): int`, "the number
//     of lanes set" (vec.mk): [0, N]. Codegen counts the N mask bits
//     (zext(ctpop(iN)), intrinsic_simd.go; count() refuses more than 64
//     lanes) and eval counts the set lanes (vmaskReduce). N is the call's
//     second generic argument (Fn.Args: the Mask type's T, N); a well-typed call
//     has the receiver's N. In a generic body N may be a const parameter or
//     an expression of one, no value: then 64, since the call's lane-count
//     clause holds at every instance (a concrete call evaluates it,
//     false is E0706, and a generic caller must restate it on its own
//     parameters), and N == 64 is its greatest value. A known N past 64
//     cannot pass the clause; it gets the whole range, not 64.
//   - std.raw.Popcount64, Ctz64 and Clz64, `(x: u64): int` ("counts x's one
//     bits", "trailing zero bits; 64 for 0", "leading zero bits; 64 for 0",
//     std/raw/raw.mk): [0, 64]. Codegen calls llvm.ctpop, cttz and ctlz on
//     an i64 with is_zero_poison false (intrinsic.go), eval Go's
//     bits.OnesCount64, TrailingZeros64 and LeadingZeros64, all in [0, 64].
func (p *rangePass) callResult(t *mir.Terminator, in intInfo) wi {
	if t.Kind != mir.TermCall || t.HasErr || t.Spawn != mir.NoLocal || t.Callee.Kind != mir.CallDirect || t.Callee.Fn.Item == types.NoItem {
		return in.full()
	}
	it := p.cfg.Items.Get(t.Callee.Fn.Item)
	if fd, ok := it.Decl.(*types.FuncDecl); !ok || !fd.Intrinsic {
		return in.full()
	}
	var r wi
	switch it.Key.String() {
	case "std.simd.Mask:CountSet":
		if t.Callee.Fn.Args.Len != 2 {
			return in.full()
		}
		n := int64(simdMaxLanes)
		if p.mut == mutCountSetGen {
			n = 32
		}
		if ga := p.b.GenericArgs[t.Callee.Fn.Args.Start+1]; ga.IsConst && ga.Const.Kind == types.CAValue {
			cv := p.cfg.Consts.Get(ga.Const.Value)
			if cv == nil || len(cv.Bytes) != 8 {
				return in.full()
			}
			v := types.LoadUint(cv.Bytes, 8)
			if v > simdMaxLanes {
				return in.full()
			}
			n = int64(v)
		}
		if p.mut == mutCountSetShort {
			n--
		}
		// Obligation: CountSet counts the set lanes of an N-lane mask, so
		// 0 <= result <= N, and N <= 64 at every instance.
		r = wi{wInt(0), wInt(n)}
	case "std.raw.Popcount64", "std.raw.Ctz64", "std.raw.Clz64":
		// Obligation: a count of the bits of a u64, 0 to 64 (64 for 0 in
		// Ctz64 and Clz64).
		r = wi{wInt(0), wInt(64)}
		if p.mut == mutBitsShort {
			r.hi = wInt(63)
		}
	default:
		return in.full()
	}
	if !in.fits(r) {
		return in.full()
	}
	return r
}

// updateContents recomputes every content local's contents from its
// initial contents and every store under the solved states. It reports
// whether some store's value lies outside the contents the run used, which
// needs another dataflow run: the contents grow by the join in round 0 and
// widen to the element type after that, so they stop changing by round 2.
// Contents that already hold every store are a fixpoint and stay as they
// are, so the states just solved are the ones the rewrite reads.
func (p *rangePass) updateContents(round int) bool {
	if len(p.locs) == 0 {
		return false
	}
	next := append([]ival(nil), p.cinit...)
	if p.mut != mutContentStore {
		cur := make([]ival, p.n)
		for _, blk := range p.rpo {
			if !p.reached[blk] {
				continue
			}
			copy(cur, p.state(blk))
			stmts := p.b.Blocks[blk].Stmts
			p.run(blk, cur, func(j int, st []ival) {
				if j >= len(stmts) {
					return
				}
				s := &stmts[j]
				if s.Kind != mir.StAssign || s.Place.Proj.Len != 1 {
					return
				}
				c := p.cidx[s.Place.Local]
				if c < 0 {
					return
				}
				next[c] = joinI(next[c], p.rvalue(st, &s.Rvalue, p.cinfo[c]))
			}, func(mir.BlockID, []ival, []int64) {})
		}
	} else {
		for c := range next {
			next[c] = joinI(next[c], p.cinit[c])
		}
	}
	changed := false
	for c := range next {
		cur := p.cval[c]
		if within(next[c], cur) {
			continue
		}
		changed = true
		if round > 0 {
			p.cval[c] = p.cinfo[c].store(p.cinfo[c].full())
		} else {
			p.cval[c] = joinI(cur, next[c])
		}
	}
	return changed
}

// within reports a ⊆ b; the empty interval lies in every interval.
func within(a, b ival) bool {
	return a.empty() || !b.empty() && a.lo >= b.lo && a.hi <= b.hi
}
