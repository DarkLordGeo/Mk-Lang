// Why: compile-time code may use raw pointers, so the evaluator's memory
// must refuse what it cannot model and keep pointers intact where it can.
// What: memory-level tests: raw access outside the evaluation's own
// allocations, raw writes to read-only const copies and any access to a var
// global are refused; a copy cutting a pointer word, pointer comparison and
// zeroed pooled slices. The MIR forms through std.raw are in intrinsic_test.go.
// How: drives the memory layer (copyBytes and friends) directly.

package eval

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// refusal runs fn and returns the refusal it stopped with, 0 for none.
func refusal(t *testing.T, fn func()) (r types.Refusal) {
	t.Helper()
	defer func() {
		if x := recover(); x != nil {
			s, ok := x.(*stop)
			if !ok {
				panic(x)
			}
			if s.err == nil || s.err.Kind != types.EvalRefused {
				t.Fatalf("stopped with %+v, want a refusal", s.err)
			}
			r = s.err.Refusal
		}
	}()
	fn()
	return 0
}

func newMem() *memory {
	m := newMemory(&bytePool{})
	m.materialize = func(uint32) {}
	return &m
}

const somePtr = uint64(3)<<32 | 5

func TestPointerBytesRefused(t *testing.T) {
	m := newMem()
	a := m.newAlloc(allocOwn, 32)
	al := m.access(a, 8, 8, true)
	al.storePtr(8, somePtr)
	for _, c := range []struct{ off, w uint32 }{{8, 8}, {8, 1}, {12, 4}, {15, 1}, {4, 8}, {1, 8}} {
		if r := refusal(t, func() { al.loadScalar(c.off, int(c.w)) }); r != types.RefusePointerBytes {
			t.Errorf("scalar load of %d bytes at %d over a pointer at 8: refusal %v", c.w, c.off, r)
		}
	}
	if r := refusal(t, func() { al.loadScalar(16, 8) }); r != 0 {
		t.Errorf("load after the pointer refused: %v", r)
	}
	if r := refusal(t, func() { al.loadScalar(0, 1) }); r != 0 {
		t.Errorf("load of byte 0 refused: %v", r)
	}
	if got := al.loadPtr(8); got != somePtr {
		t.Errorf("pointer load = %#x, want %#x", got, somePtr)
	}
}

func TestIntegerAsPointerRefused(t *testing.T) {
	m := newMem()
	a := m.newAlloc(allocOwn, 16)
	al := m.access(a, 0, 16, true)
	al.storeScalar(0, 8, somePtr)
	if r := refusal(t, func() { al.loadPtr(0) }); r != types.RefusePointerBytes {
		t.Errorf("integer read as a pointer: refusal %v", r)
	}
	if got := al.loadPtr(8); got != 0 {
		t.Errorf("eight zero bytes are null, got %#x", got)
	}
}

func TestRawAccessOutside(t *testing.T) {
	m := newMem()
	a := m.newAlloc(allocOwn, 8)
	for name, fn := range map[string]func(){
		"allocation 0":    func() { m.access(0, 0, 1, false) },
		"unknown":         func() { m.access(99, 0, 1, false) },
		"past the end":    func() { m.access(a, 4, 8, false) },
		"offset past end": func() { m.access(a, 9, 0, false) },
	} {
		if r := refusal(t, fn); r != types.RefuseRawAccess {
			t.Errorf("%s: refusal %v, want raw-access", name, r)
		}
	}
	m.free(a)
	if r := refusal(t, func() { m.access(a, 0, 1, false) }); r != types.RefuseRawAccess {
		t.Errorf("dead allocation: refusal %v", r)
	}
	g := m.add(allocGlobal, alloc{})
	if r := refusal(t, func() { m.access(g, 0, 0, false) }); r != types.RefuseGlobal {
		t.Errorf("var global: refusal %v, want global", r)
	}
}

