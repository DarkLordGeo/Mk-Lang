// Why: the generator API types exist only at compile time, so code that
// touches them must never reach a compiled program.
//
// What: genAPI, genType and genSig (does a type or signature mention an API
// type), genOnlyUnit, genUse and quoteExpr.
//
// How: a gen-only function (one whose signature mentions an API type) may be
// called, used as a fn value, or hold a `quote`, only from a unit that is
// itself gen-only; elsewhere each is E0705 with fact param. The walk reads
// declarations only, so the answer depends on no body.

package check

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/types"
)

// genAPI names the generator API item of t's constructor, "" when t's
// constructor is not one.
func (c *Checker) genAPI(id types.ItemID) string {
	if id == types.NoItem {
		return ""
	}
	k := &c.known
	for _, g := range []types.ItemID{k.Gen, k.TypeInfo, k.Field, k.Variant, k.Method, k.Param, k.Code, k.Block, k.TypeKind, k.Receiver, k.Flow, k.FlowKind} {
		if g != types.NoItem && g == id {
			return c.cfg.Interner.Str(c.cfg.Items.Get(id).Name)
		}
	}
	return ""
}

// genType names the first generator API type t mentions, through type
// arguments, references, slices, optionals, arrays, records, error unions,
// and the fields of structs and enums; "" when it mentions none. The walk
// is over declarations only, so the answer depends on no body.
func (c *Checker) genType(t types.TypeID) string {
	if c.known.Gen == types.NoItem && c.known.Code == types.NoItem && c.known.TypeInfo == types.NoItem {
		return ""
	}
	if v, ok := c.genMemo.Load(t); ok {
		return v.(string)
	}
	seen := map[types.ItemID]bool{}
	r := c.genWalk(t, seen)
	c.genMemo.Store(t, r)
	return r
}

func (c *Checker) genWalk(t types.TypeID, seen map[types.ItemID]bool) string {
	tab := c.cfg.Types
	ty := tab.Get(t)
	switch ty.Kind {
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KOptional, types.KArray, types.KErrorUnion:
		return c.genWalk(ty.Elem, seen)
	case types.KRecord:
		for _, f := range ty.Fields {
			if r := c.genWalk(f.Type, seen); r != "" {
				return r
			}
		}
	case types.KNamed:
		if r := c.genAPI(ty.Item); r != "" {
			return r
		}
		for _, a := range ty.Args {
			if !a.IsConst {
				if r := c.genWalk(a.Type, seen); r != "" {
					return r
				}
			}
		}
		if seen[ty.Item] {
			return ""
		}
		seen[ty.Item] = true
		it := c.cfg.Items.Get(ty.Item)
		switch d := it.Decl.(type) {
		case *types.StructDecl:
			for _, f := range d.Fields {
				if r := c.genWalk(f.Type, seen); r != "" {
					return r
				}
			}
		case *types.EnumDecl:
			for _, v := range d.Variants {
				for _, f := range v.Fields {
					if r := c.genWalk(f.Type, seen); r != "" {
						return r
					}
				}
			}
		}
	}
	return ""
}

// genSig names the API type a signature mentions (a parameter, the
// receiver or the result), "" when none: the signature is gen-only.
func (c *Checker) genSig(s *types.Signature) string {
	if s == nil {
		return ""
	}
	for _, p := range s.Params {
		if r := c.genType(p.Type); r != "" {
			return r
		}
	}
	return c.genType(s.Result)
}

// genOnlyUnit reports whether the unit being checked is gen-only: a
// function, method or fn literal whose own signature mentions an API type.
// Main, tests, const and global initializers are never gen-only.
func (u *unit) genOnlyUnit() bool {
	switch u.b.Node.(type) {
	case *ast.FuncDecl, *ast.FnLit, *ast.ExternDecl, *ast.Contract:
		return u.c.genSig(u.sig) != ""
	}
	return false
}

// genUse is E0705 for a call or fn value of a gen-only function in a unit
// that is not gen-only.
func (u *unit) genUse(at ast.Node, s *types.Signature, what string) {
	u.buildOnly(at, s, what)
	api := u.c.genSig(s)
	if api == "" || u.genOnlyUnit() {
		return
	}
	u.err(diag.E0705, at.Span(), "a "+what+" of a generator function runs only at compile time, in another generator: it takes `"+api+"`").
		Text("param", api)
}

// quoteExpr types `quote { }` as Code; outside a gen-only unit it
// is E0705. Its splices are typed as ordinary expressions.
func (u *unit) quoteExpr(e *ast.QuoteExpr) types.TypeID {
	for _, sp := range e.Splices {
		if sp != nil && sp.X != nil {
			u.spliceOperand(sp.X, u.expr(sp.X, types.NoType))
		}
	}
	if !u.genOnlyUnit() {
		u.err(diag.E0705, e.Span(), "`quote` builds generator output and runs only at compile time, in a generator").Text("param", "Code")
	}
	if u.c.known.Code == types.NoItem {
		return types.Poison
	}
	return u.tab.Named(u.c.known.Code, nil)
}
