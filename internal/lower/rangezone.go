// Why: intervals alone cannot prove `i < n` with `n := s.Len()` safe for
// s[i]: that needs a relation between two variables. The zone domain keeps
// such relations.
//
// What: rules 9 and 10 of the range analysis: zoneSet (add, assign, join,
// forget, the incremental closure), the zone's terms (zOperand, zRvalue,
// zSliceLen) and its transfers (zAssign, zCompare, zCallLen).
//
// How: difference bounds between integer locals and slice lengths, the zone
// domain of Miné ("A New Numerical Abstract Domain Based on Difference-Bound
// Matrices", PADO 2001), run beside the intervals in the same dataflow. A
// zone is a matrix d over a few variables, v0 the constant 0, the others
// integer locals and the lengths of slice locals, where d[x][y] bounds x - y
// from above (zInf: no bound), kept closed (d[x][y] is the shortest path x
// to y) by the incremental closure after each new constraint.
//
// It decides compares the intervals cannot, so it proves bounds checks
// (`i < n` with `n := s.Len()` proves s[i], rule 9; `i < j` with s[j]
// checked proves s[i], rule 10). A compare it decides makes the other edge
// infeasible, which narrows the intervals downstream, so it can prove
// arithmetic too: a `+ - *` or a checked cast after such a compare may be
// proved (NoWrap, CastTrunc) on the zone's evidence alone, in the MIR
// borrow sees and eval runs, and a wrong zone fact could drop an overflow
// or cast trap as well as a bounds check
// (pcEdge's rel-arith pins that path). It changes no CFG. A bounds check it
// proves is folded only after borrow accepts the body, as every proved
// bounds check is (FoldProvedBounds).
//
// Its terms are exact: a term (v, c) is an operand whose value is v + c in
// mathematical value, found through the same-block definitions rule 5
// follows (copies, value-preserving casts, + and - of an immediate that did
// not trap or was proved, a length). The one inexact form is the bounds
// check's own Trunc of a signed index to uint, which keeps the value only
// when it is not negative: such a term is flagged, and is used only once
// the zone proves it not negative, or, on the edge where the check passed,
// to learn that it is (a negative index becomes at least 2^63 and
// fails i < len, so a passed check means 0 <= i < len).
//
// A length is a variable per slice local: a []T, mut []T or str value
// never changes its length, so the variable stays valid until the
// local itself is assigned, which forgets it (a local that is borrowed as a
// whole, reborrowed or an asm output has no length variable). Its sources
// are a copy of another slice local, a slice expression `xs[a..b]` (length
// b - a, a an immediate) and Arena.Slice[T](n) (n elements, from
// std.core's contract "A slice of n zero-filled elements", as rule 7 reads
// its zero fill).

package lower

import (
	"math"

	"mk/internal/mir"
	"mk/internal/types"
)

const (
	zInf = math.MaxInt64 // no bound
	zLim = 1 << 61       // |bound| past this is no bound (above) or -zLim (below)

	zoneMaxVars  = 24      // variables, v0 and the lengths included
	zoneMaxCells = 1 << 18 // blocks × variables²
	zoneMaxWiden = 4       // changes of a widening point's zone after widening before it is top
)

// zterm is an operand's value as zone variable v plus c; flag marks the
// uint reinterpretation of a signed v + c (equal to it when not negative).
type zterm struct {
	v    int32
	c    int64
	flag bool
}

// zoneSet is the zone of one body: its variables and per-block states.
type zoneSet struct {
	m      int
	zv     []int32  // tracked index -> zone variable, or -1
	sym    []int32  // local -> zone variable of its length, or -1
	maxLen []int64  // per zone variable: a length's greatest value (0 for others)
	sver   []uint32 // per zone variable: definitions of a length so far
	lenVer []uint32 // per tracked index: sver of the length its RvLen read
	lenOf  []int32  // per tracked index: the length variable its RvLen read, or -1
	in     []int64  // blocks × m × m
	next   []int64  // narrowing's next states
	cnt    []uint8  // per block: changes after widening
	cur    []int64  // run's state
	f      []int64  // a switch's false edge
	e      []int64  // a call's normal edge
}

