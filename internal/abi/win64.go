// Why: x86_64 Windows uses the Microsoft x64 calling convention, which passes
// anything but 1, 2, 4 or 8 bytes by reference; its rules share nothing with
// SysV's eightbyte classification.
//
// What: win64Param and win64Result, plus scalarSlot and ext, the extension
// table of small scalars for every target.
//
// How: the convention as clang's WinX86_64ABIInfo applies it: a value of size
// 1, 2, 4 or 8 travels as one integer of its size, anything else as a pointer
// to a caller-made copy, or through sret as a result.

package abi

import "mk/internal/types"

// scalarSlot is the Direct slot of a C scalar, with the target's extension.
func (v *view) scalarSlot(s scalar) Slot {
	p := intPart(s.size)
	if s.class == leafFloat {
		p = floatPart(s.size)
	}
	sl := direct(s.size, uint32(s.size), p)
	sl.Ext = v.ext(s)
	return sl
}

// ext is the extension table of small scalars: Win64 extends only bool; SysV
// and Darwin extend bool, i8 and i16 by signedness; AAPCS64 Linux extends
// nothing.
func (v *view) ext(s scalar) Ext {
	if s.class != leafInt || s.size >= 4 {
		return ExtNone
	}
	switch {
	case v.target.Arch == types.ArchX64 && v.target.OS == types.OSWindows:
		if s.isBool {
			return ZeroExt
		}
	case v.target.Arch == types.ArchX64, v.target.OS == types.OSMacos:
		if s.signed {
			return SignExt
		}
		return ZeroExt
	}
	return ExtNone
}

// win64Param: scalars Direct; any other value of size 1, 2, 4 or 8 Direct as
// one integer of its size (floats inside included); anything else Indirect,
// a pointer to a caller-made copy.
func win64Param(v *view, t types.TypeID) Slot {
	size, align := v.sizeAlign(t)
	if size == 0 {
		return Slot{Kind: Ignore, Align: align}
	}
	if s, ok := v.scalarOf(t); ok {
		return v.scalarSlot(s)
	}
	switch size {
	case 1, 2, 4, 8:
		return direct(size, align, intPart(size))
	}
	return Slot{Kind: Indirect, Size: size, Align: align}
}

// win64Result: as a parameter, except that Indirect is sret.
func win64Result(v *view, t types.TypeID) Slot {
	return win64Param(v, t)
}
