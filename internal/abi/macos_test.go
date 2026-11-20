// Why: x86_64-macos calls C through the System V ABI, as x86_64-linux does,
// and has no clang record of its own.
// What: TestMacosX64IsSysV.
// How: every suite row lowers to the same printed signature for
// x86_64-macos as for x86_64-linux. clang 21.1.8 agrees: the probe run for
// x86_64-apple-macosx11.0.0 gives x86_64-linux's record body byte for byte,
// so that record is the clang evidence for both.

package abi

import (
	"testing"

	"mk/internal/types"
)

func TestMacosX64IsSysV(t *testing.T) {
	w := loadSuite(t)
	for _, r := range w.rows {
		mac := printSig(w, types.X86_64Macos, r, lowerRow(w, types.X86_64Macos, r))
		linux := printSig(w, types.X86_64Linux, r, lowerRow(w, types.X86_64Linux, r))
		if mac != linux {
			t.Errorf("%s (suite.txt:%d): x86_64-macos %s, linux %s", r.name, r.line, mac, linux)
		}
	}
}