func (zs *zoneSet) state(blk mir.BlockID) []int64 {
	mm := zs.m * zs.m
	return zs.in[int(blk)*mm : int(blk+1)*mm]
}

func zsum(a, b int64) int64 {
	if a == zInf || b == zInf {
		return zInf
	}
	s := a + b
	if s > zLim {
		return zInf
	}
	if s < -zLim {
		return -zLim
	}
	return s
}

// top is the zone with no constraint.
func (zs *zoneSet) top(d []int64) {
	for i := range d {
		d[i] = zInf
	}
	for i := 0; i < zs.m; i++ {
		d[i*zs.m+i] = 0
	}
}

// forget drops every constraint on x.
func (zs *zoneSet) forget(d []int64, x int32) {
	m := zs.m
	for j := 0; j < m; j++ {
		d[int(x)*m+j], d[j*m+int(x)] = zInf, zInf
	}
	d[int(x)*m+int(x)] = 0
}

// add adds x - y <= c to the closed zone d and closes it (Miné's
// incremental closure, O(m²)); false when d becomes infeasible, and then d
// is unchanged.
func (zs *zoneSet) add(d []int64, x, y int32, c int64) bool {
	if c > zLim {
		return true
	}
	if c < -zLim {
		c = -zLim
	}
	if x == y {
		return c >= 0
	}
	m := zs.m
	if c >= d[int(x)*m+int(y)] {
		return true
	}
	if dyx := d[int(y)*m+int(x)]; dyx != zInf && c+dyx < 0 {
		return false
	}
	// d[i][j] = min(d[i][j], d[i][x] + c + d[y][j]). Neither d[i][x] nor
	// d[y][j] decreases while the loop runs, since c + d[y][x] >= 0.
	yrow := d[int(y)*m : int(y)*m+m]
	for i := 0; i < m; i++ {
		t := zsum(d[i*m+int(x)], c)
		if t == zInf {
			continue
		}
		row := d[i*m : i*m+m]
		for j, dyj := range yrow {
			if v := zsum(t, dyj); v < row[j] {
				row[j] = v
			}
		}
	}
	return true
}

// assign is x := t.v + t.c on the closed zone d (t not flagged): a shift
// when t.v is x, else x is forgotten and tied to t.v. d stays closed.
func (zs *zoneSet) assign(d []int64, x int32, t zterm) {
	m := zs.m
	if t.v == x {
		for j := 0; j < m; j++ {
			if j != int(x) {
				d[int(x)*m+j] = zsum(d[int(x)*m+j], t.c)
				d[j*m+int(x)] = zsum(d[j*m+int(x)], -t.c)
			}
		}
		return
	}
	zs.forget(d, x)
	y := int(t.v)
	for j := 0; j < m; j++ {
		if j != int(x) {
			d[int(x)*m+j] = zsum(t.c, d[y*m+j])
			d[j*m+int(x)] = zsum(d[j*m+y], -t.c)
		}
	}
}

// bound adds lo <= x <= hi, where they fit; infeasible bounds are dropped
// (the state is unreachable, and the zone without them stays sound).
func (zs *zoneSet) bound(d []int64, x int32, r wi) {
	if r.hi.cmp(wInt(zLim)) <= 0 && r.hi.cmp(wInt(-zLim)) >= 0 {
		zs.add(d, x, 0, int64(r.hi.lo))
	}
	if r.lo.cmp(wInt(zLim)) <= 0 && r.lo.cmp(wInt(-zLim)) >= 0 {
		zs.add(d, 0, x, -int64(r.lo.lo))
	}
}

// ub is the zone's upper bound on a - b, zInf when there is none.
func (zs *zoneSet) ub(d []int64, a, b zterm) int64 {
	if a.v == b.v {
		return zsum(a.c, -b.c)
	}
	return zsum(zsum(d[int(a.v)*zs.m+int(b.v)], a.c), -b.c)
}

