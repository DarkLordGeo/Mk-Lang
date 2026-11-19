// Why: capturing literals, fn values of generic instances, Arena.Fmt's
// short form and simd.Vec values each add borrow rules; the soundness
// generator must draw programs that use them (profiles lang and vec).
//
// What: shapes S44 to S54 with langWeight and helperShapesLang: capRead,
// capResult, capRecv and capMisuseShape (capturing literals), fnValue,
// fmtShort, and vecMake, vecArith, vecStore, vecReduce and vec32.
//
// How: literals capture ints, references, cells, fields, array elements,
// vectors, the entry's k and, by shared reference, a linear sub-arena, with
// nested const forwarding (FoldFwd) and nested literals. S45 draws a
// literal whose result holds a loan on a capture (the restriction
// M-E0609-flow cuts); S47 draws the forms check rejects. Vectors use
// integer lanes only. Every form keeps the generator's invariants: no trap,
// no fuel, the same value in the evaluator and both builds.

package corpus

import (
	"fmt"
	"strconv"
	"strings"
)

// langProfile reports whether profile p draws the capture shapes, and
// vecProfile the vector shapes. oracle and reject take both (they need no
// io and no arena), lang only the first; full, core and tasks keep their
// version-6 draws. m7 and reject7 (GenVersion 8) take both.
func langProfile(p Profile) bool {
	return p == ProfileLang || p == ProfileVec || p == ProfileOracle || p == ProfileReject || p == ProfileViews || p == ProfileReject7 || fieldsProfile(p)
}

func vecProfile(p Profile) bool {
	return p == ProfileVec || p == ProfileOracle || p == ProfileReject || p == ProfileViews || p == ProfileReject7 || fieldsProfile(p)
}

// langWeight is shape id's weight (id at least S44) in profile p, from its
// base weight w. S47's forms are rejected in the types group, which hides
// every flow diagnostic, so only lang and vec draw it (never the
// rejection sample or the oracle).
func langWeight(id int, p Profile, w int) int {
	switch {
	case id == sCapMisuse:
		if p != ProfileLang && p != ProfileVec && p != ProfileViews {
			return 0
		}
	case id >= sVecMake:
		if !vecProfile(p) {
			return 0
		}
	case !langProfile(p):
		return 0
	}
	if needsArena(id) && p == ProfileOracle {
		return 0 // S49: oracle programs have no arena
	}
	return w
}

// helperShapesLang is helperShapes with S44, for the lang profiles: a
// helper's capturing literal captures its parameters.
var helperShapesLang = append(append([]struct{ id, w int }{}, helperShapes...), struct{ id, w int }{sCapRead, 4})

// langShape writes shape id of GenVersion 7 when it is ready.
func (g *sgen) langShape(id int, inRun bool) bool {
	if id >= sArmDiverge {
		return g.viewsShape(id, inRun)
	}
	switch id {
	case sCapRead:
		return g.capRead(inRun)
	case sCapResult:
		return inRun && g.capResult()
	case sCapRecv:
		return inRun && g.capRecv()
	case sCapMisuse:
		return inRun && g.capMisuseShape()
	case sFnValue:
		return inRun && g.fnValue()
	case sFmtShort:
		return inRun && g.fmtShort()
	case sVecMake:
		return inRun && g.vecMake()
	case sVecArith:
		return inRun && g.vecArith()
	case sVecStore:
		return inRun && g.vecStore()
	case sVecReduce:
		return inRun && g.vecReduce()
	case sVec32:
		return inRun && g.vec32()
	}
	return false
}

