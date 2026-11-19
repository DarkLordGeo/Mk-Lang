// Why: a shared body whose call passes a value of its const parameter on to a
// forwarder must reach a different instance per value, in the evaluator too.
// What: TestValueKeyedSitePerRun over the forwarders H, HA, H2 (with Rec) and
// HG (with GF).
// How: compares each const, computed by the evaluator's call-site record
// (internal/eval exec.go, constCallee), with the value the program prints.

package driver

import (
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

const ctSiteMain = `struct Opts {
    n: int
}

K(const m: int): int {
    return m * 10
}

G(const n: int): int {
    return K(n)
}

// H passes an expression of its const parameter: H runs one shared body.
H(const f: int): int {
    return G(f + 1)
}

KA(const o: Opts): int {
    return o.n * 10
}

GA(const o: Opts): int {
    return KA(o)
}

// HA passes an aggregate built from its const parameter.
HA(const f: int): int {
    return GA(Opts(n: f))
}

// Rec recurses through a forwarder; H2's shared body reaches it at two
// values.
Rec(const n: int, k: int): int {
    if k == 0 {
        return 0
    }
    return G(n) + Rec(n, k - 1)
}

H2(const f: int, k: int): int {
    return Rec(f + 1, k)
}

// GF is a generic forwarder; HG reaches it at two types, each at the
// value of the call.
GF[T](const n: int, x: &T): int {
    return K(n)
}

HG(const f: int): int {
    i := 1
    s := "s"
    return GF(f + 1, &i) + GF(f + 1, &s) * 100
}

const A: int = H(1) + H(2) * 1000
const B: int = HA(1) + HA(2) * 1000
const C: int = H2(1, 2) + H2(2, 3) * 1000
const D: int = HG(1) + HG(2) * 10000

export Main(sys: &System): WriteError!void {
    ra := H(1) + H(2) * 1000
    rb := HA(1) + HA(2) * 1000
    rc := H2(1, 2) + H2(2, 3) * 1000
    rd := HG(1) + HG(2) * 10000
    try sys.Print("{A} {ra}\n{B} {rb}\n{C} {rc}\n{D} {rd}\n", (A, ra, B, rb, C, rc, D, rd))
}
`

func TestValueKeyedSitePerRun(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.mk": ctSiteMain})
	// A: G(2) + G(3)*1000; B: KA(1) + KA(2)*1000; C: Rec(2, 2) + Rec(3,
	// 3)*1000 = 40 + 90*1000; D: (K(2) + K(2)*100) + (K(3) + K(3)*100)*10000.
	const want = "30020 30020\n20010 20010\n90040 90040\n30302020 30302020\n"
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		exe := buildProgram(t, filepath.Join(dir, "main.mk"), types.X86_64Windows, prof, t.TempDir())
		out, errOut, code := runExe(t, types.X86_64Windows, exe, "")
		if got := strings.ReplaceAll(out, "\r\n", "\n"); code != 0 || got != want {
			t.Errorf("%v: exit %d, stdout (const, run-time)\n%s\nwant\n%s\n%s", prof, code, got, want, errOut)
		}
	}
}
