// Why: on System V x86_64 the support pack's memcpy and memset do the whole
// job with one rep movsb or rep stosb from ermsMin bytes.
// What: TestErmsIR (Linux, nutshell and freestanding use the string
// instruction as inline asm; Win64 and aarch64 keep the general-register
// code, see ermsMin's comment) and TestErmsRun.
// How: the run test checks every length around the threshold, unaligned,
// with each byte value class and with equal ranges, against Go.

package codegen

import (
	"math/rand"
	"strings"
	"testing"

	"mk/internal/types"
)

func TestErmsIR(t *testing.T) {
	targets := append(types.Targets(), types.X86_64Freestanding, types.X86_64Nutshell)
	for _, target := range targets {
		ll := supportLL(t, target)
		cpy, set := fnText(ll, symMemcpy), fnText(ll, symMemset)
		sysv := target.Arch == types.ArchX64 && target.OS != types.OSWindows
		inline := strings.Contains(cpy, `asm sideeffect "cld\0A\09rep movsb", "={di},={si},={cx},0,1,2,`) &&
			strings.Contains(set, `asm sideeffect "cld\0A\09rep stosb", "={di},={cx},0,{ax},1,`)
		if inline != sysv {
			t.Errorf("%s: inline asm %v, want %v", target.Name, inline, sysv)
		}
		// Off System V nothing names a string instruction or a routine for
		// one: no call, no declaration, no module asm.
		if !sysv && (strings.Contains(ll, "rep movsb") || strings.Contains(ll, "rep stosb") || strings.Contains(ll, "mk_rep_")) {
			t.Errorf("%s: a string instruction or its routine is in the support pack", target.Name)
		}
		if mm := fnText(ll, symMemmove); strings.Contains(mm, "rep ") || strings.Contains(mm, "mk_rep") {
			t.Errorf("%s: memmove uses a string instruction", target.Name)
		}
		if !sysv {
			continue
		}
		for _, fn := range []string{cpy, set} {
			if !strings.Contains(fn, "icmp uge i64 %p2, 2048") {
				t.Errorf("%s: a string instruction is not behind n >= %d:\n%s", target.Name, ermsMin, fn)
			}
		}
	}
}

func TestErmsRun(t *testing.T) {
	work := make([]byte, 16384)
	rand.New(rand.NewSource(11)).Read(work)
	var ops []memOp
	for _, n := range []int{ermsMin - 65, ermsMin - 1, ermsMin, ermsMin + 1, ermsMin + 7, ermsMin + 63, 2*ermsMin + 13} {
		for k := 0; k < 3; k++ {
			// a+n < b and b+n+k <= len(work) for every n here: disjoint.
			a, b := 1+k*5, 8192+3+k*3
			ops = append(ops,
				memOp{op: 0, a: a, b: b, n: n},
				memOp{op: 4, a: a, b: b, n: n}, // equal after the copy
				memOp{op: 2, a: b + k, n: n, c: []int{0, 0xff, 0x5a}[k]},
				memOp{op: 3, a: a, b: b, n: n})
		}
	}
	// Equal ranges, which LLVM's memcpy allows:
	// the bytes stay as they are, through the string instruction too.
	for _, n := range []int{ermsMin, 2 * ermsMin} {
		ops = append(ops, memOp{op: 0, a: 3, b: 3, n: n}, memOp{op: 0, a: 8200, b: 8200, n: n})
	}
	checkMemOps(t, work, ops)
}
