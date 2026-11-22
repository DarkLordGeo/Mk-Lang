// Why: each kind of declaration puts different names in scope (type
// parameters, fields and self in an invariant, parameters in contracts and
// flows), and the scope order must be the same for every item of a kind.
//
// What: walkTask and the per-kind walkers: structDecl, typeParamDecls,
// function, interfaceMethod, paramFlows, flows, flowName and genLine.
//
// How: each item walks in a frame of its own, so an expression outside a
// function body can still declare a local. Flow and contract names bind to
// the parameters of the current signature, else E0201. A gen line's
// arguments read as a value when literal, else as a type.

package resolve

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// walkTask resolves one task's declaration and its dependents.
func (w *walker) walkTask() {
	r := w.t.root
	w.rec = r
	// An expression outside any function body (a const or global
	// initializer, an expression in a type) can still declare a local: an
	// `else` handler's name, or a local in its block. It goes in a frame of
	// the item's own, which is a const's or a global's Body; a function,
	// test or generator pushes its own frame.
	switch r.node.(type) {
	case *ast.FuncDecl, *ast.ExternDecl, *ast.TestItem, *ast.GenItem:
	case *ast.ConstDecl, *ast.GlobalDecl:
		w.pushBody(r, true)
		defer w.popBody()
	default:
		w.pushBody(r, false)
		defer w.popBody()
	}
	switch d := r.node.(type) {
	case *ast.StructDecl:
		w.structDecl(r, d)
	case *ast.EnumDecl:
		mark := w.pushTypeParams(r.gens, true)
		w.typeParamDecls(d.TypeParams)
		w.typ(d.Tag)
		for _, v := range d.Variants {
			for _, f := range v.Fields {
				w.typ(f.Type)
			}
			w.expr(v.Value)
		}
		w.tparams = w.tparams[:mark]
	case *ast.ErrorDecl:
		for _, f := range d.Fields {
			w.typ(f.Type)
		}
	case *ast.InterfaceDecl:
		mark := w.pushTypeParams(r.gens, true)
		w.typeParamDecls(d.TypeParams)
		w.selfType = r.item.ID
		for _, sig := range d.Methods {
			w.interfaceMethod(r, sig)
		}
		w.selfType = types.NoItem
		w.tparams = w.tparams[:mark]
	case *ast.ImplDecl:
		mark := w.pushTypeParams(r.gens, true)
		w.typeParamDecls(d.TypeParams)
		w.bound(d.Interface)
		w.typ(d.For)
		w.selfType = r.item.ID
		for _, dep := range r.deps {
			if fd, ok := dep.node.(*ast.FuncDecl); ok {
				w.rec = dep
				w.function(dep, fd.Sig, fd.Body)
			}
		}
		w.rec = r
		w.selfType = types.NoItem
		w.tparams = w.tparams[:mark]
	case *ast.AliasDecl:
		mark := w.pushTypeParams(r.gens, true)
		w.typeParamDecls(d.TypeParams)
		w.typ(d.Type)
		w.tparams = w.tparams[:mark]
	case *ast.ConstDecl:
		w.typ(d.Type)
		w.expr(d.Value)
		w.expr(d.Fuel)
	case *ast.GlobalDecl:
		w.typ(d.Type)
		w.expr(d.Value)
	case *ast.FuncDecl:
		w.function(r, d.Sig, d.Body)
	case *ast.ExternDecl:
		w.function(r, d.Sig, d.Body)
	case *ast.TestItem:
		w.pushBody(r, true)
		if d.Param != nil {
			w.typ(d.Param.Type)
			w.declareParam(d.Param.Name, d.Param.Var, d.Param.Const, d.Param.Type)
		}
		w.block(d.Body)
		w.popBody()
	case *ast.GenItem:
		w.genLine(r, d)
	}
}

