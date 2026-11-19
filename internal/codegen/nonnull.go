// Why: codegen loads every str or slice data pointer, &T and mut T with
// !nonnull !noundef, so a null one in a constant makes the load poison and
// lets LLVM delete whole paths. A "" const of sixteen zero bytes once did
// exactly that: inlined in release, System.Env's `for v in env { if ... {
// return some(v.value) } }; return none` lost its loop exit, because
// `.Or("")` on the none result reached the poison load.
//
// What: constInit (a const's initializer), nonnullBytes, the nnWalk over a
// layout's nonnull words, and holdsNonnull.
//
// How: every nonnull word of a constant that is zero with no relocation is
// repaired to a dangling aligned address when it can never be dereferenced
// (an empty view, a zero-size pointee), else the build stops with an ICE.
// check points an empty const at a zero-byte const (bytesConst); this file
// makes codegen refuse to emit the null, whichever producer writes it.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/source"
	"mk/internal/types"
)

// constInit is the initializer of const value cv (a private const or a var
// global's value): byte runs and relocated pointers, packed, with every
// nonnull word checked by nonnullBytes.
func (g *packGen) constInit(cv *types.ConstValue) llvm.Value {
	m := g.m
	b := g.nonnullBytes(cv)
	var vals []llvm.Value
	at := uint32(0)
	io := g.isIoTable(cv.Type)
	for _, rl := range cv.Relocs {
		if rl.Offset > at {
			vals = append(vals, m.Bytes(b[at:rl.Offset]))
		}
		if io && g.deadIoEntry(rl) {
			vals = append(vals, g.deadEntry())
			at = rl.Offset + 8
			continue
		}
		vals = append(vals, g.relocValue(rl))
		at = rl.Offset + 8
	}
	if int(at) < len(b) {
		vals = append(vals, m.Bytes(b[at:]))
	}
	return m.ConstStruct(true, vals...)
}

// nonnullBytes is cv's bytes with every nonnull word that is zero with no
// relocation repaired, or an ICE when it cannot be. A zero-length view's data
// pointer and a reference to a zero-size value are never dereferenced, so
// any non-null address serves: they get the pointee's alignment, the
// dangling address an empty slice gets. A null data pointer of a
// non-empty view, or a null &T or mut T of a sized T, is a producer's error
// (lower or eval) that no address repairs.
func (g *packGen) nonnullBytes(cv *types.ConstValue) []byte {
	if !g.holdsNonnull(cv.Type) {
		return cv.Bytes
	}
	w := nnWalk{g: g, cv: cv, b: cv.Bytes}
	if len(cv.Relocs) > 0 {
		w.reloc = make(map[uint64]bool, len(cv.Relocs))
		for _, r := range cv.Relocs {
			w.reloc[uint64(r.Offset)] = true
		}
	}
	w.walk(cv.Type, 0)
	return w.b
}

// nnWalk walks one const's bytes by its type.
type nnWalk struct {
	g     *packGen
	cv    *types.ConstValue
	b     []byte // cv.Bytes, copied on the first repair
	reloc map[uint64]bool
}

func (w *nnWalk) walk(t types.TypeID, off uint64) {
	g := w.g
	if !g.holdsNonnull(t) {
		return
	}
	ty := g.tt.Get(t)
	switch ty.Kind {
	case types.KStr, types.KSlice, types.KMutSlice:
		if !w.null(off) {
			return
		}
		if off+16 > uint64(len(w.b)) || types.LoadUint(w.b[off+8:], 8) != 0 {
			source.ICEf(source.Span{}, "codegen: const %s at byte %d: a non-empty %s with a null data pointer", g.tt.TypeString(w.cv.Type, nil), off, g.tt.TypeString(t, nil))
		}
		align := uint32(1)
		if ty.Kind != types.KStr {
			_, align = g.sizeAlign(ty.Elem)
		}
		w.repair(off, align)
	case types.KRef, types.KMut:
		if !w.null(off) {
			return
		}
		size, align := g.sizeAlign(ty.Elem)
		if size != 0 {
			source.ICEf(source.Span{}, "codegen: const %s at byte %d: a null %s", g.tt.TypeString(w.cv.Type, nil), off, g.tt.TypeString(t, nil))
		}
		w.repair(off, align)
	case types.KArray:
		es, _ := g.sizeAlign(ty.Elem)
		if es == 0 {
			return
		}
		n := g.reprOf(t).size / es
		for i := uint64(0); i < n; i++ {
			w.walk(ty.Elem, off+i*es)
		}
	default:
		l := g.env.Layout(t)
		if l == nil {
			return
		}
		for _, f := range l.Fields {
			w.walk(f.Type, off+f.Offset)
		}
		if !l.HasTag || l.Tag.Niche {
			return
		}
		to := off + l.Tag.Offset
		if to+uint64(l.Tag.Size) > uint64(len(w.b)) {
			return
		}
		tag := types.LoadUint(w.b[to:], int(l.Tag.Size))
		mask := ^uint64(0) >> (64 - 8*uint(l.Tag.Size))
		for _, v := range l.Variants {
			if v.Discr&mask == tag {
				for _, f := range v.Fields {
					w.walk(f.Type, off+f.Offset)
				}
				return
			}
		}
	}
}

// null reports whether the pointer word at off is zero with no relocation.
func (w *nnWalk) null(off uint64) bool {
	if w.reloc[off] || off+8 > uint64(len(w.b)) {
		return false
	}
	return types.LoadUint(w.b[off:], 8) == 0
}

// repair writes the dangling address align (at least 1) at off.
func (w *nnWalk) repair(off uint64, align uint32) {
	if &w.b[0] == &w.cv.Bytes[0] {
		w.b = append([]byte(nil), w.b...)
	}
	types.StoreUint(w.b[off:], 8, uint64(max(align, 1)))
}

// holdsNonnull reports whether a value of type t has a word codegen loads as
// !nonnull: a str or slice data pointer, &T or mut T, at any depth by value.
// Memoized per pack; a type never holds itself by value.
func (g *packGen) holdsNonnull(t types.TypeID) bool {
	if v, ok := g.nonnull[t]; ok {
		return v
	}
	if g.nonnull == nil {
		g.nonnull = map[types.TypeID]bool{}
	}
	g.nonnull[t] = false
	ty := g.tt.Get(t)
	v := false
	switch ty.Kind {
	case types.KStr, types.KSlice, types.KMutSlice, types.KRef, types.KMut:
		v = true
	case types.KArray:
		v = g.holdsNonnull(ty.Elem)
	case types.KNamed, types.KRecord, types.KOptional, types.KErrorUnion, types.KErrorSet:
		if ty.Kind == types.KOptional && types.NicheOptional(g.tt.Kind(ty.Elem)) {
			break // none is the zero word
		}
		if l := g.env.Layout(t); l != nil {
			for _, f := range l.Fields {
				v = v || g.holdsNonnull(f.Type)
			}
			for _, vr := range l.Variants {
				for _, f := range vr.Fields {
					v = v || g.holdsNonnull(f.Type)
				}
			}
		}
	}
	g.nonnull[t] = v
	return v
}