// capTerm draws an i64 expression a literal reads through its captures:
// an int local, *r of a reference, a cell's content, an Sl's field, an
// array element, a Deep's or D2's or Lx's reference, a vector's lane, and
// in Run the entry's k. It marks what it reads used; "" when nothing can
// be captured. In a helper only the parameters are in the pools.
func (g *sgen) capTerm() string {
	type c struct {
		k  kind
		op int
	}
	var cs []c
	for _, k := range []kind{kInt, kRef, kMut, kCell, kACell, kSl, kArray, kDeep, kD2, kLx, kVec} {
		if g.has(k, false) {
			cs = append(cs, c{k, 0})
		}
	}
	if g.cur == nil {
		cs = append(cs, c{nKinds, 0}) // k
	}
	if len(cs) == 0 {
		return ""
	}
	ch := cs[g.rng.intn(len(cs))]
	if ch.k == nKinds {
		return "k"
	}
	s := g.pick(ch.k, false)
	s.used = true
	switch ch.k {
	case kInt:
		return s.name
	case kRef, kMut:
		return "*" + s.name
	case kCell, kACell:
		return "*" + s.name + ".Load()"
	case kSl:
		return "*" + s.name + ".m"
	case kArray:
		return s.name + "[" + strconv.Itoa(g.rng.intn(4)) + "]"
	case kDeep, kD2:
		return "*" + s.name + ".a"
	case kLx:
		return "*" + s.name + ".src"
	}
	return s.name + ".Lane(" + strconv.Itoa(g.rng.intn(2)) + ")" // kVec
}

// capExpr is 1 to 3 capture terms joined by wrapping operators.
func (g *sgen) capExpr() string {
	t := g.capTerm()
	if t == "" {
		return ""
	}
	for range g.rng.intn(3) {
		t += []string{" +% ", " *% ", " ^ "}[g.rng.intn(3)] + g.capTerm()
	}
	return t
}

// foldSlice is the []i64 argument of Fold: an array of Run, or a slice
// local or parameter; "" when there is none.
func (g *sgen) foldSlice() string {
	xs, _, _ := g.foldSliceOf()
	return xs
}

// foldSliceOf is foldSlice, and the slice local it marked used with its
// mark before (nil for an array or none), so a caller that ends up not
// reading it can restore the mark.
func (g *sgen) foldSliceOf() (string, *sname, bool) {
	if g.cur == nil {
		if z := g.pick(kSlice, false); z != nil && g.chance(1, 3) {
			was := z.used
			z.used = true
			return z.name, z, was
		}
		if xs := g.pick(kArray, false); xs != nil {
			return xs.name + "[..]", nil, false
		}
		return "", nil, false
	}
	if z := g.pick(kSlice, false); z != nil {
		was := z.used
		z.used = true
		return z.name, z, was
	}
	return "", nil, false
}

// capRead is S44: h folds Fold(xs, fn(x: &i64): i64 { .. }) whose literal
// reads captures; one in three through FoldFwd (a const parameter
// forwarded to another); one in six with a nested capturing literal (at
// any nesting); in Run, one in three of the literals with a live sub-arena
// captures it by shared reference and allocates from it (a linear value
// captured, called through &self).
func (g *sgen) capRead(inRun bool) bool {
	xs, z, zWas := g.foldSliceOf()
	if xs == "" {
		return false
	}
	e := g.capExpr()
	if e == "" {
		// Nothing reads the slice: restore its mark, or the ending skips it
		// and it is never used (E0208, as capMisuseShape's seed).
		if z != nil {
			z.used = zWas
		}
		return false
	}
	g.use(itemFold)
	callee := "Fold"
	if g.chance(1, 3) {
		g.use(itemFoldFwd)
		callee = "FoldFwd"
	}
	var arena *sname
	if inRun {
		if b := g.pick(kArena, false); b != nil && g.chance(1, 3) {
			arena = b
		}
	}
	switch {
	case arena != nil:
		in := strings.Repeat("    ", g.ind+1)
		g.linef("h = h *%% 31 +%% %s(%s, fn(x: &i64): i64 {", callee, xs)
		g.b.WriteString(in + "s := " + arena.name + ".Dup(\"ab\") else { return *x }\n")
		g.b.WriteString(in + "return *x +% " + e + " +% i64(s.Len())\n")
		g.line("})")
		g.hUsed = true
	case g.chance(1, 6):
		inner := g.capTerm()
		g.linef("h = h *%% 31 +%% %s(%s, fn(x: &i64): i64 { return *x +%% %s +%% Fold(%s, fn(y: &i64): i64 { return *y *%% %s }) })", callee, xs, e, xs, inner)
		g.hUsed = true
	default:
		g.fold(fmt.Sprintf("%s(%s, fn(x: &i64): i64 { return *x +%% %s })", callee, xs, e))
	}
	return true
}

