// Why: every Mk function uses the platform C ABI, so codegen must know, per
// target, how each parameter and result travels: in which registers, by
// reference, through sret or byval. Keeping that in one package keeps the
// target rules out of codegen and testable against clang.
//
// What: Classify (one value in isolation) and Lower (a whole signature, with
// register accounting and variadics), returning Slot and Sig; the per-target
// rules live in sysv.go, win64.go, aapcs64.go and darwin.go.
//
// How: classifyParam and lowerResult dispatch on the target's arch and OS.
// Layouts come from the caller through LayoutOf; abi decides no layout and
// emits no IR. A violated caller invariant panics through ice.

// Package abi classifies concrete Mk types under the C ABI of each supported
// target and lowers whole signatures (coercion, sret, byval, register
// exhaustion, variadics) to the form codegen declares and calls. It reads
// layouts; it decides none (that is check's shape pass), and it emits no IR.
package abi

import (
	"fmt"

	"mk/internal/types"
)

// LayoutOf returns the layout of a concrete type (no KParam, KSelf, KInfer,
// KPoison); it must be safe for concurrent use. abi calls it only for
// aggregates (structs, records, enums, optionals that are not niches, error
// unions, error-set values, arrays); scalars and slices it lays out itself.
type LayoutOf func(types.TypeID) *types.Layout

// SlotKind tags a Slot.
type SlotKind uint8

const (
	// Ignore: zero-sized; passed as nothing and never occupies a register.
	Ignore SlotKind = iota
	// Direct: in registers (or, past the registers, on the stack as LLVM
	// places the same values), as Parts, each one LLVM scalar or vector.
	Direct
	// Indirect: for a parameter, a pointer to a copy (Byval says the callee's
	// frame holds it, SysV MEMORY); for a result, the hidden sret pointer
	// parameter, which comes before every Mk parameter.
	Indirect
)

// String is ignore, direct or indirect.
func (k SlotKind) String() string {
	switch k {
	case Ignore:
		return "ignore"
	case Direct:
		return "direct"
	case Indirect:
		return "indirect"
	}
	return "slotkind(?)"
}

// Ext is the extension attribute of a scalar below 32 bits: the
// caller (for a parameter) or the callee (for a result) extends the value to
// 32 bits, and the other side may rely on it.
type Ext uint8

const (
	ExtNone Ext = iota
	SignExt
	ZeroExt
)

// Slot is how one Mk parameter or result travels.
type Slot struct {
	Kind SlotKind
	// Parts are the register parts of a Direct slot, in order. Bytes is the
	// width of the LLVM part (an iN of 8*Bytes bits for RegInt, float or
	// double for RegFloat, <2 x float> for RegVector of 8 bytes); a Direct
	// value whose parts cover more bytes than Size (aarch64 composites) goes
	// through a temporary of the coerced size.
	Parts []types.AbiPart
	// Byval is set on an Indirect parameter that the target copies into the
	// callee's frame (SysV MEMORY): codegen passes the argument's address with
	// byval, and LLVM makes the copy.
	Byval bool
	// Ext is the extension of a Direct scalar of fewer than 32 bits.
	Ext Ext
	// Size and Align are the Mk type's size and alignment, except that Align
	// of a byval slot is the byval alignment the target requires (SysV: at
	// least 8), which codegen attaches as align(Align).
	Size  uint64
	Align uint32
	// AlignStack is the stack alignment of a Direct homogeneous float
	// aggregate under AAPCS64 Linux (alignstack(8)), 0 when none applies; it
	// places an HFA that no longer fits in v0..v7 on an 8-byte stack slot.
	AlignStack uint32
}

// Sig is a lowered signature: what codegen declares and calls.
type Sig struct {
	// Ret is Ignore, Direct (Parts) or Indirect (sret: a hidden first pointer
	// parameter before Params).
	Ret Slot
	// Params has one slot per Mk parameter, in order (receiver first), the
	// variadic arguments of a call after the fixed ones.
	Params []Slot
	// Variadic is true when the signature is variadic (a variadic extern's
	// declaration, or a call of one), whether or not any variadic argument
	// is given: codegen declares and calls it with "...".
	Variadic bool
	// Fixed is the number of fixed parameters.
	Fixed int
}

