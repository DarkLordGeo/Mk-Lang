// Why: closed const value arguments that mono does not fold itself must reach
// Collect already folded by check, or the instance-limit walk raises an ICE.
// What: TestClosedConstArgsInstantiate and
// TestFnInstancesStayAccepted.
// How: builds source worlds with each argument form and requires Collect to
// finish with no diagnostic and no ICE.

package mono

import (
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

// TestClosedConstArgsInstantiate: a closed const value argument that
// mono does not fold itself (two literals, a negation, a conversion, an
// array's Len, a division, SizeOf of a closed type in an operator) reaches
// Collect as the constant check folded and recorded on the argument (check
// constarg.go). Without that, each was "const parameter argument is not a
// constant (operand kind 0)", an ICE in the instance-limit walk.
func TestClosedConstArgsInstantiate(t *testing.T) {
	for _, tc := range []struct {
		name, arg string
		want      string // the instance's name
	}{
		{"two literals", "2 + 3", "m.Lane[5]"},
		{"negated const", "-K", "m.Lane[-7]"},
		{"conversion", "int(K8)", "m.Lane[3]"},
		{"array length", "Tab.Len()", "m.Lane[3]"},
		{"division", "K / 2", "m.Lane[3]"},
		{"SizeOf of a closed type, plus", "SizeOf[u32]() + 1", "m.Lane[5]"},
		{"open generic beside a closed operator", "N + (2 * 3)", "m.Lane[11]"},
		{"open generic beside a conversion", "N + int(3)", "m.Lane[8]"},
		{"an array length in a closed operator", "Tab.Len() * 2", "m.Lane[6]"},
		// An if-expression with a closed condition, and `<<` dropping the
		// bits shifted out (7 << 62 wraps to -2^62).
		{"if with a closed condition", "if K > 3 { 1 } else { 2 }", "m.Lane[1]"},
		{"shift dropping bits", "K << 62", "m.Lane[-4611686018427387904]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "const K: int = 7\nconst K8: u8 = 3\nconst Tab: [3]int = [1, 2, 3]\n\n" +
				"Lane(const i: int): int {\n    return i\n}\n\n" +
				"G[const N: int](): int {\n    return Lane(" + tc.arg + ")\n}\n\n" +
				"Root(): int {\n    return G[5]()\n}\n"
			sw := newSrcWorld(t, src)
			var in *Instances
			var n int
			if ie := source.Guard("collect", source.Span{}, func() {
				got, dd := Collect(sw, []Root{{Kind: RootExport, Item: sw.item(t, "Root")}}, Options{Target: types.X86_64Windows, Validate: true})
				in, n = got, len(dd)
				if n > 0 {
					t.Errorf("Collect: %v %s", dd[0].Code, dd[0].Message)
				}
			}); ie != nil {
				t.Fatalf("Collect ICE: %v", ie)
			}
			var names []string
			for _, x := range instancesOf(in, sw.item(t, "Lane")) {
				names = append(names, x.Name)
			}
			if len(names) != 1 || names[0] != tc.want {
				t.Fatalf("Lane instances %q, want %q", strings.Join(names, ", "), tc.want)
			}
		})
	}
}

// TestFnInstancesStayAccepted: a generic function's instance (`H[int]`,
// `H[T]`) as a const argument, bare to a type-parameter const or inside a
// struct, array or record literal, is accepted and runs. Making each E0705 at
// check would fail newSrcWorld here. Check, lower and Collect must take k04,
// k20 and k21 with no diagnostic and no ICE.
func TestFnInstancesStayAccepted(t *testing.T) {
	const decls = `H[X](x: int): int {
    return x + 1
}

struct PF {
    f: fn(int): int
}

Ap[F: Plain](const f: F): int {
    return 1
}

Id[F: Plain](const f: F): F {
    return f
}

Sp(const p: PF): int {
    return p.f(1)
}

Ar(const a: [2]fn(int): int): int {
    return a[0](1) + a[1](2)
}

Rc(const r: (f: fn(int): int, n: int)): int {
    return r.f(r.n)
}

W[T](): int {
    return Sp(PF(f: H[T]))
}
`
	for _, tc := range []struct{ name, body string }{
		{"k04 to a type-parameter const", "return Ap(H[int])"},
		{"k20 returned and called", "h := Id(H[int])\n    return h(4)"},
		{"k21 inside an array literal", "return Ar([H[int], H[u8]])"},
		// k18, k22 and k29 (an instance in a struct or record literal) are
		// pinned at check (TestConstArgForms): this world's Collect,
		// without the driver's Unit.Lookup (see codegen call.go), meets the
		// literal as operand kind 0 here.
	} {
		t.Run(tc.name, func(t *testing.T) {
			sw := newSrcWorld(t, decls+"\nRoot(): int {\n    "+tc.body+"\n}\n")
			if ie := source.Guard("collect", source.Span{}, func() {
				_, dd := Collect(sw, []Root{{Kind: RootExport, Item: sw.item(t, "Root")}}, Options{Target: types.X86_64Windows, Validate: true})
				if len(dd) > 0 {
					t.Errorf("Collect: %v %s", dd[0].Code, dd[0].Message)
				}
			}); ie != nil {
				t.Fatalf("Collect ICE: %v", ie)
			}
		})
	}
}
