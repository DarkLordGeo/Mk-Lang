// Why: const arguments, array lengths and requires clauses need values
// during type checking, and starting the evaluator for `N + 1` would be slow
// and circular.
//
// What: folder and fval, with fold, name, variant, binary, enumBinary, fit,
// minByMinusOne and panicAt; plainEnum, enumFval, variantConst, fvalOf and internFolded on
// the Checker.
//
// How: check folds integer and bool literals, named consts with a value,
// plain enum variants and the operators over them itself, in big.Int
// arithmetic wrapped to the operand type. A run-time panic met while
// folding (an overflow, a division by zero, a shift past the width) is
// E0901 at compile time.

package check

import (
	"math/big"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// fval is a folded value: an integer of type t (NoType for an untyped
// literal), a bool, or, in a folder with enums set, a value of t, an enum
// with no fields in any variant (enum: its interned value; n is nil).
type fval struct {
	isBool bool
	b      bool
	n      *big.Int
	t      types.TypeID
	enum   types.ConstID
}

// folder folds one expression of file fx in module m. A panic while folding
// is reported once through emit at the operator; depth bounds const chains.
type folder struct {
	m     *Module
	fx    *fileIndex
	emit  func(*diag.Diagnostic)
	rec   recorder
	depth int
	// write lets the folder record a same-module const's value in its decl:
	// true only on the module's stage-5 goroutine.
	write bool
	// params and gens bind a predicate's const parameters (RefParam's A)
	// and const generic parameters (RefTypeParam's A) to the call's folded
	// arguments (consts.go foldedRequires); nil elsewhere, where neither
	// folds.
	params, gens map[uint32]fval
	// call folds a call expression (foldedRequires' imported method on a
	// const parameter, forwardreq.go methodCall); nil elsewhere, where a
	// call does not fold.
	call func(*ast.CallExpr) (fval, bool, bool)
	// enums lets the folder read a value of an enum with no fields (a
	// variant `E.A`, a named const of the enum, a bound const argument) and
	// fold == and != over two of them (a same-module requires over an
	// enum const generic, a same-module enum const as a const argument).
	// False elsewhere, where such a value does not fold.
	enums bool

	// fail is why the fold failed, for a caller that must diagnose every
	// failure (computedArg: a const argument that does not fold is never
	// Poison without a diagnostic); the innermost reason wins. quiet marks
	// a failure already reported, or one the const's own check owns.
	// Other callers ignore both.
	fail  *diag.Diagnostic
	quiet bool
	// panicCode is the first panic the fold met (panicAt), PanicNone for
	// none; a same-module clause reports it as E0901 at its site.
	panicCode types.PanicCode
	// want is the type the folded value must have (E0301's expected),
	// param the const parameter it is an argument of (E0705's param).
	want  types.TypeID
	param string
	// constUse resolves a named const whose value constValue cannot fold
	// (computedArg: the same request path as a bare const argument);
	// on false it has reported, or chosen silence.
	constUse func(it *types.Item, at source.Span) (types.ConstID, bool)
	// leaf, when set, decides a node before fold does (litfold.go): handled
	// true means v and ok are e's value as a body typed it, ok false a node
	// that is not a constant there (a quiet failure).
	leaf func(e ast.Expr) (v fval, ok, handled bool)
	// panicNode is the operator of the first panic (panicAt).
	panicNode ast.Node
}

// reject records a failure reason; the first one (the innermost) wins.
func (f *folder) reject(code diag.Code, at source.Span, msg string) *diag.Diagnostic {
	d := diag.New(code, at, msg)
	if f.fail == nil && !f.quiet {
		f.fail = d
	}
	return d
}

// notConst is E0705 at a node check does not fold.
func (f *folder) notConst(at ast.Node) (fval, bool, bool) {
	paramFact(f.reject(diag.E0705, at.Span(), "a const argument must be a compile-time constant"), f.param)
	return fval{}, false, false
}

// noOp is E0308: operator op is not defined on the operand's type.
func (f *folder) noOp(at ast.Node, op ast.TokenKind, v fval) (fval, bool, bool) {
	s := f.typeName(v)
	f.reject(diag.E0308, at.Span(), "`"+opText[op]+"` is not defined on `"+s+"`").Text("operator", opText[op]).Text("type", s)
	return fval{}, false, false
}

// mismatch is E0301 at the right operand: expected the left's type.
func (f *folder) mismatch(at ast.Node, expected, found string) (fval, bool, bool) {
	f.reject(diag.E0301, at.Span(), "expected `"+expected+"`, found `"+found+"`").Text("expected", expected).Text("found", found)
	return fval{}, false, false
}

// typeName spells a folded value's type: bool, its integer type, or the
// type an untyped literal takes (the wanted type, else int).
func (f *folder) typeName(v fval) string {
	tab := f.m.c.cfg.Types
	switch {
	case v.isBool:
		return "bool"
	case v.t != types.NoType:
		return tab.TypeString(v.t, nil)
	case f.want != types.NoType && f.want != types.Poison && tab.Get(f.want).IsInteger():
		return tab.TypeString(f.want, nil)
	}
	return "int"
}

// wantName spells the wanted type (int when none).
func (f *folder) wantName() string {
	if f.want == types.NoType || f.want == types.Poison {
		return "int"
	}
	return f.m.c.cfg.Types.TypeString(f.want, nil)
}

const maxFoldDepth = 64

func (f *folder) ref(n ast.Node) resolve.Ref {
	id := n.ID()
	if int(id) >= len(f.fx.info.Refs) {
		return resolve.Ref{}
	}
	return f.fx.info.Refs[id]
}

// fold returns e's value, or false when e is not a foldable constant (the
// caller then asks the evaluator or leaves it). panicked reports an E0901
// already emitted.
func (f *folder) fold(e ast.Expr) (v fval, ok, panicked bool) {
	if f.leaf != nil {
		if v, ok, handled := f.leaf(e); handled {
			if !ok {
				f.quiet = true
			}
			return v, ok, false
		}
	}
	switch e := e.(type) {
	case *ast.ParenExpr:
		return f.fold(e.X)
	case *ast.BasicLit:
		switch e.Kind {
		case ast.TokInt:
			if u, good := parseIntLit(e.Raw); good {
				return fval{n: new(big.Int).SetUint64(u)}, true, false
			}
			f.reject(diag.E0303, e.Span(), "`"+e.Raw+"` does not fit in `"+f.wantName()+"`").Text("value", e.Raw).Text("type", f.wantName())
			return fval{}, false, false
		case ast.TokFloat:
			return f.mismatch(e, f.wantName(), "f64")
		case ast.TokChar:
			return f.mismatch(e, f.wantName(), "char")
		case ast.TokByte:
			return f.mismatch(e, f.wantName(), "u8")
		case ast.TokStr, ast.TokRawStr, ast.TokMultiStr:
			return f.mismatch(e, f.wantName(), "str")
		case ast.TokByteStr:
			return f.mismatch(e, f.wantName(), "[]u8")
		}
		return f.notConst(e)
	case *ast.Ident, *ast.FieldExpr:
		return f.name(e)
	case *ast.UnaryExpr:
		switch e.Op {
		case ast.TokMinus, ast.TokBang, ast.TokTilde:
		default:
			return f.notConst(e)
		}
		x, ok, p := f.fold(e.X)
		if !ok {
			return x, false, p
		}
		if x.enum != types.NoConst {
			return f.noOp(e, e.Op, x)
		}
		switch e.Op {
		case ast.TokMinus:
			if x.isBool || x.t != types.NoType && !f.m.c.cfg.Types.Get(x.t).Signed {
				return f.noOp(e, e.Op, x) // E0308 on bool and unsigned
			}
			r := fval{n: new(big.Int).Neg(x.n), t: x.t}
			return f.fit(r, e)
		case ast.TokBang:
			if x.isBool {
				return fval{isBool: true, b: !x.b}, true, false
			}
			return f.noOp(e, e.Op, x)
		case ast.TokTilde:
			if x.isBool {
				return f.noOp(e, e.Op, x)
			}
			// ~x is -x-1 in two's complement, wrapped to the width (an
			// untyped operand is int).
			r := fval{n: new(big.Int).Not(x.n), t: x.t}
			r.n = f.wrap(r.n, x.t)
			return r, true, false
		}
	case *ast.BinaryExpr:
		return f.binary(e)
	case *ast.CallExpr:
		if f.call != nil {
			return f.call(e)
		}
	}
	return f.notConst(e)
}

// wrap reduces n modulo 2^bits of integer type t (int for an untyped
// value) into t's range: the wrapping operators and ~.
func (f *folder) wrap(n *big.Int, t types.TypeID) *big.Int {
	bits := f.bits(t)
	signed := true
	if t != types.NoType {
		signed = f.m.c.cfg.Types.Get(t).Signed
	}
	mod := new(big.Int).Lsh(big.NewInt(1), bits)
	r := new(big.Int).Mod(n, mod)
	if signed && r.Cmp(new(big.Int).Rsh(mod, 1)) >= 0 {
		r.Sub(r, mod)
	}
	return r
}

// name folds true, false and a named const with a known or foldable value.
func (f *folder) name(e ast.Expr) (fval, bool, bool) {
	r := f.ref(e)
	switch r.Kind {
	case resolve.RefPoison, resolve.RefNone:
		f.quiet = true // resolve reported it
		return fval{}, false, false
	case resolve.RefUniverse:
		switch source.Symbol(r.A) {
		case source.SymTrue:
			return fval{isBool: true, b: true}, true, false
		case source.SymFalse:
			return fval{isBool: true}, true, false
		}
	case resolve.RefParam:
		if v, ok := f.params[r.A]; ok {
			return v, true, false
		}
	case resolve.RefTypeParam:
		if v, ok := f.gens[r.A]; ok {
			return v, true, false
		}
	case resolve.RefItem:
		it := f.m.c.cfg.Items.Get(types.ItemID(r.A))
		if it.Kind != types.KindConst {
			break
		}
		id, ok := f.m.constValue(it, f)
		if !ok && f.constUse != nil {
			id, ok = f.constUse(it, e.Span())
		}
		if !ok {
			// The const's own check reports a broken initializer; a
			// value not known yet is not reported here either.
			f.quiet = true
			return fval{}, false, false
		}
		v, ok := f.m.c.fvalOf(id)
		if !ok && f.enums {
			v, ok = f.m.c.enumFval(id)
		}
		if !ok {
			return f.mismatch(e, f.wantName(), f.m.c.cfg.Types.TypeString(f.m.c.cfg.Consts.Get(id).Type, nil))
		}
		return v, true, false
	}
	if fe, ok := e.(*ast.FieldExpr); ok && f.enums {
		if v, ok := f.variant(fe); ok {
			return v, true, false
		}
	}
	return f.notConst(e)
}

// variant folds `E.A` (`lib.E.A`), a variant of E, a non-generic enum
// with no fields in any variant, to its value (folder.enums).
func (f *folder) variant(fe *ast.FieldExpr) (fval, bool) {
	c := f.m.c
	r := f.ref(fe.X)
	if r.Kind != resolve.RefItem || fe.Name == nil {
		return fval{}, false
	}
	it := c.cfg.Items.Get(types.ItemID(r.A))
	if it.Kind != types.KindEnum {
		return fval{}, false
	}
	d, ok := c.declOfItem(it).(*types.EnumDecl)
	if !ok || len(d.Generics) > 0 {
		return fval{}, false
	}
	// The name's variant index is the declaration's, which the shape read
	// (variantConst) does not hash: swapping two variants' names keeps it.
	// Belt and braces: TestInvalidation_SameModuleClauseFolded's "enum
	// names swapped" passes without this read as well: another read of the
	// caller covers the swap.
	f.rec.signature(it.Key)
	for i, v := range d.Variants {
		if v.Name != fe.Name.Name {
			continue
		}
		id, ok := c.variantConst(c.cfg.Types.Named(it.ID, nil), i, f.rec)
		if !ok {
			return fval{}, false
		}
		return c.enumFval(id)
	}
	return fval{}, false
}

// plainEnum reports whether t is a non-generic enum with no fields in any
// variant: the only enum whose values the folder compares.
func (c *Checker) plainEnum(t types.TypeID) bool {
	ty := c.cfg.Types.Get(t)
	if ty.Kind != types.KNamed || len(ty.Args) > 0 {
		return false
	}
	it := c.cfg.Items.Get(ty.Item)
	if it.Kind != types.KindEnum {
		return false
	}
	d, ok := c.declOfItem(it).(*types.EnumDecl)
	if !ok || len(d.Generics) > 0 || len(d.Variants) == 0 {
		return false
	}
	for _, v := range d.Variants {
		if len(v.Fields) > 0 {
			return false
		}
	}
	return true
}

// enumFval reads a value of a plainEnum type back as an fval; equal values
// intern once (types.ConstTable), so the ConstID is the value.
func (c *Checker) enumFval(id types.ConstID) (fval, bool) {
	if id == types.NoConst {
		return fval{}, false
	}
	t := c.cfg.Consts.Get(id).Type
	if !c.plainEnum(t) {
		return fval{}, false
	}
	return fval{t: t, enum: id}, true
}

// variantConst interns variant v of plainEnum t in the evaluator's form
// (eval constval.go, exec.go aggregate): the type's size, zero but for the
// variant's discriminant at the tag's offset and width, the type's
// alignment. Its layout is a shape read (rec), as layoutCall's is. False
// for a type on a layout cycle (E0907 at its declaration), and for an enum
// whose module has not finished its step 3 (impls): its layout asks for
// facts that are not final (requireFinal's ICE at a declaration's
// `s: S[MA]`), so such a use stays E0908 (localConstArg).
func (c *Checker) variantConst(t types.TypeID, v int, rec recorder) (types.ConstID, bool) {
	if !c.plainEnum(t) {
		return types.NoConst, false
	}
	if m := c.module(c.cfg.Items.Get(c.cfg.Types.Get(t).Item).Module); m != nil && !m.final {
		return types.NoConst, false
	}
	if c.sizeOnCycle(t) {
		return types.NoConst, false
	}
	c.shapeReads(nil, t, rec)
	lay := c.Layout(t)
	if !lay.HasTag || lay.Tag.Niche || v < 0 || v >= len(lay.Variants) || lay.Tag.Offset+uint64(lay.Tag.Size) > lay.Shape.Size {
		return types.NoConst, false
	}
	b := make([]byte, lay.Shape.Size)
	types.StoreUint(b[lay.Tag.Offset:], int(lay.Tag.Size), lay.Variants[v].Discr)
	return c.cfg.Consts.Intern(types.ConstValue{Type: t, Align: lay.Shape.Align, Bytes: b}), true
}

// enumBinary folds a binary operator with an enum operand (folder.enums):
// == and != over two values of one enum compare them; any other operator
// is E0308, another operand type E0301 at the right.
func (f *folder) enumBinary(e *ast.BinaryExpr, a, b fval) (fval, bool, bool) {
	if a.enum == types.NoConst {
		return f.mismatch(e.Y, f.typeName(a), f.typeName(b))
	}
	if e.Op != ast.TokEqEq && e.Op != ast.TokNotEq {
		return f.noOp(e, e.Op, a)
	}
	if b.enum == types.NoConst || b.t != a.t {
		return f.mismatch(e.Y, f.typeName(a), f.typeName(b))
	}
	return fval{isBool: true, b: (a.enum == b.enum) == (e.Op == ast.TokEqEq)}, true, false
}

// fvalOf reads a scalar const value back as an fval.
func (c *Checker) fvalOf(id types.ConstID) (fval, bool) {
	cv := c.cfg.Consts.Get(id)
	if cv.Type == types.Bool {
		return fval{isBool: true, b: len(cv.Bytes) == 1 && cv.Bytes[0] == 1}, true
	}
	ty := c.cfg.Types.Get(cv.Type)
	if !ty.IsInteger() || len(cv.Bytes) == 0 || len(cv.Bytes) > 8 {
		return fval{}, false
	}
	x := types.LoadUint(cv.Bytes, len(cv.Bytes))
	n := new(big.Int).SetUint64(x)
	if ty.Signed {
		v, _ := c.constInt(id)
		n.SetInt64(v)
	}
	return fval{n: n, t: cv.Type}, true
}

func (f *folder) binary(e *ast.BinaryExpr) (fval, bool, bool) {
	a, ok, p := f.fold(e.X)
	if !ok {
		return a, false, p
	}
	// A left side of && or || that decides the value: a panic on the right
	// is not reached, as at run time and in the evaluator, so it neither
	// stops the fold nor is reported (`N == 0 || 8 / N > 1` at N = 0).
	// The right side must still fold, and is still typed.
	decides := a.isBool && (e.Op == ast.TokAmpAmp && !a.b || e.Op == ast.TokPipePipe && a.b)
	emit, code := f.emit, f.panicCode
	if decides {
		f.emit = nil
	}
	b, ok, p := f.fold(e.Y)
	if decides {
		f.emit, f.panicCode = emit, code
		if p {
			return a, true, false
		}
	}
	if !ok {
		return b, false, p
	}
	if a.enum != types.NoConst || b.enum != types.NoConst {
		return f.enumBinary(e, a, b)
	}
	// The operand rules of the body's typing (expr_ops.go binary): && and
	// || want bool on both sides, shifts an integer on both, every other
	// operator one type on both sides (E0301 at the right) in its domain
	// (E0308).
	switch e.Op {
	case ast.TokAmpAmp, ast.TokPipePipe:
		if !a.isBool {
			return f.noOp(e, e.Op, a)
		}
		if !b.isBool {
			return f.noOp(e, e.Op, b)
		}
	case ast.TokShl, ast.TokShr:
		if a.isBool {
			return f.noOp(e, e.Op, a)
		}
		if b.isBool {
			return f.noOp(e, e.Op, b)
		}
	default:
		if a.isBool != b.isBool {
			return f.mismatch(e.Y, f.typeName(a), f.typeName(b))
		}
	}
	if a.isBool {
		switch e.Op {
		case ast.TokAmpAmp:
			return fval{isBool: true, b: a.b && b.b}, true, false
		case ast.TokPipePipe:
			return fval{isBool: true, b: a.b || b.b}, true, false
		case ast.TokEqEq:
			return fval{isBool: true, b: a.b == b.b}, true, false
		case ast.TokNotEq:
			return fval{isBool: true, b: a.b != b.b}, true, false
		}
		return f.noOp(e, e.Op, a)
	}
	t := a.t
	switch {
	case e.Op == ast.TokShl || e.Op == ast.TokShr:
		// the shift amount is any integer type; the result is the left's
	case t == types.NoType:
		t = b.t
	case b.t != types.NoType && b.t != t:
		return f.mismatch(e.Y, f.typeName(a), f.typeName(b))
	}
	cmp := a.n.Cmp(b.n)
	switch e.Op {
	case ast.TokEqEq:
		return fval{isBool: true, b: cmp == 0}, true, false
	case ast.TokNotEq:
		return fval{isBool: true, b: cmp != 0}, true, false
	case ast.TokLt:
		return fval{isBool: true, b: cmp < 0}, true, false
	case ast.TokLtEq:
		return fval{isBool: true, b: cmp <= 0}, true, false
	case ast.TokGt:
		return fval{isBool: true, b: cmp > 0}, true, false
	case ast.TokGtEq:
		return fval{isBool: true, b: cmp >= 0}, true, false
	}
	r := fval{n: new(big.Int), t: t}
	switch e.Op {
	case ast.TokPlus:
		r.n.Add(a.n, b.n)
	case ast.TokMinus:
		r.n.Sub(a.n, b.n)
	case ast.TokStar:
		r.n.Mul(a.n, b.n)
	case ast.TokSlash, ast.TokPercent:
		if b.n.Sign() == 0 || f.minByMinusOne(a, b, t) {
			// The type's minimum by -1 is the zero divisor's panic, for
			// `%` too, as in the evaluator and the run time.
			f.panicAt(e, types.PanicDivideByZero)
			return fval{}, false, true
		}
		if e.Op == ast.TokSlash {
			r.n.Quo(a.n, b.n)
		} else {
			r.n.Rem(a.n, b.n)
		}
	case ast.TokAmp:
		r.n.And(a.n, b.n)
	case ast.TokPipe:
		r.n.Or(a.n, b.n)
	case ast.TokCaret:
		r.n.Xor(a.n, b.n)
	case ast.TokPlusWrap, ast.TokMinusWrap, ast.TokStarWrap:
		switch e.Op {
		case ast.TokPlusWrap:
			r.n.Add(a.n, b.n)
		case ast.TokMinusWrap:
			r.n.Sub(a.n, b.n)
		default:
			r.n.Mul(a.n, b.n)
		}
		r.n = f.wrap(r.n, t)
		return r, true, false
	case ast.TokShl, ast.TokShr:
		bits := f.bits(a.t)
		if b.n.Sign() < 0 || b.n.Cmp(big.NewInt(int64(bits))) >= 0 {
			f.panicAt(e, types.PanicShiftAmount)
			return fval{}, false, true
		}
		r.t = a.t
		if e.Op == ast.TokShl {
			r.n.Lsh(a.n, uint(b.n.Uint64()))
			if r.t != types.NoType {
				// `<<` drops the bits shifted out, as the evaluator and
				// run time do; it never overflows (`K << 62`).
				r.n = f.wrap(r.n, r.t)
				return r, true, false
			}
		} else {
			r.n.Rsh(a.n, uint(b.n.Uint64()))
		}
	default:
		return f.notConst(e)
	}
	return f.fit(r, e)
}

// minByMinusOne reports a / b or a % b at signed type t with a the type's
// minimum and b -1. An untyped t is the type the value takes (the wanted
// integer type, else int), as the evaluator types it: `% -1` gives 0,
// which fits any type, so it is judged here, not when it meets its type.
func (f *folder) minByMinusOne(a, b fval, t types.TypeID) bool {
	tab := f.m.c.cfg.Types
	if t == types.NoType {
		t = types.Int
		if f.want != types.NoType && f.want != types.Poison && tab.Get(f.want).IsInteger() {
			t = f.want
		}
	}
	if !tab.Get(t).Signed || b.n.Cmp(big.NewInt(-1)) != 0 {
		return false
	}
	return a.n.Cmp(new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), f.bits(t)-1))) == 0
}

