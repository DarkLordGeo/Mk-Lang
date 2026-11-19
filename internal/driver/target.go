// Why: the universe name Target is a compile-time constant of type
// TargetInfo that describes the build target, and no source file can declare
// it, because its value depends on the command line.
//
// What: targetConst, world.addTargetItem and buildTarget, which synthesize
// the const item std.target.Target, and writeTargetValue, which stores its
// value in the ConstDecl.
//
// How: the item lives in the module that declares TargetInfo (std.target),
// where resolve's KnownItems lookup finds it by its stable key. Its value is
// Config.Target laid out in TargetInfo's layout, each field matched by name
// and each enum field by variant name, so a reordered std enum cannot change
// the value.

package driver

import (
	"sync"

	"mk/internal/diag"
	"mk/internal/eval"
	"mk/internal/source"
	"mk/internal/types"
)

// targetConst is the synthesized Target item and its value, computed once
// TargetInfo, Arch and Os are checked (after their module's Signatures).
// A computation that finds them unchecked is not cached.
type targetConst struct {
	item types.ItemID
	mu   sync.Mutex
	val  types.ConstID // NoConst until computed
}

// targetModule is the std module that declares TargetInfo, Arch and Os
// (std/target): layer 0's lowest, below std.core and std.simd, so
// std.simd's freestanding forms read Target.os below the prelude.
const targetModule = "std.target"

// targetHome is the module whose TargetInfo the build uses: std.target, or
// the prelude module for a std that declares TargetInfo there (the stand-in
// stds of the driver's tests), as resolve's Known does.
func (w *world) targetHome() (string, types.ItemID, bool) {
	for _, mod := range []string{targetModule, preludeModule} {
		if ti, ok := w.items.ByKey(types.ItemKey(mod, "TargetInfo")); ok {
			return mod, ti, true
		}
	}
	return "", types.NoItem, false
}

// isTargetKey reports whether k is the synthesized Target item's key, in
// either home targetHome allows. Target is a universe name, so no std item
// takes either key.
func isTargetKey(k types.Key) bool {
	return k == types.ItemKey(targetModule, "Target") || k == types.ItemKey(preludeModule, "Target")
}

// addTargetItem adds the const item Target to TargetInfo's module
// (targetHome) when std declares TargetInfo, before KnownItems is read
// (startSemantics), so Known.Target names it. Target is a universe name, so
// no std item can take its key (E0102). The item is in no Module.Items: it
// has no source and is never a unit; its Decl carries only the type until
// its value is written (writeTargetValue). It runs on the session goroutine
// before any check.
func (w *world) addTargetItem() types.ItemID {
	home, ti, ok := w.targetHome()
	if !ok {
		return types.NoItem
	}
	key := types.ItemKey(home, "Target")
	if id, ok := w.items.ByKey(key); ok {
		return id
	}
	tiIt := w.items.Get(ti)
	return w.items.AddItem(&types.Item{Module: tiIt.Module, Name: source.SymTarget, Kind: types.KindConst,
		Exported: true, Span: tiIt.Span, Key: key, Decl: &types.ConstDecl{Type: w.tab.Named(ti, nil)}})
}

// value is Target's value for the build target, computing it on first use:
// Program.Const for the Target item, and writeTargetValue. ok is false
// while TargetInfo, or the enum of one of its fields, is not checked yet.
func (tc *targetConst) value(w *world) (types.ConstID, bool) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.val != types.NoConst {
		return tc.val, true
	}
	v, ok := w.buildTarget(w.sem5.known.TargetInfo)
	if ok {
		tc.val = v
	}
	return v, ok
}

