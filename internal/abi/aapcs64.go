// Why: aarch64 targets pass C values under AAPCS64, whose rules for float
// aggregates and composites differ from x86-64's; they get a file of their own.
//
// What: aapcsParam and aapcsResult, the parameter and result rules, and
// aapcsHFA, the homogeneous float aggregate test.
//
// How: the AAPCS64 procedure call standard 6.8.2 as clang's AArch64ABIInfo
// applies it: an HFA goes in float registers, a composite of up to 16 bytes in
// integer registers, a larger one by reference, a large result through sret
// in x8. The Darwin differences are in darwin.go.

package abi

import "mk/internal/types"

// aapcsHFA is the Direct slot of a homogeneous float aggregate, ok false
// when t is not one.
func aapcsHFA(v *view, t types.TypeID, size uint64, align uint32) (Slot, bool) {
	if size > maxLeafBytes {
		return Slot{}, false
	}
	members, base, ok := hfa(v.flatten(t), size)
	if !ok {
		return Slot{}, false
	}
	s := Slot{Kind: Direct, Size: size, Align: align}
	for i := 0; i < members; i++ {
		s.Parts = append(s.Parts, floatPart(base))
	}
	return s, true
}

// aapcsParam: an HFA Direct as its float members; another composite of up to
// 16 bytes Direct as one or two 8-byte integers (one 16-byte integer when its
// alignment is 16); larger Indirect, a pointer to a caller-made copy.
func aapcsParam(v *view, t types.TypeID) Slot {
	size, align := v.sizeAlign(t)
	if size == 0 {
		return Slot{Kind: Ignore, Align: align}
	}
	if s, ok := v.scalarOf(t); ok {
		return v.scalarSlot(s)
	}
	if s, ok := aapcsHFA(v, t, size, align); ok {
		s.AlignStack = v.hfaAlignStack(align)
		return s
	}
	switch {
	case size <= 16 && align >= 16:
		return direct(size, align, intPart(16))
	case size <= 8:
		return direct(size, align, intPart(8))
	case size <= 16:
		return direct(size, align, intPart(8), intPart(8))
	}
	return Slot{Kind: Indirect, Size: size, Align: align}
}

// aapcsResult: an HFA in v0..v3; a composite of up to 8 bytes as one integer
// of its size (x0); up to 16 bytes as two 8-byte integers (x0, x1); larger
// through sret (x8).
func aapcsResult(v *view, t types.TypeID) Slot {
	size, align := v.sizeAlign(t)
	if size == 0 {
		return Slot{Kind: Ignore, Align: align}
	}
	if s, ok := v.scalarOf(t); ok {
		return v.scalarSlot(s)
	}
	if s, ok := aapcsHFA(v, t, size, align); ok {
		return s
	}
	switch {
	case size <= 8:
		return direct(size, align, intPart(size))
	case size <= 16 && align >= 16:
		return direct(size, align, intPart(16))
	case size <= 16:
		return direct(size, align, intPart(8), intPart(8))
	}
	return Slot{Kind: Indirect, Size: size, Align: align}
}
