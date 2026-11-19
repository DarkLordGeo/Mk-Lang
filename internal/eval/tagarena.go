// Why: std's Arena.Reset (`ends self`, std/core/mem_arena.mk) keeps its
// first and current chunks and bumps them from their start, so their pages
// stay live and no page free marks the objects they held dead; without help
// the tagged mode would miss a use of an object after its arena was reset.
//
// What: tgArenaReset, called at the Reset's call, tgEndOwn and
// tgRestartOwn, with the helpers that read the arena's chunk list (peek,
// peekPtr, structField, rawElem).
//
// How: at the call, every chunk's bytes past its record (the first chunk's
// past the header too) become ended, in a new activation, so a later access
// through a reference made before the Reset is VDeadScope with Removed at
// the Reset's call: no execution reads memory whose lifetime has ended,
// whether or not the bytes were handed out again. The arena's own write of a
// fresh object starts the bytes' next activation (tacc, the own-memory
// counterpart of restartEnded). The chunks Reset gives back die through the
// page source's free, as any freed page does (VDead).

package eval

import (
	"mk/internal/source"
	"mk/internal/types"
)

// arenaResetKey is std's Arena.Reset (std/core/mem_arena.mk).
var arenaResetKey = types.MemberKey("std.core", "Arena", "Reset")

// maxResetChunks bounds the chunk walk against a malformed list.
const maxResetChunks = 1 << 20

// resetRec is one range of own memory an arena's Reset ended (tagAlloc
// resets): the bytes [lo, hi), the activation they moved to, and the call.
type resetRec struct {
	lo, hi, gen uint32
	at          source.Span
}

// endedAt is the span of the Reset that ended the activation tag ti was made
// in at byte o, or, for an owner access (ti nil), the activation gen ended;
// ok is false when no Reset covers o.
func (ta *tagAlloc) endedAt(o uint32, ti *taginfo, gen uint32) (source.Span, bool) {
	for _, r := range ta.resets {
		if o < r.lo || o >= r.hi {
			continue
		}
		if (ti != nil && r.gen > ti.gen) || (ti == nil && r.gen == gen) {
			return r.at, true
		}
	}
	return source.Span{}, false
}

// peek reads the n-byte word at off of live frame or own allocation a as the
// host's own bookkeeping: no tagged access, no refusal, no step. ok is false
// when the bytes are not there; ptr reports whether a pointer word starts at
// off.
func (m *Machine) peek(a, off uint32, n int) (v uint64, ptr, ok bool) {
	if !m.mem.isLive(a) {
		return 0, false, false
	}
	if k := m.mem.kindOf(a); k != allocFrame && k != allocOwn {
		return 0, false, false
	}
	al := m.mem.at(a)
	if uint64(off)+uint64(n) > uint64(len(al.bytes)) {
		return 0, false, false
	}
	return types.LoadUint(al.bytes[off:], n), al.ptr != nil && bitAt(al.ptr, off), true
}

// peekPtr is peek of a pointer word: ok only when one starts at off and it
// is not null.
func (m *Machine) peekPtr(a, off uint32) (uint64, bool) {
	w, ptr, ok := m.peek(a, off, 8)
	return w, ok && ptr && w != 0
}

// structField is the layout of the field named name of the struct ti (Fields
// are in declaration order, which is the layout order).
func (m *Machine) structField(ti *tinfo, name string) (types.FieldLayout, *tinfo, bool) {
	if ti.ty.Kind != types.KNamed || ti.layout == nil {
		return types.FieldLayout{}, nil, false
	}
	sd, _ := m.items().Get(ti.ty.Item).Decl.(*types.StructDecl)
	if sd == nil || len(sd.Fields) != len(ti.layout.Fields) {
		return types.FieldLayout{}, nil, false
	}
	for i, fd := range sd.Fields {
		if m.cfg.Interner.Str(fd.Name) == name {
			fl := ti.layout.Fields[i]
			return fl, m.ti(fl.Type), true
		}
	}
	return types.FieldLayout{}, nil, false
}

// rawElem is the type a raw pointer field points to.
func (m *Machine) rawElem(ti *tinfo) (*tinfo, bool) {
	if ti == nil || ti.ty.Kind != types.KRawPtr {
		return nil, false
	}
	return m.ti(ti.ty.Elem), true
}

