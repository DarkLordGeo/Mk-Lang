// Why: kernels and boot code built for x86_64-freestanding call C and
// assembly through the same SysV ABI as Linux, and must lower the same way.
// What: abiTargets, isSysV, TestFreestandingSuite and
// TestFreestandingIsSysV.
// How: the target's ABI is SysV (abi.go: every x86_64 target but Windows),
// checked against clang's record for x86_64-unknown-none-elf like the
// hosted targets, and line for line against x86_64-linux.

package abi

import (
	"slices"
	"testing"

	"mk/internal/types"
)

// abiTargets is types.Targets() plus x86_64-freestanding, which has its
// clang record (testdata/clang/x86_64-freestanding.txt, written by the
// probe) but is not among the hosted Targets(). Listing it here lets the
// oracle and the probe-hash test judge it too.
func abiTargets() []types.Target {
	ts := types.Targets()
	if !slices.ContainsFunc(ts, func(t types.Target) bool { return t.Name == types.X86_64Freestanding.Name }) {
		ts = append(ts, types.X86_64Freestanding)
	}
	return ts
}

// isSysV reports a target whose C ABI is SysV x86_64: Linux and
// freestanding (the SysV rule of TestABISlotInvariants).
func isSysV(t types.Target) bool {
	return t.Arch == types.ArchX64 && (t.OS == types.OSLinux || t.OS == types.OSFreestanding)
}

// TestFreestandingSuite is the suite golden of x86_64-freestanding; it
// must equal x86_64-linux's line for line (one SysV classification), which
// TestFreestandingIsSysV checks without the golden.
func TestFreestandingSuite(t *testing.T) { suiteGolden(t, types.X86_64Freestanding) }

func TestFreestandingIsSysV(t *testing.T) {
	w := loadSuite(t)
	for _, r := range w.rows {
		free := printSig(w, types.X86_64Freestanding, r, lowerRow(w, types.X86_64Freestanding, r))
		linux := printSig(w, types.X86_64Linux, r, lowerRow(w, types.X86_64Linux, r))
		if free != linux {
			t.Errorf("%s (suite.txt:%d): freestanding %s, linux %s", r.name, r.line, free, linux)
		}
	}
}
