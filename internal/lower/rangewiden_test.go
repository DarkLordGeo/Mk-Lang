// Why: the range pass widens an interval only on a retreating edge, so an
// outer counter's copy that an inner loop never assigns keeps the outer
// header's bound inside the inner loop.
// What: TestPC_NestAgrees, TestPC_WidenNoLoss, TestPC_NestedSums.
// How: less widening can change no value (eval agrees on generated nested
// loops), loses no proof the old widening made (every site proved with
// mutWidenEvery stays proved), and proves the nested sums spectralnorm's A
// needs.

package lower

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// pcSites are a body's proofs: the statements whose operator is a NoWrap
// form or whose cast is Trunc (proved, written or a counter latch), and
// the bounds panics still reachable.
type pcSites struct {
	proved map[[2]int]bool
	left   map[int]bool
}

func sitesOf(b *mir.Body) pcSites {
	s := pcSites{proved: map[[2]int]bool{}, left: map[int]bool{}}
	targeted := make([]bool, len(b.Blocks))
	var buf []mir.BlockID
	for i := range b.Blocks {
		buf = b.Successors(mir.BlockID(i), buf[:0])
		for _, x := range buf {
			targeted[x] = true
		}
	}
	for i := range b.Blocks {
		blk := &b.Blocks[i]
		if blk.Term.Kind == mir.TermPanic && blk.Term.Panic == types.PanicBounds && targeted[i] {
			s.left[i] = true
		}
		for j, st := range blk.Stmts {
			if st.Kind != mir.StAssign {
				continue
			}
			r := st.Rvalue
			switch {
			case r.Kind == mir.RvBinary && (r.Bin == mir.AddNoWrap || r.Bin == mir.SubNoWrap || r.Bin == mir.MulNoWrap),
				r.Kind == mir.RvCast && r.Cast == mir.CastTrunc:
				s.proved[[2]int{i, j}] = true
			}
		}
	}
	return s
}

// lost is what base proves that head does not, and gained the reverse
// (proved sites and bounds checks), for one body of an unchanged CFG.
func lost(base, head pcSites) (lost, gained int) {
	for k := range base.proved {
		if !head.proved[k] {
			lost++
		}
	}
	for k := range head.proved {
		if !base.proved[k] {
			gained++
		}
	}
	for k := range head.left {
		if !base.left[k] {
			lost++
		}
	}
	for k := range base.left {
		if !head.left[k] {
			gained++
		}
	}
	return lost, gained
}

// genNestProg is H(): int with two nested counted loops whose bounds are
// immediates or hidden (pvV), an outer counter read in the inner loop as
// a copy that the inner loop assigns or never assigns, and checked
// arithmetic of every integer type on the counters near its limits.
func genNestProg(data []byte) string {
	g := &pcGen{data: data}
	var sb strings.Builder
	sb.WriteString(pvV)
	bound := func(span int) string {
		v := g.next()%span - 2
		if g.next()%4 == 0 {
			return fmt.Sprintf("V(%d)", v)
		}
		return fmt.Sprint(v)
	}
	sb.WriteString("H(): int {\n\tvar r := 0\n\tvar a: [16]int = [3; 16]\n")
	fmt.Fprintf(&sb, "\tfor i in %s..%s {\n", bound(8), bound(140))
	varC := g.next()%2 == 0
	if varC {
		sb.WriteString("\t\tvar c := i\n")
	} else {
		sb.WriteString("\t\tc := i\n")
	}
	fmt.Fprintf(&sb, "\t\tfor j in %s..%s {\n", bound(6), bound(20))
	g.vars = []pcVar{{"i", pcTys[8]}, {"j", pcTys[8]}, {"c", pcTys[8]}}
	ctr := func() string { return g.vars[g.next()%3].name }
	for q, k := 0, g.next()%4+1; q < k; q++ {
		switch g.next() % 5 {
		case 0:
			if varC {
				fmt.Fprintf(&g.sb, "\t\t\tc += %d\n", g.next()%3)
				continue
			}
			fallthrough
		case 1:
			x := g.pick()
			fmt.Fprintf(&g.sb, "\t\t\tif %s < %d {\n\t\t\t\tr = r +%% %s\n\t\t\t}\n", x.name, g.next()%130, asInt(x))
		case 2:
			fmt.Fprintf(&g.sb, "\t\t\tr = r +%% (%s + %s) * (%s + %s + 1) / 2 + %s + 1\n", ctr(), ctr(), ctr(), ctr(), ctr())
		default:
			t := g.ty()
			v := pcVar{fmt.Sprintf("v%d", q), t}
			fmt.Fprintf(&g.sb, "\t\t\t%s: %s = %s\n\t\t\tr = r ^ %s\n", v.name, t.name, g.expr(t), asInt(v))
			g.vars = append(g.vars, v)
		}
	}
	sb.WriteString(g.sb.String())
	sb.WriteString("\t\t}\n\t\tr = r +% c\n\t}\n\treturn r\n}\n")
	return sb.String()
}

// pcNestSeeds are genNestProg's inputs (splitmix64).
func pcNestSeeds(n int) [][]byte {
	x := uint64(0x850)
	var out [][]byte
	for i := 0; i < n; i++ {
		data := make([]byte, 40)
		for j := range data {
			x += 0x9e3779b97f4a7c15
			z := x
			z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
			z = (z ^ z>>27) * 0x94d049bb133111eb
			data[j] = byte(z ^ z>>31)
		}
		out = append(out, data)
	}
	return out
}

