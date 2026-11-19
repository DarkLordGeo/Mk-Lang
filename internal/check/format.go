// Why: a format string is checked against its argument record at compile
// time, so a hole that names no field or a value that cannot be formatted
// is an error, not a run-time surprise; Print-shaped user functions must
// get the same check at their callers.
//
// What: formatCheck (E0705 for a format argument that is not a string
// literal or a named str const, E0706 for a text that does not match the
// record), the Print shape (formatShaped, formatSlot, judgedSlot,
// printShapeEnv, printForward, argsMisuse), shortFormat (a call written
// without its record), forwardedFormat, formatText and formatFields.
//
// How: the callee's Fields[Format] bound is judged hole by hole against the
// call's text, not field by field. A `const` str parameter forwarded as the
// format is accepted here and judged per instance by mono
// (internal/mono/format.go).

package check

import (
	"slices"
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/fmtspec"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// isFormatter reports whether item is Print or Arena.Fmt.
func (u *unit) isFormatter(item types.ItemID) bool {
	k := u.c.known
	return item != types.NoItem && (item == k.Print || item == k.ArenaFmt)
}

// formatShaped reports the Print shape: the parameters ps (after the receiver) end in a
// `const` str parameter and a parameter whose type is a type parameter of
// sig bounded by Fields[Format], the shape of Print, Arena.Fmt,
// System.Print, Logger.Info and any user forwarder. A call of such a
// callee may leave the record out when its text has no hole. It is
// a fact of the signature alone, never of the body.
func (u *unit) formatShaped(sig *types.Signature, ps []types.Param) bool {
	return u.formatSlot(sig, ps) >= 0
}

// formatSlot is the slot in sig.Generics of the record parameter of a
// signature of the Print shape (formatShaped), or -1.
func (u *unit) formatSlot(sig *types.Signature, ps []types.Param) int {
	k := u.c.known
	if len(ps) < 2 || k.Format == types.NoItem {
		return -1
	}
	f, a := ps[len(ps)-2], ps[len(ps)-1]
	if !f.Const || f.Type != types.Str || a.Const {
		return -1
	}
	ty := u.tab.Get(a.Type)
	if ty.Kind != types.KParam || int(ty.Index) >= len(sig.Generics) {
		return -1
	}
	for _, b := range sig.Generics[ty.Index].Bounds {
		if u.c.isFieldsFormat(b) {
			return int(ty.Index)
		}
	}
	return -1
}

// judgedSlot is the record parameter of a Print-shaped signature
// whose Fields[Format] bound is judged against each call's format text,
// hole by hole, instead of field by field; -1 for none. Only the
// function's own generic: an owner's (a method of `L[A: ...]`) was judged
// when the receiver's type was formed.
func (u *unit) judgedSlot(sig *types.Signature, ps []types.Param) int {
	s := u.formatSlot(sig, ps)
	if s < len(sig.Generics)-int(sig.OwnGenerics) {
		return -1
	}
	return s
}

// isFieldsFormat reports whether b is Fields[Format].
func (c *Checker) isFieldsFormat(b types.Bound) bool {
	return b.Kind == types.BoundFields && b.Inner == types.BoundInterface && b.Item == c.known.Format && b.Item != types.NoItem
}

// printShapeEnv is the other half of judgedSlot, at the definition: in a function of
// the Print shape, and in a fn literal or a contract of one, the record
// parameter A does not meet its Fields[Format] bound, which is a promise
// about each call's text and not about A's fields. So `args` (any value of type A)
// meets it only when passed on, with f unchanged, as the record of another
// Print-shaped call (applyCall), and a fields loop over it or a generic
// bounded by Fields[Format] is refused here (argsMisuse). No instance then
// formats a field the caller's text did not judge.
func (u *unit) printShapeEnv(it *types.Item) {
	root := it
	if _, ok := u.b.Node.(*ast.Contract); ok && root.Owner != types.NoItem {
		root = u.c.cfg.Items.Get(root.Owner) // a requires or ensures predicate
	}
	for root.Kind == types.KindLiteral && root.Owner != types.NoItem {
		root = u.c.cfg.Items.Get(root.Owner)
	}
	fd, ok := root.Decl.(*types.FuncDecl)
	if !ok {
		return
	}
	sig := &fd.Sig
	ps := sig.Params
	if sig.Receiver != types.NoReceiver && len(ps) > 0 {
		ps = ps[1:]
	}
	slot := u.judgedSlot(sig, ps)
	if slot < 0 || slot >= len(u.env.gen) {
		return
	}
	gen := append([]types.GenericParam(nil), u.env.gen...)
	var bs []types.Bound
	for _, b := range gen[slot].Bounds {
		if !u.c.isFieldsFormat(b) {
			bs = append(bs, b)
		}
	}
	gen[slot].Bounds = bs
	u.env.gen = gen
	u.fmtA = int32(slot) + 1
	if root == it {
		u.fmtF = LocalID(len(sig.Params) - 1)
	}
	u.x.fmtMisuse = func(at source.Span, t types.TypeID, b types.Bound) bool {
		if !u.c.isFieldsFormat(b) || t != u.tab.Param(uint32(u.fmtA-1)) {
			return false
		}
		u.argsMisuse(at, "type needs `"+u.str(t)+": Fields[Format]`")
		return true
	}
}

// argsMisuse is E0705: inside a Print-shaped function, its record
// or its format is used other than by passing both on together.
func (u *unit) argsMisuse(at source.Span, what string) {
	a, f := "args", "f"
	if u.sig != nil && u.fmtF > 0 && int(u.fmtF) <= len(u.sig.Params) {
		a = u.c.cfg.Interner.Str(u.sig.Params[u.fmtF].Name)
		f = u.c.cfg.Interner.Str(u.sig.Params[u.fmtF-1].Name)
	}
	u.err(diag.E0705, at, "this "+what+": in a function of the Print shape, `"+a+"`'s `Fields[Format]` is judged by each call's format text, so the one use that may need it is passing `"+a+"` on, with `"+f+"` unchanged, as the record of another Print-shaped call").
		Text("param", f).Alt("pass `" + f + "` and `" + a + "` on together, as `Print(w, " + f + ", " + a + ")` does")
}

// isShapeF reports whether e names the unit's own format parameter f.
func (u *unit) isShapeF(e ast.Expr) bool {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			break
		}
		e = p.X
	}
	id, ok := e.(*ast.Ident)
	if !ok || u.fmtF == 0 {
		return false
	}
	use := u.useOf(id.ID())
	return use.Kind == UseLocal && use.Local == u.fmtF
}

