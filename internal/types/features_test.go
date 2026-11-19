// Why: CPU features and targets are public facts (std.simd and the
// baseline a build assumes), so their table must stay fixed and exact.
// What: TestFeatureTable, TestClosure and TestTargetsAndBaseline.
// How: pins the feature rows' order (a row's bit is its index), the implication
// closure, vector_bits, the baselines and the freestanding target.

package types

import (
	"slices"
	"strings"
	"testing"
)

// TestFeatureTable: the x86_64 rows in their fixed order (a row's bit is
// its index, so the order is pinned), then aarch64's; names unique per
// architecture; every implied name a row of the same architecture; the level
// names have no LLVM spelling and every other row has one starting with +.
func TestFeatureTable(t *testing.T) {
	x86 := "sse sse2 sse3 ssse3 sse4.1 sse4.2 popcnt cx16 sahf avx avx2 bmi bmi2 f16c fma lzcnt movbe xsave " +
		"pclmul aes sha gfni vpclmulqdq avx512f avx512bw avx512cd avx512dq avx512vl avx512vbmi x86-64-v2 x86-64-v3 x86-64-v4"
	var gotX86, gotArm []string
	for _, f := range CPUFeatures() {
		switch f.Arch {
		case ArchX64:
			gotX86 = append(gotX86, f.Name)
		case ArchArm64:
			gotArm = append(gotArm, f.Name)
		default:
			t.Errorf("%s: architecture %v", f.Name, f.Arch)
		}
		for _, m := range f.Implies {
			if _, ok := CPUFeatureByName(f.Arch, m); !ok {
				t.Errorf("%s implies %s, which %v has no row for", f.Name, m, f.Arch)
			}
		}
		level := strings.HasPrefix(f.Name, "x86-64-")
		if level != (f.LLVM == "") || (!level && !strings.HasPrefix(f.LLVM, "+")) {
			t.Errorf("%s: LLVM spelling %q", f.Name, f.LLVM)
		}
	}
	if strings.Join(gotX86, " ") != x86 {
		t.Errorf("x86_64 rows:\n%s\nwant\n%s", strings.Join(gotX86, " "), x86)
	}
	if strings.Join(gotArm, " ") != "neon fp16 dotprod sve aes sha2 pmull" {
		t.Errorf("aarch64 rows: %v", gotArm)
	}
	if len(gotX86) > 128 || len(gotArm) > 128 {
		t.Error("a row's bit must fit CpuFeatures' 128 bits")
	}
	// The row at index 11 is LLVM's bmi, and bmi1 names none.
	if f, _ := CPUFeatureByName(ArchX64, "bmi"); f.LLVM != "+bmi" || CPUFeatures()[11].Name != "bmi" {
		t.Errorf("bmi is LLVM's +bmi at index 11, got %q", f.LLVM)
	}
	if _, ok := CPUFeatureByName(ArchX64, "bmi1"); ok {
		t.Error("bmi1 is still a row")
	}
	c := CPUFeatures()
	c[0].Name = "changed"
	if CPUFeatures()[0].Name != "sse" {
		t.Error("CPUFeatures is not a copy")
	}
}

// TestClosure: the closure is sorted, follows implication transitively,
// drops another architecture's names, and names the first unknown one.
func TestClosure(t *testing.T) {
	set, bad := CPUClosure(ArchX64, []string{"avx2"})
	if want := []string{"avx", "avx2", "sse", "sse2", "sse3", "sse4.1", "sse4.2", "ssse3"}; !slices.Equal(set, want) || bad != "" {
		t.Errorf("closure of avx2 = %v %q, want %v", set, bad, want)
	}
	set, bad = CPUClosure(ArchX64, []string{"x86-64-v4"})
	for _, n := range []string{"x86-64-v4", "x86-64-v3", "x86-64-v2", "avx512vl", "fma", "f16c", "movbe", "cx16", "sse"} {
		if !slices.Contains(set, n) || bad != "" {
			t.Errorf("closure of x86-64-v4 lacks %s: %v", n, set)
		}
	}
	if slices.Contains(set, "avx512vbmi") || slices.Contains(set, "gfni") {
		t.Errorf("x86-64-v4 implies only the levels' rows: %v", set)
	}
	if !slices.IsSorted(set) {
		t.Errorf("closure not sorted: %v", set)
	}
	set, bad = CPUClosure(ArchX64, []string{"neon", "popcnt"})
	if !slices.Equal(set, []string{"popcnt"}) || bad != "" {
		t.Errorf("an aarch64 name on x86_64: %v %q, want [popcnt] and no bad name", set, bad)
	}
	set, bad = CPUClosure(ArchArm64, []string{"sse2", "dotprod"})
	if !slices.Equal(set, []string{"dotprod"}) || bad != "" {
		t.Errorf("an x86_64 name on aarch64: %v %q", set, bad)
	}
	set, bad = CPUClosure(ArchX64, []string{"sse2", "avx3", "avx9"})
	if bad != "avx3" || !slices.Equal(set, []string{"sse", "sse2"}) {
		t.Errorf("unknown names: %v %q, want bad avx3", set, bad)
	}
}

// TestTargetsAndBaseline: vector_bits is 128 everywhere; the baseline is
// x86-64-v2's closure on hosted x86_64, empty on freestanding, neon on
// aarch64; the freestanding target is defined but not yet listed.
func TestTargetsAndBaseline(t *testing.T) {
	for _, tg := range append(Targets(), X86_64Freestanding) {
		if tg.VectorBits != 128 {
			t.Errorf("%s: VectorBits %d", tg.Name, tg.VectorBits)
		}
	}
	v2, _ := CPUClosure(ArchX64, []string{"x86-64-v2"})
	for _, tg := range []Target{X86_64Windows, X86_64Linux} {
		if b := CPUBaseline(tg); !slices.Equal(b, v2) || !slices.Contains(b, "sse4.2") || slices.Contains(b, "avx") {
			t.Errorf("%s baseline %v", tg.Name, b)
		}
	}
	if b := CPUBaseline(X86_64Freestanding); len(b) != 0 {
		t.Errorf("freestanding baseline %v, want none", b)
	}
	if b := CPUBaseline(Aarch64Linux); !slices.Equal(b, []string{"neon"}) {
		t.Errorf("aarch64 baseline %v", b)
	}
	fs := X86_64Freestanding
	if fs.Name != "x86_64-freestanding" || fs.OS != OSFreestanding || fs.Arch != ArchX64 || fs.LLVMTriple != "x86_64-unknown-none-elf" || fs.PointerBytes != 8 || !fs.Built {
		t.Errorf("X86_64Freestanding = %+v", fs)
	}
	// Found by name and in AllTargets(), never in the hosted Targets().
	if got, ok := TargetByName(fs.Name); !ok || got != fs {
		t.Errorf("TargetByName(%q) = %+v, %v", fs.Name, got, ok)
	}
	for _, h := range Targets() {
		if h.OS == OSFreestanding {
			t.Errorf("Targets() lists the freestanding target %s", h.Name)
		}
	}
	if all := AllTargets(); len(all) != len(Targets())+3 || all[len(Targets())] != fs {
		t.Errorf("AllTargets() = %v", all)
	}
	if TargetVectorBits.String() != "vector_bits" || TargetPointerBytes != 2 || TargetVectorBits != 3 {
		t.Errorf("TargetField: %v %d", TargetVectorBits, TargetVectorBits)
	}
	if RefuseAsm.String() != "asm" || RefuseVolatile.String() != "volatile" || RefuseAsm != RefuseGlobal+1 {
		t.Errorf("refusals: %v %v", RefuseAsm, RefuseVolatile)
	}
}
