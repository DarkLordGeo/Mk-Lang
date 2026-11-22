// Why: generators inspect functions and types at stage 4, before the view's
// own module has checked signatures, and must see the same answer either
// way.
//
// What: Func (a function's signature as a generator sees it), Funcs (a
// struct's or enum's inherent functions, Type.Methods), GenFlow and the
// flows lists (a signature's declared flow clauses, Method.Flows).
//
// How: two routes answer alike: the AST for an item of the view's own
// module, and the checked Decl for an item of another module. Only declared
// clauses are listed (an imported signature also holds automatic sources,
// which Declared tells apart), sorted by kind, field, parameter, then path.

package check

import (
	"cmp"
	"slices"
	"strings"

	"mk/internal/ast"
	"mk/internal/source"
	"mk/internal/types"
)

// FlowKind discriminants: the declaration order of std.core's FlowKind
// (From, Into, Ends, Store), the values gen sets.
const (
	FlowFrom uint8 = iota
	FlowInto
	FlowEnds
	FlowStore
)

// GenFlow is one declared flow clause, one entry per source of a from
// clause. Param is what the clause names as its destination or subject:
// "result" for From, the receiving parameter for Into and Store, the
// parameter whose loans end for Ends; the receiver is "self". Path is the
// source as written ("a", "self.buf"; the stored parameter for Store, the
// flowing one for Into), "" for Ends. Field is the result field of a
// per-field from clause, else "".
type GenFlow struct {
	Kind               uint8
	Param, Path, Field string
}

// sortFlows puts flows in the canonical order.
func sortFlows(fs []GenFlow) []GenFlow {
	slices.SortStableFunc(fs, func(a, b GenFlow) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), strings.Compare(a.Field, b.Field),
			strings.Compare(a.Param, b.Param), strings.Compare(a.Path, b.Path))
	})
	return fs
}

// flowsAST lists the clauses s declares.
func (v *GenView) flowsAST(s *ast.Signature) []GenFlow {
	in := v.c.cfg.Interner
	name := func(id *ast.Ident) string {
		if id == nil {
			return ""
		}
		return in.Str(id.Name)
	}
	srcPath := func(src *ast.Source) string {
		parts := make([]string, 0, len(src.Names))
		for _, n := range src.Names {
			parts = append(parts, name(n))
		}
		return strings.Join(parts, ".")
	}
	var out []GenFlow
	if s.Self != nil && s.Self.Into != nil {
		out = append(out, GenFlow{Kind: FlowInto, Param: name(s.Self.Into), Path: "self"})
	}
	for _, p := range s.Params {
		if p != nil && p.Into != nil {
			out = append(out, GenFlow{Kind: FlowInto, Param: name(p.Into), Path: name(p.Name)})
		}
	}
	for _, f := range s.Flows {
		switch f := f.(type) {
		case *ast.FromFlow:
			for _, src := range f.Sources {
				if src != nil {
					out = append(out, GenFlow{Kind: FlowFrom, Param: "result", Path: srcPath(src)})
				}
			}
			for _, fs := range f.Fields {
				if fs == nil {
					continue
				}
				for _, src := range fs.Sources {
					if src != nil {
						out = append(out, GenFlow{Kind: FlowFrom, Param: "result", Path: srcPath(src), Field: name(fs.Name)})
					}
				}
			}
		case *ast.EndsFlow:
			out = append(out, GenFlow{Kind: FlowEnds, Param: name(f.Name)})
		case *ast.StoreFlow:
			out = append(out, GenFlow{Kind: FlowStore, Param: name(f.Into), Path: name(f.From)})
		}
	}
	return sortFlows(out)
}

// flowsSig lists the clauses a checked signature declares.
func (v *GenView) flowsSig(s *types.Signature) []GenFlow {
	in := v.c.cfg.Interner
	pname := func(p types.ParamIndex) string {
		if p == types.NoParam || int(p) > len(s.Params) {
			return ""
		}
		return in.Str(s.Params[p-1].Name)
	}
	srcPath := func(src types.Source) string {
		parts := []string{pname(src.Param)}
		owner := types.NoType
		if src.Param != types.NoParam && int(src.Param) <= len(s.Params) {
			owner = s.Params[src.Param-1].Type
		}
		for _, st := range src.Path {
			parts = append(parts, v.fieldName(owner, st.Field))
			owner = st.Type
		}
		return strings.Join(parts, ".")
	}
	var out []GenFlow
	for i, p := range s.Params {
		if p.Into != types.NoParam {
			out = append(out, GenFlow{Kind: FlowInto, Param: pname(p.Into), Path: pname(types.ParamIndex(i + 1))})
		}
	}
	if s.Flows.Declared {
		if len(s.Flows.Fields) > 0 {
			for _, fs := range s.Flows.Fields {
				field := v.resultField(s.Result, fs.Field)
				for _, src := range fs.Sources {
					out = append(out, GenFlow{Kind: FlowFrom, Param: "result", Path: srcPath(src), Field: field})
				}
			}
		} else {
			for _, src := range s.Flows.Sources {
				out = append(out, GenFlow{Kind: FlowFrom, Param: "result", Path: srcPath(src)})
			}
		}
	}
	for _, e := range s.Flows.Ends {
		out = append(out, GenFlow{Kind: FlowEnds, Param: pname(e)})
	}
	for _, st := range s.Flows.Stores {
		out = append(out, GenFlow{Kind: FlowStore, Param: pname(st.Into), Path: pname(st.From)})
	}
	return sortFlows(out)
}