// printForward judges a call e of a Print-shaped callee whose record
// slot's Fields[Format] the text judges (in.fmtSlot). A literal or named
// const text is judged by formatCheck. A forwarded text is the
// unit's own f passed on with a value of type A, judged at the unit's
// callers, so the bound is not judged here at all; f with another record,
// or A with another format, is E0705; any other const str parameter
// passed on is forwardedFormat's case for a function not of the Print shape, and
// the bound is judged as written.
func (u *unit) printForward(e *ast.CallExpr, in *infer, rest []types.Param, slot int, short bool) {
	fi := len(rest) - 2
	if fi < 0 || fi >= len(e.Args) || e.Args[fi] == nil || e.Args[fi].Value == nil || !u.forwardedFormat(e.Args[fi].Value) {
		u.shapeFElsewhere(e, rest, -1)
		return
	}
	fArg := e.Args[fi].Value
	rt, at := u.tab.Record(nil), e.Span()
	if !short {
		rt = in.at(rest[len(rest)-1].Type)
		if fi+1 < len(e.Args) && e.Args[fi+1] != nil && e.Args[fi+1].Value != nil {
			at = e.Args[fi+1].Value.Span()
		}
	}
	a := types.NoType
	if u.fmtA > 0 {
		a = u.tab.Param(uint32(u.fmtA - 1))
	}
	switch {
	case u.isShapeF(fArg) && rt == a:
		in.fmtSkip = true
	case u.isShapeF(fArg):
		u.argsMisuse(at, "passes the format on with another record")
		in.fmtSkip = true
	case a != types.NoType && rt == a:
		u.argsMisuse(at, "passes the record on with another format")
		in.fmtSkip = true
	default:
		in.fmtSlot = 0
	}
	u.shapeFElsewhere(e, rest, fi)
}