func TestRawAccessInsideOwn(t *testing.T) {
	m := newMem()
	a := m.newAlloc(allocOwn, 24)
	if r := refusal(t, func() {
		al := m.access(a, 0, 24, true)
		al.storeScalar(16, 8, 42)
		if al.loadScalar(16, 8) != 42 {
			t.Errorf("own memory does not read back")
		}
		m.copyBytes(a, 0, a, 16, 8)
		if m.access(a, 0, 8, false).loadScalar(0, 8) != 42 {
			t.Errorf("copy inside own memory lost the value")
		}
	}); r != 0 {
		t.Errorf("access inside own memory refused: %v", r)
	}
}

func TestConstWriteRefused(t *testing.T) {
	m := newMem()
	a := m.add(allocConst, alloc{bytes: make([]byte, 8)})
	if r := refusal(t, func() { m.access(a, 0, 8, false) }); r != 0 {
		t.Errorf("read of a const copy refused: %v", r)
	}
	if r := refusal(t, func() { m.access(a, 0, 8, true) }); r != types.RefuseRawAccess {
		t.Errorf("write into a const copy: refusal %v", r)
	}
}

func TestPartialPointerCopyRefused(t *testing.T) {
	m := newMem()
	src := m.newAlloc(allocOwn, 8)
	dst := m.newAlloc(allocOwn, 8)
	m.access(src, 0, 8, true).storePtr(0, somePtr)
	m.copyBytes(dst, 0, src, 0, 4)
	al := m.access(dst, 0, 8, false)
	if r := refusal(t, func() { al.loadPtr(0) }); r != types.RefusePointerBytes {
		t.Errorf("pointer load of a half-copied pointer: refusal %v", r)
	}
	if r := refusal(t, func() { al.loadScalar(0, 8) }); r != types.RefusePointerBytes {
		t.Errorf("u64 load of a half-copied pointer: refusal %v", r)
	}
	if r := refusal(t, func() { al.loadScalar(4, 4) }); r != 0 {
		t.Errorf("bytes 4 to 7 were never written, refused: %v", r)
	}
	// A whole copy keeps the pointer, at the destination offset.
	big := m.newAlloc(allocOwn, 24)
	m.copyBytes(big, 8, src, 0, 8)
	if got := m.access(big, 8, 8, false).loadPtr(8); got != somePtr {
		t.Errorf("whole copy: pointer %#x, want %#x", got, somePtr)
	}
	// A copy that starts inside a pointer word cuts it.
	m.copyBytes(big, 0, src, 2, 6)
	if r := refusal(t, func() { m.access(big, 0, 8, false).loadScalar(0, 4) }); r != types.RefusePointerBytes {
		t.Errorf("copy starting inside a pointer: refusal %v", r)
	}
}

func TestPartialOverwriteFragments(t *testing.T) {
	m := newMem()
	a := m.newAlloc(allocOwn, 8)
	al := m.access(a, 0, 8, true)
	al.storePtr(0, somePtr)
	al.storeScalar(1, 1, 7)
	if r := refusal(t, func() { al.loadScalar(4, 4) }); r != types.RefusePointerBytes {
		t.Errorf("u32 load of the old pointer's bytes 4 to 7: refusal %v", r)
	}
	if r := refusal(t, func() { al.loadScalar(1, 1) }); r != 0 {
		t.Errorf("the byte written is readable, refused: %v", r)
	}
	if r := refusal(t, func() { al.loadPtr(0) }); r != types.RefusePointerBytes {
		t.Errorf("pointer load after a partial overwrite: refusal %v", r)
	}
	al.storeScalar(0, 4, 1)
	al.storeScalar(4, 4, 2)
	if r := refusal(t, func() {
		if got := al.loadScalar(0, 8); got != 2<<32|1 {
			t.Errorf("rewritten word = %#x", got)
		}
	}); r != 0 {
		t.Errorf("a fully rewritten word is readable as u64, refused: %v", r)
	}
}

