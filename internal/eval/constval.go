// Why: an evaluation's result leaves the interpreter's flat memory as an
// interned types.ConstValue in canonical form, and a const read by an
// evaluation enters it again; both directions live here.
//
// What: serialize, serValue and serInto (memory to const), pointee (what a
// pointer word in a result refers to), constAlloc (a const's read-only copy
// in memory), globalRef, fnValue and fnOf.
//
// How: a depth-first walk over the type's layout on an explicit stack, with
// pointees interned before the value holding them. It zeroes padding, makes
// every NaN canonical, checks bools and gives an empty slice a [0]T pointee.
// A pointer to offset 0 of a const's copy stays that const; other memory the
// evaluation allocated is copied fresh. A var global is refused: a result may
// not hold one. Consts are materialized lazily as read-only allocations.

package eval

import (
	"encoding/binary"

	"mk/internal/source"
	"mk/internal/types"
)

// constAlloc is the evaluation's read-only copy of const c, made once per
// evaluation and filled on first access.
func (m *Machine) constAlloc(c types.ConstID) uint32 {
	if a, ok := m.consts[c]; ok {
		return a
	}
	a := m.mem.add(allocConst, alloc{pending: true, cid: c})
	m.consts[c] = a
	return a
}

// materializeConst fills a pending const copy: its bytes, its RelocData words
// as pointers to the pointees' copies (themselves pending), its RelocFn words
// as function values. Reading a const costs no steps.
func (m *Machine) materializeConst(a uint32) {
	v := m.cfg.Consts.Get(m.mem.at(a).cid)
	b := m.mem.pool.get(len(v.Bytes))
	copy(b, v.Bytes)
	m.mem.at(a).bytes = b
	m.mem.at(a).pending = false
	for _, r := range v.Relocs {
		var w uint64
		switch r.Kind {
		case types.RelocData:
			w = ptrWord(m.constAlloc(r.Data), 0)
		case types.RelocFn:
			w = m.fnValue(r.Fn)
		}
		al := m.mem.at(a)
		types.StoreUint(al.bytes[r.Offset:], 8, w)
		al.setTag(r.Offset)
	}
}

// fnValue is the pointer word of a function value: one allocFn allocation
// per distinct FnRef per evaluation.
func (m *Machine) fnValue(fn types.FnRef) uint64 {
	k := m.fnKey(fn)
	if a, ok := m.fns[k]; ok {
		return ptrWord(a, 0)
	}
	a := m.mem.add(allocFn, alloc{fn: types.FnRef{Item: fn.Item, Args: append([]types.GenericArg(nil), fn.Args...)}})
	m.fns[k] = a
	return ptrWord(a, 0)
}

func (m *Machine) fnKey(fn types.FnRef) string {
	b := m.keyBuf[:0]
	b = binary.AppendUvarint(b, uint64(fn.Item))
	for _, g := range fn.Args {
		if g.IsConst {
			b = append(b, 1)
			b = binary.AppendUvarint(b, uint64(g.Const.Kind))
			b = binary.AppendUvarint(b, uint64(g.Const.Param))
			b = binary.AppendUvarint(b, uint64(g.Const.Value))
			b = binary.AppendUvarint(b, uint64(g.Const.Type))
		} else {
			b = append(b, 0)
			b = binary.AppendUvarint(b, uint64(g.Type))
		}
	}
	m.keyBuf = b
	return string(b)
}

// fnOf is the function a value names; anything but a function value is
// refused.
func (m *Machine) fnOf(w uint64) types.FnRef {
	a := ptrAlloc(w)
	if w == 0 || m.mem.kindOf(a) != allocFn || !m.mem.isLive(a) || ptrOff(w) != 0 {
		refuse(types.RefuseRawAccess, "")
	}
	return m.mem.at(a).fn
}

// globalRef is &global (a pointer to memory every access to which is
// refused) or &C of a const (a pointer to its read-only copy).
// A tagged run gives a global of the unit's own modules storage of its own
// instead (tgGlobal).
func (m *Machine) globalRef(item types.ItemID) uint64 {
	if it := m.items().Get(item); it.Kind == types.KindGlobal {
		if a, ok := m.globals[item]; ok {
			return ptrWord(a, 0)
		}
		if m.tg != nil {
			return ptrWord(m.tgGlobal(it), 0)
		}
		a := m.mem.add(allocGlobal, alloc{item: item})
		m.globals[item] = a
		return ptrWord(a, 0)
	}
	return ptrWord(m.constAlloc(m.constValue(item)), 0)
}

// intConst interns an int value (an array length the walk needs).
func (m *Machine) intConst(n uint64) types.ConstID {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, n)
	return m.cfg.Consts.Intern(types.ConstValue{Type: types.Int, Align: 8, Bytes: b})
}

type serKey struct {
	a, off uint32
	t      types.TypeID
}

