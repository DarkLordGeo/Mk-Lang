// Why: every MIR block ends in a terminator, and none may become LLVM
// undefined behaviour: a MIR unreachable must panic, not let LLVM assume
// the point is never reached.
//
// What: term (every terminator kind), switchTerm and ret.
//
// How: goto is br; a switch gets an LLVM unreachable default only when its
// cases are total; calls go to call.go, tail calls to tailcall.go; a return
// loads the lowered result; panics and unreachable are panic hook calls.

package codegen

import (
	"mk/internal/abi"
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

func (fg *fnGen) term(t *mir.Terminator) {
	fg.setLoc(t.Span)
	switch t.Kind {
	case mir.TermGoto:
		fg.cur.Br(fg.blocks[t.Target])
	case mir.TermSwitch:
		fg.switchTerm(t)
	case mir.TermCall:
		fg.callTerm(t)
	case mir.TermTailCall:
		fg.tailCallTerm(t) // tailcall.go
	case mir.TermReturn:
		fg.ret()
	case mir.TermPanic:
		if t.Panic == types.PanicBounds && fg.g.cfg.TestNoBoundsChecks {
			fg.cur.Unreachable() // Config.TestNoBoundsChecks: test-only
			return
		}
		if t.HasMsg {
			msg := fg.operand(t.Msg)
			fg.panicCall(t.Panic, &msg)
		} else {
			fg.panicCall(t.Panic, nil)
		}
	case mir.TermUnreachable:
		fg.panicCall(types.PanicUnreachable, nil)
	default:
		source.ICEf(fg.spanOf(t.Span), "codegen: terminator kind %d in an instance body", t.Kind)
	}
}

// switchTerm is br i1 for a bool, else switch on the bit patterns; a NoBlock
// default goes to an unreachable block (the cases are total).
func (fg *fnGen) switchTerm(t *mir.Terminator) {
	m := fg.g.m
	on := fg.operand(t.On)
	if on.kind != rScalar {
		source.ICEf(fg.spanOf(t.Span), "codegen: switch on %s", fg.g.tt.TypeString(on.t, nil))
	}
	cases := fg.b.Cases[t.Cases.Start:t.Cases.End()]
	def := (*llvm.Block)(nil)
	if t.Default != mir.NoBlock {
		def = fg.blocks[t.Default]
	}
	// Successor j of the MIR terminator is case j, then the default (the
	// PGO counter order); counts holds their recorded counts when profiled.
	nsucc := len(cases)
	if def != nil {
		nsucc++
	}
	counts := fg.edgeCounts(nsucc)
	if def != nil {
		def = fg.edge(len(cases), def)
	}
	if on.v.Ty.Bits() == 1 {
		var tb, fb *llvm.Block = def, def
		tj, fj := len(cases), len(cases) // the edge each side takes, for weights
		for j, c := range cases {
			if c.Value&1 == 1 {
				tb, tj = fg.edge(j, fg.blocks[c.Target]), j
			} else {
				fb, fj = fg.edge(j, fg.blocks[c.Target]), j
			}
		}
		if tb == nil || fb == nil {
			u := fg.newBlock()
			u.Unreachable()
			if tb == nil {
				tb, tj = u, -1
			}
			if fb == nil {
				fb, fj = u, -1
			}
		}
		var w []llvm.Attach
		if counts != nil {
			at := func(j int) uint64 {
				if j < 0 || j >= len(counts) {
					return 0
				}
				return counts[j]
			}
			w = fg.weights([]uint64{at(tj), at(fj)}) // br: true, then false
		}
		fg.cur.CondBr(on.v, tb, fb, w...)
		return
	}
	var dc uint64
	if def == nil {
		def = fg.newBlock()
		def.Unreachable()
	} else if counts != nil {
		dc = counts[len(cases)]
	}
	var lc []llvm.Case
	for j, c := range cases {
		lc = append(lc, llvm.Case{Val: m.ConstInt(on.v.Ty, c.Value), Target: fg.edge(j, fg.blocks[c.Target])})
	}
	var w []llvm.Attach
	if counts != nil {
		w = fg.weights(append([]uint64{dc}, counts[:len(cases)]...)) // switch: default, then the cases
	}
	fg.cur.Switch(on.v, def, lc, w...)
}

// ret returns local 0: Direct results load their parts, sret and void
// return nothing.
func (fg *fnGen) ret() {
	sig := fg.sig
	if sig.s.Ret.Kind != abi.Direct {
		fg.cur.Ret(nil)
		return
	}
	parts := fg.loadParts(fg.locals[mir.ReturnLocal], sig.result, sig.s.Ret, sig.retArray, true)
	v := parts[0]
	if len(parts) > 1 {
		v = fg.g.m.Poison(sig.ret)
		for k, p := range parts {
			v = fg.cur.InsertValue(v, p, k)
		}
	}
	fg.cur.Ret(&v)
}