// buildTarget lays out TargetInfo's value: each field is matched by name (arch,
// os, pointer_bytes, vector_bits); an enum field holds the discriminant of the
// variant named like the target's Arch or OS, so a reordered std enum cannot
// silently change the value. A field the language reference does not name stays
// zero; an Arch or Os with no such variant is an ICE.
func (w *world) buildTarget(ti types.ItemID) (types.ConstID, bool) {
	sd, ok := w.items.Get(ti).Decl.(*types.StructDecl)
	if !ok {
		return types.NoConst, false
	}
	chk := w.sem5.chk
	tt := w.tab.Named(ti, nil)
	lay := chk.Layout(tt)
	if len(lay.Fields) != len(sd.Fields) {
		source.ICEf(w.items.Get(ti).Span, "driver: TargetInfo has %d fields and %d field layouts", len(sd.Fields), len(lay.Fields))
	}
	b := make([]byte, lay.Shape.Size)
	tgt := w.cfg.Target
	for i, f := range sd.Fields {
		fl := lay.Fields[i]
		switch w.in.Str(f.Name) {
		case "arch":
			if !w.storeVariant(b, fl, tgt.Arch.String()) {
				return types.NoConst, false
			}
		case "os":
			if !w.storeVariant(b, fl, tgt.OS.String()) {
				return types.NoConst, false
			}
		case "pointer_bytes":
			n := chk.Layout(fl.Type).Shape.Size
			types.StoreUint(b[fl.Offset:], int(n), uint64(tgt.PointerBytes))
		case "vector_bits":
			// Fixed per target, never from --cpu or the profile.
			n := chk.Layout(fl.Type).Shape.Size
			types.StoreUint(b[fl.Offset:], int(n), uint64(tgt.VectorBits))
		}
	}
	return w.tab.Consts().Intern(types.ConstValue{Type: tt, Align: lay.Shape.Align, Bytes: b}), true
}

// storeVariant writes the tag of the variant called name of the enum at fl.
func (w *world) storeVariant(b []byte, fl types.FieldLayout, name string) bool {
	ft := w.tab.Get(fl.Type)
	if ft.Kind != types.KNamed {
		source.ICEf(source.Span{}, "driver: TargetInfo field of type %s is no enum", w.tab.TypeString(fl.Type, nil))
	}
	it := w.items.Get(ft.Item)
	ed, ok := it.Decl.(*types.EnumDecl)
	if !ok {
		if it.Decl == nil {
			return false
		}
		source.ICEf(it.Span, "driver: TargetInfo field of type %s is no enum", it.Key)
	}
	lay := w.sem5.chk.Layout(fl.Type)
	for j, v := range ed.Variants {
		if w.in.Str(v.Name) == name {
			types.StoreUint(b[fl.Offset+lay.Tag.Offset:], int(lay.Tag.Size), lay.Variants[j].Discr)
			return true
		}
	}
	source.ICEf(it.Span, "driver: %s has no variant %s for target %s", it.Key, name, w.cfg.Target.Name)
	return false
}

// targetProgramConst is Program.Const for the Target item: its value
// whatever the prelude module's state, since it reads nothing a module
// computes but TargetInfo's layout; poisoned while TargetInfo is unchecked.
func (p *program) targetProgramConst() eval.Const {
	v, ok := p.s.target.value(p.w)
	if !ok {
		return eval.Const{Poisoned: true}
	}
	return eval.Const{Value: v}
}

// writeTargetValue writes Target's value into its ConstDecl, on the
// goroutine of TargetInfo's module (std.target) after its declaration
// evaluations and before its state, so every module that imports it, and
// every module above (std.simd, std.core and the rest), reads a final Decl. A
// TargetInfo that did not check leaves the Decl without a value. An ICE
// (an Arch or Os with no variant for the target) is an E0000 of the module.
func (w *world) writeTargetValue(m *modNode) *diag.Diagnostic {
	s := w.sem5
	if s.target.item == types.NoItem {
		return nil
	}
	it := w.items.Get(s.target.item)
	if it.Module != m.id {
		return nil
	}
	if ie := source.Guard("value "+it.Key.String(), it.Span, func() {
		if v, ok := s.target.value(w); ok {
			d := it.Decl.(*types.ConstDecl)
			d.Value, d.HasValue = v, true
		}
	}); ie != nil {
		return diag.FromInternal(ie)
	}
	return nil
}