// structDecl walks a struct and its invariants (each its own item, with the
// fields, self and the type parameters in scope).
func (w *walker) structDecl(r *itemRec, d *ast.StructDecl) {
	mark := w.pushTypeParams(r.gens, true)
	w.typeParamDecls(d.TypeParams)
	var fields []fieldName
	for _, mem := range d.Members {
		if f, ok := mem.(*ast.FieldMember); ok {
			w.typ(f.Type)
			if f.Name != nil {
				fields = append(fields, fieldName{sym: f.Name.Name, idx: len(fields), item: r.item.ID, at: f.Name.Span()})
			}
		}
	}
	for _, dep := range r.deps {
		inv, ok := dep.node.(*ast.InvariantMember)
		if !ok {
			continue
		}
		w.rec = dep
		w.pushBody(dep, true)
		w.declareSelf(inv.Span())
		w.fields, w.fieldsBody = fields, len(w.bodies)-1
		w.expr(inv.X)
		w.fields = nil
		w.popBody()
	}
	w.rec = r
	w.tparams = w.tparams[:mark]
}

// typeParamDecls resolves the bounds and const types of type parameters.
func (w *walker) typeParamDecls(tps *ast.TypeParams) {
	if tps == nil {
		return
	}
	for _, tp := range tps.Params {
		if tp == nil {
			continue
		}
		for _, b := range tp.Bounds {
			w.bound(b)
		}
		w.typ(tp.Type)
	}
}

// function walks a function, method or extern: generics, parameters, the
// result, flows, contracts, then the body.
func (w *walker) function(r *itemRec, sig *ast.Signature, body *ast.Block) {
	if sig == nil {
		return
	}
	mark := w.pushTypeParams(r.gens, true)
	// A bound's expression can declare a local (an `else` handler's name,
	// a local in its block; FuzzCheck's `A[:[else{A:`), and a function
	// task has no frame yet: walk the bounds in a frame of their own that
	// belongs to no item, so the function's Body still starts with its
	// parameters.
	w.pushBody(r, false)
	w.typeParamDecls(sig.TypeParams)
	w.popBody()
	w.pushBody(r, true)
	if sig.Self != nil {
		w.declareSelf(sig.Self.Span())
	}
	for _, p := range sig.Params {
		if p != nil {
			w.typ(p.Type)
		}
	}
	var ensures *ast.Contract
	for _, c := range sig.Contracts {
		if c.Kind == ast.Ensures && ensures == nil {
			ensures = c
		}
	}
	for _, p := range sig.Params {
		if p == nil {
			continue
		}
		if ensures != nil && p.Name != nil && p.Name.Name == source.SymResult {
			w.t.diags = append(w.t.diags, dup(p.Name.Span(), keywordSpan(w.fs.fi.File, w.fs.fi.FileID, ensures.Span().Start, ast.TokEnsures), "result", "parameter"))
		}
		w.declareParam(p.Name, p.Var, p.Const, p.Type)
	}
	w.typ(sig.Result)
	w.paramFlows(sig.Self, sig.Params)
	w.flows(sig.Flows, sig.Self != nil)
	for _, dep := range r.deps {
		c, ok := dep.node.(*ast.Contract)
		if !ok {
			continue
		}
		w.rec = dep
		w.pushBody(dep, true)
		if c.Kind == ast.Ensures {
			w.result, w.resultBody = r.item.ID, len(w.bodies)-1
		}
		w.expr(c.X)
		w.result = types.NoItem
		w.popBody()
	}
	w.rec = r
	w.block(body)
	w.popBody()
	w.tparams = w.tparams[:mark]
}

// interfaceMethod walks one interface method signature: Self and the
// interface's type parameters are in scope; its parameters live in a body
// of their own that belongs to no item (RefParam B is the interface).
func (w *walker) interfaceMethod(r *itemRec, sig *ast.Signature) {
	if sig == nil {
		return
	}
	own := make([]genParam, 0)
	if sig.TypeParams != nil {
		for _, tp := range sig.TypeParams.Params {
			if tp != nil && tp.Name != nil {
				own = append(own, genParam{sym: tp.Name.Name, idx: len(r.gens) + len(own), item: r.item.ID, at: tp.Name.Span(), konst: tp.Const})
			}
		}
	}
	mark := w.pushTypeParams(own, true)
	w.typeParamDecls(sig.TypeParams)
	w.pushBody(r, false)
	if sig.Self != nil {
		w.declareSelf(sig.Self.Span())
	}
	for _, p := range sig.Params {
		if p != nil {
			w.typ(p.Type)
		}
	}
	for _, p := range sig.Params {
		if p != nil {
			w.declareParam(p.Name, p.Var, p.Const, p.Type)
		}
	}
	w.typ(sig.Result)
	w.paramFlows(sig.Self, sig.Params)
	w.flows(sig.Flows, sig.Self != nil)
	for _, c := range sig.Contracts {
		if c.Kind == ast.Ensures {
			w.result, w.resultBody = r.item.ID, len(w.bodies)-1
		}
		w.expr(c.X)
		w.result = types.NoItem
	}
	w.popBody()
	w.tparams = w.tparams[:mark]
}

