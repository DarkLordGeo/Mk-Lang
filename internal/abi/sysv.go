// Why: x86_64 Linux and other System V targets classify C values by
// eightbyte, the most intricate of the supported ABIs; it gets a file of its
// own.
//
// What: sysvParam, sysvResult, sysvClassify (eightbyte classes), intWidth and
// ssePart (the LLVM type of each eightbyte), and lowerSysVParams (register
// accounting over a whole signature).
//
// How: the AMD64 psABI 3.2.3 as clang's X86_64ABIInfo applies it. A value
// whose eightbytes do not all fit in the remaining INTEGER and SSE registers
// goes wholly to the stack, byval; a MEMORY result goes through sret.

package abi

import "mk/internal/types"

const (
	sysvIntRegs = 6 // rdi rsi rdx rcx r8 r9
	sysvSSERegs = 8 // xmm0..xmm7
)

type ebClass uint8

const (
	ebNone ebClass = iota
	ebInt
	ebSSE
)

// sysvClass is a value's class before register accounting.
type sysvClass struct {
	slot    Slot
	memory  bool
	scalar  bool
	needInt int
	needSSE int
}

func sysvParam(v *view, t types.TypeID) Slot {
	c := sysvClassify(v, t)
	if c.memory {
		return v.sysvByval(t)
	}
	return c.slot
}

func sysvResult(v *view, t types.TypeID) Slot {
	c := sysvClassify(v, t)
	if c.memory {
		size, align := v.sizeAlign(t)
		return Slot{Kind: Indirect, Size: size, Align: align}
	}
	return c.slot
}

// sysvByval is a MEMORY parameter: byval, aligned to at least 8.
func (v *view) sysvByval(t types.TypeID) Slot {
	size, align := v.sizeAlign(t)
	return Slot{Kind: Indirect, Byval: true, Size: size, Align: max(align, 8)}
}

func sysvClassify(v *view, t types.TypeID) sysvClass {
	size, align := v.sizeAlign(t)
	if size == 0 {
		return sysvClass{slot: Slot{Kind: Ignore, Align: align}}
	}
	if s, ok := v.scalarOf(t); ok {
		c := sysvClass{slot: v.scalarSlot(s), scalar: true}
		if s.class == leafFloat {
			c.needSSE = 1
		} else {
			c.needInt = 1
		}
		return c
	}
	if size > 16 {
		return sysvClass{memory: true}
	}
	fl := v.flatten(t)
	if fl.unaligned {
		return sysvClass{memory: true}
	}
	n := int((size + 7) / 8)
	var cls [2]ebClass
	for _, l := range fl.leaves {
		for eb := l.off / 8; eb <= (l.off+l.size-1)/8 && eb < 2; eb++ {
			if l.class == leafFloat {
				if cls[eb] == ebNone {
					cls[eb] = ebSSE
				}
			} else {
				cls[eb] = ebInt
			}
		}
	}
	// A high eightbyte of padding only is not passed; a low one (which Mk
	// layouts do not produce) is passed as an integer.
	if n == 2 && cls[1] == ebNone {
		n = 1
	}
	if cls[0] == ebNone {
		cls[0] = ebInt
	}
	c := sysvClass{slot: Slot{Kind: Direct, Size: size, Align: align}}
	for i := 0; i < n; i++ {
		start := uint64(i) * 8
		rem := min(size-start, 8)
		if cls[i] == ebSSE {
			c.slot.Parts = append(c.slot.Parts, ssePart(fl, start))
			c.needSSE++
		} else {
			c.slot.Parts = append(c.slot.Parts, intPart(intWidth(fl, start, rem)))
			c.needInt++
		}
	}
	return c
}

// intWidth is the width of the INTEGER eightbyte at start: the width of the
// integer or pointer leaves that all begin at start, when no other data lies
// in the eightbyte; else the remaining size, up to 8 (clang's
// GetINTEGERTypeAtOffset).
func intWidth(fl flat, start, rem uint64) uint64 {
	w := uint64(0)
	for _, l := range fl.leaves {
		if l.off+l.size <= start || l.off >= start+8 {
			continue
		}
		if l.off != start || l.class == leafFloat {
			return rem
		}
		w = max(w, l.size)
	}
	if w == 0 {
		return rem
	}
	return w
}

// ssePart is the SSE eightbyte at start: float when one f32 begins it and
// nothing else lies in it, <2 x float> when the f32s at start and start+4 are
// all it holds, else double (clang's GetSSETypeAtOffset). Overlapping leaves
// of another width (variants of a union) make it double, so the widest
// variant travels whole.
func ssePart(fl flat, start uint64) types.AbiPart {
	lo, hi := false, false
	for _, l := range fl.leaves {
		if l.off+l.size <= start || l.off >= start+8 {
			continue
		}
		switch {
		case l.size == 4 && l.off == start:
			lo = true
		case l.size == 4 && l.off == start+4:
			hi = true
		default:
			return floatPart(8)
		}
	}
	switch {
	case lo && hi:
		return types.AbiPart{Class: types.RegVector, Bytes: 8}
	case lo:
		return floatPart(4)
	}
	return floatPart(8)
}

// lowerSysVParams assigns registers left to right: a value whose eightbytes
// do not all fit in the remaining INTEGER and SSE registers goes wholly to
// the stack (byval, or as one integer of its size once no INTEGER register is
// left, as clang's getIndirectResult does); scalars stay Direct and LLVM
// places them. An sret result takes the first INTEGER register.
func lowerSysVParams(v *view, sig *Sig, params []types.TypeID) {
	freeInt, freeSSE := sysvIntRegs, sysvSSERegs
	if sig.Ret.Kind == Indirect {
		freeInt--
	}
	for i, p := range params {
		c := sysvClassify(v, p)
		switch {
		case c.slot.Kind == Ignore && !c.memory:
			sig.Params[i] = c.slot
		case !c.memory && c.needInt <= freeInt && c.needSSE <= freeSSE:
			freeInt -= c.needInt
			freeSSE -= c.needSSE
			sig.Params[i] = c.slot
		case !c.memory && c.scalar:
			sig.Params[i] = c.slot
		default:
			sig.Params[i] = v.sysvIndirect(p, freeInt)
		}
	}
}

// sysvIndirect is an aggregate passed on the stack.
func (v *view) sysvIndirect(t types.TypeID, freeInt int) Slot {
	size, align := v.sizeAlign(t)
	if freeInt == 0 && max(align, 8) == 8 && size <= 8 {
		return direct(size, align, intPart(size))
	}
	return v.sysvByval(t)
}