// serWalk is one serialization's state: the pointees on the current path
// (cycle detection) and the finished ones. Memory does not change while a
// value is serialized, so a pointee reached again by another path is the same
// const, and each (allocation, offset, type) is walked once: a value whose
// pointers share targets costs time linear in its distinct pointees, not in
// its paths.
type serWalk struct {
	onPath map[serKey]bool
	done   map[serKey]types.ConstID
}

// serFrame is one value being serialized: its bytes and relocations, and
// the pointer words whose pointees must be interned before it is (kids, in
// relocation order; next is the first not yet resolved).
type serFrame struct {
	k    serKey
	v    types.ConstValue
	kids []serKid
	next int
}

// serKid is a RelocData of a serFrame waiting for its pointee: the reloc's
// index in v.Relocs, the pointer word and the pointee's type.
type serKid struct {
	reloc int
	w     uint64
	ti    *tinfo
}

// serialize turns the value at l into a canonical interned const.
func (m *Machine) serialize(l loc) types.ConstID {
	return m.serValue(l.a, l.off, l.ti, &serWalk{onPath: map[serKey]bool{}, done: map[serKey]types.ConstID{}})
}

// serValue is the depth-first walk with pointees interned first, on an
// explicit stack of serFrames rather than the Go stack: a result holding a
// pointer chain of any length costs heap linear in it, never host stack,
// so the host's stack depth never decides whether a result is accepted. Each
// value's own bytes are written by serInto, which recurses only over its
// type's structure (bounded by the type, not the data); a pointee's walk
// starts when its parent's turn comes, in relocation order, so consts are
// interned in the order the recursive walk interned them.
func (m *Machine) serValue(a, off uint32, ti *tinfo, sw *serWalk) types.ConstID {
	root := serKey{a, off, ti.id}
	if c, ok := sw.done[root]; ok {
		return c
	}
	stack := []*serFrame{m.serOpen(root, ti, sw)}
	for {
		top := stack[len(stack)-1]
		if top.next < len(top.kids) {
			kid := &top.kids[top.next]
			c, k, ok := m.pointee(kid.w, kid.ti, sw)
			if ok {
				top.v.Relocs[kid.reloc].Data = c
				top.next++
				continue
			}
			stack = append(stack, m.serOpen(k, kid.ti, sw))
			continue
		}
		c := m.serClose(top, sw)
		stack[len(stack)-1] = nil
		stack = stack[:len(stack)-1]
		if len(stack) == 0 {
			return c
		}
		p := stack[len(stack)-1]
		p.v.Relocs[p.kids[p.next].reloc].Data = c
		p.next++
	}
}

// serOpen starts the value at k: a value already on the path is a cycle,
// refused; otherwise its bytes are written and its pointees listed.
func (m *Machine) serOpen(k serKey, ti *tinfo, sw *serWalk) *serFrame {
	if sw.onPath[k] {
		refuse(types.RefuseRawAccess, "cyclic value")
	}
	sw.onPath[k] = true
	fr := &serFrame{k: k, v: types.ConstValue{Type: ti.id, Align: ti.align, Bytes: make([]byte, ti.size)}}
	m.serInto(fr, 0, k.a, k.off, ti)
	return fr
}

// serClose interns a value whose pointees are all interned.
func (m *Machine) serClose(fr *serFrame, sw *serWalk) types.ConstID {
	delete(sw.onPath, fr.k)
	if err := fr.v.Canonicalize(m.cfg.Target.PointerBytes); err != nil {
		source.ICEf(source.Span{}, "eval: %v", err)
	}
	c := m.cfg.Consts.Intern(fr.v)
	sw.done[fr.k] = c
	return c
}

// serPtr appends a RelocData at at whose pointee, the target of w with type
// ti, the walk interns before fr's value.
func (fr *serFrame) serPtr(at uint64, w uint64, ti *tinfo) {
	fr.v.Relocs = append(fr.v.Relocs, types.Reloc{Offset: uint32(at), Kind: types.RelocData})
	fr.kids = append(fr.kids, serKid{reloc: len(fr.v.Relocs) - 1, w: w, ti: ti})
}

