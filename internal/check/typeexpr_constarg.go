// Why: a type may take const arguments (`[N]T`, `G[Mode.A]`, `G[SizeOf[T]()]`),
// and each must be folded to a value, or kept as a parameter, and judged
// against the const generic's type before the type is interned.
//
// What: constArg and judgeConstArg, with the special forms: variantArg,
// computedArg, callArg, boolLitArg, sizeNow, layoutFinal and negativeLen.
//
// How: a literal folds here, a const parameter is CAParam, SizeOf[T]() is
// CASizeOf, a named const is its value, and anything else inside a generic
// is E0709. An integer is converted to the generic's type (E0303 when it
// does not fit); a size is read only once the layout it needs is final. Kept
// apart from typeexpr.go to hold both under the 1,200-line limit.

package check

import (
	"math/big"
	"strconv"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// constArg reads a const argument of type want in a type: a
// literal folds here, a const parameter is CAParam, SizeOf[T]() is
// CASizeOf, a named const is its value. Inside a generic item anything else
// is E0709. ok is false for Poison.
func (x *tctx) constArg(e ast.Node, want types.TypeID) (types.ConstArg, bool) {
	c := x.m.c
	switch e := e.(type) {
	case nil:
		return types.ConstArg{}, false
	case *ast.ParenExpr:
		return x.constArg(e.X, want)
	case *ast.RecordLit:
		// `G[(true)]`, `G[(K)]`: a name in parentheses parses as a record
		// of one bare field; as a const argument it is the name.
		if len(e.Fields) == 1 && e.Fields[0].Value == nil && e.Fields[0].Name != nil {
			return x.constArg(e.Fields[0].Name, want)
		}
	case *ast.BasicLit:
		if e.Kind == ast.TokInt {
			return x.intLitArg(e, false, want)
		}
	case *ast.UnaryExpr:
		if lit, ok := e.X.(*ast.BasicLit); ok && e.Op == ast.TokMinus && lit.Kind == ast.TokInt {
			return x.intLitArg(lit, true, want)
		}
	case *ast.Ident, *ast.FieldExpr, *ast.PathType:
		r := x.ref(e)
		switch r.Kind {
		case resolve.RefPoison:
			return types.ConstArg{}, false
		case resolve.RefNone:
			// A field of an expression that is no name (`S[0].a`, `F().a`)
			// has no ref, but is no more constant than `P.a`: E0705 below.
			// A name that did not resolve was reported by resolve.
			if fe, isField := e.(*ast.FieldExpr); !isField || x.unresolvedRoot(fe) {
				return types.ConstArg{}, false
			}
		case resolve.RefUniverse:
			if s := source.Symbol(r.A); s == source.SymTrue || s == source.SymFalse {
				return x.boolLitArg(e, s == source.SymTrue, want) // a literal (5e)
			}
		case resolve.RefTypeParam:
			if int(r.A) < len(x.gens) && x.gens[r.A].Const {
				// A const parameter passed on is judged by its declared
				// type (`GD[K]` with `const K: int` for `const D: Pick`).
				return x.judgeConstArg(e.Span(), types.ConstArg{Kind: types.CAParam, Param: r.A}, want)
			}
		case resolve.RefItem:
			it := c.cfg.Items.Get(types.ItemID(r.A))
			if it.Kind == types.KindConst {
				a, ok := x.m.constValueArg(x, it, e.Span())
				if !ok || a.Kind != types.CAValue {
					return a, ok
				}
				// A value of another type (`N[MA]` with `const K: int`,
				// `GD[K]` with an int K for `const D: Pick`, 5e) is E0301,
				// not an accept: the instance would read the bytes of the
				// smaller const as the bigger type.
				return x.judgeConstArg(e.Span(), a, want)
			}
		}
		if fe, isField := e.(*ast.FieldExpr); isField {
			if a, ok, done := x.variantArg(fe, want); done {
				return a, ok
			}
			if x.payloadVariant(fe) {
				paramFact(x.errorf(diag.E0705, e.Span(), "a variant is a const argument only of an enum with no payload in any variant"), x.constParam)
				return types.ConstArg{}, false
			}
		}
		paramFact(x.errorf(diag.E0705, e.Span(), "a const argument must be a compile-time constant"), x.constParam)
		return types.ConstArg{}, false
	case *ast.CallExpr:
		if ix, ok := e.Fun.(*ast.IndexExpr); ok && len(e.Args) == 0 && len(ix.Args) == 1 {
			if r := x.ref(ix.X); r.Kind == resolve.RefUniverse && source.Symbol(r.A) == source.SymSizeOf {
				t := x.typeOf(ix.Args[0], posPlain)
				if t == types.Poison {
					return types.ConstArg{}, false
				}
				return x.judgeConstArg(e.Span(), types.ConstArg{Kind: types.CASizeOf, Type: t}, want)
			}
			if r := x.ref(ix.X); r.Kind == resolve.RefItem && c.simdItem(types.ItemID(r.A), "Lanes") {
				return x.lanesArg(e, ix, want) // simd.Lanes[T](): the lane count
			}
		}
	}
	if len(x.gens) > 0 {
		x.errorf(diag.E0709, e.Span(), "inside a generic, a const argument is a const parameter, a literal, SizeOf, AlignOf, a named const or an enum variant without a payload")
		return types.ConstArg{}, false
	}
	return x.computedArg(e, want)
}

// paramFact adds fact param, the const parameter an argument binds, to d;
// an array length binds none, so its diagnostic has no such fact.
func paramFact(d *diag.Diagnostic, param string) *diag.Diagnostic {
	if param != "" {
		d.Text("param", param)
	}
	return d
}

// unresolvedRoot reports whether the name at the root of field expression
// fe (through fields, indexes, calls and parentheses) did not resolve, so
// that its own diagnostic stands alone.
func (x *tctx) unresolvedRoot(fe *ast.FieldExpr) bool {
	var e ast.Expr = fe
	for {
		switch y := e.(type) {
		case *ast.FieldExpr:
			e = y.X
		case *ast.IndexExpr:
			e = y.X
		case *ast.CallExpr:
			e = y.Fun
		case *ast.ParenExpr:
			e = y.X
		case *ast.Ident:
			k := x.ref(y).Kind
			return k == resolve.RefNone || k == resolve.RefPoison
		default:
			return false
		}
	}
}

// judgeConstArg judges const argument a, written at at, against want, the
// type of the const generic it binds with the call's or type's type arguments
// substituted. A value or a const parameter of want's type stands. An integer
// value for an integer generic of another type is converted to want's type,
// E0303 when it does not fit (`G8[K]` with an int K of 300 for `const N: u8`
// read 44 as a value and 300 as a length). `SizeOf[T]()` is an int, which any
// integer generic takes. Any other type is E0301: a char for an int, an int
// or a SizeOf for an enum, and every value or const parameter of a closed
// type where want is the caller's own open type parameter (`GT[U, Mode.B]` in
// `H[U]`), since no instance is checked again at mono. A want that still
// holds an inference variable (T bound by a later argument) is not judged
// here: judgePending does it once T is known. So is NoType and Poison.
func (x *tctx) judgeConstArg(at source.Span, a types.ConstArg, want types.TypeID) (types.ConstArg, bool) {
	c := x.m.c
	if want == types.Poison || want == types.NoType || c.hasHole(want) {
		return a, true
	}
	var got types.TypeID
	switch a.Kind {
	case types.CAValue:
		got = c.cfg.Consts.Get(a.Value).Type
	case types.CAParam:
		if int(a.Param) < len(x.gens) {
			got = x.gens[a.Param].Type
		}
	case types.CASizeOf:
		// An int, which an int generic takes as it is. Any other integer
		// generic takes it converted, as an int value is: for a closed T
		// the size is its value at want's type, E0303 when it does not fit
		// (`G8[SizeOf[Big]()]` for `const N: u8` with a 300-byte Big read
		// 44). For an open T, whose size no check reads before
		// mono, it is E0301 (no instance is checked again at mono), as
		// it is for a closed T whose layout is not final here (a
		// same-module type in a declaration). For any other type it is
		// E0301, as an int const is (`GD[SizeOf[int]()]` for `const D:
		// Pick` read a Pick out of the int).
		if want == types.Int {
			return a, true
		}
		if c.cfg.Types.Get(want).IsInteger() {
			size, ok := x.sizeNow(a.Type)
			if !ok {
				x.errorf(diag.E0301, at, "expected `"+x.str(want)+"`, found `int`: the size of `"+x.str(a.Type)+"` is not known here, so it cannot be judged to fit (pass it to an int const generic)").Text("expected", x.str(want)).Text("found", "int")
				return types.ConstArg{}, false
			}
			id, fits := c.internFolded(fval{n: new(big.Int).SetUint64(size)}, want)
			if !fits {
				x.errorf(diag.E0303, at, "the size of `"+x.str(a.Type)+"`, "+strconv.FormatUint(size, 10)+", does not fit in `"+x.str(want)+"`").Text("value", strconv.FormatUint(size, 10)).Text("type", x.str(want))
				return types.ConstArg{}, false
			}
			return types.ConstArg{Kind: types.CAValue, Value: id}, true
		}
		got = types.Int
	}
	if got == types.NoType || got == types.Poison || got == want {
		return a, true
	}
	if a.Kind == types.CAValue && c.cfg.Types.Get(got).IsInteger() && c.cfg.Types.Get(want).IsInteger() {
		if fv, ok := c.fvalOf(a.Value); ok {
			id, fits := c.internFolded(fval{n: fv.n}, want)
			if !fits {
				x.errorf(diag.E0303, at, "the value does not fit in `"+x.str(want)+"`").Text("value", fv.n.String()).Text("type", x.str(want))
				return types.ConstArg{}, false
			}
			return types.ConstArg{Kind: types.CAValue, Value: id}, true
		}
	}
	x.errorf(diag.E0301, at, "expected `"+x.str(want)+"`, found `"+x.str(got)+"`").Text("expected", x.str(want)).Text("found", x.str(got))
	return types.ConstArg{}, false
}

// sizeNow is the size of t for a SizeOf const argument judged against an
// integer generic narrower or other than int (judgeConstArg): known
// only for a closed t that is on no layout cycle and whose type items'
// modules have finished step 3 of stage 5, as variantConst requires; its
// layout is a shape read. false otherwise. A reference, a slice, a raw
// pointer or a fn value is one or two pointers whatever its element
// (`SizeOf[&Big]()`, `SizeOf[[]Big]()` in a declaration), so its
// size needs no layout and no shape read. Nested in another type it still
// needs its element final: the enclosing layout is memoized with the
// element's facts (Layout, Facts), which a module reads only once final.
func (x *tctx) sizeNow(t types.TypeID) (uint64, bool) {
	c := x.m.c
	if t == types.NoType || t == types.Poison || !c.closed(t) {
		return 0, false
	}
	ptr := uint64(c.cfg.Target.PointerBytes)
	if ptr == 0 {
		ptr = 8 // as layoutWalk.compute
	}
	switch c.cfg.Types.Kind(t) {
	case types.KRef, types.KMut, types.KRawPtr, types.KFn:
		return ptr, true
	case types.KSlice, types.KMutSlice:
		return 2 * ptr, true
	}
	if !c.layoutFinal(t, 0) || c.sizeOnCycle(t) {
		return 0, false
	}
	c.shapeReads(nil, t, x.rec)
	return c.Shape(t).Size, true
}

// layoutFinal reports whether every type item whose layout closed t's
// size reads, at any depth, is of a module that has finished step 3 of
// stage 5 (requireFinal). An error union or error set reads each error's
// payload (tagged4), so it is final only when every error of the set and
// every payload type is (`SizeOf[Bad!u8]()` with `error Bad(b:
// Big)`).
func (c *Checker) layoutFinal(t types.TypeID, depth int) bool {
	if depth > maxFoldDepth {
		return false
	}
	ty := c.cfg.Types.Get(t)
	switch ty.Kind {
	case types.KErrorUnion, types.KErrorSet:
		for _, em := range c.cfg.Types.Members(ty.Errors) {
			if em.IsParam {
				return false
			}
			it := c.cfg.Items.Get(em.Item)
			if m := c.module(it.Module); m != nil && !m.final {
				return false
			}
			d, ok := c.declOfItem(it).(*types.ErrorDecl)
			if !ok {
				return false
			}
			for _, f := range d.Fields {
				if !c.layoutFinal(f.Type, depth+1) {
					return false
				}
			}
		}
		if ty.Kind == types.KErrorUnion && ty.Elem != types.NoType {
			return c.layoutFinal(ty.Elem, depth+1)
		}
	case types.KNamed:
		if m := c.module(c.cfg.Items.Get(ty.Item).Module); m != nil && !m.final {
			return false
		}
		for _, a := range ty.Args {
			if !a.IsConst && !c.layoutFinal(a.Type, depth+1) {
				return false
			}
		}
	case types.KRecord:
		for _, f := range ty.Fields {
			if !c.layoutFinal(f.Type, depth+1) {
				return false
			}
		}
	default:
		if ty.Elem != types.NoType {
			return c.layoutFinal(ty.Elem, depth+1)
		}
	}
	return true
}

// variantArg is a variant written as a const argument (`G[Mode.A]`,
// `G[lib.Mode.A]`): an enum variant without a payload,
// of a non-generic enum with no fields in any variant, built as
// the folder builds it, judged against want as a named
// const is (judgeConstArg). done is false when fe is no such variant, or
// its enum's facts are not final yet (a declaration's type in stage 5):
// the caller's E0705 stands.
func (x *tctx) variantArg(fe *ast.FieldExpr, want types.TypeID) (types.ConstArg, bool, bool) {
	f := &folder{m: x.m, fx: x.fx, rec: x.rec, enums: true}
	v, ok := f.variant(fe)
	if !ok {
		return types.ConstArg{}, false, false
	}
	a, ok := x.judgeConstArg(fe.Span(), types.ConstArg{Kind: types.CAValue, Value: v.enum}, want)
	return a, ok, true
}

// payloadVariant reports whether fe names a variant of an enum that is not
// a plainEnum (one with a payload in some variant, or a generic one): E0705
// names that restriction.
func (x *tctx) payloadVariant(fe *ast.FieldExpr) bool {
	c := x.m.c
	if fe.Name == nil {
		return false
	}
	r := x.ref(fe.X)
	if r.Kind != resolve.RefItem {
		return false
	}
	it := c.cfg.Items.Get(types.ItemID(r.A))
	if it.Kind != types.KindEnum {
		return false
	}
	d, ok := c.declOfItem(it).(*types.EnumDecl)
	if !ok {
		return false
	}
	for _, v := range d.Variants {
		if v.Name == fe.Name.Name {
			return len(d.Generics) > 0 || !c.plainEnum(c.cfg.Types.Named(it.ID, nil))
		}
	}
	return false
}

// computedArg is a computed const argument outside a generic:
// folded by check when it is operators over literals and named consts with
// values, a named const resolved as a bare one is (localConstArg), a call
// as callArg says. A failure always has a diagnostic: the fold's reason
// (E0308, E0301, E0303), E0705 for anything check does not fold, or silence
// only where the const's own check reports it.
func (x *tctx) computedArg(e ast.Node, want types.TypeID) (types.ConstArg, bool) {
	ex, ok := e.(ast.Expr)
	if !ok || want == types.Poison {
		return types.ConstArg{}, false
	}
	hole := x.m.c.hasHole(want)
	if hole {
		want = types.NoType // judged at the generic's type later (judgePending)
	}
	f := &folder{m: x.m, fx: x.fx, rec: x.rec, emit: func(d *diag.Diagnostic) { x.emit(d) }, write: x.m.d != nil,
		want: want, param: x.constParam}
	f.constUse = func(it *types.Item, at source.Span) (types.ConstID, bool) {
		a, ok := x.m.constValueArg(x, it, at)
		return a.Value, ok && a.Kind == types.CAValue
	}
	v, ok, panicked := f.fold(ex)
	if !ok && !panicked {
		if ce, isCall := ex.(*ast.CallExpr); isCall {
			return x.callArg(ce, f, want)
		}
		x.foldFailed(f, ex)
	}
	if !ok || panicked {
		return types.ConstArg{}, false
	}
	// A bool result is a bool generic's argument (`G[!false]`);
	// against another type it is E0301, as before.
	if !hole && (v.isBool && want != types.Bool || !v.isBool && v.t != types.NoType && v.t != want) {
		found := "bool"
		if !v.isBool {
			found = x.str(v.t)
		}
		x.errorf(diag.E0301, e.Span(), "expected `"+x.str(want)+"`, found `"+found+"`").Text("expected", x.str(want)).Text("found", found)
		return types.ConstArg{}, false
	}
	id, ok := x.m.c.internFolded(v, want)
	if !ok {
		x.errorf(diag.E0303, e.Span(), "the value does not fit in `"+x.str(want)+"`").Text("value", v.n.String()).Text("type", x.str(want))
		return types.ConstArg{}, false
	}
	return types.ConstArg{Kind: types.CAValue, Value: id}, true
}

// foldFailed reports a fold that gave up without a panic: its recorded
// reason, nothing when it was quiet, else E0705.
func (x *tctx) foldFailed(f *folder, at ast.Node) {
	switch {
	case f.quiet:
	case f.fail == nil:
		f.notConst(at)
		fallthrough
	default:
		x.emit(f.fail)
	}
}

// negativeLen is E0303 for a known array length below 0: the length of
// `[N]T` and the count of `[e; n]` are an int of at least 0. nil when n is
// not negative.
func (c *Checker) negativeLen(at source.Span, n types.ConstArg) *diag.Diagnostic {
	if n.Kind != types.CAValue {
		return nil
	}
	if v, known := c.constInt(n.Value); known && v < 0 {
		return diag.New(diag.E0303, at, "an array length is at least 0").Text("value", c.cfg.Types.ConstString(n.Value)).Text("type", "int")
	}
	return nil
}

// intLitArg folds an integer literal into a value of type want (E0303 when
// it does not fit).
func (x *tctx) intLitArg(lit *ast.BasicLit, neg bool, want types.TypeID) (types.ConstArg, bool) {
	v, ok := parseIntLit(lit.Raw)
	tab := x.tab()
	if want == types.Poison {
		return types.ConstArg{}, false
	}
	if x.m.c.hasHole(want) {
		want = types.Int // judged at the generic's type later (judgePending)
	}
	ty := tab.Get(want)
	if !ty.IsInteger() {
		x.errorf(diag.E0301, lit.Span(), "an integer is not a `"+x.str(want)+"`").Text("expected", x.str(want)).Text("found", "int")
		return types.ConstArg{}, false
	}
	bits := uint(ty.Bits)
	if ty.Kind == types.KIntPtr {
		bits = 64
	}
	if !ok || !intFits(v, neg, bits, ty.Signed) {
		s := lit.Raw
		if neg {
			s = "-" + s
		}
		x.errorf(diag.E0303, lit.Span(), "`"+s+"` does not fit in `"+x.str(want)+"`").Text("value", s).Text("type", x.str(want))
		return types.ConstArg{}, false
	}
	bitsVal := v
	if neg {
		bitsVal = -v
	}
	return types.ConstArg{Kind: types.CAValue, Value: x.m.c.scalarConst(want, int(bits/8), bitsVal)}, true
}

// boolLitArg is the const argument of a literal true or false: a bool
// value, E0301 against
// another type.
func (x *tctx) boolLitArg(e ast.Node, v bool, want types.TypeID) (types.ConstArg, bool) {
	if want == types.Poison {
		return types.ConstArg{}, false
	}
	if want != types.Bool && !x.m.c.hasHole(want) { // a hole: judgePending
		x.errorf(diag.E0301, e.Span(), "a bool is not a `"+x.str(want)+"`").Text("expected", x.str(want)).Text("found", "bool")
		return types.ConstArg{}, false
	}
	var bits uint64
	if v {
		bits = 1
	}
	return types.ConstArg{Kind: types.CAValue, Value: x.m.c.scalarConst(types.Bool, 1, bits)}, true
}

// callArg is a computed const argument that is a call of a non-generic
// free function with arguments that fold: a function of
// this module is E0908 at once, since evaluating it at StageSignatures
// enters a same-module body whatever it does; an imported one is an
// EvalCall request, its value checked against want. Any other call is
// E0705, an argument that does not fold its fold's reason.
func (x *tctx) callArg(ce *ast.CallExpr, f *folder, want types.TypeID) (types.ConstArg, bool) {
	c := x.m.c
	notConst := func() (types.ConstArg, bool) {
		f.fail, f.quiet = nil, false
		x.foldFailed(f, ce)
		return types.ConstArg{}, false
	}
	var r resolve.Ref
	switch fn := ce.Fun.(type) {
	case *ast.Ident, *ast.FieldExpr:
		r = x.ref(fn)
	}
	switch r.Kind {
	case resolve.RefPoison:
		return types.ConstArg{}, false // resolve reported it
	case resolve.RefItem:
	default:
		return notConst()
	}
	it := c.cfg.Items.Get(types.ItemID(r.A))
	if it.Kind != types.KindFunc || it.Owner != types.NoItem || it.OwnerPrim != types.NoType {
		return notConst()
	}
	if it.Module == x.m.ID {
		x.emit(c.stageDiag(ce.Span(), []types.ItemID{it.ID}, diag.ListFact()))
		return types.ConstArg{}, false
	}
	fd, ok := c.declOfItem(it).(*types.FuncDecl)
	if !ok || len(fd.Sig.Generics) > 0 || len(fd.Sig.Params) != len(ce.Args) {
		return notConst() // a generic callee waits for mono
	}
	if fd.Sig.Errors != types.EmptyErrors {
		found := x.str(x.tab().ErrorUnion(fd.Sig.Errors, fd.Sig.Result))
		x.errorf(diag.E0301, ce.Span(), "expected `"+x.str(want)+"`, found `"+found+"`").Text("expected", x.str(want)).Text("found", found)
		return types.ConstArg{}, false
	}
	x.rec.signature(it.Key)
	req := c.evalRequest(x.m.ID, types.EvalCall, it.ID, types.StageSignatures, ce.Span())
	for i, a := range ce.Args {
		if a == nil || a.Name != nil || a.Value == nil {
			return notConst()
		}
		f.fail, f.quiet = nil, false
		f.want = fd.Sig.Params[i].Type
		v, ok, panicked := f.fold(a.Value)
		if panicked {
			return types.ConstArg{}, false
		}
		if !ok {
			x.foldFailed(f, a.Value)
			return types.ConstArg{}, false
		}
		pt := fd.Sig.Params[i].Type
		id, ok := c.internFolded(v, pt)
		if !ok {
			isInt := c.cfg.Types.Get(pt).IsInteger()
			if found := f.typeName(v); v.isBool != (pt == types.Bool) || !v.isBool && (!isInt || v.t != types.NoType && v.t != pt) {
				x.errorf(diag.E0301, a.Value.Span(), "expected `"+x.str(pt)+"`, found `"+found+"`").Text("expected", x.str(pt)).Text("found", found)
			} else {
				x.errorf(diag.E0303, a.Value.Span(), "the value does not fit in `"+x.str(pt)+"`").Text("value", v.n.String()).Text("type", x.str(pt))
			}
			return types.ConstArg{}, false
		}
		req.Args = append(req.Args, id)
	}
	if fd.Sig.Result != want {
		x.errorf(diag.E0301, ce.Span(), "expected `"+x.str(want)+"`, found `"+x.str(fd.Sig.Result)+"`").
			Text("expected", x.str(want)).Text("found", x.str(fd.Sig.Result))
		return types.ConstArg{}, false
	}
	v, d := c.evaluate(req, x.rec, faultUse)
	if d != nil {
		x.emit(d)
	}
	if v == types.NoConst {
		return types.ConstArg{}, false
	}
	return types.ConstArg{Kind: types.CAValue, Value: v}, true
}
