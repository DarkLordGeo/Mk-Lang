// Why: the lowering of `try scope` and std's public view and carrier facts
// depend on layouts and bits that only check's own tables show.
// What: TestScopeErrOffsetAndFreeze and TestViewAndCarrierBits.
// How: checks std through stage 5 and reads Scope[E]'s err offset, Scope's
// Freeze bit and the view and carrier bits of std's types, which no mk check
// probe can tell apart. Package std's TestScopeLayout and
// TestStdKindsPinned pin the rest through mk check.

package stdtest

import (
	"testing"

	"mk/internal/check"
	"mk/internal/types"
)

// checkedStd is std modules through check's stage 5 (signatures, shapes
// and layouts), for x86_64-windows; a layout of these types does not depend
// on the target (int is 8 bytes on every target).
// checkStd loads std.raw and std.core.
type checkedStd struct {
	*stdWorld
	c       *check.Checker
	tab     *types.Table
	known   types.KnownItems
	checked []*check.Module // one per stdWorld.mods entry, same order
}

func checkStd(t *testing.T) *checkedStd {
	t.Helper()
	return checkStdMods(t, baseMods)
}

// checkStdMods runs stage 5 over mods (resolveStdMods's order and rule)
// for x86_64-windows.
func checkStdMods(t *testing.T, mods []modDir) *checkedStd {
	t.Helper()
	return checkStdModsFor(t, mods, types.X86_64Windows)
}

// checkStdModsFor runs stage 5 over mods for target; mods holding std.sys
// must name the target's directory (sysMod).
func checkStdModsFor(t *testing.T, mods []modDir, target types.Target) *checkedStd {
	t.Helper()
	w := resolveStdMods(t, mods)
	cs := &checkedStd{stdWorld: w, known: w.rs.Known()}
	consts := types.NewConstTable()
	cs.tab = types.NewTable(w.in, w.items, consts)
	cs.c = check.New(&check.Config{Interner: w.in, Types: cs.tab, Items: w.items, Consts: consts, Known: &cs.known, Target: target})
	for _, m := range w.mods {
		mod := cs.c.Signatures(&check.ModuleInput{Module: m.id, Files: m.files, Info: m.info})
		if mod == nil {
			t.Fatalf("%s: no checked module", m.path)
		}
		cs.checked = append(cs.checked, mod)
	}
	return cs
}

// item is the std item with key k.
func (cs *checkedStd) item(t *testing.T, k types.Key) types.ItemID {
	t.Helper()
	id, ok := cs.items.ByKey(k)
	if !ok {
		t.Fatalf("no std item %s", k)
	}
	return id
}

// TestScopeErrOffsetAndFreeze: Scope is 128 bytes at align 8 and not
// Freeze; Scope[OutOfMemory] and Scope[OutOfMemory|Canceled] hold the
// scope record at offset 0 and err at offset 128 (lower's `try scope` reads
// it there), and are not Freeze either.
func TestScopeErrOffsetAndFreeze(t *testing.T) {
	cs := checkStd(t)
	scope := cs.tab.Named(cs.known.Scope, nil)
	l := cs.c.Layout(scope)
	if l.Shape.Size != 128 || l.Shape.Align != 8 {
		t.Errorf("Scope: size %d align %d, want 128 and 8", l.Shape.Size, l.Shape.Align)
	}
	if cs.c.Facts(scope).Freeze {
		t.Errorf("Scope is Freeze; want not (its record changes under &Scope)")
	}
	oom := types.ErrorMember{Item: cs.known.OutOfMemory}
	canc := types.ErrorMember{Item: cs.known.Canceled}
	for _, set := range [][]types.ErrorMember{{oom}, {oom, canc}} {
		es := cs.tab.ErrorSet(set)
		st := cs.tab.Named(cs.known.Scope, []types.GenericArg{types.TypeArg(cs.tab.ErrorSetType(es))})
		name := cs.tab.TypeString(st, nil)
		sl := cs.c.Layout(st)
		if len(sl.Fields) != 2 {
			t.Errorf("%s: %d fields, want hdr and err", name, len(sl.Fields))
			continue
		}
		errT := cs.tab.ErrorUnion(es, types.Void)
		el := cs.c.Layout(errT)
		want := (128 + el.Shape.Size + 7) / 8 * 8
		if sl.Fields[0].Offset != 0 || sl.Fields[1].Offset != 128 || sl.Shape.Size != want || sl.Shape.Align != 8 {
			t.Errorf("%s: offsets %d and %d, size %d align %d; want 0, 128, %d, 8", name, sl.Fields[0].Offset, sl.Fields[1].Offset, sl.Shape.Size, sl.Shape.Align, want)
		}
		if cs.c.Facts(st).Freeze {
			t.Errorf("%s is Freeze; want not", name)
		}
	}
}

// TestViewAndCarrierBits: the view and carrier bits check computes for
// std.core's types equal what doc/reference.md states, read
// from check's own facts (package std's TestStdViewsPinned pins the
// same through mk check probes): every view is a carrier; Cell, Duration
// and Hasher are neither.
func TestViewAndCarrierBits(t *testing.T) {
	cs := checkStd(t)
	intT := cs.tab.IntType(64, true)
	named := func(name string, args ...types.TypeID) types.TypeID {
		var ga []types.GenericArg
		for _, a := range args {
			ga = append(ga, types.TypeArg(a))
		}
		return cs.tab.Named(cs.item(t, types.ItemKey("std.core", name)), ga)
	}
	rows := []struct {
		name    string
		ty      types.TypeID
		view    bool
		carrier bool
	}{
		{"Arena", named("Arena"), true, true},
		{"List[int]", named("List", intT), true, true},
		{"Map[int, int]", named("Map", intT, intT), true, true},
		{"Set[int]", named("Set", intT), true, true},
		{"Text", named("Text"), true, true},
		{"Split", named("Split"), true, true},
		{"Lines", named("Lines"), true, true},
		{"Box[int]", named("Box", intT), true, true},
		{"HeapList[int]", named("HeapList", intT), true, true},
		{"HeapText", named("HeapText"), true, true},
		{"Cell[int]", named("Cell", intT), false, false},
		{"Duration", named("Duration"), false, false},
		{"Hasher", named("Hasher"), false, false},
	}
	for _, r := range rows {
		f := cs.c.Facts(r.ty)
		if f.View != r.view || f.Carrier != r.carrier {
			t.Errorf("%s: view %v carrier %v, want %v %v", r.name, f.View, f.Carrier, r.view, r.carrier)
		}
	}
}
