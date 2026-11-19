// Why: mem.Fixed's padding and refusal paths are where a bump allocator
// goes wrong, and the Go model alone could share the same mistake.
// What: TestSTDMEM_FixedLIFO (a LIFO free gives its padding back at
// any depth and alignment mix) and TestSTDMEM_FixedRefusals.
// How: random stack walks judged by properties of the Mk run alone; every
// refusal branch of the Fixed entries is reached and changes nothing.

package std

import (
	"fmt"
	"strings"
	"testing"
)

// lifoCase is one buffer of TestSTDMEM_FixedLIFO.
type lifoCase struct {
	name   string
	shift  int
	seed   uint64
	rounds int
}

const lifoSize = 4096

// lifoOps is a random stack walk in RunFixed's encoding: per round, pushes
// (allocations) and pops (frees of the newest live block), up to depth 10,
// then the rest popped, then the whole buffer past its first 8-byte
// boundary allocated at alignment 1 (op 6) and freed. sigs[k] is, for the k-th op that allocates, the stack it builds
// (every block's size and alignment, bottom first).
func lifoOps(seed uint64, rounds int) (ops []int, sigs []string) {
	s := splitmix(seed)
	sizes := []int{0, 1, 3, 8, 13, 24, 64, 120}
	aligns := []int{1, 2, 4, 8, 8, 16, 32, 64, 128}
	type blk struct{ size, align int }
	var stack []blk
	sig := func() string {
		var b strings.Builder
		for _, x := range stack {
			fmt.Fprintf(&b, "%d/%d ", x.size, x.align)
		}
		return b.String()
	}
	pop := func() {
		ops = append(ops, 2, len(stack)-1)
		stack = stack[:len(stack)-1]
		sigs = append(sigs, "")
	}
	for range rounds {
		for range 30 {
			if len(stack) < 10 && (len(stack) == 0 || s.below(100) < 60) {
				x := blk{sizes[s.below(len(sizes))], aligns[s.below(len(aligns))]}
				ops = append(ops, 0, x.size, x.align)
				stack = append(stack, x)
				sigs = append(sigs, sig())
			} else {
				pop()
			}
		}
		for len(stack) > 0 {
			pop()
		}
		ops = append(ops, 6)
		sigs = append(sigs, "probe")
		ops = append(ops, 2, 0)
		sigs = append(sigs, "")
	}
	return ops, sigs
}

// TestSTDMEM_FixedLIFO: random stack walks over 4 KiB
// Fixed buffers at the eight address skews, with sizes 0 to 120 and
// alignments 1 to 128, dev and release. Judged on the Mk run alone: no
// allocation is refused (the deepest stack needs under 2.7 KiB); every
// allocation that builds a stack of the same blocks gets the same offset
// (history independence: no free leaves anything behind); and after each
// round the whole buffer past its first 8-byte boundary is one allocation
// there. The Go model must
// print the same lines too. Before the fix the probe was refused within the
// first rounds (planted by hand with the earlier FixedFreeEntry).
func TestSTDMEM_FixedLIFO(t *testing.T) {
	t.Parallel()
	var cases []lifoCase
	for sh := range 8 {
		cases = append(cases, lifoCase{fmt.Sprintf("lifo%d", sh), sh, 100 + uint64(sh), 25})
	}
	var b strings.Builder
	b.WriteString("import std.memfix\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n")
	ops := map[string][]int{}
	sigs := map[string][]string{}
	for i, c := range cases {
		o, sg := lifoOps(c.seed, c.rounds)
		ops[c.name], sigs[c.name] = o, sg
		fmt.Fprintf(&b, "    try Print(mut out, \"case %s\\n\")\n", c.name)
		fmt.Fprintf(&b, "    var buf%d: [%d]u8 = [0; %d]\n", i, lifoSize+8, lifoSize+8)
		fmt.Fprintf(&b, "    ops%d := %s\n", i, mkInts(o))
		fmt.Fprintf(&b, "    try memfix.RunFixed(mut out, mut buf%d[%d..%d], ops%d[..])\n", i, c.shift, c.shift+lifoSize, i)
	}
	b.WriteString("}\n")
	src := b.String()
	for _, flags := range [][]string{nil, {"--release"}} {
		got := splitCases(collExec(t, memBuild(t, src, flags...)))
		for _, c := range cases {
			g := got[c.name]
			lines := strings.Split(strings.TrimSuffix(g, "\n"), "\n")
			if len(lines) != len(sigs[c.name])+1 {
				t.Fatalf("%v %s: %d lines for %d ops\n%s", flags, c.name, len(lines)-1, len(sigs[c.name]), g)
			}
			var skew int
			if _, err := fmt.Sscanf(lines[0], "skew %d", &skew); err != nil {
				t.Fatalf("%v %s: no skew line: %q", flags, c.name, lines[0])
			}
			lines = lines[1:]
			seen := map[string]string{}
			for k, l := range lines {
				sg := sigs[c.name][k]
				if sg == "" {
					continue
				}
				if l == "a null" {
					t.Errorf("%v %s (skew %d) op %d: refused (stack %q)", flags, c.name, skew, k, sg)
					continue
				}
				if sg == "probe" {
					if w := fmt.Sprintf("a %d", (-skew)&7); l != w {
						t.Errorf("%v %s (skew %d) op %d: the whole buffer after a round gives %q, want %s", flags, c.name, skew, k, l, w)
					}
					continue
				}
				if prev, ok := seen[sg]; ok && prev != l {
					t.Errorf("%v %s (skew %d) op %d: stack %q gave %q before, %q now", flags, c.name, skew, k, sg, prev, l)
				}
				seen[sg] = l
			}
			if want := fixedExpect(t, skew, lifoSize, ops[c.name]); g != want {
				t.Errorf("%v %s (skew %d): differs from the model", flags, c.name, skew)
				diffLines(t, g, want)
			}
		}
	}
}