// bits is the width of integer type t (int for an untyped literal).
func (f *folder) bits(t types.TypeID) uint {
	if t == types.NoType {
		return 64
	}
	ty := f.m.c.cfg.Types.Get(t)
	if ty.Kind == types.KIntPtr || ty.Bits == 0 {
		return 64
	}
	return uint(ty.Bits)
}

// fit checks that a typed result is in range of its type: an overflow is
// the run-time panic at compile time (E0901 at the operator). An untyped
// result is judged when it meets its type (E0303).
func (f *folder) fit(r fval, at ast.Node) (fval, bool, bool) {
	if r.t == types.NoType {
		return r, true, false
	}
	if !f.m.c.inRange(r.n, r.t) {
		f.panicAt(at, types.PanicOverflow)
		return fval{}, false, true
	}
	return r, true, false
}

// inRange reports whether n fits integer type t.
func (c *Checker) inRange(n *big.Int, t types.TypeID) bool {
	ty := c.cfg.Types.Get(t)
	if !ty.IsInteger() {
		return false
	}
	bits := uint(ty.Bits)
	if ty.Kind == types.KIntPtr || bits == 0 {
		bits = 64
	}
	lo, hi := new(big.Int), new(big.Int).Lsh(big.NewInt(1), bits)
	if ty.Signed {
		lo.Neg(new(big.Int).Lsh(big.NewInt(1), bits-1))
		hi.Lsh(big.NewInt(1), bits-1)
	}
	return n.Cmp(lo) >= 0 && n.Cmp(hi) < 0
}