// nonneg reports whether the zone proves a >= 0.
func (zs *zoneSet) nonneg(d []int64, a zterm) bool {
	return zs.ub(d, zterm{}, zterm{v: a.v, c: a.c}) <= 0
}

// cons adds a - b <= c; false when infeasible.
func (zs *zoneSet) cons(d []int64, a, b zterm, c int64) bool {
	k := zsum(zsum(c, -a.c), b.c)
	if k == zInf {
		return true
	}
	if a.v == b.v {
		return k >= 0
	}
	return zs.add(d, a.v, b.v, k)
}

// join merges src into dst; with widen, a bound that grows is dropped. It
// reports a change.
func (zs *zoneSet) join(dst, src []int64, widen bool) bool {
	changed := false
	for i, v := range src {
		if v <= dst[i] {
			continue
		}
		if widen {
			v = zInf
		}
		dst[i] = v
		changed = true
	}
	return changed
}

// zoneSetup picks the zone's variables (p.zs, or nil when the body has no
// use for one): the integer locals and slice lengths a bounds check reads,
// through same-block definitions, and those a compare or an assignment
// ties to them, at most zoneMaxVars with v0, in the order found.
func (p *rangePass) zoneSetup() {
	b := p.b
	nl := len(b.Locals)
	// A slice local whose length may change behind a reference has none.
	symOK := make([]bool, nl)
	for l := range b.Locals {
		switch p.tab.Get(b.Locals[l].Type).Kind {
		case types.KSlice, types.KMutSlice, types.KStr:
			symOK[l] = true
		}
	}
	for i := range b.Blocks {
		blk := &b.Blocks[i]
		for j := range blk.Stmts {
			s := &blk.Stmts[j]
			if s.Kind == mir.StAssign && s.Rvalue.Kind == mir.RvRef && s.Rvalue.Place.Proj.Len == 0 {
				symOK[s.Rvalue.Place.Local] = false
			}
			if s.Kind == mir.StAsm {
				for _, op := range b.Asms[s.Asm].Operands {
					if l := op.Out.Local; l != mir.NoLocal && int(l) < nl {
						symOK[l] = false
					}
				}
			}
		}
		// An ArgReborrow of a mut []T local passes the callee its own copy
		// of the slice value (mir.ArgReborrow; codegen's fresh copy per
		// call), whose pointer and length the callee cannot write
		// back: the local keeps its length.
	}
	// Static bases: an operand followed back through its block's earlier
	// definitions (no versions: only the choice of variables reads them).
	defAt := make([]int32, nl)
	for l := range defAt {
		defAt[l] = -1
	}
	var cur mir.BlockID
	stamp := make([]mir.BlockID, nl)
	var baseOf func(op mir.Operand, depth int) (mir.LocalID, bool)
	var rvBase func(r *mir.Rvalue, depth int) (mir.LocalID, bool)
	baseOf = func(op mir.Operand, depth int) (mir.LocalID, bool) {
		if op.Kind > mir.OpMove || op.Place.Proj.Len != 0 {
			return 0, false
		}
		l := op.Place.Local
		if depth < 8 && stamp[l] == cur && defAt[l] >= 0 {
			if base, ok := rvBase(&b.Blocks[cur].Stmts[defAt[l]].Rvalue, depth+1); ok {
				return base, true
			}
		}
		return l, true
	}
	rvBase = func(r *mir.Rvalue, depth int) (mir.LocalID, bool) {
		switch r.Kind {
		case mir.RvUse, mir.RvCast:
			return baseOf(r.A, depth)
		case mir.RvBinary:
			switch r.Bin {
			case mir.Add, mir.AddNoWrap, mir.AddWrap, mir.Sub, mir.SubNoWrap, mir.SubWrap:
				if r.B.Kind == mir.OpImm {
					return baseOf(r.A, depth)
				}
				if r.A.Kind == mir.OpImm && r.Bin != mir.Sub && r.Bin != mir.SubNoWrap && r.Bin != mir.SubWrap {
					return baseOf(r.B, depth)
				}
			}
		case mir.RvLen:
			if r.Place.Proj.Len == 0 {
				return r.Place.Local, true
			}
		}
		return 0, false
	}
	isInt := func(l mir.LocalID) bool { k := p.idx[l]; return k >= 0 && p.info[k].bits > 1 }
	var order []mir.LocalID // variables in the order found
	in := make([]bool, nl)
	full := false
	want := func(l mir.LocalID) bool {
		if in[l] || full || !(isInt(l) || symOK[l]) {
			return false
		}
		if len(order)+1 >= zoneMaxVars {
			full = true
			return false
		}
		in[l] = true
		order = append(order, l)
		return true
	}
	// visit runs f on every statement with its block's definitions so far.
	visit := func(f func(blk mir.BlockID, j int, s *mir.Statement), term func(blk mir.BlockID, t *mir.Terminator)) {
		for i := range b.Blocks {
			cur = mir.BlockID(i)
			blk := &b.Blocks[i]
			for j := range blk.Stmts {
				s := &blk.Stmts[j]
				f(cur, j, s)
				if s.Kind == mir.StAssign && s.Place.Proj.Len == 0 {
					defAt[s.Place.Local], stamp[s.Place.Local] = int32(j), cur
				}
			}
			term(cur, &blk.Term)
			for j := range blk.Stmts { // the block's definitions end with it
				if s := &blk.Stmts[j]; s.Kind == mir.StAssign && s.Place.Proj.Len == 0 {
					defAt[s.Place.Local] = -1
				}
			}
		}
	}
	addOp := func(op mir.Operand) bool {
		if l, ok := baseOf(op, 0); ok {
			return want(l)
		}
		return false
	}
	// Seeds: the compares of bounds and slice checks.
	seeds := false
	visit(func(blk mir.BlockID, j int, s *mir.Statement) {}, func(blk mir.BlockID, t *mir.Terminator) {
		if boundsTarget(b, &b.Blocks[blk]) == mir.NoBlock {
			return
		}
		k := t.On.Place.Local
		if stamp[k] != blk || defAt[k] < 0 {
			return
		}
		if r := &b.Blocks[blk].Stmts[defAt[k]].Rvalue; r.Kind == mir.RvBinary && r.Bin.IsCompare() {
			seeds = true
			addOp(r.A)
			addOp(r.B)
		}
	})
	if !seeds || len(order) == 0 {
		return
	}
	// Ties, to a fixpoint: a compare with one side in reaches the other;
	// a variable's definitions reach their sources; a length's definitions
	// reach the slices and counts it comes from.
	for changed := true; changed && !full; {
		changed = false
		visit(func(blk mir.BlockID, j int, s *mir.Statement) {
			if s.Kind != mir.StAssign {
				return
			}
			r := &s.Rvalue
			if r.Kind == mir.RvBinary && r.Bin.IsCompare() {
				la, oka := baseOf(r.A, 0)
				lb, okb := baseOf(r.B, 0)
				if oka && okb && (in[la] || in[lb]) {
					changed = want(la) || changed
					changed = want(lb) || changed
				}
			}
			if s.Place.Proj.Len != 0 || !in[s.Place.Local] {
				return
			}
			switch r.Kind {
			case mir.RvSlice:
				if r.HasHi {
					changed = addOp(r.B) || changed
				} else if r.Place.Proj.Len == 0 {
					changed = want(r.Place.Local) || changed
				}
			default:
				if l, ok := rvBase(r, 0); ok {
					changed = want(l) || changed
				}
			}
		}, func(blk mir.BlockID, t *mir.Terminator) {
			if isCall(t) && t.Dest.Proj.Len == 0 && in[t.Dest.Local] && symOK[t.Dest.Local] && p.zeroFilled(t) {
				if args := b.Args[t.Args.Start:t.Args.End()]; len(args) == 2 {
					changed = addOp(args[1].Operand) || changed
				}
			}
		})
	}
	m := len(order) + 1
	if m < 3 {
		return
	}
	nb := len(b.Blocks)
	for m > 2 && nb*m*m > zoneMaxCells {
		m--
	}
	if m < 3 {
		return
	}
	zs := &zoneSet{m: m}
	zs.zv = make([]int32, p.n)
	for i := range zs.zv {
		zs.zv[i] = -1
	}
	zs.sym = make([]int32, nl)
	for i := range zs.sym {
		zs.sym[i] = -1
	}
	zs.maxLen = make([]int64, m)
	for i, l := range order[:m-1] {
		v := int32(i + 1)
		if k := p.idx[l]; k >= 0 {
			zs.zv[k] = v
			continue
		}
		zs.sym[l] = v
		elem := types.U8
		if t := p.tab.Get(b.Locals[l].Type); t.Kind != types.KStr {
			elem = t.Elem
		}
		zs.maxLen[v] = maxLen(p.tab, elem)
	}
	zs.sver = make([]uint32, m)
	zs.lenVer = make([]uint32, p.n)
	zs.lenOf = make([]int32, p.n)
	for i := range zs.lenOf {
		zs.lenOf[i] = -1
	}
	mm := m * m
	zs.in = make([]int64, nb*mm+3*mm)
	zs.cur, zs.f, zs.e = zs.in[nb*mm:nb*mm+mm], zs.in[nb*mm+mm:nb*mm+2*mm], zs.in[nb*mm+2*mm:]
	zs.in = zs.in[: nb*mm : nb*mm]
	zs.cnt = make([]uint8, nb)
	p.zs = zs
}