// TestPC_NestAgrees: on 300 generated nested loops the pass (its
// widening included) gives every program the value or panic it has with
// the pass off, and the pass proves a site the old widening
// left checked in many of them.
func TestPC_NestAgrees(t *testing.T) {
	built, gainers, panics := 0, 0, 0
	for i, data := range pcNestSeeds(300) {
		src := genNestProg(data)
		ok, d := rangeAgrees(t, src)
		if d != "" {
			t.Errorf("program %d: %s", i, d)
			continue
		}
		if !ok {
			continue
		}
		built++
		head, _ := pcBuild(t, src, pcOpt{fold: true})
		base, _ := pcBuild(t, src, pcOpt{fold: true, tweak: func(c *Config) { c.rangeMut = mutWidenEvery }})
		if head == nil || base == nil {
			t.Fatalf("program %d builds once only\n%s", i, src)
		}
		if out := pcRun(base); out != pcRun(head) {
			t.Errorf("program %d: old widening gives %q, the retreating-edge widening %q", i, out, pcRun(head))
		}
		if strings.Contains(pcRun(head), " panic ") {
			panics++
		}
		if l, g := lost(sitesOf(base.body("H")), sitesOf(head.body("H"))); l > 0 {
			t.Errorf("program %d: the retreating-edge widening loses %d proofs\n%s", i, l, src)
		} else if g > 0 {
			gainers++
		}
	}
	t.Logf("%d of 300 built, %d prove more under the retreating-edge widening, %d panic", built, gainers, panics)
	if built < 200 || gainers < 50 || panics < 30 {
		t.Errorf("the generator is too weak: built %d, gainers %d, panics %d", built, gainers, panics)
	}
}

// TestPC_WidenNoLoss: on every program the range tests run (facts, edges,
// genRelProg's and genRangeProg's first 300, the bench kernels and the
// codegen kernels), every body proves under today's widening every site it
// proved under the old widening; the count of bodies that prove more is
// logged.
func TestPC_WidenNoLoss(t *testing.T) {
	type prog struct {
		name, src string
		bin       bool
	}
	var progs []prog
	for i, c := range pcFacts {
		progs = append(progs, prog{fmt.Sprintf("fact %d", i), c.src, false})
	}
	for _, e := range pcEdge {
		progs = append(progs, prog{e.name, e.src, false})
	}
	for _, e := range pvEdge {
		progs = append(progs, prog{e.name, e.src, false})
	}
	for i, data := range pcNestSeeds(300) {
		progs = append(progs, prog{fmt.Sprintf("nest %d", i), genRelProg(data), false})
		progs = append(progs, prog{fmt.Sprintf("range %d", i), genRangeProg(append(data, data[:8]...)), false})
	}
	for _, f := range pcKernels(t) {
		progs = append(progs, prog{kernelName(f), readKernel(t, f), true})
	}
	for name, src := range pvCodegenKernels(t) {
		progs = append(progs, prog{name, src, false})
	}
	bodies, gainers := 0, 0
	for _, p := range progs {
		head, _ := pcBuild(t, p.src, pcOpt{bin: p.bin, fold: true})
		base, _ := pcBuild(t, p.src, pcOpt{bin: p.bin, fold: true, tweak: func(c *Config) { c.rangeMut = mutWidenEvery }})
		if head == nil || base == nil {
			continue
		}
		for _, k := range head.keys {
			bodies++
			l, g := lost(sitesOf(base.body(k)), sitesOf(head.body(k)))
			if l > 0 {
				t.Errorf("%s, %s: the retreating-edge widening loses %d proofs", p.name, k, l)
			}
			if g > 0 {
				gainers++
				t.Logf("%s, %s: %d more proofs", p.name, k, g)
			}
		}
	}
	t.Logf("%d bodies, %d prove more under the retreating-edge widening", bodies, gainers)
	if bodies < 500 {
		t.Errorf("only %d bodies built", bodies)
	}
}

// TestPC_NestedSums: the motivating shape, spectralnorm's A(i, j) written
// into its loops: every + of the counters is proved and the * stays
// checked; under the old widening none was.
func TestPC_NestedSums(t *testing.T) {
	src := "F(v: []f64): int {\n\tn := v.Len()\n\tvar r := 0\n\tfor i in 0..n {\n\t\tfor j in 0..n {\n\t\t\tr = r +% ((i + j) * (i + j + 1) / 2 + i + 1)\n\t\t}\n\t}\n\treturn r\n}\n"
	for _, c := range []struct {
		mut  rangeMut
		want string
	}{
		{mutNone, "add=0 sub=0 mul=1 nowrap=5 casts=0 bounds=0/0"},
		{mutWidenEvery, "add=5 sub=0 mul=1 nowrap=0 casts=0 bounds=0/0"},
	} {
		m := c.mut
		got := pcLower(t, src, pcOpt{fold: true, tweak: func(cfg *Config) { cfg.rangeMut = m }}).counts("F").String()
		if got != c.want {
			t.Errorf("mut %d: got %s, want %s", c.mut, got, c.want)
		}
	}
}