func (f *folder) panicAt(at ast.Node, code types.PanicCode) {
	if f.panicCode == types.PanicNone {
		f.panicCode, f.panicNode = code, at
	}
	if f.emit != nil {
		f.emit(diag.New(diag.E0901, at.Span(), "compile-time evaluation panicked: "+code.String()).
			Text("panic", code.String()).Fact("trace", diag.ListFact()))
	}
}

// intern turns a folded value into a const of type want (an integer type,
// or bool, or an enum value's type); false when it does not fit (the
// caller's E0303 or silence).
func (c *Checker) internFolded(v fval, want types.TypeID) (types.ConstID, bool) {
	if v.enum != types.NoConst {
		if want != v.t && want != types.NoType {
			return types.NoConst, false
		}
		return v.enum, true
	}
	if v.isBool {
		if want != types.Bool && want != types.NoType {
			return types.NoConst, false
		}
		var x uint64
		if v.b {
			x = 1
		}
		return c.scalarConst(types.Bool, 1, x), true
	}
	if want == types.NoType {
		want = v.t
		if want == types.NoType {
			want = types.Int
		}
	}
	if v.t != types.NoType && v.t != want || !c.inRange(v.n, want) {
		return types.NoConst, false
	}
	ty := c.cfg.Types.Get(want)
	size := int(ty.Bits / 8)
	if ty.Kind == types.KIntPtr || size == 0 {
		size = 8
	}
	var bits uint64
	if v.n.Sign() < 0 {
		bits = uint64(v.n.Int64())
	} else {
		bits = v.n.Uint64()
	}
	return c.scalarConst(want, size, bits), true
}