// zEntry is the zone on entry: every length in [0, its greatest value],
// every integer variable in its type.
func (p *rangePass) zEntry(d []int64) {
	zs := p.zs
	zs.top(d)
	for v := 1; v < zs.m; v++ {
		if zs.maxLen[v] > 0 {
			zs.bound(d, int32(v), wi{wInt(0), wInt(zs.maxLen[v])})
		}
	}
	for k, v := range zs.zv {
		if v >= 0 {
			zs.bound(d, v, p.info[k].full())
		}
	}
}

// zLen bounds length variable v by its type, after a definition.
func (p *rangePass) zLen(d []int64, v int32) {
	p.zs.bound(d, v, wi{wInt(0), wInt(p.zs.maxLen[v])})
}

// defValid reports that tracked local k's same-block definition still
// reads the values it read (rule 5's test).
func (p *rangePass) defValid(k int32) bool {
	if a := p.defA[k]; a >= 0 && p.ver[a] != p.verA[k] {
		return false
	}
	if c := p.defB[k]; c >= 0 && p.ver[c] != p.verB[k] {
		return false
	}
	return true
}

func zConst(v wide) (zterm, bool) {
	if v.cmp(wInt(zLim)) > 0 || v.cmp(wInt(-zLim)) < 0 {
		return zterm{}, false
	}
	return zterm{c: int64(v.lo)}, true
}

