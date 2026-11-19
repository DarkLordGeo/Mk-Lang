// Why: aarch64 macOS follows Apple's ARM64 calling conventions, which change
// a few AAPCS64 rules; this file holds those differences.
//
// What: hfaAlignStack, the stack alignment of a homogeneous float aggregate
// argument per OS.
//
// How: the caller extends integer arguments below 32 bits and the callee its
// results (see ext in win64.go); an HFA carries no stack-alignment override,
// because Darwin packs stack arguments at their natural alignment; variadic
// arguments go on the stack, which LLVM does for a variadic call.

package abi

import "mk/internal/types"

// hfaAlignStack is the alignstack of an HFA argument: 8 (16 for a 16-aligned
// one) under AAPCS64 Linux, none under Darwin.
func (v *view) hfaAlignStack(align uint32) uint32 {
	if v.target.OS == types.OSMacos {
		return 0
	}
	if align >= 16 {
		return 16
	}
	return 8
}