// paramFlows checks each "p: T into q", and a receiver's "self into q":
// q is a parameter or self.
func (w *walker) paramFlows(self *ast.SelfParam, params []*ast.Param) {
	hasSelf := self != nil
	if hasSelf && self.Into != nil {
		w.flowName(self.Into, hasSelf)
	}
	for _, p := range params {
		if p != nil && p.Into != nil {
			w.flowName(p.Into, hasSelf)
		}
	}
}

// flows resolves from, ends and store clauses: the first
// name of each source and both names of a store or ends are parameters.
func (w *walker) flows(fl []ast.Flow, hasSelf bool) {
	for _, f := range fl {
		switch f := f.(type) {
		case *ast.FromFlow:
			for _, src := range f.Sources {
				if src != nil && len(src.Names) > 0 {
					w.flowName(src.Names[0], hasSelf)
				}
			}
			// A per-field clause's sources; the field name is
			// check's (E0203), never a parameter.
			for _, fs := range f.Fields {
				if fs == nil {
					continue
				}
				for _, src := range fs.Sources {
					if src != nil && len(src.Names) > 0 {
						w.flowName(src.Names[0], hasSelf)
					}
				}
			}
		case *ast.EndsFlow:
			w.flowName(f.Name, hasSelf)
		case *ast.StoreFlow:
			w.flowName(f.From, hasSelf)
			w.flowName(f.Into, hasSelf)
		}
	}
}

// flowName binds a flow name to a parameter of the current body's
// signature, else E0201 with expected "parameter".
func (w *walker) flowName(id *ast.Ident, hasSelf bool) {
	if id == nil {
		return
	}
	f := w.bodies[len(w.bodies)-1]
	for i := 1; i < len(f.body.Locals); i++ {
		l := f.body.Locals[i]
		if l.Kind != LocalParam && l.Kind != LocalSelf {
			break
		}
		if l.Name == id.Name && (l.Kind == LocalParam || hasSelf) {
			w.fs.setRef(id.ID(), w.localRef(LocalID(i)))
			return
		}
	}
	spell := w.m.str(id.Name)
	d := diag.New(diag.E0201, id.Span(), "`"+spell+"` is not a parameter of this signature").Text("name", spell).Text("expected", "parameter")
	w.report(d, id.Name)
	w.fs.setRef(id.ID(), poisonRef)
}

// genLine resolves a gen line: the generator path, the arguments' readings
// (a literal is a value, anything else a type) and fuel.
func (w *walker) genLine(r *itemRec, d *ast.GenItem) {
	gen := types.NoItem
	if d.Generator != nil {
		ref := w.path(d.Generator, posValue)
		w.fs.setRef(d.Generator.ID(), ref)
		if ref.Kind == RefItem {
			gen = types.ItemID(ref.A)
		}
	}
	for i := range w.m.info.Gens {
		if w.m.info.Gens[i].Item == r.item.ID {
			w.m.info.Gens[i].Generator = gen
		}
	}
	for _, a := range d.Args {
		if isLiteralArg(a) {
			w.fs.setReading(a.ID(), ReadExpr)
			w.expr(a.(ast.Expr))
		} else {
			w.fs.setReading(a.ID(), ReadType)
			w.typeSlot(a, posType)
		}
	}
	w.expr(d.Fuel)
}

// isLiteralArg reports a BasicLit or a negated one.
func isLiteralArg(a ast.TypeOrExpr) bool {
	switch a := a.(type) {
	case *ast.BasicLit:
		return true
	case *ast.UnaryExpr:
		_, ok := a.X.(*ast.BasicLit)
		return ok && a.Op == ast.TokMinus
	}
	return false
}
