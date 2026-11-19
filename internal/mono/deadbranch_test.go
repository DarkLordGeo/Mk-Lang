// Why: code under an arch test that is false for the target is not
// instantiated, so its asm must never be judged against that target.
// What: TestDeadBranchNotJudged, TestPrunedAsmNotJudged and
// BenchmarkCheckWalkGRO.
// How: collects the same source for x86_64 and aarch64 and counts the asm
// statements left in each instance's blocks and the E1104 diagnostics.

package mono

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// targetDecls are std.core's TargetInfo, Arch and Os (std/core/core_target.mk).
const targetDecls = `struct TargetInfo {
    arch: Arch
    os: Os
    pointer_bytes: int
    vector_bits: int
}

enum Arch {
    X64
    Arm64
    Riscv64
}

enum Os {
    Linux
    Windows
    Macos
    Freestanding
}

`

// TestDeadBranchNotJudged: Spin's pause is under `Target.arch ==
// Arch.X64`. Collected for aarch64 it draws no E1104 (the branch is not in
// the instance); for x86_64 it is live and fits. Spin's yield, under a
// parenthesized aarch64 test, is the reverse: absent on x86_64, live (and
// E1104 no-register-table, as any live asm there) on aarch64. Pick's asm is under a
// compound test of Target constants (||, !, &&, an int field), live only on
// x86_64-linux. With no arch test, aarch64 judges the asm (E1104
// no-register-table).
func TestDeadBranchNotJudged(t *testing.T) {
	const tested = targetDecls + `Spin() {
    if Target.arch == Arch.X64 {
        unsafe "x86 only" {
            asm("pause")
        }
    }
    if (Target.arch == Arch.Arm64) {
        unsafe "aarch64 only, a parenthesized test" {
            asm("yield")
        }
    }
}

Pick(): u64 {
    var r: u64 = 0
    if Target.arch != Arch.X64 || Target.pointer_bytes != 8 {
        r = 1
    } else if !(Target.os == Os.Windows) && Target.arch == Arch.X64 {
        unsafe "x86 Linux only" {
            asm("mov {r}, 7", r: out(reg) r)
        }
    }
    return r
}
`
	const untested = targetDecls + `Spin() {
    unsafe "any target" {
        asm("pause")
    }
}
`
	for _, tc := range []struct {
		name   string
		target types.Target
		src    string
		want   []string
		asms   map[string]int // asm statements per instance, by key
	}{
		{"x86 asm, aarch64", types.Aarch64Linux, tested, []string{"no-register-table "}, map[string]int{"m.Spin": 1, "m.Pick": 0}},
		{"x86 asm, x86_64-linux", types.X86_64Linux, tested, nil, map[string]int{"m.Spin": 1, "m.Pick": 1}},
		{"x86 asm, x86_64-windows", types.X86_64Windows, tested, nil, map[string]int{"m.Spin": 1, "m.Pick": 0}},
		{"no arch test, aarch64", types.Aarch64Linux, untested, []string{"no-register-table "}, map[string]int{"m.Spin": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sw := newSrcWorldFor(t, "m", false, tc.target, true, tc.src)
			in, ds := Collect(sw, sw.limitRoots(), Options{Target: tc.target, Validate: true})
			if got := asmCodes(t, ds); strings.Join(got, "; ") != strings.Join(tc.want, "; ") {
				t.Errorf("E1104s %q, want %q", got, tc.want)
			}
			for key, want := range tc.asms {
				id, ok := sw.items.ByKey(types.KeyFromString(key))
				if !ok {
					t.Fatalf("no item %s", key)
				}
				for _, inst := range instancesOf(in, id) {
					if n := asmStatements(inst.Body); n != want {
						t.Errorf("%s on %s holds %d asm statements, want %d", key, tc.target.Name, n, want)
					}
				}
			}
		})
	}
}

// TestPrunedAsmNotJudged: the walk judges the asm statements of the
// instance's blocks, not every entry of Body.Asms, so an entry whose
// statement was pruned is not live code.
func TestPrunedAsmNotJudged(t *testing.T) {
	sw := newSrcWorld(t, `Nop() {
    unsafe "test" {
        asm("nop")
    }
}
`)
	nop := sw.item(t, "Nop")
	b := sw.bodies[nop]
	for bi := range b.Blocks {
		var keep []mir.Statement
		for _, st := range b.Blocks[bi].Stmts {
			if st.Kind != mir.StAsm {
				keep = append(keep, st)
			}
		}
		b.Blocks[bi].Stmts = keep
	}
	if len(b.Asms) != 1 {
		t.Fatalf("Body.Asms has %d entries, want the orphan one", len(b.Asms))
	}
	_, ds := Collect(sw, []Root{{Kind: RootExport, Item: nop}}, Options{Target: types.Aarch64Linux})
	if got := asmCodes(t, ds); len(got) != 0 {
		t.Errorf("an asm entry with no statement was judged: %q", got)
	}
}

func asmStatements(b *mir.Body) int {
	n := 0
	for _, blk := range b.Blocks {
		for _, st := range blk.Stmts {
			if st.Kind == mir.StAsm {
				n++
			}
		}
	}
	return n
}

// groModule is fns integer functions, each calling the previous one and
// two instances of a generic helper, so the walk substitutes bodies.
func groModule(fns int) string {
	var sb strings.Builder
	sb.WriteString("Twice[T](x: T): T {\n    return x\n}\n\nF0(a: u64): u64 {\n    return a\n}\n\n")
	for i := 1; i < fns; i++ {
		fmt.Fprintf(&sb, "F%d(a: u64): u64 {\n    var s := Twice[u64](a) +%% Cast[u64](Twice[u32](Trunc[u32](a)))\n    for j in 0..4 {\n        s = s *%% 31 +%% Cast[u64](j)\n    }\n    return F%d(s)\n}\n\n", i, i-1)
	}
	return sb.String()
}

// BenchmarkCheckWalkGRO: the instance walk with bodies that mk check runs
// for a general-regs-only bin, over 1,000 functions and their generic
// instances, substituting and judging each body (E1108) and dropping it. It
// reports; it gates nothing.
// go test ./internal/mono -run XXX -bench SY40_CheckWalkGRO
func BenchmarkCheckWalkGRO(b *testing.B) {
	sw := newSrcWorldFor(b, "m", false, types.X86_64Freestanding, false, groModule(1000))
	roots := sw.limitRoots()
	opt := Options{Target: types.X86_64Freestanding, GeneralRegsOnly: true, Bin: "kernel"}
	in, ds := Collect(sw, roots, opt)
	if len(ds) != 0 {
		b.Fatalf("diagnostics: %s", ds[0].Message)
	}
	n := len(in.List)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Collect(sw, roots, opt)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n), "ns/instance")
}
