// Why: codegen finds every callee and fn value by an exact (item, args) match
// read from the instance body (symbols.go funcIndex, call.go calleeArgs,
// const.go's RelocFn), so the instance table must be closed over its bodies.
// What: closedOver and bodyRefs, which require every reference of every
// instance body to name an entry of Instances.List exactly.
// How: reads each body as codegen reads it and compares item and GenericArg
// values, ConstIDs included.

package mono

import (
	"fmt"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// ref is one reference of an instance body, read as codegen reads it.
type ref struct {
	where string
	item  types.ItemID
	args  []types.GenericArg
}

// bodyRefs lists every reference codegen resolves in body b: each direct
// callee (its raw FnRef arguments, then one CAValue per const value
// parameter, from an OpConst or an OpImm interned as mono and codegen intern
// a scalar), each RvFnRef, and each RelocFn reachable from an OpConst, an
// RvConstRef or an RvGlobalRef value through data relocations. An argument
// for a const value parameter that is not a constant is reported as an
// error string.
func bodyRefs(w *world, b *mir.Body) ([]ref, []string) {
	var out []ref
	var bad []string
	seen := map[types.ConstID]bool{}
	var consts func(where string, c types.ConstID)
	consts = func(where string, c types.ConstID) {
		if c == types.NoConst || seen[c] {
			return
		}
		seen[c] = true
		for _, r := range w.consts.Get(c).Relocs {
			switch r.Kind {
			case types.RelocFn:
				out = append(out, ref{where + " reloc", r.Fn.Item, r.Fn.Args})
			case types.RelocData:
				consts(where, r.Data)
			}
		}
	}
	op := func(where string, o *mir.Operand) {
		if o.Kind == mir.OpConst {
			consts(where, o.Const)
		}
	}
	decl := func(where string, item types.ItemID) {
		switch d := w.items.Get(item).Decl.(type) {
		case *types.ConstDecl:
			if d.HasValue {
				consts(where, d.Value)
			}
		case *types.GlobalDecl:
			if d.HasValue {
				consts(where, d.Value)
			}
		}
	}
	for i := range b.GenericArgs {
		if a := b.GenericArgs[i]; a.IsConst && a.Const.Kind == types.CAValue {
			consts("generic arg", a.Const.Value)
		}
	}
	for bi := range b.Blocks {
		blk := &b.Blocks[bi]
		for si := range blk.Stmts {
			st := &blk.Stmts[si]
			if st.Kind != mir.StAssign {
				continue
			}
			rv := &st.Rvalue
			where := fmt.Sprintf("bb%d stmt %d", bi, si)
			op(where, &rv.A)
			op(where, &rv.B)
			if rv.Kind == mir.RvAggregate {
				for oi := rv.Ops.Start; oi < rv.Ops.End(); oi++ {
					op(where, &b.Operands[oi])
				}
			}
			switch rv.Kind {
			case mir.RvFnRef:
				out = append(out, ref{where + " RvFnRef", rv.Fn.Item, b.GenericArgs[rv.Fn.Args.Start:rv.Fn.Args.End()]})
			case mir.RvConstRef, mir.RvGlobalRef:
				decl(where, rv.Item)
			}
		}
		t := &blk.Term
		if t.Kind != mir.TermCall {
			continue
		}
		where := fmt.Sprintf("bb%d call", bi)
		op(where, &t.Callee.Value)
		for ai := t.Args.Start; ai < t.Args.End(); ai++ {
			op(where, &b.Args[ai].Operand)
		}
		if t.Callee.Kind != mir.CallDirect {
			continue
		}
		f := t.Callee.Fn
		args := append([]types.GenericArg(nil), b.GenericArgs[f.Args.Start:f.Args.End()]...)
		if fd, ok := w.items.Get(f.Item).Decl.(*types.FuncDecl); ok {
			cargs := b.Args[t.Args.Start:t.Args.End()]
			for pi, p := range fd.Sig.Params {
				if !p.Const {
					continue
				}
				o := cargs[pi].Operand
				var c types.ConstID
				switch o.Kind {
				case mir.OpConst:
					c = o.Const
				case mir.OpImm:
					n := int(w.Layout(o.Type).Shape.Size)
					bs := make([]byte, n)
					types.StoreUint(bs, n, o.Bits)
					c = w.consts.Intern(types.ConstValue{Type: o.Type, Align: uint32(n), Bytes: bs})
				default:
					bad = append(bad, fmt.Sprintf("%s: const parameter %d of %s is operand kind %d, not a constant",
						where, pi+1, w.items.Get(f.Item).Key, o.Kind))
					continue
				}
				args = append(args, types.ConstGenericArg(types.ConstArg{Kind: types.CAValue, Value: c}))
			}
		}
		out = append(out, ref{where, f.Item, args})
	}
	return out, bad
}

func sameArgs(a, b []types.GenericArg) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// closedOver requires every reference of every instance body to resolve, by
// exact (item, args), to an entry of in.List.
func closedOver(t *testing.T, w *world, in *Instances) {
	t.Helper()
	for i := range in.List {
		inst := &in.List[i]
		if inst.Body == nil {
			continue
		}
		refs, bad := bodyRefs(w, inst.Body)
		for _, s := range bad {
			t.Errorf("%s: %s", inst.Name, s)
		}
		for _, r := range refs {
			found := false
			for j := range in.List {
				if in.List[j].Item == r.item && sameArgs(in.List[j].Args, r.args) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s, %s: no instance of %s with args %s", inst.Name, r.where, w.items.Get(r.item).Key, argsString(w, r.args))
			}
		}
	}
}

func argsString(w *world, as []types.GenericArg) string {
	s := "["
	for i, a := range as {
		if i > 0 {
			s += ", "
		}
		s += w.tab.GenericArgString(a, nil)
		if a.IsConst {
			s += fmt.Sprintf("(kind %d, const %d)", a.Const.Kind, a.Const.Value)
		}
	}
	return s + "]"
}