// serInto writes the value of type ti at (a, off) into fr's bytes at at,
// and its relocations; a data pointer's pointee is left to serValue.
func (m *Machine) serInto(fr *serFrame, at uint64, a, off uint32, ti *tinfo) {
	v := &fr.v
	l := loc{a: a, off: off, ti: ti}
	ty := ti.ty
	switch ti.class {
	case cVoid:
		return
	case cInt, cBool, cChar:
		types.StoreUint(v.Bytes[at:], ti.width, m.load(l)) // load checks bools are 0 or 1, and tags
		return
	case cFloat:
		x := m.load(l)
		if ti.width == 4 {
			x = f32bits(f32of(x)) // canonical NaN
		} else {
			x = f64bits(f64of(x))
		}
		types.StoreUint(v.Bytes[at:], ti.width, x)
		return
	case cPtr:
		w := m.load(l)
		if w == 0 {
			return // null, none of a niche optional: zero, no reloc
		}
		elemKind := ty.Kind
		elem := ty.Elem
		if ty.Kind == types.KOptional {
			et := m.tab().Get(ty.Elem)
			elemKind, elem = et.Kind, et.Elem
		}
		if elemKind == types.KFn {
			v.Relocs = append(v.Relocs, types.Reloc{Offset: uint32(at), Kind: types.RelocFn, Fn: m.fnOf(w)})
			return
		}
		fr.serPtr(at, w, m.ti(elem))
		return
	}
	lay := ti.layout
	if ty.Kind == types.KNamed && m.items().Get(ty.Item).Key.String() == maskKey {
		// A std.simd.Mask's lanes are all zeros or all ones of T's width,
		// declared [N]T only for the size: its bytes are copied as they
		// are, so an f32 mask lane of all ones is not taken for a NaN (NaNs
		// are made canonical in float values, and a mask lane is none).
		if ti.size > 0 {
			copy(v.Bytes[at:at+ti.size], m.readBytes(ptrWord(a, off), ti.size, 0))
		}
		return
	}
	switch ty.Kind {
	case types.KSlice, types.KMutSlice, types.KStr:
		data, n := m.sliceWords(l)
		types.StoreUint(v.Bytes[at+8:], 8, n)
		var et types.TypeID = types.U8
		if ty.Kind != types.KStr {
			et = ty.Elem
		}
		arr := m.tab().Array(types.ConstArg{Kind: types.CAValue, Value: m.intConst(n)}, et)
		if n == 0 {
			// An empty slice relocates to the [0]T const, as check's and
			// lower's "" do: a zero word with no reloc is a null pointer,
			// which native code may not hold (!nonnull).
			// There is no memory to walk, whatever the data word is.
			data := m.cfg.Consts.Intern(types.ConstValue{Type: arr, Align: max(m.ti(arr).align, 1)})
			v.Relocs = append(v.Relocs, types.Reloc{Offset: uint32(at), Kind: types.RelocData, Data: data})
			return
		}
		fr.serPtr(at, data, m.ti(arr))
	case types.KArray:
		n := m.arrayLen(ty)
		elem := m.ti(ty.Elem)
		if elem.size == 0 {
			return // no bytes and no pointers, however many elements
		}
		if elem.class == cInt && !elem.enum {
			// Plain integers: one checked block copy.
			nb := n * elem.size
			if nb > 0 {
				src := m.readBytes(ptrWord(a, off), nb, 0)
				copy(v.Bytes[at:at+nb], src)
			}
			return
		}
		for i := uint64(0); i < n; i++ {
			m.serInto(fr, at+i*elem.size, a, off+uint32(i*elem.size), elem)
		}
	case types.KOptional:
		some := m.tagAt(l, lay)
		types.StoreUint(v.Bytes[at+lay.Tag.Offset:], int(lay.Tag.Size), some)
		if some == 1 {
			fl := lay.Variants[1].Fields[0]
			m.serInto(fr, at+fl.Offset, a, off+uint32(fl.Offset), m.ti(fl.Type))
		}
	case types.KErrorUnion, types.KErrorSet, types.KNamed:
		if !lay.HasTag {
			m.serFields(fr, at, a, off, lay.Fields)
			return
		}
		tag := m.tagAt(l, lay)
		var vl *types.VariantLayout
		for i := range lay.Variants {
			if lay.Variants[i].Discr == tag {
				vl = &lay.Variants[i]
			}
		}
		if vl == nil {
			panicWith(types.PanicEnumTag, "")
		}
		types.StoreUint(v.Bytes[at+lay.Tag.Offset:], int(lay.Tag.Size), tag)
		m.serFields(fr, at, a, off, vl.Fields)
	case types.KRecord:
		m.serFields(fr, at, a, off, lay.Fields)
	default:
		source.ICEf(source.Span{}, "eval: serializing %s", ty.Kind)
	}
}

func (m *Machine) serFields(fr *serFrame, at uint64, a, off uint32, fs []types.FieldLayout) {
	for _, f := range fs {
		m.serInto(fr, at+f.Offset, a, off+uint32(f.Offset), m.ti(f.Type))
	}
}

// pointee resolves a pointer word's target: the original const when the
// word points at offset 0 of a const copy of exactly that type (&Limit
// stays a reference to Limit), or a copy already interned by this walk
// (ok true); else the key of the memory to copy fresh, which serValue walks
// next (ok false; slices and strs in the result become static data). A var
// global is refused.
func (m *Machine) pointee(w uint64, ti *tinfo, sw *serWalk) (types.ConstID, serKey, bool) {
	a, off := ptrAlloc(w), ptrOff(w)
	switch m.mem.kindOf(a) {
	case allocGlobal:
		refuse(types.RefuseGlobal, "")
	case allocConst:
		if off == 0 && m.mem.isLive(a) {
			if cid := m.mem.at(a).cid; m.cfg.Consts.Get(cid).Type == ti.id {
				return cid, serKey{}, true
			}
		}
	}
	m.mem.access(a, off, ti.size, false)
	k := serKey{a, off, ti.id}
	if c, ok := sw.done[k]; ok {
		return c, k, true
	}
	return types.NoConst, k, false
}