// shapeFElsewhere is E0705 for the unit's own f passed to a const
// parameter of e other than the format slot allowed (-1 for none): its
// text was judged at the unit's callers as the format of args only.
func (u *unit) shapeFElsewhere(e *ast.CallExpr, rest []types.Param, allowed int) {
	if u.fmtF == 0 {
		return
	}
	for i, a := range e.Args {
		if i == allowed || i >= len(rest) || !rest[i].Const || a == nil || a.Value == nil {
			continue
		}
		if u.isShapeF(a.Value) {
			u.argsMisuse(a.Value.Span(), "passes the format to another const parameter")
		}
	}
}

// recordLike reports whether t is a record or a struct, the types whose
// fields a format text names.
func (u *unit) recordLike(t types.TypeID) bool {
	ty := u.tab.Get(t)
	switch ty.Kind {
	case types.KRecord:
		return true
	case types.KNamed:
		_, ok := u.c.declOfItem(u.c.cfg.Items.Get(ty.Item)).(*types.StructDecl)
		return ok
	}
	return false
}

// recordOpen reports whether the record parameter p of a call may still be
// (): its type is an inference variable of the call, already (), or
// poison. An explicit type argument or the receiver's type (`Print[(x:
// int)]`, a method of `L[(x: int)]`) fixes it to another type, and then the
// call takes no short form: lower would pass () for
// that parameter.
func (u *unit) recordOpen(in *infer, p types.TypeID) bool {
	t := in.at(p)
	return t == types.Poison || t == u.tab.Record(nil) || u.tab.Get(t).Kind == types.KInfer
}

// shortFormat checks the short form: the format
// argument a of a Print or Arena.Fmt call e written without its record,
// whose record parameter has type p. With no hole in the text the record
// is (), bound in in, and e is marked ImplicitRecord so lower supplies the
// (); a scan problem with no hole is then reported as for a written ().
// With a hole it is E0706 alone, and with no text formatText's E0705 alone:
// the record parameter becomes Poison, so neither E0304 nor E0711 follows,
// and the call keeps its declared result.
func (u *unit) shortFormat(e *ast.CallExpr, a *ast.Arg, in *infer, p types.TypeID) {
	slot := -1
	if w := u.tab.Get(in.at(p)); w.Kind == types.KInfer {
		slot = int(w.Index)
	}
	bind := func(t types.TypeID) {
		if slot >= 0 {
			in.set(slot, types.TypeArg(t))
		}
	}
	if a == nil || a.Value == nil {
		bind(types.Poison)
		return
	}
	if u.forwardedFormat(a.Value) {
		// A forwarded text in the short form: the text is the caller's, so whether it has a
		// hole is judged per instance against the () record (mono).
		bind(u.tab.Record(nil))
		u.b.markImplicitRecord(e.ID())
		return
	}
	text, ok := u.formatText(a.Value)
	if !ok {
		bind(types.Poison)
		return
	}
	holes, _, problem := fmtspec.Scan(text)
	if len(holes) > 0 {
		hn := holeNames(holes)
		var top, spelled []string
		for _, h := range holes {
			top = append(top, h.Path[0])
			spelled = append(spelled, "{"+h.Name()+"}")
		}
		u.err(diag.E0706, a.Value.Span(), "the format string has holes and no argument record: `"+strings.Join(spelled, " ")+
			"`; pass a record with one field per hole, `("+strings.Join(dedup(top), ", ")+")`").
			Fact("holes", diag.TextList(hn...)).Fact("fields", diag.TextList()).Text("problem", "no record").
			Alt("pass a record with one field per hole")
		bind(types.Poison)
		return
	}
	bind(u.tab.Record(nil))
	u.b.markImplicitRecord(e.ID())
	u.b.setConst(a.Value.ID(), u.c.strConst(text))
	if problem != "" {
		u.formatProblem(a.Value, nil, nil, problem)
	}
}

