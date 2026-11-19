// Why: std.simd's Vec and Mask take operators and lane-typed methods whose
// rules (lane classes, lane counts, CPU features, the C ABI) depend on the
// lane type and the target, so the type checker judges them.
//
// What: the operators (vecBinary, vecShift, vecUnary, vecAssign, maskCond,
// maskOperand, vecNoOperator), Lanes (lanesValue, lanesCall, lanesArg), the
// lane-class and widening rules (simdCallRules, laneMeets, laneTarget),
// shuffleIndices, vectorABI (E1107), the cpu clause and its call rule
// (cpuClause, cpuUse, needsFeature, cpuNameArg) and std.mmio's rules.
//
// How: each operator check accepts is recorded as a call of the private
// intrinsic method of std.simd it denotes (`Use{UseMethod, item, inst}` on
// the operator's node, generic arguments [T, N], plus the amount's type for
// a shift), which lower lowers as a direct call and eval and codegen
// implement by key. The rest are E0308 with the method to call instead.

package check

import (
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// simdModule is the module of the compiler-known SIMD items.
const simdModule = "std.simd"

// vecShape is a Vec[T, N] or Mask[T, N] type: its item, lane type and lane
// count argument.
type vecShape struct {
	item types.ItemID
	mask bool
	lane types.TypeID
	n    types.GenericArg
}

// simdItem reports whether item is std.simd's top-level item name. It reads
// the item's name and module, not KnownItems (see isLaneItem).
func (c *Checker) simdItem(item types.ItemID, name string) bool {
	if item == types.NoItem {
		return false
	}
	it := c.cfg.Items.Get(item)
	if it.Owner != types.NoItem || c.cfg.Interner.Str(it.Name) != name {
		return false
	}
	m := c.cfg.Items.Module(it.Module)
	return m != nil && m.Path == simdModule
}

// vecOf is t's shape when t is a Vec or a Mask with both arguments.
func (u *unit) vecOf(t types.TypeID) (vecShape, bool) {
	ty := u.tab.Get(t)
	if ty.Kind != types.KNamed || len(ty.Args) != 2 {
		return vecShape{}, false
	}
	switch {
	case ty.Item == u.c.known.Vec && ty.Item != types.NoItem || u.c.simdItem(ty.Item, "Vec"):
		return vecShape{item: ty.Item, lane: ty.Args[0].Type, n: ty.Args[1]}, true
	case ty.Item == u.c.known.Mask && ty.Item != types.NoItem || u.c.simdItem(ty.Item, "Mask"):
		return vecShape{item: ty.Item, mask: true, lane: ty.Args[0].Type, n: ty.Args[1]}, true
	}
	return vecShape{}, false
}

// vecIntrinsic records node as a call of the method name of v's type
// (`std.simd.Vec:Add`), with generic arguments [T, N] and extra, and
// returns result. It is Poison when std.simd declares no such method (a
// std older than the operator).
func (u *unit) vecIntrinsic(node ast.Node, v vecShape, name string, extra []types.GenericArg, result types.TypeID) types.TypeID {
	owner := u.c.cfg.Items.Get(v.item)
	mod := u.c.cfg.Items.Module(owner.Module)
	id, ok := u.c.cfg.Items.ByKey(types.MemberKey(mod.Path, u.c.cfg.Interner.Str(owner.Name), name))
	if !ok {
		u.err(diag.E0308, node.Span(), "`"+u.str(u.vecType(v))+"` has no `"+name+"` intrinsic in this std").Text("operator", name).Text("type", u.str(u.vecType(v)))
		return types.Poison
	}
	args := append([]types.GenericArg{types.TypeArg(v.lane), v.n}, extra...)
	if v.n.IsConst && v.n.Const.Kind == types.CAParam {
		// In a body generic over N the operator's lane-count clause
		// is restated, as for a method call (E0706 with the clause as a fix).
		if fd, ok := u.c.declOfItem(u.c.cfg.Items.Get(id)).(*types.FuncDecl); ok && len(fd.Sig.Contracts) > 0 {
			u.restatedRequires(node, nil, nil, &fd.Sig, args)
		}
	}
	u.b.Generic = append(u.b.Generic, args)
	*u.b.use(node.ID()) = Use{Kind: UseMethod, Item: id, Inst: uint32(len(u.b.Generic) - 1)}
	return result
}

// vecType is v's own type.
func (u *unit) vecType(v vecShape) types.TypeID {
	return u.tab.Named(v.item, []types.GenericArg{types.TypeArg(v.lane), v.n})
}

// maskOf is the Mask[T, N] a comparison of Vec[T, N] gives.
func (u *unit) maskOf(v vecShape) types.TypeID {
	mask := u.c.known.Mask
	if mask == types.NoItem {
		owner := u.c.cfg.Items.Get(v.item)
		mod := u.c.cfg.Items.Module(owner.Module)
		mask, _ = u.c.cfg.Items.ByKey(types.ItemKey(mod.Path, "Mask"))
	}
	if mask == types.NoItem {
		return types.Poison
	}
	return u.tab.Named(mask, []types.GenericArg{types.TypeArg(v.lane), v.n})
}

// vecLaneCount is the lane-count rule for an operator on a concrete N:
// a power of two from 2 to 64, else E0706. A generic N is the generic
// body's to restate, as for every intrinsic (vecIntrinsic).
func (u *unit) vecLaneCount(at ast.Node, v vecShape) bool {
	if !v.n.IsConst || v.n.Const.Kind != types.CAValue {
		return true
	}
	n, ok := u.c.constInt(v.n.Const.Value)
	switch {
	case !ok:
		return true
	case n == 2 || n == 4 || n == 8 || n == 16 || n == 32 || n == 64:
		return true
	}
	u.err(diag.E0706, at.Span(), "vector operators need a lane count that is a power of two from 2 to 64").
		Text("requires", "N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64")
	return false
}

var vecBinaryNames = map[ast.TokenKind]string{
	ast.TokPlus: "Add", ast.TokMinus: "Sub", ast.TokStar: "Mul", ast.TokSlash: "Div",
	ast.TokPlusWrap: "AddWrap", ast.TokMinusWrap: "SubWrap", ast.TokStarWrap: "MulWrap",
	ast.TokAmp: "And", ast.TokPipe: "Or", ast.TokCaret: "Xor",
	ast.TokEqEq: "Eq", ast.TokNotEq: "Ne", ast.TokLt: "Lt", ast.TokLtEq: "Le", ast.TokGt: "Gt", ast.TokGtEq: "Ge",
}

// vecBinary is the table for a binary operator whose operands are both
// of type t, a Vec or a Mask.
func (u *unit) vecBinary(e *ast.BinaryExpr, v vecShape) types.TypeID {
	return u.vecBinaryOp(e, e.Op, v)
}

// vecBinaryOp is vecBinary for operator op recorded on node e: a binary
// expression, or a compound assignment's statement (`v +%= w`: compound
// assignments follow their operators).
func (u *unit) vecBinaryOp(e ast.Node, op ast.TokenKind, v vecShape) types.TypeID {
	t := u.vecType(v)
	if !u.vecLaneCount(e, v) {
		return types.Poison
	}
	if v.mask {
		switch op {
		case ast.TokAmp, ast.TokPipe, ast.TokCaret:
			return u.vecIntrinsic(e, v, vecBinaryNames[op], nil, t)
		}
		return u.vecNoOperator(e, op, t, "")
	}
	float := u.isFloat(v.lane)
	switch op {
	case ast.TokPlus, ast.TokMinus, ast.TokStar, ast.TokSlash:
		if float {
			return u.vecIntrinsic(e, v, vecBinaryNames[op], nil, t)
		}
		if op == ast.TokSlash {
			return u.vecNoOperator(e, op, t, "DivTrunc")
		}
		return u.vecNoOperator(e, op, t, opText[op]+"%")
	case ast.TokPercent:
		if float {
			return u.vecNoOperator(e, op, t, "")
		}
		return u.vecNoOperator(e, op, t, "DivTrunc")
	case ast.TokPlusWrap, ast.TokMinusWrap, ast.TokStarWrap, ast.TokAmp, ast.TokPipe, ast.TokCaret:
		if float {
			return u.vecNoOperator(e, op, t, "")
		}
		return u.vecIntrinsic(e, v, vecBinaryNames[op], nil, t)
	case ast.TokEqEq, ast.TokNotEq, ast.TokLt, ast.TokLtEq, ast.TokGt, ast.TokGtEq:
		return u.vecIntrinsic(e, v, vecBinaryNames[op], nil, u.maskOf(v))
	}
	return u.vecNoOperator(e, op, t, "")
}

// vecShift is `v << k` and `v >> k` on integer lanes with a scalar integer
// amount: the scalar rule per operation, arithmetic on signed lanes.
func (u *unit) vecShift(e *ast.BinaryExpr, v vecShape, k types.TypeID) types.TypeID {
	return u.vecShiftOp(e, e.Op, v, k)
}

// vecShiftOp is vecShift for operator op recorded on node e.
func (u *unit) vecShiftOp(e ast.Node, op ast.TokenKind, v vecShape, k types.TypeID) types.TypeID {
	t := u.vecType(v)
	if v.mask || u.isFloat(v.lane) {
		return u.vecNoOperator(e, op, t, "")
	}
	if k == types.Poison {
		return types.Poison
	}
	if !u.isInt(k) {
		return u.noOperator(e.Span(), op, k)
	}
	if !u.vecLaneCount(e, v) {
		return types.Poison
	}
	name := "Shl"
	if op == ast.TokShr || op == ast.TokShrEq { // `>>` and the compound `>>=`
		name = "Shr"
	}
	return u.vecIntrinsic(e, v, name, []types.GenericArg{types.TypeArg(k)}, t)
}

// vecUnary is the prefix operators: `-` on float lanes, `~` on integer
// lanes, `!` on a mask.
func (u *unit) vecUnary(e *ast.UnaryExpr, v vecShape) types.TypeID {
	t := u.vecType(v)
	if !u.vecLaneCount(e, v) {
		return types.Poison
	}
	switch {
	case e.Op == ast.TokBang && v.mask:
		return u.vecIntrinsic(e, v, "Not", nil, t)
	case e.Op == ast.TokMinus && !v.mask && u.isFloat(v.lane):
		return u.vecIntrinsic(e, v, "Neg", nil, t)
	case e.Op == ast.TokMinus && !v.mask:
		return u.vecNoOperator(e, e.Op, t, "Splat(0) -%")
	case e.Op == ast.TokTilde && !v.mask && !u.isFloat(v.lane):
		return u.vecIntrinsic(e, v, "Not", nil, t)
	}
	return u.vecNoOperator(e, e.Op, t, "")
}

// vecNoOperator is E0308 for an operator the lane class lacks, with the
// mapping as the fix or alternatives: `+ - *` on integer
// lanes to the wrapping form (class compiles: it chooses wrapping) with
// AddSat and SubSat as alternatives, `/` and `%` to DivTrunc, unary `-` to
// `Splat(0) -% v`.
func (u *unit) vecNoOperator(n ast.Node, op ast.TokenKind, t types.TypeID, to string) types.TypeID {
	d := u.err(diag.E0308, n.Span(), "`"+opText[op]+"` is not defined on `"+u.str(t)+"`").Text("operator", opText[op]).Text("type", u.str(t))
	switch to {
	case "":
	case "DivTrunc":
		d.Alt("a.DivTrunc(b): division per lane, which panics on a zero lane")
	case "Splat(0) -%":
		d.Alt("Vec[T, N].Splat(0) -% v: negation that wraps")
	default:
		if b, ok := n.(*ast.BinaryExpr); ok {
			gap := diag.Edit{Span: spanBetween(b.X, b.Y), Text: " " + to + " "}
			d.Fix(diag.Fix{Class: diag.Compiles, Edits: []diag.Edit{gap}, Why: "integer lanes have no trapping `" + opText[op] + "`: `" + to + "` wraps per lane"})
		}
		if op == ast.TokPlus {
			d.Alt("a.AddSat(b): saturating addition")
		} else if op == ast.TokMinus {
			d.Alt("a.SubSat(b): saturating subtraction")
		}
	}
	return types.Poison
}

// spanBetween is the text between two expressions: the operator and the
// blanks around it.
func spanBetween(x, y ast.Expr) source.Span {
	a, b := x.Span(), y.Span()
	return source.Span{File: a.File, Start: a.End, End: b.Start}
}

// laneClass is a lane-class restriction: which lanes a std.simd function
// takes.
type laneClass uint8

const (
	laneAny laneClass = iota
	laneInt
	laneInt32
	laneSigned
	laneFloat
)

// simdLaneClass is the lane-class rules as one table, by name: the Vec
// methods and the two library routines Dot and Sum (the implicit-bound
// pattern applied to keys that are not intrinsics).
var simdLaneClass = map[string]laneClass{
	"ReduceSumWrap": laneInt, "ReduceAnd": laneInt, "ReduceOr": laneInt, "ReduceXor": laneInt,
	"AddSat": laneInt, "SubSat": laneInt, "DivTrunc": laneInt, "ShlEach": laneInt, "ShrEach": laneInt,
	"MulWidenLo": laneInt, "MulWidenHi": laneInt, "NarrowSat": laneInt, "Trunc": laneInt,
	"ReduceSumWide": laneInt32, "MulHigh": laneInt32,
	"AbsWrap":   laneSigned,
	"ReduceSum": laneFloat, "Abs": laneFloat, "Fma": laneFloat, "Sqrt": laneFloat, "Floor": laneFloat,
	"Ceil": laneFloat, "RoundEven": laneFloat, "RoundToZero": laneFloat, "Dot": laneFloat, "Sum": laneFloat,
}

// lanesValue is simd.Lanes[T]() for a lane type t: Target.vector_bits / (8 *
// SizeOf[T]()); ok is false on a target with no vector width.
func (c *Checker) lanesValue(t types.TypeID) (int64, bool) {
	vb := int64(c.cfg.Target.VectorBits)
	size := int64(c.Layout(t).Shape.Size)
	if vb == 0 || size == 0 {
		return 0, false
	}
	return vb / (8 * size), true
}

// lanesCall folds a call of simd.Lanes at a closed lane type into
// Body.Consts (lower emits the constant), with the read of
// Target.vector_bits it depends on. Over a type parameter the call stays a
// call, answered per instance by eval and codegen.
func (u *unit) lanesCall(e *ast.CallExpr, item types.ItemID, args []types.GenericArg) {
	if len(args) != 1 || args[0].IsConst || !u.c.simdItem(item, "Lanes") {
		return
	}
	t := args[0].Type
	if t == types.Poison || !u.c.closed(t) || !isLane(u.tab.Get(t)) {
		return
	}
	if n, ok := u.c.lanesValue(t); ok {
		u.x.rec.target("vector_bits")
		u.b.setConst(e.ID(), u.c.intConst(n))
	}
}

// lanesArg is simd.Lanes[T]() as a const argument in a type: the constant
// for a closed lane type. Of a type parameter it is E0703: types.ConstArg
// has a symbolic form only for SizeOf (CASizeOf), so
// `simd.Vec[T, simd.Lanes[T]()]` in a generic body does not fold.
func (x *tctx) lanesArg(e *ast.CallExpr, ix *ast.IndexExpr, want types.TypeID) (types.ConstArg, bool) {
	c := x.m.c
	t := x.typeOf(ix.Args[0], posPlain)
	if t == types.Poison || want == types.Poison {
		return types.ConstArg{}, false
	}
	if !c.closed(t) {
		x.errorf(diag.E0703, e.Span(), "`simd.Lanes["+x.str(t)+"]()` of a type parameter is not a const argument yet: it has no symbolic form").
			Text("param", x.str(t)).Text("operation", "simd.Lanes as a const argument").
			Alt("use a concrete lane type: `simd.Lanes[f32]()`").
			Alt("take the lane count as a const parameter N with the lane-count clause")
		return types.ConstArg{}, false
	}
	if !isLane(x.tab().Get(t)) {
		ts := x.str(t)
		x.errorf(diag.E0701, ix.Args[0].Span(), "`"+ts+"` is not a lane type: `simd.Lanes[T]` takes an integer type, int, uint, f32 or f64").
			Text("param", "T").Text("bound", "simd.Lane").Text("type", ts)
		return types.ConstArg{}, false
	}
	if want != types.Int {
		ws := x.str(want)
		x.errorf(diag.E0301, e.Span(), "expected `"+ws+"`, found `int`").Text("expected", ws).Text("found", "int")
		return types.ConstArg{}, false
	}
	n, ok := c.lanesValue(t)
	if !ok {
		return types.ConstArg{}, false
	}
	x.rec.target("vector_bits")
	return types.ConstArg{Kind: types.CAValue, Value: c.intConst(n)}, true
}

// simdValueRules is simdCallRules for a std.simd intrinsic named as a fn
// value at instance inst (`f := simd.Vec[f32, 4].Fma`): the lane
// classes, the widening rule and E1106 `needs` judge the instance the value
// names, as they judge a call of it, since its wrapper is that call
// (Fma at the baseline would be an fmaf libcall).
func (u *unit) simdValueRules(at ast.Node, item types.ItemID, inst uint32) {
	if inst == 0 || int(inst) >= len(u.b.Generic) {
		return
	}
	u.simdCallRules(at, nil, item, u.b.Generic[inst])
}

// simdCallRules judges a checked call of item at instance args against the
// lane classes (E0701 `lanes`, E0703 on a T bounded only by Lane) and
// the widening rule (E0701 for a U of another class or signedness);
// call is nil for a value (simdValueRules), which judges no const
// argument. Inert for any item outside std.simd.
func (u *unit) simdCallRules(at ast.Node, call *ast.CallExpr, item types.ItemID, args []types.GenericArg) {
	it := u.c.cfg.Items.Get(item)
	if m := u.c.cfg.Items.Module(it.Module); m == nil || m.Path != simdModule || len(args) == 0 || args[0].IsConst {
		return
	}
	name := u.c.cfg.Interner.Str(it.Name)
	if it.Owner != types.NoItem && !u.c.simdItem(it.Owner, "Vec") {
		return // Mask's and CpuFeatures' methods have no lane class
	}
	t := args[0].Type
	if name == "Shuffle" && call != nil {
		u.shuffleIndices(call, args)
	}
	if !u.laneMeets(at, name, simdLaneClass[name], t, "T") {
		return
	}
	switch name {
	case "Fma":
		u.needsFeature(at, name, "fma")
	case "Floor", "Ceil", "RoundEven", "RoundToZero":
		u.needsFeature(at, name, "sse4.1")
	case "Gather":
		if len(args) > 2 && !args[2].IsConst {
			u.laneMeets(at, name+"'s index", laneInt, args[2].Type, "I")
		}
	case "WidenLo", "WidenHi", "MulWidenLo", "MulWidenHi", "Trunc":
		if len(args) > 2 && !args[2].IsConst {
			u.laneTarget(at, name, t, args[2].Type)
		}
	case "NarrowSat":
		if len(args) > 2 && !args[2].IsConst {
			u.laneMeets(at, name+"'s result", laneInt, args[2].Type, "U")
		}
	}
}

// laneMeets reports whether lane type t is of class k, else reports E0701
// (a concrete type) or E0703 (a parameter bounded only by Lane, with the
// alternatives the language names).
func (u *unit) laneMeets(at ast.Node, name string, k laneClass, t types.TypeID, param string) bool {
	if k == laneAny || t == types.Poison {
		return true
	}
	ty := u.tab.Get(t)
	if ty.Kind == types.KParam {
		if int(ty.Index) < len(u.env.narrow) {
			if n := u.env.narrow[ty.Index]; n != types.NoType {
				return u.laneMeets(at, name, k, n, param)
			}
		}
		u.err(diag.E0703, at.Span(), "`"+name+"` takes only "+laneWords[k]+" lanes, and `"+u.str(t)+"` is bounded only by `simd.Lane`").
			Text("lanes", laneFacts[k]).Text("type", u.str(t)).
			Alt("use a concrete lane type").Alt("narrow with `if " + u.str(t) + " is f32` (or the lane type it needs)")
		return false
	}
	ok := false
	switch k {
	case laneInt:
		ok = ty.IsInteger()
	case laneInt32:
		ok = ty.IsInteger() && u.c.Layout(t).Shape.Size <= 4
	case laneSigned:
		ok = ty.IsInteger() && ty.Signed
	case laneFloat:
		ok = ty.Kind == types.KFloat
	}
	if !ok {
		u.err(diag.E0701, at.Span(), "`"+name+"` takes only "+laneWords[k]+" lanes, not `"+u.str(t)+"`").
			Text("param", param).Text("bound", "simd.Lane ("+laneFacts[k]+")").Text("type", u.str(t)).Text("lanes", laneFacts[k])
	}
	return ok
}

var laneWords = map[laneClass]string{laneInt: "integer", laneInt32: "integer (at most 32-bit)", laneSigned: "signed integer", laneFloat: "float"}
var laneFacts = map[laneClass]string{laneInt: "integer", laneInt32: "integer-32", laneSigned: "signed", laneFloat: "float"}

// laneTarget is the widening rule: a widening result U has T's class and signedness
// (f32 to f64 for floats); Trunc's U is an integer of at most T's width.
func (u *unit) laneTarget(at ast.Node, name string, t, target types.TypeID) {
	tt, ut := u.tab.Get(t), u.tab.Get(target)
	if tt.Kind == types.KParam || ut.Kind == types.KParam || t == types.Poison || target == types.Poison {
		return
	}
	ok := false
	if name == "Trunc" {
		ok = ut.IsInteger() && tt.IsInteger() && u.c.Layout(target).Shape.Size <= u.c.Layout(t).Shape.Size
	} else {
		ok = tt.IsInteger() && ut.IsInteger() && tt.Signed == ut.Signed || t == types.F32 && target == types.F64
	}
	if !ok {
		why := "the result keeps the lane class and signedness"
		if name == "Trunc" {
			why = "the result is an integer lane no wider than the source's"
		}
		u.err(diag.E0701, at.Span(), "`"+name+"` cannot make `"+u.str(target)+"` lanes from `"+u.str(t)+"` lanes: "+why).
			Text("param", "U").Text("bound", "simd.Lane").Text("type", u.str(target))
	}
}

// shuffleIndices judges Shuffle's const idx when it is an array literal
// each index names a lane of the concatenation of self and b, 0 to
// 2N - 1, else E0706 naming the index. A literal of
// constant elements is recorded as one [N]int const, so lower passes it as
// an OpConst and mono and codegen read the indices from it. A
// const parameter passed on is judged at each instance.
func (u *unit) shuffleIndices(call *ast.CallExpr, args []types.GenericArg) {
	if call == nil || len(call.Args) == 0 || len(args) < 2 || !args[1].IsConst || args[1].Const.Kind != types.CAValue {
		return
	}
	lit, ok := unparenExpr(call.Args[len(call.Args)-1].Value).(*ast.ArrayLit)
	if !ok {
		return
	}
	nv := u.c.cfg.Consts.Get(args[1].Const.Value)
	n := int64(types.LoadUint(nv.Bytes, len(nv.Bytes)))
	if int64(len(lit.Elems)) != n {
		return // the array's type is judged by the argument's check
	}
	b := make([]byte, 8*len(lit.Elems))
	for j, el := range lit.Elems {
		c := u.nodeConst(el.ID())
		if c == types.NoConst {
			return
		}
		cv := u.c.cfg.Consts.Get(c)
		k := int64(types.LoadUint(cv.Bytes, len(cv.Bytes)))
		if len(cv.Bytes) < 8 {
			k = int64(signExtendBytes(types.LoadUint(cv.Bytes, len(cv.Bytes)), len(cv.Bytes)))
		}
		if k < 0 || k >= 2*n {
			u.err(diag.E0706, el.Span(), "a Shuffle index names a lane of self (0 to N-1) or of b (N to 2N-1)").
				Int("index", k).Int("lanes", 2*n)
			return
		}
		types.StoreUint(b[8*j:], 8, uint64(k))
	}
	at8 := u.tab.Array(args[1].Const, types.Int)
	u.b.setConst(lit.ID(), u.c.cfg.Consts.Intern(types.ConstValue{Type: at8, Align: 8, Bytes: b}))
}

// nodeConst is the compile-time value check recorded for node id.
func (u *unit) nodeConst(id ast.NodeID) types.ConstID {
	if u.b.Consts == nil {
		return types.NoConst
	}
	if i := u.b.slot(id); i < len(u.b.Consts) {
		return u.b.Consts[i]
	}
	return types.NoConst
}

func signExtendBytes(x uint64, n int) uint64 {
	s := 64 - 8*uint(n)
	return uint64(int64(x<<s) >> s)
}

func unparenExpr(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// vectorABI reports E1107 for each parameter and the result of an extern or
// export extern signature that is or holds by value a std.simd Vec or Mask
// a vector's place in the C ABI
// depends on the callee's features, so Mk passes none across C.
func (c *Checker) vectorABI(x *tctx, sig *types.Signature, s *ast.Signature) {
	for i, p := range sig.Params {
		if c.holdsVector(p.Type, 0) {
			at := p.Span
			if i < len(s.Params) && s.Params[i] != nil {
				at = s.Params[i].Span()
			}
			x.errorf(diag.E1107, at, "a vector crosses no C signature: its place there depends on the callee's CPU features").
				Text("param", c.cfg.Interner.Str(p.Name)).Text("type", c.cfg.Types.TypeString(p.Type, nil)).
				Alt("pass raw.Of(&v)").Alt("pass the lanes as an array")
		}
	}
	if s.Result != nil && c.holdsVector(sig.Result, 0) {
		x.errorf(diag.E1107, s.Result.Span(), "a vector crosses no C signature: its place there depends on the callee's CPU features").
			Text("param", "result").Text("type", c.cfg.Types.TypeString(sig.Result, nil)).
			Alt("return the lanes as an array")
	}
}

// holdsVector reports whether t is or holds by value a Vec or Mask: the
// type itself, an array, record or optional of one, or a struct whose
// declared fields hold one (through no pointer, slice or reference).
func (c *Checker) holdsVector(t types.TypeID, depth int) bool {
	if depth > 16 || t == types.NoType || t == types.Poison {
		return false
	}
	ty := c.cfg.Types.Get(t)
	switch ty.Kind {
	case types.KArray, types.KOptional:
		return c.holdsVector(ty.Elem, depth+1)
	case types.KRecord:
		for _, f := range ty.Fields {
			if c.holdsVector(f.Type, depth+1) {
				return true
			}
		}
	case types.KNamed:
		if c.simdItem(ty.Item, "Vec") || c.simdItem(ty.Item, "Mask") {
			return true
		}
		if d, ok := c.cfg.Items.Get(ty.Item).Decl.(*types.StructDecl); ok && len(ty.Args) == 0 {
			for _, f := range d.Fields {
				if c.holdsVector(f.Type, depth+1) {
					return true
				}
			}
		}
	}
	return false
}

// cpuClause reads a signature's cpu clause:
// each comma-separated name must be in the feature table for some
// architecture (E1106 `feature` at the clause); Signature.CPU is the sorted
// closure over the build target's architecture. A clause on a bodiless
// extern is E1106 `no-body`, and on a method of an impl E1106 `impl` (a
// call through an interface bound names no function, so it could never
// meet the unsafe-call rule).
func (m *Module) cpuClause(it *types.Item, x *tctx, sig *types.Signature, s *ast.Signature, bodilessExtern bool) {
	c := m.c
	at := s.Cpu.Span()
	switch {
	case bodilessExtern:
		x.errorf(diag.E1106, at, "a cpu clause needs a body to compile with those features").Text("reason", "no-body")
		return
	case it.Owner != types.NoItem && c.cfg.Items.Get(it.Owner).Kind == types.KindImpl:
		x.errorf(diag.E1106, at, "a method of an impl takes no cpu clause: a call through an interface bound names no function").Text("reason", "impl")
		return
	}
	raw, ok := litString(s.Cpu.Kind, s.Cpu.Raw)
	if !ok {
		return
	}
	names := cpuNames(string(raw))
	set, bad := types.CPUClosure(c.cfg.Target.Arch, names)
	if bad != "" || len(names) == 0 {
		x.errorf(diag.E1106, at, "a cpu clause names features outside the reference's CPU feature table").Text("feature", bad).Text("reason", "feature")
		return
	}
	sig.CPU = set
}

// cpuNames splits a cpu clause's comma list, spaces allowed after commas.
func cpuNames(list string) []string {
	var out []string
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// cpuMissing is the names of g's feature set the context of the function
// being checked lacks: the target's baseline united with its own cpu clause
// (never --cpu's).
func (u *unit) cpuMissing(g *types.Signature) []string {
	if len(g.CPU) == 0 {
		return nil
	}
	have := map[string]bool{}
	for _, n := range types.CPUBaseline(u.c.cfg.Target) {
		have[n] = true
	}
	if u.sig != nil {
		for _, n := range u.sig.CPU {
			have[n] = true
		}
	}
	var miss []string
	for _, n := range g.CPU {
		if !have[n] {
			miss = append(miss, n)
		}
	}
	return miss
}

// cpuUse is the unsafe-call rule: a call of a function whose
// cpu set the context lacks, or naming one as a value, is E1101 outside
// unsafe, with the missing features.
func (u *unit) cpuUse(at ast.Node, g *types.Signature, value bool) {
	if u.unsafeDepth > 0 {
		return
	}
	miss := u.cpuMissing(g)
	if len(miss) == 0 {
		return
	}
	op, what := "cpu-call", "a call of"
	if value {
		op, what = "cpu-value", "naming"
	}
	u.err(diag.E1101, at.Span(), what+" a function compiled for features this function's context lacks needs an `unsafe \"reason\" { }` block saying which Cpu() test guards it").
		Text("operation", op).Text("features", strings.Join(miss, ","))
}

// needsFeature is E1106 `needs`:
// on x86_64 an intrinsic LLVM lowers to an instruction only with a feature
// (Fma: fma; the rounding methods: sse4.1) compiles only in a function whose
// context (the target's baseline and its cpu clause) holds it, since Mk
// links no fma or floor to fall back on.
func (u *unit) needsFeature(at ast.Node, name, feature string) {
	if u.c.cfg.Target.Arch != types.ArchX64 {
		return
	}
	if len(u.cpuMissing(&types.Signature{CPU: []string{feature}})) == 0 {
		return
	}
	u.err(diag.E1106, at.Span(), "`"+name+"` compiles only in a function whose cpu features hold "+feature).
		Text("needs", feature).Text("reason", "needs")
}

// cpuNameArg is E1106 for CpuFeatures.Of's or Has's const name written as a
// string literal that names no feature of the table; inert for any
// other callee.
func (u *unit) cpuNameArg(call *ast.CallExpr, item types.ItemID) {
	if call == nil || len(call.Args) == 0 || item == types.NoItem {
		return
	}
	it := u.c.cfg.Items.Get(item)
	name := u.c.cfg.Interner.Str(it.Name)
	if it.Owner == types.NoItem || (name != "Of" && name != "Has") || !u.c.simdItem(it.Owner, "CpuFeatures") {
		return
	}
	lit, ok := unparenExpr(call.Args[len(call.Args)-1].Value).(*ast.BasicLit)
	if !ok {
		return
	}
	raw, ok := litString(lit.Kind, lit.Raw)
	if !ok {
		return
	}
	names := cpuNames(string(raw))
	if name == "Has" && len(names) != 1 {
		u.err(diag.E1106, lit.Span(), "Has takes one feature name").Text("reason", "feature")
		return
	}
	if _, bad := types.CPUClosure(u.c.cfg.Target.Arch, names); bad != "" || len(names) == 0 {
		u.err(diag.E1106, lit.Span(), "a feature name of the reference's CPU feature table").Text("feature", bad).Text("reason", "feature")
	}
}

// mmioItem reports whether it is a function of std.mmio (its free functions
// and Reg's methods): every call needs unsafe.
func (u *unit) mmioItem(it types.ItemID) bool {
	item := u.c.cfg.Items.Get(it)
	mod := u.c.cfg.Items.Module(item.Module)
	return mod != nil && mod.Std && mod.Path == "std.mmio" && item.Kind == types.KindFunc
}

// mmioLane is std.mmio's implicit bound: T is an integer type, int,
// uint or a raw pointer, so each access is one instruction of exactly T's
// width; any other T is E0701 at the call.
func (u *unit) mmioLane(at ast.Node, item types.ItemID, args []types.GenericArg) {
	if !u.mmioItem(item) || len(args) == 0 || args[0].IsConst {
		return
	}
	t := args[0].Type
	switch u.tab.Kind(t) {
	case types.KInt, types.KIntPtr, types.KRawPtr, types.KPoison, types.KParam:
		return
	}
	u.err(diag.E0701, at.Span(), "a volatile access is one instruction of an integer or raw pointer type, not `"+u.str(t)+"`").
		Text("bound", "integer or raw pointer").Text("type", u.str(t))
}

// vecCompound maps a compound assignment's operator to its binary operator.
var vecCompound = map[ast.TokenKind]ast.TokenKind{
	ast.TokPlusEq: ast.TokPlus, ast.TokMinusEq: ast.TokMinus, ast.TokStarEq: ast.TokStar, ast.TokSlashEq: ast.TokSlash,
	ast.TokPercentEq: ast.TokPercent, ast.TokPlusWrapEq: ast.TokPlusWrap, ast.TokMinusWrapEq: ast.TokMinusWrap,
	ast.TokStarWrapEq: ast.TokStarWrap, ast.TokAmpEq: ast.TokAmp, ast.TokPipeEq: ast.TokPipe, ast.TokCaretEq: ast.TokCaret,
	ast.TokShlEq: ast.TokShl, ast.TokShrEq: ast.TokShr,
}

// vecAssign is `p op= e` on a Vec or Mask place: the operator's
// table, recorded on the statement as the intrinsic call `p op e` would be,
// so lower writes `p = Vec:Op[T, N](p, e)`. A shift's amount is any
// integer type; every other operand has the place's type.
func (u *unit) vecAssign(s *ast.AssignStmt, v vecShape) {
	op, ok := vecCompound[s.Op]
	if !ok {
		u.expr(s.Value, types.NoType)
		u.vecNoOperator(s, s.Op, u.vecType(v), "")
		return
	}
	if op == ast.TokShl || op == ast.TokShr {
		k := u.operand(s.Value, types.NoType)
		u.vecShiftOp(s, s.Op, v, k)
		return
	}
	u.expr(s.Value, u.vecType(v))
	if !v.mask && !u.isFloat(v.lane) {
		// Integer lanes have no trapping `+= -= *=` and no `/= %=`: E0308
		// names the compound operator, as for its binary form.
		switch op {
		case ast.TokPlus, ast.TokMinus, ast.TokStar:
			t := u.vecType(v)
			u.err(diag.E0308, s.Span(), "`"+opText[s.Op]+"` is not defined on `"+u.str(t)+"`").Text("operator", opText[s.Op]).Text("type", u.str(t)).
				Alt("`" + opText[op] + "%=`: the wrapping form, per lane")
			return
		case ast.TokSlash, ast.TokPercent:
			u.vecNoOperator(s, s.Op, u.vecType(v), "DivTrunc")
			return
		}
	}
	u.vecBinaryOp(s, op, v)
}

// maskCond gives the E0301 a Mask condition drew (an if or for condition,
// an operand of && or ||) its alternatives: a mask is no
// bool; .All() and .Any() make one. before is the body's diagnostic count
// before the condition was checked.
func (u *unit) maskCond(c ast.Expr, before int) {
	i := int(c.ID()) - int(u.b.Base)
	if i < 0 || i >= len(u.b.Types) {
		return
	}
	if v, ok := u.vecOf(u.b.Types[i]); !ok || !v.mask {
		return
	}
	for _, d := range u.b.Diags[before:] {
		if d.Code == diag.E0301 {
			d.Alt("`.All()`: every lane is set").Alt("`.Any()`: some lane is set")
			return
		}
	}
}

// maskOperand is E0301 for a Mask operand of && or ||: reported at
// the operand with the alternatives; it reports whether t was a mask.
func (u *unit) maskOperand(x ast.Expr, t types.TypeID) bool {
	v, ok := u.vecOf(t)
	if !ok || !v.mask {
		return false
	}
	u.err(diag.E0301, x.Span(), "expected `bool`, found `"+u.str(t)+"`: a mask is no bool").
		Text("expected", "bool").Text("found", u.str(t)).
		Alt("`.All()`: every lane is set").Alt("`.Any()`: some lane is set")
	return true
}
