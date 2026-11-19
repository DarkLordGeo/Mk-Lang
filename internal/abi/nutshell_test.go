// Why: the Nutshell targets must call C exactly as Linux does on the same
// architecture, without a golden file of their own.
// What: TestNutshellIsSysV, the x86_64-nutshell and aarch64-nutshell
// lowerings.
// How: x86_64-nutshell is System V x86_64, as on x86_64-linux and
// x86_64-freestanding (abi.go lowers every x86_64 target but Windows by
// SysV), checked row by row over testdata/suite.txt against x86_64-linux's
// lowering, so the hosted goldens need not move. The aarch64-nutshell
// target lowers by AAPCS64 as aarch64-linux does.

package abi

import (
	"testing"

	"mk/internal/types"
)

func TestNutshellIsSysV(t *testing.T) {
	w := loadSuite(t)
	pairs := []struct{ nut, ref types.Target }{
		{types.X86_64Nutshell, types.X86_64Linux},
		{types.Aarch64Nutshell, types.Aarch64Linux},
	}
	for _, p := range pairs {
		for _, r := range w.rows {
			got := printSig(w, p.nut, r, lowerRow(w, p.nut, r))
			want := printSig(w, p.ref, r, lowerRow(w, p.ref, r))
			if got != want {
				t.Errorf("%s (suite.txt:%d): %s %s, %s %s", r.name, r.line, p.nut.Name, got, p.ref.Name, want)
			}
		}
	}
}