// dedup keeps the first of each repeated string, in order.
func dedup(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// formatCheck runs after a call of a Print-shaped callee (Print,
// Arena.Fmt, System.Print, a forwarder) was checked as a generic call: fArg
// is the format argument, args the argument value, and a the inferred
// argument type (Poison when inference failed). The text judges the
// callee's Fields[Format] bound hole by hole: each hole's value,
// the top-level field or a path's last field, implements Format. Print and
// Arena.Fmt (formatter) also refuse a generic argument type, which lower
// cannot lay out; a forwarder's instance is laid out by mono.
func (u *unit) formatCheck(fArg, args ast.Expr, a types.TypeID, formatter bool) {
	if fArg == nil || u.forwardedFormat(fArg) {
		return // a forwarded format: judged per instance (mono), the argument type included
	}
	text, ok := u.formatText(fArg)
	if !ok || a == types.Poison || a == types.NoType {
		return
	}
	u.b.setConst(fArg.ID(), u.c.strConst(text))
	if formatter && !u.c.closed(a) && u.tab.Kind(a) != types.KRecord {
		u.formatProblem(fArg, nil, nil, "the argument type is generic")
		return
	}
	// A record whose field types are generic, `(e)` with e: E and
	// E: Format (DF-kvd-02, DF-textkit-01), is judged here by its field
	// names and bounds; lower leaves the call plain and mono expands it per
	// instance, as for a forwarded format.
	fs, ok := u.formatFields(a)
	if !ok {
		u.formatProblem(fArg, nil, nil, "the argument is not a record or a struct")
		return
	}
	holes, _, problem := fmtspec.Scan(text)
	if problem == "" {
		_, problem = fmtspec.Match(holes, fs, u.pathFields, u.isFormat)
	}
	if problem != "" {
		d := u.formatProblem(fArg, holeNames(holes), fieldNames(fs), problem)
		if name, ok := strings.CutPrefix(problem, "field without Format: "); ok {
			for _, f := range fs {
				if f.Name == name {
					u.c.errFmtFacts(d, f.Type)
				}
			}
		}
	}
}

func (u *unit) formatProblem(at ast.Expr, holes, fields []string, problem string) *diag.Diagnostic {
	alt := "name every field of the arguments in a hole, and give each hole a field"
	if strings.HasPrefix(problem, "field without Format") || strings.HasPrefix(problem, "path ends at a field without Format") {
		alt = "implement Format for the hole's type, or name its exported fields in paths, `{r.name}`"
	}
	return u.err(diag.E0706, at.Span(), "the format string does not match the arguments: "+problem).
		Fact("holes", diag.TextList(holes...)).Fact("fields", diag.TextList(fields...)).Text("problem", problem).
		Alt(alt)
}

// forwardedFormat reports a forwarded format, the one case where a format
// argument that is not a literal or a named const is no E0705: e names a
// `const` parameter of type str
// of the function being checked. Each caller instance is specialised with
// the value, so the text is known per instance: mono judges it there
// (E0706 at the outermost concrete call) and expands the call. A const
// parameter is still no value elsewhere.
func (u *unit) forwardedFormat(e ast.Expr) bool {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			break
		}
		e = p.X
	}
	id, ok := e.(*ast.Ident)
	if !ok || u.b.Sig == nil {
		return false
	}
	use := u.useOf(id.ID())
	if use.Kind != UseLocal || use.Local < 1 || int(use.Local) > len(u.b.Sig.Params) {
		return false
	}
	p := u.b.Sig.Params[use.Local-1]
	return p.Const && p.Type == types.Str
}

// formatText is the format argument's bytes: a string literal's decoded
// content, or a named const of type str with a value (an imported one's
// decl, a same-module literal initializer, else a use request). Anything
// else is E0705 (a local, or a const parameter that is not forwarded as
// forwardedFormat allows). False when the value is not known (an error already
// reported, or poison).
func (u *unit) formatText(e ast.Expr) ([]byte, bool) {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return u.formatText(e.X)
	case *ast.BasicLit:
		switch e.Kind {
		case ast.TokStr, ast.TokRawStr, ast.TokMultiStr:
			return litString(e.Kind, e.Raw)
		}
	case *ast.Ident, *ast.FieldExpr:
		r := u.ref(e)
		if r.Kind == resolve.RefPoison {
			return nil, false
		}
		if r.Kind == resolve.RefItem {
			it := u.c.cfg.Items.Get(types.ItemID(r.A))
			if d, ok := it.Decl.(*types.ConstDecl); ok && (d.Type == types.Str || d.Type == types.NoType) {
				return u.constStr(it, d, e)
			}
		}
	}
	if u.tab.Kind(u.b.TypeOf(e.ID())) == types.KPoison {
		return nil, false
	}
	u.err(diag.E0705, e.Span(), "the format argument must be a string literal or a named str const").Text("param", "f")
	return nil, false
}