// TestSTDMEM_FixedRefusals: every refusal branch of
// FixedAllocEntry and FixedResizeEntry, and the no-op branches of
// FixedFreeEntry, over a 64-byte buffer at two skews, dev and release:
// alignments 3, 0, -8, -16 and 2^31, a negative size, alignment 2^30 (valid,
// but it cannot fit), a resize to -1, a resize with a negative old size and
// one larger than used, frees with a size larger than used and a negative
// one. Each prints the refusal, and the next allocation lands exactly
// where it would have without them. The expected lines are written out
// here, not taken from the model (which must agree as well).
func TestSTDMEM_FixedRefusals(t *testing.T) {
	t.Parallel()
	ops := []int{
		0, 8, 8, // a p0
		0, 8, 3, // bad alignments
		0, 8, 0,
		0, 8, -8,
		0, 8, -16,
		0, 8, 1 << 31,
		0, -1, 8, // negative size
		0, -1, 64,
		0, 8, 1 << 30, // valid, too large to fit (unless the buffer's address sits within 48 bytes below a 2^30 boundary: about 2^-24)
		1, 0, -1, // resize to -1
		5, 0, -1, 8, // negative old
		5, 0, 100, 16, // old > used
		4, 0, 100, 8, // free larger than used
		4, 0, -1, 8, // negative free
		0, 8, 8, // a p0+8: nothing moved
		0, 64, 1, // does not fit
		2, 1, // LIFO frees
		2, 0,
		6, // a p0, all 64 - p0 bytes: the first block's padding came back too
	}
	want := func(skew int) string {
		p0 := (-skew) & 7
		lines := []string{
			fmt.Sprintf("skew %d", skew),
			fmt.Sprintf("a %d", p0),
			"a null", "a null", "a null", "a null", "a null", "a null", "a null", "a null",
			fmt.Sprintf("r %d false", p0),
			fmt.Sprintf("y %d false", p0),
			fmt.Sprintf("y %d false", p0),
			fmt.Sprintf("x %d", p0),
			fmt.Sprintf("x %d", p0),
			fmt.Sprintf("a %d", p0+8),
			"a null",
			fmt.Sprintf("f %d", p0+8),
			fmt.Sprintf("f %d", p0),
			fmt.Sprintf("a %d", p0),
		}
		return strings.Join(lines, "\n") + "\n"
	}
	shifts := []int{0, 3}
	var b strings.Builder
	b.WriteString("import std.memfix\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n")
	fmt.Fprintf(&b, "    ops := %s\n", mkInts(ops))
	for i, sh := range shifts {
		fmt.Fprintf(&b, "    try Print(mut out, \"case s%d\\n\")\n", i)
		fmt.Fprintf(&b, "    var buf%d: [%d]u8 = [0; %d]\n", i, 64+sh+1, 64+sh+1)
		fmt.Fprintf(&b, "    try memfix.RunFixed(mut out, mut buf%d[%d..%d], ops[..])\n", i, sh, sh+64)
	}
	b.WriteString("}\n")
	for _, flags := range [][]string{nil, {"--release"}} {
		got := splitCases(collExec(t, memBuild(t, b.String(), flags...)))
		for i := range shifts {
			g := got[fmt.Sprintf("s%d", i)]
			var skew int
			if _, err := fmt.Sscanf(g, "skew %d\n", &skew); err != nil {
				t.Fatalf("%v s%d: no skew line: %q", flags, i, g)
			}
			if w := want(skew); g != w {
				t.Errorf("%v s%d (skew %d): refusals", flags, i, skew)
				diffLines(t, g, w)
			}
			if w := fixedExpect(t, skew, 64, ops); g != w {
				t.Errorf("%v s%d (skew %d): differs from the model", flags, i, skew)
				diffLines(t, g, w)
			}
		}
	}
}