// zOperand is op as a term, under the state st of the statement that
// reads it.
func (p *rangePass) zOperand(st []ival, op mir.Operand, depth int) (zterm, bool) {
	switch op.Kind {
	case mir.OpImm:
		in := intOf(p.tab, p.b.OperandType(op))
		if !in.ok || in.bits == 1 {
			return zterm{}, false
		}
		return zConst(in.immValue(op.Bits))
	case mir.OpCopy, mir.OpMove:
		if op.Place.Proj.Len != 0 {
			return zterm{}, false
		}
		k := p.idx[op.Place.Local]
		if k < 0 || p.info[k].bits == 1 {
			return zterm{}, false
		}
		if v := p.zs.zv[k]; v >= 0 {
			return zterm{v: v}, true
		}
		// A definition that reads k itself read its previous value.
		if depth < 8 && p.def[k] >= 0 && p.defA[k] != k && p.defB[k] != k {
			r := &p.b.Blocks[p.blk].Stmts[p.def[k]].Rvalue
			if r.Kind == mir.RvLen {
				// RvLen reads no tracked local: its length must be the same.
				if v := p.zs.lenOf[k]; v >= 0 && p.zs.sver[v] == p.zs.lenVer[k] {
					return zterm{v: v}, true
				}
			} else if p.defValid(k) {
				if t, ok := p.zRvalue(st, r, p.info[k], depth+1); ok {
					return t, true
				}
			}
		}
		if r := p.info[k].load(st[k]); r.lo.cmp(r.hi) == 0 {
			return zConst(r.lo)
		}
	}
	return zterm{}, false
}

