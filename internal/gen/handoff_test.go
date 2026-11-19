// Why: the driver hands gen's Output to resolve, which needs an origin for
// every generated name and every generated item.
// What: TestHandoff_EveryIdentHasItsOrigin, TestHandoff_PinnedTypeOrigins and
// TestHandoff_ItemOriginsMatchAST over the outputs of handoffOutputs.
// How: resolve's AddGenerated looks up each name token's rule by its ident's
// Span.Start and falls back to handwritten lookup when there is none, and the
// driver matches ItemOrigins to the parsed items by span. So every ident of
// Output.AST must have its TokenOrigin, every TokenOrigin must sit on an
// ident, and every parsed item must match exactly one ItemOrigin.

package gen

import (
	"testing"

	"mk/internal/ast"
	"mk/internal/mir"
	"mk/internal/types"
)

// handoffOutputs runs the hand-built generators of this package over one
// world and returns their outputs by name.
func handoffOutputs(t *testing.T) map[string]*Output {
	w := newWorld(t)
	_, user := w.structType("main", "User", false, "name", types.Str, "age", types.Int)
	pid, _ := w.structType("app", "Pair", true, "A", w.tab.Param(0))
	w.views[pid].Generics = []types.GenericParam{{Name: w.in.Intern("T")}}
	pair := w.tab.Named(pid, []types.GenericArg{types.TypeArg(types.Int)})
	store := w.item("app", "Store", types.KindInterface, &types.InterfaceDecl{})
	w.items.Get(store).Exported = true
	self := w.tab.Ref(w.tab.SelfType())
	w.views[store] = &DeclView{Item: store, Kind: types.KindInterface, Exported: true, Methods: []MethodView{{Sig: types.Signature{
		Name: w.in.Intern("Get"), Receiver: types.ByShared, Result: types.Str,
		Params: []types.Param{{Name: w.in.Intern("self"), Type: self}, {Name: w.in.Intern("k"), Type: types.Int}}}}}}
	// A pinned type of another module, spliced into a quote.
	pin := w.gen1("Pin", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		return x.emit(0, "var p: $0 = x\nP(q: $0): $0 { return q }", cp(tr))
	})
	out := map[string]*Output{}
	for _, c := range []struct {
		name string
		gen  types.ItemID
		arg  Arg
	}{
		{"derive.Eq", w.deriveEq(), typeArg(user)},
		{"derive.Hash", w.deriveHash(), typeArg(user)},
		{"derive.Format", w.deriveFormat(), typeArg(user)},
		{"dyn.Of", w.dynOf(), Arg{Kind: ArgInterface, Item: store}},
		{"lib.Pin", pin, typeArg(pair)},
	} {
		o := w.context(1).Run(w.module("main"), []Line{w.line(0, c.name, c.gen, c.arg)})
		noDiags(t, o)
		if o.AST == nil || len(o.AST.Items) == 0 {
			t.Fatalf("%s: no parsed items:\n%s", c.name, o.Text())
		}
		out[c.name] = o
	}
	return out
}

// TestHandoff_EveryIdentHasItsOrigin: the TokenOrigins and the idents of
// Output.AST are the same set of offsets, and the ident's text is the
// origin's token. A missing entry would make resolve look a quote literal
// up as handwritten code of the output module, with no diagnostic.
func TestHandoff_EveryIdentHasItsOrigin(t *testing.T) {
	for name, out := range handoffOutputs(t) {
		t.Run(name, func(t *testing.T) {
			origins := map[uint32]TokenOrigin{}
			for i, o := range out.Origins {
				if i > 0 && out.Origins[i-1].Offset >= o.Offset {
					t.Fatalf("Origins not sorted by Offset at %d: %+v", i, out.Origins[i-1:i+1])
				}
				origins[o.Offset] = o
			}
			idents := map[uint32]bool{}
			ast.Inspect(out.AST, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok || id == nil {
					return true
				}
				sp := id.Span()
				idents[sp.Start] = true
				if _, ok := origins[sp.Start]; !ok {
					t.Errorf("ident %q at %d has no TokenOrigin:\n%s", out.File.Text[sp.Start:sp.End], sp.Start, out.Text())
				}
				return true
			})
			for off, o := range origins {
				if !idents[off] {
					end := off
					for end < uint32(len(out.File.Text)) && isIdentByte(out.File.Text[end]) {
						end++
					}
					// The receiver `self` of a signature is a SelfParam, not an
					// Ident: resolve never looks its origin up, so the extra
					// entry is harmless.
					if w := out.File.Text[off:end]; w != "self" {
						t.Errorf("TokenOrigin %+v at %d (%q) is on no ident of the AST", o, off, w)
					}
				}
			}
		})
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// TestHandoff_PinnedTypeOrigins: a spliced Type of another module prints its
// module qualifier as OriginPinnedModule and its name as OriginPinnedItem,
// and the quote's own names stay OriginLiteral of the generator's module.
func TestHandoff_PinnedTypeOrigins(t *testing.T) {
	out := handoffOutputs(t)["lib.Pin"]
	got := map[string][]OriginKind{}
	for _, o := range out.Origins {
		end := o.Offset
		for end < uint32(len(out.File.Text)) && isIdentByte(out.File.Text[end]) {
			end++
		}
		w := out.File.Text[o.Offset:end]
		got[w] = append(got[w], o.Kind)
	}
	want := map[string]OriginKind{"app": OriginPinnedModule, "Pair": OriginPinnedItem, "int": OriginSpliced, "x": OriginLiteral, "q": OriginLiteral, "P": OriginLiteral, "p": OriginLiteral}
	for w, k := range want {
		if len(got[w]) == 0 {
			t.Fatalf("no origin for %q:\n%s", w, out.Text())
		}
		for _, g := range got[w] {
			if g != k {
				t.Errorf("%q: origin %d, want %d", w, g, k)
			}
		}
	}
}

// TestHandoff_ItemOriginsMatchAST: each parsed item's span equals exactly one
// ItemOrigin's Output span, so the driver can build resolve's NodeOrigins by
// span (Output.AST.Items drops bad items, so the lists are not parallel).
func TestHandoff_ItemOriginsMatchAST(t *testing.T) {
	for name, out := range handoffOutputs(t) {
		t.Run(name, func(t *testing.T) {
			if len(out.AST.Items) != len(out.Items) {
				t.Fatalf("%d parsed items, %d item origins", len(out.AST.Items), len(out.Items))
			}
			for _, it := range out.AST.Items {
				n := 0
				for _, o := range out.Items {
					if o.Output == it.Span() {
						n++
					}
				}
				if n != 1 {
					sp := it.Span()
					t.Errorf("item %T %v (%q) matches %d ItemOrigins: %+v", it, sp, out.File.Text[sp.Start:sp.End], n, out.Items)
				}
				if o, ok := out.OriginOf(it); !ok || o.Output != it.Span() {
					t.Errorf("OriginOf(%T) = %+v, %v", it, o, ok)
				}
			}
		})
	}
}