func TestPackedPointerStore(t *testing.T) {
	m := newMem()
	a := m.newAlloc(allocOwn, 16)
	al := m.access(a, 0, 16, true)
	p2 := uint64(4)<<32 | 1
	al.storePtr(0, somePtr)
	al.storePtr(4, p2)
	if bitAt(al.ptr, 0) || !bitAt(al.ptr, 4) {
		t.Errorf("tags after packed stores: at 0 %v, at 4 %v; want only 4", bitAt(al.ptr, 0), bitAt(al.ptr, 4))
	}
	if r := refusal(t, func() { al.loadPtr(0) }); r != types.RefusePointerBytes {
		t.Errorf("pointer load at 0: refusal %v", r)
	}
	if got := al.loadPtr(4); got != p2 {
		t.Errorf("pointer at 4 = %#x, want %#x", got, p2)
	}
}

func TestOverlappingCopyKeepsTags(t *testing.T) {
	m := newMem()
	a := m.newAlloc(allocOwn, 32)
	al := m.access(a, 0, 32, true)
	al.storePtr(0, somePtr)
	al.storeScalar(8, 8, 99)
	m.copyBytes(a, 8, a, 0, 16) // memmove forward over itself
	al = m.at(a)
	if got := al.loadPtr(8); got != somePtr {
		t.Errorf("moved pointer = %#x", got)
	}
	if got := al.loadScalar(16, 8); got != 99 {
		t.Errorf("moved integer = %d", got)
	}
	if got := al.loadPtr(0); got != somePtr {
		t.Errorf("source pointer kept = %#x", got)
	}
}

func TestPointerEqualityAcrossAllocations(t *testing.T) {
	w := newWorld(t)
	pt := w.tab.RawPtr(types.U8)
	ti := &tinfo{class: cPtr, width: 8, ty: w.tab.Get(pt)}
	a, b := ptrWord(1, 0), ptrWord(2, 0)
	for _, c := range []struct {
		x, y uint64
		eq   uint64
	}{{a, b, 0}, {a, a, 1}, {0, 0, 1}, {a, 0, 0}, {ptrWord(1, 0), ptrWord(1, 8), 0}} {
		var got uint64
		if r := refusal(t, func() { got, _ = binaryOp(mir.Eq, ti, ti, c.x, c.y) }); r != 0 || got != c.eq {
			t.Errorf("%#x == %#x: %d (refusal %v), want %d", c.x, c.y, got, r, c.eq)
		}
	}
	if r := refusal(t, func() { binaryOp(mir.Lt, ti, ti, a, b) }); r != types.RefuseRawAccess {
		t.Errorf("< across allocations: refusal %v, want raw-access", r)
	}
	var lt uint64
	if r := refusal(t, func() { lt, _ = binaryOp(mir.Lt, ti, ti, ptrWord(1, 0), ptrWord(1, 8)) }); r != 0 || lt != 1 {
		t.Errorf("< within one allocation: %d, refusal %v", lt, r)
	}
}

func TestPoolSlicesZeroed(t *testing.T) {
	p := &bytePool{}
	for i := 0; i < 100; i++ {
		b := p.get(100 + i)
		for j := range b {
			if b[j] != 0 {
				t.Fatalf("round %d: byte %d of a pooled slice is %d", i, j, b[j])
			}
			b[j] = 0xff
		}
		p.put(b)
	}
}

func TestMemoryFreeKeepsNumberDead(t *testing.T) {
	m := newMem()
	a := m.newAlloc(allocFrame, 64)
	da := m.recs[a].d
	m.free(a)
	b := m.newAlloc(allocFrame, 64)
	if a == b {
		t.Fatalf("allocation number %d reused", a)
	}
	if r := m.recs[a]; r.live || r.d != 0 || r.kind != allocFrame {
		t.Errorf("dead allocation record %+v, want its kind only", r)
	}
	if m.recs[b].d != da {
		t.Errorf("dead allocation's data record %d not reused by %d", da, m.recs[b].d)
	}
}
