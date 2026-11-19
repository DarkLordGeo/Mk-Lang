// Why: compile-time evaluation and generated code must agree on every
// result, or a const and the same expression at run time differ.
// What: TestCarryCompileTimeMatchesRunTime over raw.AddCarry64 and
// raw.SubBorrow64.
// How: evaluates a table of rows at compile time (eval's carryAdd and
// carrySub, through a const initializer), computes them at run time
// (codegen's with.overflow pairs), and checks both against math/bits for a
// carry in of 0 or 1.

package driver

import (
	"math/bits"
	"strconv"
	"strings"
	"testing"
)

const carryManifest = `const Package = pkg.Package(
    name: "c",
    bins: [pkg.Bin(name: "c", root: "src", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["src"],
    links: [],
)
`

const carryMain = `import std.raw

const Max: u64 = 0xFFFFFFFFFFFFFFFF

// Each row is x, y, c; the carry in runs above 1 too (defined).
const Rows: [6][3]u64 = [[Max, 1, 0], [Max, Max, 1], [0, 0, 0], [1, 2, 5], [Max, 0, 2], [Max, Max, Max]]

// Folded is every row's sum, carry, diff and borrow, computed at compile time.
Folded(): [24]u64 {
    var out: [24]u64 = [0; 24]
    for i in 0..6 {
        a := raw.AddCarry64(Rows[i][0], Rows[i][1], Rows[i][2])
        s := raw.SubBorrow64(Rows[i][0], Rows[i][1], Rows[i][2])
        out[4 * i] = a.sum
        out[4 * i + 1] = a.carry
        out[4 * i + 2] = s.diff
        out[4 * i + 3] = s.borrow
    }
    return out
}

const AtCompileTime: [24]u64 = Folded()

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    run := Folded()
    for i in 0..24 {
        a := AtCompileTime[i]
        b := run[i]
        try Print(mut out, "{a} {b}\n", (a, b))
    }
}
`

func TestCarryCompileTimeMatchesRunTime(t *testing.T) {
	dir := testPkg(t, map[string]string{"pkg.mk": carryManifest, "src/main.mk": carryMain})
	out := buildRun(t, dir, t.TempDir())
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n")), "\n")
	if len(lines) != 24 {
		t.Fatalf("%d lines, want 24:\n%s", len(lines), out)
	}
	const max = ^uint64(0)
	rows := [6][3]uint64{{max, 1, 0}, {max, max, 1}, {0, 0, 0}, {1, 2, 5}, {max, 0, 2}, {max, max, max}}
	for i, l := range lines {
		f := strings.Fields(l)
		if len(f) != 2 || f[0] != f[1] {
			t.Errorf("value %d: compile time and run time differ: %q", i, l)
			continue
		}
		r := rows[i/4]
		if r[2] > 1 {
			continue // a carry in above 1: eval and the binary agree, math/bits has no such case
		}
		sum, carry := bits.Add64(r[0], r[1], r[2])
		diff, borrow := bits.Sub64(r[0], r[1], r[2])
		want := [4]uint64{sum, carry, diff, borrow}[i%4]
		if got, err := strconv.ParseUint(f[0], 10, 64); err != nil || got != want {
			t.Errorf("value %d of row %v: %s, want %d", i%4, r, f[0], want)
		}
	}
}