// capResult is S45: a reference through Pass, whose literal
// returns its parameter (accepted) or, one time in four, &v of a captured
// int or a captured reference: the result then holds a loan on a capture,
// E0609 in the flow group. With that restriction cut (M-E0609-flow) the
// result outlives the statement: a later write of the int is VRemovedTag,
// and a captured local of an inner block read after the block is
// VDeadScope (an outer reference reassigned here).
func (g *sgen) capResult() bool {
	if !g.refable() {
		return false
	}
	arg, _ := g.ref()
	ret := "x"
	if g.chance(1, 4) {
		if v := g.pick(kInt, false); v != nil && (g.chance(1, 2) || !g.has(kRef, false)) {
			v.used = true
			ret = "&" + v.name
		} else if r := g.pick(kRef, false); r != nil {
			r.used = true
			ret = r.name
		}
	}
	g.use(itemPass)
	call := fmt.Sprintf("Pass(%s, fn(x: &i64): &i64 { return %s })", arg, ret)
	if r := g.pick(kRef, true); r != nil && g.chance(1, 2) {
		g.linef("%s = %s", r.name, call)
	} else {
		s := g.declare(kRef, "r")
		g.linef("var %s := %s", s.name, call)
	}
	return true
}

// capRecv is S46: a call with a mut receiver whose capturing literal reads
// names the receiver may hold: Sl.Apply on an Sl (capturing the Sl, or the
// int behind its field: E0504), or Sort on a mut slice of an array
// (capturing the slice, E0501, or its array). The comparator xors both
// sides with one captured value, a strict weak order whatever it is, so
// Sort never sees an inconsistent order.
func (g *sgen) capRecv() bool {
	if !g.has(kSl, false) && !g.has(kMSlice, false) {
		// No receiver yet: an Sl over a fresh int (the shape is otherwise
		// too rare to be accepted in a sample).
		v := g.declare(kInt, "v")
		g.linef("var %s: i64 = %s", v.name, g.lit())
		g.use(itemSl)
		s := g.declare(kSl, "s")
		g.linef("var %s := Sl(m: mut %s)", s.name, v.name)
	}
	if s := g.pick(kSl, false); s != nil && (g.chance(1, 2) || !g.has(kMSlice, false)) {
		e := g.capExpr()
		if e == "" {
			return false
		}
		g.use(itemSlApply)
		g.linef("%s.Apply(fn(x: &i64): i64 { return *x +%% %s })", s.name, e)
		return true
	}
	z := g.pick(kMSlice, false)
	if z == nil {
		return false
	}
	t := g.capTerm()
	z.used = true
	g.linef("%s.Sort(fn(p: &i64, q: &i64): bool { return (*p ^ %s) < (*q ^ %s) })", z.name, t, t)
	return true
}

// capMisuseShape is S47: one capture form check rejects in the types group,
// so the program is rejected by the capture restrictions
// (SoundProgram.CapMisuse): a capturing literal bound to a
// local, a write of a capture, a mut borrow of one, or a sub-arena freed
// (moved) inside a literal.
func (g *sgen) capMisuseShape() bool {
	v := g.pick(kInt, true)
	xs, z, zWas := g.foldSliceOf()
	if v == nil || xs == "" {
		// No int to capture: restore the slice's mark, or the ending skips
		// it and it is never used (E0208; seed 0x77a32bde516f1ab8, the
		// version-10 600 s run).
		if z != nil {
			z.used = zWas
		}
		return false
	}
	v.used = true
	g.use(itemFold)
	switch g.rng.intn(4) {
	case 0:
		// This form reads no slice: a slice local foldSliceOf marked keeps
		// its mark, so the ending still reads it (else E0208, a defect).
		if z != nil {
			z.used = zWas
		}
		f := g.fresh("f")
		if f == "f32" || f == "f64" {
			f = "fv" + f[1:] // f32 and f64 are type names (E0102)
		}
		g.linef("%s := fn(x: &i64): i64 { return *x +%% %s }", f, v.name)
		g.fold(fmt.Sprintf("%s(&%s)", f, v.name))
	case 1:
		g.fold(fmt.Sprintf("Fold(%s, fn(x: &i64): i64 { %s = *x\n%s    return *x })", xs, v.name, strings.Repeat("    ", g.ind)))
	case 2:
		g.use(itemBump)
		g.fold(fmt.Sprintf("Fold(%s, fn(x: &i64): i64 { Bump(mut %s)\n%s    return *x })", xs, v.name, strings.Repeat("    ", g.ind)))
	default:
		b := g.pick(kArena, false)
		if b == nil {
			g.fold(fmt.Sprintf("Fold(%s, fn(x: &i64): i64 { %s += 1\n%s    return *x })", xs, v.name, strings.Repeat("    ", g.ind)))
			break
		}
		g.fold(fmt.Sprintf("Fold(%s, fn(x: &i64): i64 { %s.Free()\n%s    return *x })", xs, b.name, strings.Repeat("    ", g.ind)))
	}
	g.capMisuse = true
	return true
}