// zRvalue is r, of type in, as a term.
func (p *rangePass) zRvalue(st []ival, r *mir.Rvalue, in intInfo, depth int) (zterm, bool) {
	switch r.Kind {
	case mir.RvUse:
		return p.zOperand(st, r.A, depth)
	case mir.RvCast:
		a, ok := p.operand(st, r.A)
		if !ok {
			return zterm{}, false
		}
		t, tok := p.zOperand(st, r.A, depth)
		if !tok || t.flag {
			return zterm{}, false
		}
		switch r.Cast {
		case mir.CastLossless, mir.CastChecked:
			return t, true // the value went through (rule 3)
		case mir.CastTrunc:
			if in.fits(a.r) {
				return t, true
			}
			// A signed index to uint at equal width (the bounds check's
			// cast): the value when not negative.
			if a.in.signed && !in.signed && a.in.bits == 64 && in.bits == 64 {
				t.flag = true
				return t, true
			}
		}
	case mir.RvBinary:
		sub := false
		switch r.Bin {
		case mir.Add, mir.AddNoWrap:
		case mir.Sub, mir.SubNoWrap:
			sub = true
		case mir.AddWrap, mir.SubWrap:
			// Exact only when it does not wrap.
			a, aok := p.operand(st, r.A)
			bb, bok := p.operand(st, r.B)
			if !aok || !bok {
				return zterm{}, false
			}
			if x, ok := binRange(r.Bin, a.r, bb.r, a.in); !ok || !in.fits(x) {
				return zterm{}, false
			}
			sub = r.Bin == mir.SubWrap
		default:
			return zterm{}, false
		}
		// A checked + or - past this point did not trap, and a NoWrap one
		// was proved: the value is exact.
		ta, aok := p.zOperand(st, r.A, depth)
		tb, bok := p.zOperand(st, r.B, depth)
		if !aok || !bok || ta.flag || tb.flag {
			return zterm{}, false
		}
		if sub {
			tb.c = -tb.c
			if tb.v != 0 {
				return zterm{}, false // -v is no term
			}
		}
		switch {
		case tb.v == 0:
		case ta.v == 0:
			ta, tb = tb, ta
		default:
			return zterm{}, false
		}
		c := zsum(ta.c, tb.c)
		if c == zInf || c == -zLim {
			return zterm{}, false
		}
		return zterm{v: ta.v, c: c}, true
	case mir.RvLen:
		if r.Place.Proj.Len != 0 {
			break
		}
		if v := p.zs.sym[r.Place.Local]; v >= 0 {
			return zterm{v: v}, true
		}
		if t := p.tab.Get(p.b.PlaceType(r.Place)); t.Kind == types.KArray && t.Len.Kind == types.CAValue {
			if l := p.lenRange(r.Place); l.lo.cmp(l.hi) == 0 {
				return zConst(l.lo)
			}
		}
	}
	return zterm{}, false
}

// zSliceLen is the length of slice expression r as a term: b - a, with a
// an immediate, the place's length when b is omitted.
func (p *rangePass) zSliceLen(st []ival, r *mir.Rvalue) (zterm, bool) {
	lo := zterm{}
	if r.HasLo {
		t, ok := p.zOperand(st, r.A, 0)
		if !ok || t.flag || t.v != 0 {
			return zterm{}, false
		}
		lo = t
	}
	var hi zterm
	if r.HasHi {
		t, ok := p.zOperand(st, r.B, 0)
		if !ok || t.flag {
			return zterm{}, false
		}
		hi = t
	} else {
		t, ok := p.zRvalue(st, &mir.Rvalue{Kind: mir.RvLen, Place: r.Place, Type: types.Int}, intOf(p.tab, types.Int), 0)
		if !ok {
			return zterm{}, false
		}
		hi = t
	}
	c := zsum(hi.c, -lo.c)
	if c == zInf || c == -zLim {
		return zterm{}, false
	}
	if p.mut == mutSliceLenLong {
		c++
	}
	return zterm{v: hi.v, c: c}, true
}

