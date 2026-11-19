// Why: every error type has a provided Format, and it must be declared as
// real items so check, codegen and a written impl's duplicate check see it.
//
// What: declareErrorFormat (the impl of std.core.Format for an error and its
// Format method) and declareSynth.
//
// How: both items are synthesized in pass 1 with no node of their own (the
// error's node stands for them), so a handwritten impl gets the same key and
// becomes a DuplicateImpl; check decides whether the synthesized ones stay.

package resolve

import (
	"mk/internal/ast"
	"mk/internal/source"
	"mk/internal/types"
)

// declareErrorFormat declares the impl of std.core.Format for error r and
// its Format method, when the module is the prelude module or
// sees it (every user module, through the prelude). It runs in pass 1, so
// a written `impl Format for E` of this module, declared in pass 2, has the
// same key and becomes a DuplicateImpl that check reports as E0704.
// The items have no node of their own: their ItemInfo.Node is the error's,
// and the error's Ref is left alone. Check decides whether they stay.
func (m *Module) declareErrorFormat(fs *fileState, d *ast.ErrorDecl, r *itemRec) {
	core := m.s.cfg.PreludePath
	if core == "" {
		return
	}
	if m.mod.Path != core && m.in.Prelude == types.NoModule {
		if _, ok := m.in.Imports[core]; !ok {
			return
		}
	}
	key := types.ImplKey(m.mod.Path, core+".Format", r.item.Key.String())
	impl := m.declareSynth(fs, d, key, types.KindImpl, source.NoSymbol, r, nil)
	if impl == nil {
		return
	}
	name := m.s.cfg.Interner.Intern("Format")
	m.declareSynth(fs, d, types.ImplMemberKey(key, "Format"), types.KindFunc, name, r, impl)
}

// declareSynth adds one synthesized item made from error rec errRec. It has
// no task of its own (errRec is its parent record, so newTasks skips it):
// nothing in it is resolved. owner is the impl of a method.
func (m *Module) declareSynth(fs *fileState, n ast.Node, key types.Key, kind types.ItemKind, name source.Symbol, errRec, owner *itemRec) *itemRec {
	if _, ok := m.keys[key]; ok {
		return nil
	}
	if _, ok := m.s.cfg.Items.ByKey(key); ok {
		return nil
	}
	at := errRec.item.Span
	m.keys[key] = at
	it := &types.Item{Module: m.mod.ID, Name: name, Kind: kind, Span: at, Key: key}
	if owner != nil {
		it.Owner = owner.item.ID
	}
	m.s.cfg.Items.AddItem(it)
	r := &itemRec{
		info: &ItemInfo{Item: it.ID, File: fs.idx, Node: n.ID(), Synth: SynthErrorFormat},
		item: it, node: n, file: fs.idx, parent: errRec,
	}
	if owner != nil {
		r.info.Parent = owner.item.ID
		owner.deps = append(owner.deps, r)
	}
	m.recs = append(m.recs, r)
	m.byID[it.ID] = r
	m.mod.Items = append(m.mod.Items, it.ID)
	return r
}
