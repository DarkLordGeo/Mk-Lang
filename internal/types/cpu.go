// Why: a cpu clause and --cpu name CPU features, and check, codegen and
// std.simd must agree on what each name means and implies.
//
// What: CPUFeature and the cpuFeatures table, CPUFeatures,
// CPUFeatureByName, CPUClosure (the closure under implication) and
// CPUBaseline (each target's fixed baseline).
//
// How: the table is data, one row per feature with its architecture, the
// names it implies and LLVM's spelling. A feature's bit in std.simd's
// CpuFeatures is its index among its architecture's rows, so the table is
// append-only.

package types

import "sort"

// CPUFeature is one row of the feature table: a name a cpu clause may use,
// its architecture, the names it implies, and LLVM's spelling of it ("+avx2";
// "" for a level name such as x86-64-v3, which only expands). A feature's bit
// in std.simd's CpuFeatures is its index among its architecture's rows, so
// the table is append-only.
type CPUFeature struct {
	Name    string
	Arch    Arch
	Implies []string
	LLVM    string
}

var cpuFeatures = []CPUFeature{
	{"sse", ArchX64, nil, "+sse"},
	{"sse2", ArchX64, []string{"sse"}, "+sse2"},
	{"sse3", ArchX64, []string{"sse2"}, "+sse3"},
	{"ssse3", ArchX64, []string{"sse3"}, "+ssse3"},
	{"sse4.1", ArchX64, []string{"ssse3"}, "+sse4.1"},
	{"sse4.2", ArchX64, []string{"sse4.1"}, "+sse4.2"},
	{"popcnt", ArchX64, nil, "+popcnt"},
	{"cx16", ArchX64, nil, "+cx16"},
	{"sahf", ArchX64, nil, "+sahf"},
	{"avx", ArchX64, []string{"sse4.2"}, "+avx"},
	{"avx2", ArchX64, []string{"avx"}, "+avx2"},
	{"bmi", ArchX64, nil, "+bmi"}, // LLVM 21's name, was bmi1 (index unchanged)
	{"bmi2", ArchX64, nil, "+bmi2"},
	{"f16c", ArchX64, []string{"avx"}, "+f16c"},
	{"fma", ArchX64, []string{"avx"}, "+fma"},
	{"lzcnt", ArchX64, nil, "+lzcnt"},
	{"movbe", ArchX64, nil, "+movbe"},
	{"xsave", ArchX64, nil, "+xsave"},
	{"pclmul", ArchX64, nil, "+pclmul"},
	{"aes", ArchX64, nil, "+aes"},
	{"sha", ArchX64, nil, "+sha"},
	{"gfni", ArchX64, []string{"sse2"}, "+gfni"},
	{"vpclmulqdq", ArchX64, []string{"pclmul", "avx"}, "+vpclmulqdq"},
	{"avx512f", ArchX64, []string{"avx2", "fma", "f16c"}, "+avx512f"},
	{"avx512bw", ArchX64, []string{"avx512f"}, "+avx512bw"},
	{"avx512cd", ArchX64, []string{"avx512f"}, "+avx512cd"},
	{"avx512dq", ArchX64, []string{"avx512f"}, "+avx512dq"},
	{"avx512vl", ArchX64, []string{"avx512f"}, "+avx512vl"},
	{"avx512vbmi", ArchX64, []string{"avx512f"}, "+avx512vbmi"},
	{"x86-64-v2", ArchX64, []string{"sse4.2", "popcnt", "cx16", "sahf"}, ""},
	{"x86-64-v3", ArchX64, []string{"x86-64-v2", "avx2", "bmi", "bmi2", "f16c", "fma", "lzcnt", "movbe", "xsave"}, ""},
	{"x86-64-v4", ArchX64, []string{"x86-64-v3", "avx512f", "avx512bw", "avx512cd", "avx512dq", "avx512vl"}, ""},
	// aarch64 (base neon); the first four imply nothing. aes (FEAT_AES,
	// PMULL included) and sha2 imply neon, appended so earlier rows keep
	// their bits.
	{"neon", ArchArm64, nil, "+neon"},
	{"fp16", ArchArm64, nil, "+fullfp16"},
	{"dotprod", ArchArm64, nil, "+dotprod"},
	{"sve", ArchArm64, nil, "+sve"},
	{"aes", ArchArm64, []string{"neon"}, "+aes"},
	{"sha2", ArchArm64, []string{"neon"}, "+sha2"},
	// pmull names aarch64's polynomial multiply alone (LLVM enables it with
	// +aes), so a clause can ask for x86_64's pclmul and aarch64's PMULL
	// without also asking for x86_64's aes.
	{"pmull", ArchArm64, []string{"neon"}, "+aes"},
}

// CPUFeatures returns the table, in its fixed order: x86_64's rows, then
// aarch64's. The slice is a copy; its Implies slices are shared and must not
// be modified.
func CPUFeatures() []CPUFeature {
	return append([]CPUFeature(nil), cpuFeatures...)
}

// CPUFeatureByName finds the row of name for arch.
func CPUFeatureByName(arch Arch, name string) (CPUFeature, bool) {
	for _, f := range cpuFeatures {
		if f.Arch == arch && f.Name == name {
			return f, true
		}
	}
	return CPUFeature{}, false
}

// CPUClosure is the sorted closure under Implies of the names of arch: each
// name of arch, and every name it implies, transitively. A name of another
// architecture is dropped (a cpu clause ignores another architecture's
// names). bad is the first name no row of any architecture has (E1106),
// "" when all are known; set still holds the closure of the known ones.
func CPUClosure(arch Arch, names []string) (set []string, bad string) {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(n string) {
		if seen[n] {
			return
		}
		f, ok := CPUFeatureByName(arch, n)
		if !ok {
			return
		}
		seen[n] = true
		for _, m := range f.Implies {
			walk(m)
		}
	}
	for _, n := range names {
		known := false
		for _, f := range cpuFeatures {
			if f.Name == n {
				known = true
				break
			}
		}
		if !known && bad == "" {
			bad = n
		}
		walk(n)
	}
	for n := range seen {
		set = append(set, n)
	}
	sort.Strings(set)
	return set, bad
}

// CPUBaseline is the target's fixed baseline set: x86-64-v2's
// closure on hosted x86_64, none on x86_64-freestanding (whose bins default
// to general-regs-only), neon on aarch64. --cpu never changes it.
func CPUBaseline(t Target) []string {
	switch {
	case t.Arch == ArchX64 && t.OS == OSFreestanding:
		return nil
	case t.Arch == ArchX64:
		s, _ := CPUClosure(ArchX64, []string{"x86-64-v2"})
		return s
	case t.Arch == ArchArm64:
		return []string{"neon"}
	}
	return nil
}