// Classify is the C ABI class of a value of type t passed as a parameter, in
// isolation (no register accounting): Ignore for size 0; Direct with the
// register parts; Indirect for what the target passes by reference.
// Deterministic, pure, concurrency-safe. check leaves types.Shape.ABI zero (it
// cannot import abi); callers memoize Classify per worker instead of reading
// the shape record. Results follow the target's result rules, which
// only Lower applies.
func Classify(tt *types.Table, layout LayoutOf, target types.Target, t types.TypeID) types.AbiClass {
	return toClass(classifyParam(newView(tt, layout, target), t))
}

// Lower lowers a whole signature for target: per-parameter Classify, then the
// target's register accounting (SysV puts an aggregate that does not fit in
// the remaining registers wholly on the stack), sret, and the variadic rules.
// params are the Mk parameter types in order (receiver first); fixed is the
// number of non-variadic parameters; variadic says the function is variadic
// (a declaration of a variadic extern has fixed == len(params) and variadic
// set; so has a call that passes no variadic argument, such as printf("hi")).
// A non-variadic signature has fixed == len(params). result is the Mk result
// type (E!T for a function that can fail; Void or Never for none, never
// NoType).
func Lower(tt *types.Table, layout LayoutOf, target types.Target, params []types.TypeID, fixed int, variadic bool, result types.TypeID) Sig {
	if fixed < 0 || fixed > len(params) || (!variadic && fixed != len(params)) {
		ice("abi.Lower: %d fixed of %d parameters, variadic %v", fixed, len(params), variadic)
	}
	v := newView(tt, layout, target)
	sig := Sig{Variadic: variadic, Fixed: fixed, Params: make([]Slot, len(params))}
	sig.Ret = lowerResult(v, result)
	if target.Arch == types.ArchX64 && target.OS != types.OSWindows {
		lowerSysVParams(v, &sig, params)
		return sig
	}
	for i, p := range params {
		sig.Params[i] = classifyParam(v, p)
	}
	return sig
}

// classifyParam dispatches the per-target parameter classification.
func classifyParam(v *view, t types.TypeID) Slot {
	switch {
	case v.target.Arch == types.ArchX64 && v.target.OS == types.OSWindows:
		return win64Param(v, t)
	case v.target.Arch == types.ArchX64:
		return sysvParam(v, t)
	case v.target.Arch == types.ArchArm64:
		return aapcsParam(v, t)
	}
	ice("abi: unsupported target %s", v.target.Name)
	return Slot{}
}

// lowerResult dispatches the per-target result classification.
func lowerResult(v *view, t types.TypeID) Slot {
	switch {
	case v.target.Arch == types.ArchX64 && v.target.OS == types.OSWindows:
		return win64Result(v, t)
	case v.target.Arch == types.ArchX64:
		return sysvResult(v, t)
	case v.target.Arch == types.ArchArm64:
		return aapcsResult(v, t)
	}
	ice("abi: unsupported target %s", v.target.Name)
	return Slot{}
}

// toClass packs a parameter slot into a types.AbiClass.
func toClass(s Slot) types.AbiClass {
	c := types.AbiClass{Byval: s.Byval}
	switch s.Kind {
	case Ignore:
		c.Kind = types.AbiIgnore
	case Direct:
		c.Kind = types.AbiDirect
		if len(s.Parts) > len(c.Parts) {
			ice("abi: %d parts", len(s.Parts))
		}
		c.Count = uint8(len(s.Parts))
		copy(c.Parts[:], s.Parts)
	case Indirect:
		c.Kind = types.AbiIndirect
	}
	return c
}

// direct is a Direct slot of the given parts.
func direct(size uint64, align uint32, parts ...types.AbiPart) Slot {
	return Slot{Kind: Direct, Parts: parts, Size: size, Align: align}
}

func intPart(bytes uint64) types.AbiPart {
	return types.AbiPart{Class: types.RegInt, Bytes: uint8(bytes)}
}

func floatPart(bytes uint64) types.AbiPart {
	return types.AbiPart{Class: types.RegFloat, Bytes: uint8(bytes)}
}

// ice reports a violated invariant of a caller (a non-concrete type, a
// missing layout, an impossible target). abi imports only types, so ice
// panics with a plain error that source.Guard turns into an
// internal-compiler-error diagnostic.
func ice(format string, args ...any) {
	panic(fmt.Errorf("abi: "+format, args...))
}
