// Why: on Win64 the callee-saved registers clang's memset and memcmp save
// need unwind entries, or a fault resumes with the wrong rsi and rdi.
// What: TestSupportUnwindTables: the support pack's IR functions carry
// uwtable on every hosted target and get unwind entries.
// How: FaultOwner (std/sys/windows/fault.mk) passes a memory function's row
// (kind 3) at frame 0 on to its caller; tests/programs/cseh's cases 2 and 5
// are the run test.

package codegen

import (
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

// TestSupportUnwindTables: memset and memcmp carry uwtable on every
// hosted target (freestanding none), and clang -O2 gives each an unwind
// entry that records its pushes of rsi (and memset's of rdi).
func TestSupportUnwindTables(t *testing.T) {
	for _, target := range types.Targets() {
		ll := supportLL(t, target)
		for _, sym := range []string{symMemcpy, symMemset, symMemcmp} {
			head, _, _ := strings.Cut(fnText(ll, sym), "\n")
			if head == "" {
				t.Fatalf("%s: no %s", target.Name, sym)
			}
			if want := target.OS != types.OSFreestanding; strings.Contains(head, " uwtable") != want {
				t.Errorf("%s: %s uwtable %v, want %v:\n%s", target.Name, sym, !want, want, head)
			}
		}
		if target.OS != types.OSWindows {
			continue
		}
		obj := clangC(t, target, t.TempDir(), "support", ll, "-O2")
		unw := runTool(t, filepath.Join(needLLVM(t), "llvm-readobj"), "--unwind", obj)
		for _, want := range []struct {
			sym  string
			regs []string
		}{{symMemset, []string{"RSI", "RDI"}}, {symMemcmp, []string{"RSI"}}} {
			i := strings.Index(unw, "StartAddress: "+want.sym+" ")
			if i < 0 {
				t.Errorf("%s has no unwind entry:\n%s", want.sym, unw)
				continue
			}
			entry := unw[i:]
			if j := strings.Index(entry[1:], "StartAddress: "); j >= 0 {
				entry = entry[:j+1]
			}
			for _, r := range want.regs {
				if !strings.Contains(entry, "PUSH_NONVOL reg="+r) {
					t.Errorf("%s's unwind entry does not record its push of %s:\n%s", want.sym, r, entry)
				}
			}
		}
	}
}