// fnValue is S48: Twice[i64](Ident[i64], v), a reference through
// Via[i64](IdR[i64], <ref>) (the result's loans follow IdR's flow through
// the fn value), or ApplyB[i64](Bx[i64].Get, &bx) of a Bx built here.
func (g *sgen) fnValue() bool {
	switch g.rng.intn(3) {
	case 0:
		v := g.pick(kInt, false)
		if v == nil {
			return false
		}
		v.used = true
		g.use(itemTwice)
		g.use(itemIdent)
		g.fold(fmt.Sprintf("Twice[i64](Ident[i64], %s)", v.name))
	case 1:
		if !g.refable() {
			return false
		}
		r, _ := g.ref()
		g.use(itemVia)
		g.use(itemIdR)
		call := fmt.Sprintf("Via[i64](IdR[i64], %s)", r)
		if t := g.pick(kRef, true); t != nil && g.chance(1, 2) {
			g.linef("%s = %s", t.name, call)
		} else {
			s := g.declare(kRef, "r")
			g.linef("var %s := %s", s.name, call)
		}
	default:
		g.use(itemBxGet)
		g.use(itemApplyB)
		b := g.fresh("bx")
		g.linef("var %s := Bx[i64](v: %s)", b, g.lit())
		g.fold(fmt.Sprintf("ApplyB[i64](Bx[i64].Get, &%s)", b))
	}
	return true
}

// fmtShort is S49: s := b.Fmt("..") on a live sub-arena, no record;
// the str holds b's loan until the ending folds its length.
func (g *sgen) fmtShort() bool {
	b := g.pick(kArena, false)
	if b == nil {
		return false
	}
	s := g.fresh("s")
	g.elseReturn(fmt.Sprintf("var %s := %s.Fmt(\"%s\")", s, b.name, []string{"ok", "mk 1.0", "{{x}}"}[g.rng.intn(3)]))
	g.declareNamed(kStr, s).fixed = true
	return true
}

// vecLit is an i64 operand for Splat and FromArray: an int local or a
// literal.
func (g *sgen) vecLit() string {
	if v := g.pick(kInt, false); v != nil && g.chance(1, 2) {
		v.used = true
		return v.name
	}
	return g.lit()
}

// vecMake is S50: a simd.Vec[i64, 2] loaded from an array at 0, 1 or 2 (in
// range by construction), from a two-element slice local at 0, or built
// by Splat or FromArray.
func (g *sgen) vecMake() bool {
	var e string
	switch q := g.rng.intn(4); {
	case q == 0 && g.has(kArray, false):
		xs := g.pick(kArray, false)
		e = fmt.Sprintf("simd.Vec[i64, 2].FromSlice(%s[..], %d)", xs.name, g.rng.intn(3))
	case q == 1 && g.has(kSlice, false):
		z := g.pick(kSlice, false)
		if z.n != 2 {
			return false
		}
		z.used = true
		e = fmt.Sprintf("simd.Vec[i64, 2].FromSlice(%s, 0)", z.name)
	case q == 2:
		e = fmt.Sprintf("simd.Vec[i64, 2].Splat(%s)", g.vecLit())
	default:
		e = fmt.Sprintf("simd.Vec[i64, 2].FromArray([%s, %s])", g.vecLit(), g.vecLit())
	}
	g.simd = true
	s := g.declare(kVec, "vc")
	g.linef("var %s := %s", s.name, e)
	return true
}

