// Why: atomics with weaker orders publish less than seq_cst, and a race
// check that treated every atomic as seq_cst would miss races that a
// relaxed atomic leaves open ("`Relaxed` makes no such edge").
//
// What: the order constants, atomicOp and atomicOpOf (which order an
// intrinsic call uses), and raceAtomicOp, the clock updates per operation.
//
// How: each atomic intrinsic of std.raw makes exactly the happens-before
// edges its order makes in the C++20 model, and FenceOrd and Fence are
// modelled, as ThreadSanitizer's vector-clock algorithm does (Serebryany and
// Iskhodzhanov, "ThreadSanitizer: data race detection in practice", 2009,
// with the fence handling of its runtime's tsan_rtl AtomicFence and of
// Loom). The evaluator still runs every order as seq_cst
// (intrinsic_atomic.go): what a load reads is the latest value, and only
// the race check changes with the order.
//
// Per location x, a clock L(x) holds what an acquire that reads x's current
// value synchronizes with: the clock of the release that heads its release
// sequence, joined with each read-modify-write since (C++20
// [intro.races]/5: a release sequence is the release and the RMWs after
// it; a plain store by any task starts a new one). Per task t, beside its
// clock C(t), F(t) is C(t) at its last release fence (a relaxed store or
// RMW after it publishes F(t), [atomics.fences]/2) and A(t) the join of
// L(x) over the relaxed loads since (an acquire fence joins A(t) into C(t),
// [atomics.fences]/3 and /4).
//
//   load o:     acquire(o) ? C(t) ∪= L(x) : A(t) ∪= L(x)
//   store o:    L(x) = release(o) ? C(t) : F(t)
//   RMW o:      the load part, then L(x) ∪= release(o) ? C(t) : F(t)
//   CAS s, f:   an RMW of order s when it wrote, a load of order f when not
//   fence o:    acquire(o) ? C(t) ∪= A(t); release(o) ? F(t) = C(t)
//
// and C(t)'s own entry ticks after each, so an access after a release is
// not covered by what the release published. A SeqCst operation is an
// acquire and a release as it applies; the single total order of SeqCst
// operations adds no happens-before edge of its own (C++20 [atomics.order]
// /4 constrains which value is read, which the seq_cst run already fixes),
// so it is not modelled, as in ThreadSanitizer.

package eval

import (
	"strings"

	"mk/internal/mir"
)

// The memory orders as std.atomic's Order values pass them (order.mk).
const (
	ordRelaxed = 0
	ordAcquire = 1
	ordRelease = 2
	ordAcqRel  = 3
	ordSeqCst  = 4
	// ordUnordered is LLVM's unordered, a load or store order
	// only: it makes no edge, not even through a fence, since a fence
	// synchronizes only through atomics of at least relaxed (monotonic)
	// order.
	ordUnordered = 5
)

func ordAcquires(o uint8) bool { return o == ordAcquire || o == ordAcqRel || o == ordSeqCst }
func ordReleases(o uint8) bool { return o == ordRelease || o == ordAcqRel || o == ordSeqCst }

// atomicKind is what an atomic intrinsic does to its location.
type atomicKind uint8

const (
	akLoad atomicKind = iota
	akStore
	akRMW
	akCAS
	akFence
)

// atomicOp is one atomic intrinsic call's kind and orders (fail: a CAS's
// failure order).
type atomicOp struct {
	kind      atomicKind
	ord, fail uint8
}

// atomicOpOf classifies intrinsic key of call t: false for a call that is
// not an atomic access (New, or no std.raw atomic at all). The unordered
// forms are seq_cst; an ordered form's order is its const argument. A
// std.raw.Atomic method this table does not name is an RMW of seq_cst, the
// strongest reading.
func (m *Machine) atomicOpOf(f *frame, t *mir.Terminator, key string) (atomicOp, bool) {
	arg := func(i int) uint8 {
		v := uint8(m.argU(f, t, i))
		m.takeVal()
		return v
	}
	switch key {
	case "std.raw.Fence":
		return atomicOp{kind: akFence, ord: ordSeqCst}, true
	case "std.raw.FenceOrd":
		return atomicOp{kind: akFence, ord: arg(0)}, true
	case "std.raw.Atomic:New":
		return atomicOp{}, false
	case "std.raw.Atomic:Load":
		return atomicOp{kind: akLoad, ord: ordSeqCst}, true
	case "std.raw.Atomic:LoadOrd":
		return atomicOp{kind: akLoad, ord: arg(1)}, true
	case "std.raw.Atomic:Store":
		return atomicOp{kind: akStore, ord: ordSeqCst}, true
	case "std.raw.Atomic:StoreOrd":
		return atomicOp{kind: akStore, ord: arg(2)}, true
	case "std.raw.Atomic:Cas":
		return atomicOp{kind: akCAS, ord: ordSeqCst, fail: ordSeqCst}, true
	case "std.raw.Atomic:CasOrd":
		return atomicOp{kind: akCAS, ord: arg(3), fail: arg(4)}, true
	case "std.raw.Atomic:Swap", "std.raw.Atomic:Add":
		return atomicOp{kind: akRMW, ord: ordSeqCst}, true
	case "std.raw.Atomic:SwapOrd", "std.raw.Atomic:AddOrd", "std.raw.Atomic:SubOrd",
		"std.raw.Atomic:AndOrd", "std.raw.Atomic:OrOrd", "std.raw.Atomic:XorOrd":
		return atomicOp{kind: akRMW, ord: arg(2)}, true
	}
	if strings.HasPrefix(key, "std.raw.Atomic:") {
		return atomicOp{kind: akRMW, ord: ordSeqCst}, true
	}
	return atomicOp{}, false
}

// raceAtomicOp is op by the running task on the location at (a, off) (a
// fence has none), after the intrinsic ran (casOK is whether a CAS wrote):
// the table at the top of this file, then the task ticks.
func (m *Machine) raceAtomicOp(op atomicOp, a, off uint32) {
	r := &m.tg.race
	if !r.on {
		return
	}
	t := m.tg.task.cur
	c := r.clock(t)
	for int(t) >= len(r.fenceRel) {
		r.fenceRel = append(r.fenceRel, nil)
		r.fenceAcq = append(r.fenceAcq, nil)
	}
	if r.locs == nil {
		r.locs = map[uint64]vclock{}
	}
	k := raceKey(a, off)
	load := func(o uint8) {
		if o == ordUnordered {
			return
		}
		if ordAcquires(o) {
			c = c.join(r.locs[k])
		} else {
			r.fenceAcq[t] = r.fenceAcq[t].join(r.locs[k])
		}
	}
	// published is what a release part of order o publishes: the task's
	// clock, or its last release fence's (nil: nothing).
	published := func(o uint8) vclock {
		if o == ordUnordered {
			return nil
		}
		if ordReleases(o) {
			return c
		}
		return r.fenceRel[t]
	}
	switch op.kind {
	case akLoad:
		load(op.ord)
	case akStore:
		r.locs[k] = append(vclock(nil), published(op.ord)...)
	case akRMW:
		load(op.ord)
		r.locs[k] = r.locs[k].join(published(op.ord))
	case akCAS:
		if r.casOK {
			load(op.ord)
			r.locs[k] = r.locs[k].join(published(op.ord))
		} else {
			load(op.fail)
		}
	case akFence:
		if ordAcquires(op.ord) {
			c = c.join(r.fenceAcq[t])
		}
		if ordReleases(op.ord) {
			r.fenceRel[t] = append(vclock(nil), c...)
		}
	}
	r.clocks[t] = c.set(t, c.at(t)+1)
}
