// Why: the Nutshell targets must be known by name without joining the hosted
// targets every run test iterates.
// What: TestNutshellTargets for x86_64-nutshell and aarch64-nutshell.
// How: Os.Nutshell comes after the four earlier variants; both targets are
// found by name and listed by AllTargets() after freestanding, never in the
// hosted Targets() the run tests iterate.

package types

import (
	"slices"
	"testing"
)

func TestNutshellTargets(t *testing.T) {
	if OSNutshell != 4 || OSNutshell.String() != "Nutshell" || OSFreestanding != 3 {
		t.Errorf("Os.Nutshell = %d %q; the prelude enum Os lists it fifth", OSNutshell, OSNutshell.String())
	}
	x, a := X86_64Nutshell, Aarch64Nutshell
	if x.Name != "x86_64-nutshell" || x.Arch != ArchX64 || x.OS != OSNutshell || x.PointerBytes != 8 ||
		x.LLVMTriple != "x86_64-unknown-nutshell-elf" || !x.Built || x.VectorBits != 128 {
		t.Errorf("X86_64Nutshell = %+v", x)
	}
	if a.Name != "aarch64-nutshell" || a.Arch != ArchArm64 || a.OS != OSNutshell || a.PointerBytes != 8 ||
		a.LLVMTriple != "aarch64-unknown-nutshell-elf" || a.Built || a.VectorBits != 128 {
		t.Errorf("Aarch64Nutshell = %+v", a)
	}
	for _, n := range []Target{x, a} {
		if got, ok := TargetByName(n.Name); !ok || got != n {
			t.Errorf("TargetByName(%q) = %+v, %v", n.Name, got, ok)
		}
		if slices.Contains(Targets(), n) {
			t.Errorf("Targets() lists %s", n.Name)
		}
	}
	all := AllTargets()
	if !slices.Equal(all[len(all)-2:], []Target{x, a}) {
		t.Errorf("AllTargets() = %v, want the Nutshell targets last", all)
	}
	// The hosted x86_64 baseline (x86-64-v2): SSE and the red zone are the
	// hosted ABI's, as on x86_64-linux.
	if !slices.Equal(CPUBaseline(x), CPUBaseline(X86_64Linux)) {
		t.Errorf("x86_64-nutshell baseline %v, want x86_64-linux's", CPUBaseline(x))
	}
}