// zAssign is the zone's transfer of statement s (an assignment of a whole
// local), its term t computed before the statement ran, now the interval
// the statement gave a tracked local.
func (p *rangePass) zAssign(d []int64, s *mir.Statement, k int32, t zterm, ok bool, now ival) {
	zs := p.zs
	l := s.Place.Local
	if k >= 0 {
		v := zs.zv[k]
		zs.lenOf[k] = -1
		if s.Rvalue.Kind == mir.RvLen && s.Rvalue.Place.Proj.Len == 0 {
			if lv := zs.sym[s.Rvalue.Place.Local]; lv >= 0 {
				zs.lenOf[k], zs.lenVer[k] = lv, zs.sver[lv]
			}
		}
		if v < 0 {
			return
		}
		if ok && !t.flag {
			if s.Rvalue.Kind == mir.RvLen && p.mut == mutLenEqShort {
				t.c--
			}
			zs.assign(d, v, t)
		} else if p.mut != mutVarNoKill {
			zs.forget(d, v)
		}
		zs.bound(d, v, p.info[k].load(now))
		return
	}
	v := zs.sym[l]
	if v < 0 {
		return
	}
	zs.sver[v]++
	if p.mut == mutSymNoKill {
		return
	}
	if ok && !t.flag {
		zs.assign(d, v, t)
	} else {
		zs.forget(d, v)
	}
	p.zLen(d, v)
}

// zTermBefore is, for an assignment of a zone variable or a length, the
// term it assigns, read before the statement runs.
func (p *rangePass) zTermBefore(st []ival, s *mir.Statement, k int32) (zterm, bool) {
	zs := p.zs
	if k >= 0 {
		if zs.zv[k] < 0 {
			return zterm{}, false
		}
		return p.zRvalue(st, &s.Rvalue, p.info[k], 0)
	}
	if zs.sym[s.Place.Local] < 0 {
		return zterm{}, false
	}
	switch r := &s.Rvalue; r.Kind {
	case mir.RvUse:
		if r.A.Kind <= mir.OpMove && r.A.Place.Proj.Len == 0 {
			if v := zs.sym[r.A.Place.Local]; v >= 0 {
				return zterm{v: v}, true
			}
		}
	case mir.RvSlice:
		return p.zSliceLen(st, r)
	}
	return zterm{}, false
}

// zCompare is tracked bool k's same-block defining compare as terms, when
// its operands are unchanged since: the operator, both terms, and whether
// b is at most MaxInt64 (what a flagged a's compare needs).
func (p *rangePass) zCompare(st []ival, k int32) (mir.BinOp, zterm, zterm, bool, bool) {
	if p.def[k] < 0 || !p.defValid(k) {
		return 0, zterm{}, zterm{}, false, false
	}
	r := &p.b.Blocks[p.blk].Stmts[p.def[k]].Rvalue
	if r.Kind != mir.RvBinary || !r.Bin.IsCompare() {
		return 0, zterm{}, zterm{}, false, false
	}
	ta, aok := p.zOperand(st, r.A, 0)
	tb, bok := p.zOperand(st, r.B, 0)
	if !aok || !bok {
		return 0, zterm{}, zterm{}, false, false
	}
	small := false
	if bi, ok := p.operand(st, r.B); ok {
		small = bi.r.hi.cmp(wInt(math.MaxInt64)) <= 0
	}
	return r.Bin, ta, tb, small, true
}

// zUnflag clears a flag the zone proves harmless (the value not negative).
func (zs *zoneSet) zUnflag(d []int64, t zterm) zterm {
	if t.flag && zs.nonneg(d, t) {
		t.flag = false
	}
	return t
}