// fieldName is the name of field i of the struct owner names, through
// references, optionals and records.
func (v *GenView) fieldName(owner types.TypeID, i uint32) string {
	tab := v.c.cfg.Types
	for owner != types.NoType && owner != types.Poison {
		t := tab.Get(owner)
		switch t.Kind {
		case types.KRef, types.KMut, types.KOptional, types.KErrorUnion:
			owner = t.Elem
			continue
		case types.KRecord:
			if int(i) < len(t.Fields) {
				return v.c.cfg.Interner.Str(t.Fields[i].Name)
			}
		case types.KNamed:
			if d, ok := v.c.cfg.Items.Get(t.Item).Decl.(*types.StructDecl); ok && int(i) < len(d.Fields) {
				return v.c.cfg.Interner.Str(d.Fields[i].Name)
			}
		}
		break
	}
	return "?"
}

// resultField is the name of a per-field clause's field i of result.
func (v *GenView) resultField(result types.TypeID, i uint32) string {
	return v.fieldName(result, i)
}

// Func is the stage-4 view of a function item as a generator sees it: its
// signature (the receiver first for a method) and declared flows. False for
// anything but a function or method of a named type, or one the view has no
// node for.
func (v *GenView) Func(item types.ItemID) (*GenMethod, bool) {
	it := v.c.cfg.Items.Get(item)
	if it.Kind != types.KindFunc {
		return nil, false
	}
	if it.Module != v.mod {
		fd, ok := it.Decl.(*types.FuncDecl)
		if !ok || fd == nil {
			return nil, false
		}
		sig := fd.Sig
		return &GenMethod{Sig: sig, Span: it.Span, Flows: v.flowsSig(&sig)}, true
	}
	ii := v.byItem[item]
	if ii == nil || ii.File < 0 || ii.File >= len(v.files) {
		return nil, false
	}
	fx := v.files[ii.File]
	n, ok := fx.node(ii.Node).(*ast.FuncDecl)
	if !ok || n.Sig == nil {
		return nil, false
	}
	g := &gctx{v: v, fx: fx, self: types.NoType}
	var owner []types.GenericParam
	self := types.NoType
	if it.Owner != types.NoItem {
		ow := v.c.cfg.Items.Get(it.Owner)
		if ov, ok := v.Decl(it.Owner, v.mod); ok {
			owner = ov.Generics
		}
		self = v.c.cfg.Types.Named(ow.ID, nil)
		g.self = self
	}
	sig := g.method(it, n.Sig, owner)
	if self != types.NoType && sig.Receiver != types.NoReceiver && len(sig.Params) > 0 {
		// method() spells the receiver over Self; an inherent function's
		// receiver is its own type.
		tab := v.c.cfg.Types
		switch sig.Receiver {
		case types.ByValue:
			sig.Params[0].Type = self
		case types.ByShared:
			sig.Params[0].Type = tab.Ref(self)
		default:
			sig.Params[0].Type = tab.Mut(self)
		}
	}
	sig.Item, sig.Exported = item, it.Exported
	return &GenMethod{Sig: sig, Span: n.Span(), Flows: v.flowsAST(n.Sig)}, true
}

// Funcs are the inherent functions of a struct or enum item in declaration
// order, as seen from module from: only exported ones for an item of
// another module. Each is a FuncDecl of the type's namespace, never an
// invariant or an impl's method.
func (v *GenView) Funcs(item types.ItemID, from types.ModuleID) ([]types.ItemID, bool) {
	items := v.c.cfg.Items
	it := items.Get(item)
	if it.Kind != types.KindStruct && it.Kind != types.KindEnum {
		return nil, false
	}
	var ids []types.ItemID
	if it.Module == v.mod {
		for id, ii := range v.byItem {
			m := items.Get(id)
			if m.Owner != item || m.Kind != types.KindFunc || ii.File < 0 || ii.File >= len(v.files) {
				continue
			}
			if _, ok := v.files[ii.File].node(ii.Node).(*ast.FuncDecl); ok {
				ids = append(ids, id)
			}
		}
	} else {
		for _, id := range items.Module(it.Module).Items {
			m := items.Get(id)
			if m.Owner != item || m.Kind != types.KindFunc || strings.ContainsRune(m.Key.String(), '#') {
				continue
			}
			if _, ok := m.Decl.(*types.FuncDecl); ok && (m.Exported || m.Module == from) {
				ids = append(ids, id)
			}
		}
	}
	// Declaration order is file path, then position (the contract's rule;
	// a FileID is the order files were loaded in, not a path): a file's
	// rank is its place in the module's Files, which the driver lists in
	// path order; a file outside them (the generated file) ranks after.
	files := items.Module(it.Module).Files
	rank := func(f source.FileID) int {
		if i := slices.Index(files, f); i >= 0 {
			return i
		}
		return len(files)
	}
	slices.SortFunc(ids, func(a, b types.ItemID) int {
		x, y := items.Get(a).Span, items.Get(b).Span
		return cmp.Or(cmp.Compare(rank(x.File), rank(y.File)), cmp.Compare(x.File, y.File), cmp.Compare(x.Start, y.Start))
	})
	return ids, true
}