// constStr is a str const's value for the format check.
func (u *unit) constStr(it *types.Item, d *types.ConstDecl, at ast.Expr) ([]byte, bool) {
	if d.HasValue {
		u.x.rec.value(it.Key)
		return u.c.strBytes(d.Value)
	}
	if it.Module != u.m.ID {
		return nil, false
	}
	if n, _ := u.m.nodeOf(it.ID); n != nil {
		if cd, ok := n.(*ast.ConstDecl); ok {
			if lit, ok := cd.Value.(*ast.BasicLit); ok && (lit.Kind == ast.TokStr || lit.Kind == ast.TokRawStr || lit.Kind == ast.TokMultiStr) {
				return litString(lit.Kind, lit.Raw)
			}
		}
	}
	if !u.m.checkEarlyOne(it) {
		return nil, false // no MIR for it: poison
	}
	req := u.c.evalRequest(u.m.ID, types.EvalConst, it.ID, types.StageSignatures, at.Span())
	v, dg := u.c.evaluate(req, u.x.rec, faultConstUse)
	if dg != nil {
		u.b.Diags = append(u.b.Diags, dg)
	}
	if v == types.NoConst {
		return nil, false
	}
	return u.c.strBytes(v)
}

// formatFields are the visible fields of the argument type in field order
// (a record: sorted order; a struct: its exported fields), for
// fmtspec.Match. False for a type that is neither a closed record nor a
// closed struct.
func (u *unit) formatFields(a types.TypeID) ([]fmtspec.Field[types.TypeID], bool) {
	class := func(t types.TypeID) fmtspec.Class {
		ty := u.tab.Get(t)
		switch {
		case ty.IsInteger():
			return fmtspec.Int
		case ty.Kind == types.KFloat:
			return fmtspec.Float
		}
		return fmtspec.Other
	}
	ty := u.tab.Get(a)
	var fs []fmtspec.Field[types.TypeID]
	switch ty.Kind {
	case types.KRecord:
		for k, f := range ty.Fields {
			fs = append(fs, fmtspec.Field[types.TypeID]{Name: u.c.cfg.Interner.Str(f.Name), Index: uint32(k), Type: f.Type, Class: class(f.Type)})
		}
		return fs, true
	case types.KNamed:
		it := u.c.cfg.Items.Get(ty.Item)
		sd, ok := it.Decl.(*types.StructDecl)
		if !ok {
			return nil, false
		}
		u.x.rec.fields(it.Key, false) // as fieldsSatisfy reads them
		for k, f := range sd.Fields {
			if f.Exported {
				t := u.c.subst(f.Type, ty.Args, types.NoType)
				fs = append(fs, fmtspec.Field[types.TypeID]{Name: u.c.cfg.Interner.Str(f.Name), Index: uint32(k), Type: t, Class: class(t)})
			}
		}
		return fs, true
	}
	return nil, false
}

// pathFields are the fields a hole's path may name inside a value of type t
// a closed record's or struct's, through one shared reference.
func (u *unit) pathFields(t types.TypeID) ([]fmtspec.Field[types.TypeID], bool, bool) {
	deref := false
	if ty := u.tab.Get(t); ty.Kind == types.KRef {
		t, deref = ty.Elem, true
	}
	fs, ok := u.formatFields(t)
	return fs, deref, ok
}

// isFormat reports whether t implements Format: each hole's value is
// formatted through FmtValue[t], and the text, not the call's
// Fields[Format] bound, judges that.
func (u *unit) isFormat(t types.TypeID) bool {
	return u.c.satisfies(u.env, t, types.Bound{Kind: types.BoundInterface, Item: u.c.known.Format}, u.x.rec)
}

// holeNames are the holes as written, in text order (E0706's holes fact).
func holeNames(hs []fmtspec.Hole) []string {
	var out []string
	for _, h := range hs {
		out = append(out, h.Name())
	}
	return out
}

// fieldNames are the fields' names in field order (E0706's fields fact).
func fieldNames(fs []fmtspec.Field[types.TypeID]) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return out
}