// tgArenaReset ends what the arena Reset's frame f was called on held (the
// call is at, in the caller's body in). It reads the arena's header and
// walks its chunk list as Reset's body is about to: each chunk's bytes past
// its record end, the first chunk's past the header it holds. A header with
// no chunk (a compile-time fixture's) ends its current bytes [base,
// base+used). Any std layout it does not recognise leaves the run as it was.
func (m *Machine) tgArenaReset(f *frame, at source.Span, in types.ItemID) {
	if f.code.body.ParamCount < 1 {
		return
	}
	self := f.local(1) // self: mut Arena
	if self.ti.ty.Kind != types.KMut {
		return
	}
	aw, ok := m.peekPtr(self.a, self.off)
	if !ok {
		return
	}
	arena := m.ti(self.ti.ty.Elem)
	hf, hfti, ok := m.structField(arena, "hdr")
	if !ok {
		return
	}
	hw, ok := m.peekPtr(ptrAlloc(aw), ptrOff(aw)+uint32(hf.Offset))
	if !ok {
		return
	}
	hdr, ok := m.rawElem(hfti)
	if !ok {
		return
	}
	cf, cfti, ok1 := m.structField(hdr, "chunks")
	bf, _, ok2 := m.structField(hdr, "base")
	uf, _, ok3 := m.structField(hdr, "used")
	chunk, ok4 := m.rawElem(cfti)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return
	}
	pf, _, ok5 := m.structField(chunk, "prev")
	sf, _, ok6 := m.structField(chunk, "size")
	if !ok5 || !ok6 {
		return
	}
	ha, ho := ptrAlloc(hw), ptrOff(hw)
	c, ok := m.peekPtr(ha, ho+uint32(cf.Offset))
	if !ok {
		// No chunk: only the bump position resets, over the current bytes.
		base, okb := m.peekPtr(ha, ho+uint32(bf.Offset))
		used, _, oku := m.peek(ha, ho+uint32(uf.Offset), 8)
		if okb && oku && used <= maxAlloc {
			m.tgEndOwn(ptrAlloc(base), ptrOff(base), uint64(used), at, in)
		}
		return
	}
	for n := 0; c != 0 && n < maxResetChunks; n++ {
		ca, co := ptrAlloc(c), ptrOff(c)
		size, _, oks := m.peek(ca, co+uint32(sf.Offset), 8)
		prev, ptr, okp := m.peek(ca, co+uint32(pf.Offset), 8)
		if !oks || !okp || (prev != 0 && !ptr) || int64(size) < 0 {
			return
		}
		room := uint64(chunk.size)
		if prev == 0 && ha == ca && ho >= co {
			// The first chunk: its record, then the header (Arena.New).
			room = uint64(ho-co) + hdr.size
			room = (room + 7) &^ 7
		}
		if size > room {
			m.tgEndOwn(ca, co+uint32(room), size-room, at, in)
		}
		c = prev
	}
}

// tgEndOwn ends n bytes at off of own allocation a (an arena's Reset at at,
// called from in): every item above the owner is removed, with at as its
// removal site (a protected one, a live call's loan on an object of the
// arena, is VProtected), the interior marks go (the bytes may hold another
// type next), and the bytes move to one new activation, ended until the
// arena writes them again (tacc).
func (m *Machine) tgEndOwn(a, off uint32, n uint64, at source.Span, in types.ItemID) {
	if n == 0 || !m.mem.isLive(a) || m.mem.kindOf(a) != allocOwn {
		return
	}
	ta := m.tgAlloc(a)
	if ta == nil || uint64(off) >= uint64(ta.size) {
		return
	}
	lo, hi := off, uint32(min(uint64(off)+n, uint64(ta.size)))
	tg := m.tg
	i, j := ta.split(lo, hi)
	gen := uint32(0)
	for k := i; k < j; k++ {
		gen = max(gen, ta.segs[k].gen)
	}
	gen++
	for k := i; k < j; k++ {
		s := &ta.segs[k]
		for _, it := range s.stack {
			if it.tag == 0 {
				continue
			}
			if it.prot != 0 {
				v := Violation{Kind: VProtected, At: at, In: in, Tag: it.tag, Made: tg.made(it.tag), Removed: at,
					Where: m.where(a, s.lo), Off: s.lo, Len: s.hi - s.lo}
				if int(it.prot) < len(tg.callItems) {
					v.Holder = tg.callItems[it.prot]
				}
				m.violation(v)
			}
			if t := &tg.tags[it.tag]; !t.removed.IsValid() {
				t.removed = at
			}
		}
		s.stack = ownerOnly(false)
		s.ended, s.interior, s.gen = true, false, gen
	}
	ta.resets = append(ta.resets, resetRec{lo: lo, hi: hi, gen: gen, at: at})
	ta.merge()
}

// tgRestartOwn is an owner write to segments [i, j) of own allocation ta:
// ended bytes (an arena's Reset) start their next activation, live with the
// owner alone and no interior mark; the activation count stays, so a tag
// made before the Reset stays dead (as restartEnded for a frame local).
func (ta *tagAlloc) tgRestartOwn(i, j int) {
	for k := i; k < j; k++ {
		if s := &ta.segs[k]; s.ended {
			s.ended, s.interior = false, false
			s.stack = ownerOnly(false)
		}
	}
}
