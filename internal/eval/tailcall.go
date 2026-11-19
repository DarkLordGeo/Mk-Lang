// Why: `become` promises a tail call; a compile-time evaluation of a long
// chain of them must run in constant frame room, as the native program does.
//
// What: Machine.tailCall and replaceCaller.
//
// How: a TermTailCall replaces the top frame instead of pushing one, and
// each call is charged its fuel as any call is (pushFrame). The callee
// returns where the replaced frame would have: to its caller's destination,
// or out of the segment.

package eval

import (
	"mk/internal/mir"
	"mk/internal/source"
)

// tailCall runs a TermTailCall of frame f. A callee with a body takes f's
// place (callBody, then replaceCaller). One the evaluator runs in place, an
// override or a host call (check refuses intrinsics and externs, E0314),
// writes f's _0, which is the call's Dest; f then returns it. The tagged
// mode keeps a frame record per frame in its own stack (tagaccess.go), so
// there the callee goes on top and f returns when it does (ret).
func (m *Machine) tailCall(f *frame, t *mir.Terminator) {
	if t.Spawn != mir.NoLocal || t.HasErr {
		source.ICEf(f.curSpan(), "eval: a tail call with a spawn or an error edge")
	}
	n := len(m.frames)
	m.tailing = m.tg == nil
	m.call(f, t)
	if m.tg == nil && !m.tailing {
		return // callBody replaced f
	}
	m.tailing = false
	switch {
	case len(m.frames) == n:
		m.ret()
	case m.frames[len(m.frames)-1].detached:
		source.ICEf(f.curSpan(), "eval: a tail call of a function the host runs in a frame of its own")
	}
}

// replaceCaller drops the frame under the top one, the frame whose tail
// call pushed the top: the callee takes its place in the chain, with its
// call site and the way its return goes (to a caller's destination, to the
// segment, to a host's sink), and its bytes are freed.
func (m *Machine) replaceCaller() {
	top := len(m.frames) - 1
	old := &m.frames[top-1]
	nf := m.frames[top]
	nf.callSpan = old.callSpan
	nf.arena, nf.hostArena = old.arena, old.hostArena
	nf.detached, nf.hasSink, nf.sink = old.detached, old.hasSink, old.sink
	if m.tg != nil {
		m.tgPopFrame(old)
	}
	m.seg().frameBytes -= old.code.frameSize
	m.mem.free(old.base)
	m.frames[top-1] = nf
	m.frames = m.frames[:top]
}