// zDecide decides a op b in zone d: 1 always true, 0 always false, -1
// unknown.
func (p *rangePass) zDecide(d []int64, op mir.BinOp, a, b zterm) int {
	zs := p.zs
	if p.mut == mutFlagFree {
		a.flag, b.flag = false, false
	}
	a, b = zs.zUnflag(d, a), zs.zUnflag(d, b)
	if a.flag || b.flag {
		return -1
	}
	ab, ba := zs.ub(d, a, b), zs.ub(d, b, a) // a - b <= ab, b - a <= ba
	switch op {
	case mir.Lt:
		return tri(ab <= -1, ba <= 0)
	case mir.Le:
		return tri(ab <= 0, ba <= -1)
	case mir.Gt:
		return tri(ba <= -1, ab <= 0)
	case mir.Ge:
		return tri(ba <= 0, ab <= -1)
	case mir.Eq:
		return tri(ab <= 0 && ba <= 0, ab <= -1 || ba <= -1)
	case mir.Ne:
		return tri(ab <= -1 || ba <= -1, ab <= 0 && ba <= 0)
	}
	return -1
}

// zRefine narrows zone d by "a op b is truth"; false when infeasible.
// small says b is at most MaxInt64, so a flagged a's a < b (or <=) holds
// exactly when 0 <= a < b.
func (p *rangePass) zRefine(d []int64, op mir.BinOp, a, b zterm, small, truth bool) bool {
	zs := p.zs
	if p.mut == mutFlagFree {
		a.flag, b.flag = false, false
	}
	a, b = zs.zUnflag(d, a), zs.zUnflag(d, b)
	if b.flag {
		return true
	}
	if a.flag {
		if !truth || !small || op != mir.Lt && op != mir.Le {
			return true
		}
		if !zs.cons(d, zterm{}, zterm{v: a.v, c: a.c}, 0) { // a >= 0
			return false
		}
		a.flag = false
	}
	if !truth {
		switch op {
		case mir.Lt:
			op = mir.Ge
		case mir.Le:
			op = mir.Gt
		case mir.Gt:
			op = mir.Le
		case mir.Ge:
			op = mir.Lt
		case mir.Eq:
			op = mir.Ne
		case mir.Ne:
			op = mir.Eq
		}
	}
	le := int64(0)
	if p.mut == mutZoneLeAsLt {
		le = -1
	}
	switch op {
	case mir.Lt:
		return zs.cons(d, a, b, -1)
	case mir.Le:
		return zs.cons(d, a, b, le)
	case mir.Gt:
		return zs.cons(d, b, a, -1)
	case mir.Ge:
		return zs.cons(d, b, a, le)
	case mir.Eq:
		return zs.cons(d, a, b, 0) && zs.cons(d, b, a, 0)
	}
	return true
}

// zCmp is rvalueOK's compare when the intervals do not decide it.
func (p *rangePass) zCmp(st []ival, r *mir.Rvalue) int {
	ta, aok := p.zOperand(st, r.A, 0)
	tb, bok := p.zOperand(st, r.B, 0)
	if !aok || !bok {
		return -1
	}
	return p.zDecide(p.zs.cur, r.Bin, ta, tb)
}

// zCallLen is a call's transfer when its destination has a length
// variable v: v is forgotten, and after Arena.Slice[T](n) the zone of the
// normal edge, returned (else nil), has v = n (n elements; n < 0
// panics, so on that edge n is the length).
func (p *rangePass) zCallLen(st []ival, z []int64, t *mir.Terminator) []int64 {
	zs := p.zs
	v := zs.sym[t.Dest.Local]
	var ze []int64
	if p.zeroFilled(t) {
		if args := p.b.Args[t.Args.Start:t.Args.End()]; len(args) == 2 {
			// The count, read before the call writes the destination.
			if n, ok := p.zOperand(st, args[1].Operand, 0); ok && !n.flag {
				if p.mut == mutSliceLenLong {
					n.c = zsum(n.c, 1)
				}
				if n.c != zInf {
					ze = zs.e
					copy(ze, z)
					zs.assign(ze, v, n)
					p.zLen(ze, v)
				}
			}
		}
	}
	zs.sver[v]++
	zs.forget(z, v)
	p.zLen(z, v)
	return ze
}
