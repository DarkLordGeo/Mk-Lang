// Why: nothing runs aarch64 SHA-1 code, and mnemonics alone miss a wrong K,
// a missing lane reversal, swapped operands or a wrong round group.
// What: TestSHA1_LoweringIR pins each helper's IR on aarch64 and x86_64.
// How: s1Stored expands each stored result into its data-flow tree and
// compares it with the lowering; pure Go, so it never skips.

package codegen

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"mk/internal/types"
)

// s1Body is the body of helper name in ll, from its define to its closing
// brace.
func s1Body(t *testing.T, ll, name string) string {
	t.Helper()
	i := strings.Index(ll, "define internal void @"+name+"(")
	if i < 0 {
		t.Fatalf("no helper %s in the IR", name)
	}
	j := strings.Index(ll[i:], "\n}\n")
	if j < 0 {
		t.Fatalf("helper %s has no end", name)
	}
	return ll[i : i+j]
}

var (
	s1Def   = regexp.MustCompile(`^\s*(%v\d+) = (.*)$`)
	s1Store = regexp.MustCompile(`^\s*store <4 x i32> (%v\d+), ptr (%p\d+), align 16$`)
	s1Use   = regexp.MustCompile(`%v\d+`)
)

// s1Stored is the helper's one store, its value expanded: every %vN in an
// instruction replaced by its own instruction in brackets, down to the
// loads of the parameters. out is the pointer parameter stored to.
func s1Stored(t *testing.T, body string) (tree, out string) {
	t.Helper()
	defs := map[string]string{}
	var val string
	for _, ln := range strings.Split(body, "\n") {
		if m := s1Def.FindStringSubmatch(ln); m != nil {
			defs[m[1]] = m[2]
		}
		if m := s1Store.FindStringSubmatch(ln); m != nil {
			if val != "" {
				t.Fatalf("two stores:\n%s", body)
			}
			val, out = m[1], m[2]
		}
	}
	if val == "" {
		t.Fatalf("no store:\n%s", body)
	}
	var expand func(v string, depth int) string
	expand = func(v string, depth int) string {
		d, ok := defs[v]
		if !ok || depth > 16 {
			t.Fatalf("%s has no definition:\n%s", v, body)
		}
		return s1Use.ReplaceAllStringFunc(d, func(u string) string { return "[" + expand(u, depth+1) + "]" })
	}
	return expand(val, 0), out
}

// The pieces of an expected tree.
func s1Load(p int) string { return fmt.Sprintf("load <4 x i32>, ptr %%p%d, align 16", p) }

func s1Rev(x string) string {
	return "shufflevector <4 x i32> [" + x + "], <4 x i32> [" + x + "], <4 x i32> <i32 3, i32 2, i32 1, i32 0>"
}

func s1Call(ret, fn string, args ...string) string {
	return "call " + ret + " @llvm." + fn + "(" + strings.Join(args, ", ") + ")"
}

func s1V(x string) string { return "<4 x i32> [" + x + "]" }

// TestSHA1_LoweringIR: each helper's stored value, as a tree, is the
// lowering exactly, on aarch64-linux and x86_64-linux: on aarch64, K per
// round group added to the reversed words, sha1c, sha1p, sha1m, sha1p with
// e 0 between <3, 2, 1, 0> reversals, sha1h of lane 3 added to lane 3, and
// sha1su1(sha1su0(w0, w1, w2), w3) on reversed words; on x86_64 the
// instructions direct, the round group as sha1rnds4's immediate. The
// lowering was checked against the Intel SDM and Arm ARM pseudocode and
// crypto/sha1.
func TestSHA1_LoweringIR(t *testing.T) {
	// FIPS 180-4 section 4.2.1's K, as LLVM prints an i32 (signed).
	k := [4]int32{0x5a827999, 0x6ed9eba1, -0x70e44324, -0x359d3e2a}
	if uint32(k[2]) != 0x8f1bbcdc || uint32(k[3]) != 0xca62c1d6 {
		t.Fatalf("K constants: %x %x", uint32(k[2]), uint32(k[3]))
	}
	arm := [4]string{"sha1c", "sha1p", "sha1m", "sha1p"} // Ch, Parity, Maj, Parity
	for _, target := range []types.Target{types.Aarch64Linux, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			t.Parallel()
			ll := s1IR(t, target)
			isArm := target.Arch == types.ArchArm64
			check := func(name, wantOut, want string) {
				t.Helper()
				got, out := s1Stored(t, s1Body(t, ll, name))
				if out != wantOut || got != want {
					t.Errorf("%s stores to %s:\n  %s\nwant to %s:\n  %s", name, out, got, wantOut, want)
				}
			}
			for g := 0; g < 4; g++ {
				var want string
				if isArm {
					ks := fmt.Sprintf("i32 %d", k[g])
					wk := "add <4 x i32> [" + s1Rev(s1Load(1)) + "], <" + strings.Join([]string{ks, ks, ks, ks}, ", ") + ">"
					want = s1Rev(s1Call("<4 x i32>", "aarch64.crypto."+arm[g], s1V(s1Rev(s1Load(0))), "i32 0", s1V(wk)))
				} else {
					want = s1Call("<4 x i32>", "x86.sha1rnds4", s1V(s1Load(0)), s1V(s1Load(1)), fmt.Sprintf("i8 %d", g))
				}
				check(fmt.Sprintf("mk.intr.raw.Sha1Rounds4.%d", g), "%p2", want)
			}
			var nextE, sched string
			if isArm {
				lane3 := func(p int) string { return "extractelement <4 x i32> [" + s1Load(p) + "], i32 3" }
				e := s1Call("i32", "aarch64.crypto.sha1h", "i32 ["+lane3(0)+"]")
				nextE = "insertelement <4 x i32> [" + s1Load(1) + "], i32 [add i32 [" + lane3(1) + "], [" + e + "]], i32 3"
				su0 := s1Call("<4 x i32>", "aarch64.crypto.sha1su0", s1V(s1Rev(s1Load(0))), s1V(s1Rev(s1Load(1))), s1V(s1Rev(s1Load(2))))
				sched = s1Rev(s1Call("<4 x i32>", "aarch64.crypto.sha1su1", s1V(su0), s1V(s1Rev(s1Load(3)))))
			} else {
				nextE = s1Call("<4 x i32>", "x86.sha1nexte", s1V(s1Load(0)), s1V(s1Load(1)))
				msg1 := s1Call("<4 x i32>", "x86.sha1msg1", s1V(s1Load(0)), s1V(s1Load(1)))
				sched = s1Call("<4 x i32>", "x86.sha1msg2", s1V("xor <4 x i32> ["+msg1+"], ["+s1Load(2)+"]"), s1V(s1Load(3)))
			}
			check("mk.intr.raw.Sha1NextE", "%p2", nextE)
			check("mk.intr.raw.Sha1Schedule4", "%p4", sched)
		})
	}
}