// vecArith is S51: vc = a <op> b over the wrapping and bitwise operators,
// Min, Max, With and Reverse.
func (g *sgen) vecArith() bool {
	if !g.has(kVec, false) && !g.vecMake() {
		return false
	}
	t := g.pick(kVec, true)
	if t == nil {
		return false
	}
	a, b := g.pick(kVec, false), g.pick(kVec, false)
	a.used, b.used = true, true
	var e string
	switch q := g.rng.intn(9); {
	case q < 6:
		e = a.name + " " + []string{"+%", "-%", "*%", "&", "|", "^"}[q] + " " + b.name
	case q == 6:
		e = fmt.Sprintf("%s.%s(%s)", a.name, []string{"Min", "Max"}[g.rng.intn(2)], b.name)
	case q == 7:
		e = fmt.Sprintf("%s.With(%d, %s)", a.name, g.rng.intn(2), g.vecLit())
	default:
		e = a.name + ".Reverse()"
	}
	g.linef("%s = %s", t.name, e)
	return true
}

// vecStore is S52: CopyTo through a mut slice of a var array at 0, 1 or 2,
// or into a two-element mut slice local at 0.
func (g *sgen) vecStore() bool {
	if !g.has(kVec, false) && !g.vecMake() {
		return false
	}
	v := g.pick(kVec, false)
	if v == nil {
		return false
	}
	if z := g.pick(kMSlice, false); z != nil && z.n == 2 && g.chance(1, 2) {
		v.used, z.used = true, true
		g.linef("%s.CopyTo(%s, 0)", v.name, z.name)
		return true
	}
	xs := g.pick(kArray, true)
	if xs == nil || g.chance(1, 3) {
		// A fresh array, which holds no loan yet (else the store is mostly
		// rejected for the other loans on the arrays in scope).
		xs = g.declare(kArray, "xs")
		g.linef("var %s: [4]i64 = [%s, %s, %s, %s]", xs.name, g.lit(), g.lit(), g.lit(), g.lit())
	}
	v.used = true
	g.linef("%s.CopyTo(mut %s[..], %d)", v.name, xs.name, g.rng.intn(3))
	return true
}

// vecReduce is S53: h folds a reduction, a lane, or a mask of two vectors
// (CountSet, Select).
func (g *sgen) vecReduce() bool {
	if !g.has(kVec, false) && !g.vecMake() {
		return false
	}
	a := g.pick(kVec, false)
	if a == nil {
		return false
	}
	b := g.pick(kVec, false)
	a.used, b.used = true, true
	cmp := []string{"==", "!=", "<", ">="}[g.rng.intn(4)]
	switch g.rng.intn(6) {
	case 0:
		g.fold(a.name + ".ReduceSumWrap()")
	case 1:
		g.fold(a.name + ".ReduceXor()")
	case 2:
		g.fold(fmt.Sprintf("%s.Lane(%d)", a.name, g.rng.intn(2)))
	case 3:
		g.fold(fmt.Sprintf("i64((%s %s %s).CountSet())", a.name, cmp, b.name))
	case 4:
		g.fold(fmt.Sprintf("i64((%s %s %s).CountSet()) *%% 3", b.name, cmp, a.name))
	default:
		g.fold(fmt.Sprintf("(%s %s %s).Select(%s, %s).ReduceSumWrap()", a.name, cmp, b.name, b.name, a.name))
	}
	return true
}

// vec32 is S54: a simd.Vec[i32, 4] of literals, one wrapping operation with
// a splat, folded through its widening Cast or its wrapping sum.
func (g *sgen) vec32() bool {
	g.simd = true
	w := g.fresh("w")
	g.linef("var %s := simd.Vec[i32, 4].FromArray([%s, %s, %s, %s])", w, g.lit(), g.lit(), g.lit(), g.lit())
	g.linef("%s = %s %s simd.Vec[i32, 4].Splat(%s)", w, w, []string{"+%", "*%", "-%", "^"}[g.rng.intn(4)], g.lit())
	if g.chance(1, 2) {
		g.fold("i64(" + w + ".ReduceSumWrap())")
	} else {
		g.fold(w + ".Cast[i64]().ReduceSumWrap()")
	}
	return true
}
