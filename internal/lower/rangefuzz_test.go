// Why: hand-written programs cannot cover every fact the range analysis
// combines; generated ones can.
// What: TestPC_RangeAgrees and FuzzRangeAgrees (genRangeProg),
// TestPC_RelAgrees and FuzzRelAgrees (genRelProg), TestPC_StepsUnchanged.
// How: small generated programs over every integer type, with constants,
// masks, shifts, casts, bit counts, stores into an array, branch
// refinements, short-circuit guards and an accumulator, are evaluated with
// the pass on (plus the post-borrow bounds fold) and off; eval is the
// reference semantics, so a wrong fact shows as a value or a panic that
// differs.

package lower

import (
	"fmt"
	"strings"
	"testing"
)

type pcTy struct {
	name   string
	bits   uint
	signed bool
}

var pcTys = []pcTy{
	{"i8", 8, true}, {"u8", 8, false}, {"i16", 16, true}, {"u16", 16, false},
	{"i32", 32, true}, {"u32", 32, false}, {"i64", 64, true}, {"u64", 64, false}, {"int", 64, true},
}

func (t pcTy) max() uint64 {
	if t.signed {
		return 1<<(t.bits-1) - 1
	}
	if t.bits == 64 {
		return ^uint64(0)
	}
	return 1<<t.bits - 1
}

// pcGen reads a program's choices from data, then zeros.
type pcGen struct {
	data []byte
	pos  int
	sb   strings.Builder
	vars []pcVar
	// calls: expr may call rule 11's intrinsics; raw and simd: the program
	// calls std.raw's or std.simd's.
	calls, raw, simd bool
}

type pcVar struct {
	name string
	ty   pcTy
}

func (g *pcGen) next() int {
	if g.pos >= len(g.data) {
		return 0
	}
	b := g.data[g.pos]
	g.pos++
	return int(b)
}

func (g *pcGen) ty() pcTy { return pcTys[g.next()%len(pcTys)] }

// lit is a non-negative literal of t: small, a byte, or near a boundary.
func (g *pcGen) lit(t pcTy) string {
	m := t.max()
	var v uint64
	switch g.next() % 4 {
	case 0:
		v = uint64(g.next() % 16)
	case 1:
		v = uint64(g.next())
	case 2:
		v = m - uint64(g.next()%32)
	default:
		v = m>>(uint(g.next())%t.bits) + uint64(g.next()%3)
	}
	if v > m {
		v = m
	}
	return fmt.Sprint(v)
}

// conv reads variable x as type t.
func (g *pcGen) conv(x pcVar, t pcTy) string {
	switch {
	case x.ty == t:
		return x.name
	case t.bits <= x.ty.bits && g.next()%2 == 0:
		return "Trunc[" + t.name + "](" + x.name + ")"
	}
	return "Cast[" + t.name + "](" + x.name + ")"
}

func (g *pcGen) pick() pcVar { return g.vars[g.next()%len(g.vars)] }

// asInt reads x as an int without a check that can trap.
func asInt(x pcVar) string {
	switch {
	case x.ty.name == "int":
		return x.name
	case x.ty.bits == 64:
		return "Trunc[int](" + x.name + ")"
	}
	return "int(" + x.name + ")"
}

func (g *pcGen) expr(t pcTy) string {
	x := g.conv(g.pick(), t)
	// Rule 11's calls (12, 13) and | (14) only where the generator asks
	// for them: the other generators keep the programs they made before.
	k := 12
	if g.calls {
		k = 15
	}
	switch g.next() % k {
	case 0:
		return x + " + " + g.lit(t)
	case 1:
		return x + " - " + g.lit(t)
	case 2:
		return x + " * " + g.lit(t)
	case 3:
		return "(" + x + " & " + g.lit(t) + ")"
	case 4:
		return "(" + x + " >> " + fmt.Sprint(uint(g.next())%t.bits) + ")"
	case 5:
		return "(" + x + " << " + fmt.Sprint(uint(g.next())%t.bits) + ")"
	case 6:
		return x + " % " + fmt.Sprint(g.next()%100+1)
	case 7:
		return x + " / " + fmt.Sprint(g.next()%100+1)
	case 8:
		return x + " + " + g.conv(g.pick(), t)
	case 9:
		return x + " - " + g.conv(g.pick(), t)
	case 10:
		return g.conv(pcVar{"a[" + asInt(g.pick()) + " & 15]", g.vars[1].ty}, t)
	case 11:
		return "Cast[" + t.name + "](a.Len()) + " + g.lit(t)
	case 12: // rule 11: one of std.raw's bit counts of a u64, in [0, 64]
		g.raw = true
		f := []string{"Popcount64", "Ctz64", "Clz64"}[g.next()%3]
		return "Cast[" + t.name + "](raw." + f + "(" + g.bits() + ")) + " + g.edgeLit(t, 64)
	case 13: // rule 11: the set lanes of an N-lane mask of a u64's low N bits, in [0, N]
		g.simd = true
		n := []int{2, 4, 8, 16, 32, 64}[g.next()%6]
		return fmt.Sprintf("Cast[%s](simd.Mask[u8, %d].FromBits(%s).CountSet()) + %s", t.name, n, g.bits(), g.edgeLit(t, uint64(n)))
	}
	return "(" + x + " | " + g.lit(t) + ")"
}

// bits is a u64 for rule 11's counts: a variable as u64, no bit set, or
// every bit set (a count of 64, or N lanes set; Ctz64 and Clz64 of 0).
func (g *pcGen) bits() string {
	switch g.next() % 3 {
	case 0:
		return "0"
	case 1:
		return "18446744073709551615"
	}
	return g.conv(g.pick(), pcTys[7])
}

// edgeLit is a literal of t whose sum with a count in [0, top] overflows
// t at most at top: t's maximum minus top, plus 0 to 2; or lit's.
func (g *pcGen) edgeLit(t pcTy, top uint64) string {
	if v := g.next() % 3; t.max() >= top && g.next()%4 != 0 {
		return fmt.Sprint(t.max() - top + uint64(v))
	}
	return g.lit(t)
}

// imports is the import lines the generated program needs.
func (g *pcGen) imports() string {
	s := ""
	if g.raw {
		s += "import std.raw\n"
	}
	if g.simd {
		s += "import std.simd\n"
	}
	if s != "" {
		s += "\n"
	}
	return s
}

// genRangeProg is H(): int with an array a, an accumulator s and a counted
// loop whose body defines, stores, branches and accumulates.
func genRangeProg(data []byte) string {
	g := &pcGen{data: data, calls: true}
	at, st := g.ty(), g.ty()
	n := g.next() + g.next()%2*g.next()
	fmt.Fprintf(&g.sb, "H(): int {\n\tvar r := 0\n\tvar a: [16]%s = [0; 16]\n\tvar s: %s = 0\n\tfor i in 0..%d {\n", at.name, st.name, n)
	g.vars = []pcVar{{"i", pcTys[8]}, {"a[0]", at}, {"a[i & 15]", at}, {"i", pcTys[8]}}
	k := g.next()%6 + 1
	for j := 0; j < k; j++ {
		switch g.next() % 6 {
		case 0:
			fmt.Fprintf(&g.sb, "\t\ta[%s & 15] = %s\n", asInt(g.pick()), g.expr(at))
		case 1:
			x := g.pick()
			fmt.Fprintf(&g.sb, "\t\tif %s < %s {\n\t\t\tr = r +%% %s\n\t\t}\n", x.name, g.lit(x.ty), asInt(x))
		case 2:
			// A refinement through a cast of x, then a check on x itself.
			x, ct := g.pick(), g.ty()
			w := pcVar{fmt.Sprintf("w%d", j), x.ty}
			fmt.Fprintf(&g.sb, "\t\tif %s < %s {\n\t\t\t%s: %s = %s + %s\n\t\t\tr = r ^ %s\n\t\t}\n",
				g.conv(x, ct), g.lit(ct), w.name, x.ty.name, x.name, g.lit(x.ty), asInt(w))
		case 3:
			fmt.Fprintf(&g.sb, "\t\ts += %s\n", g.expr(st))
		default:
			t := g.ty()
			v := pcVar{fmt.Sprintf("v%d", j), t}
			fmt.Fprintf(&g.sb, "\t\t%s: %s = %s\n\t\tr = r ^ %s\n", v.name, t.name, g.expr(t), asInt(v))
			g.vars = append(g.vars, v)
		}
	}
	fmt.Fprintf(&g.sb, "\t}\n\treturn r ^ %s ^ %s\n}\n", asInt(pcVar{"s", st}), asInt(pcVar{"a[3]", at}))
	return g.imports() + g.sb.String()
}

// rangeAgrees evaluates src with the pass on and off; "" when it does not
// build or both agree, else the difference.
func rangeAgrees(t testing.TB, src string) (built bool, diff string) {
	on, _ := pcBuild(t, src, pcOpt{fold: true})
	if on == nil {
		return false, ""
	}
	off, why := pcBuild(t, src, pcOpt{tweak: func(c *Config) { c.NoRanges = true }})
	if off == nil {
		return true, "built with the pass on only: " + why
	}
	a, b := pcRun(on), pcRun(off)
	if a != b {
		return true, fmt.Sprintf("pass on %q, off %q\n%s", a, b, src)
	}
	return true, ""
}

// TestPC_RangeAgrees runs 300 generated programs (seeds from splitmix64):
// the pass never changes a value or a panic, and most programs build.
func TestPC_RangeAgrees(t *testing.T) {
	if testing.Short() {
		t.Skip("300 programs take a few seconds")
	}
	x := uint64(0x5eed)
	built := 0
	const n = 300
	for i := 0; i < n; i++ {
		data := make([]byte, 48)
		for j := range data {
			x += 0x9e3779b97f4a7c15
			z := x
			z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
			z = (z ^ z>>27) * 0x94d049bb133111eb
			data[j] = byte(z ^ z>>31)
		}
		ok, d := rangeAgrees(t, genRangeProg(data))
		if ok {
			built++
		}
		if d != "" {
			t.Errorf("program %d: %s", i, d)
		}
	}
	t.Logf("%d of %d programs built", built, n)
	if built < n/2 {
		t.Errorf("only %d of %d generated programs built: the generator drifted from the language", built, n)
	}
}

// FuzzRangeAgrees is TestPC_RangeAgrees's property on fuzzed choices.
func FuzzRangeAgrees(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{3, 7, 200, 1, 5, 0, 2, 9, 3, 4, 1, 0, 6, 8, 2, 2, 10, 4})
	f.Fuzz(func(t *testing.T, data []byte) {
		if _, d := rangeAgrees(t, genRangeProg(data)); d != "" {
			t.Error(d)
		}
	})
}

// genRelProg is H(): int over a slice of an array, with loops whose index
// is bounded by a length, another index or a constant, off by one or not
// (rules 8 to 10): two-pointer scans, descending scans that read s[j - 1],
// a reassigned slice, a length copied and changed, and counters stepped
// by 1 or 2; and rule 5's compares through t = i + k, k + i or i - k:
// loops under i + k <= b, and branches whose u8 add overflows one past, at
// or one before the bound rule 5 gives i, with the wrapping, self and
// reassigned forms beside; and rule 12's short-circuit joins: loops under
// i < b && s[x] != v, || guards before an else that reads s, a negated &&,
// and a stored && tested after i changed or not.
func genRelProg(data []byte) string {
	g := &pcGen{data: data}
	var sb strings.Builder
	lo := g.next() % 9
	hi := lo + g.next()%9
	k := g.next() % 3
	fmt.Fprintf(&sb, "H(): int {\n\tvar a: [16]u8 = [0; 16]\n\tfor q in 0..16 {\n\t\ta[q] = Cast[u8](q * %d & 255)\n\t}\n", g.next()%13+1)
	fmt.Fprintf(&sb, "\tvar s := a[%d..%d]\n\tvar n := s.Len()\n\tvar r := 0\n", lo, hi)
	if g.next()%4 == 0 {
		nlo := g.next() % 5
		fmt.Fprintf(&sb, "\ts = a[%d..%d]\n", nlo, nlo+g.next()%12)
	}
	switch g.next() % 6 {
	case 0:
		fmt.Fprintf(&sb, "\tif s.Len() > %d {\n\t\tn = n + 1\n\t}\n", g.next()%8)
	case 1:
		fmt.Fprintf(&sb, "\tif s.Len() > %d {\n\t\tn = n * 2\n\t}\n", g.next()%8)
	}
	starts := []string{"0", "1", "2", "n - 1", "n", "-1", fmt.Sprint(k)}
	start := func() string { return starts[g.next()%len(starts)] }
	bounds := []string{"n", "n - 1", "n + 1", "s.Len()", "j", "j + 1", fmt.Sprint(hi - lo)}
	idxs := []string{"i", "i - 1", "i + 1", "j", "j - 1"}
	ops := []string{"<", "<="}
	fmt.Fprintf(&sb, "\tvar i := %s\n\tvar j := %s\n", start(), start())
	for t := g.next()%3 + 1; t > 0; t-- {
		switch g.next() % 7 {
		case 6: // rule 12: && and || joins, a negation, a stored condition with i changed or not before its test
			b := bounds[g.next()%len(bounds)]
			x := idxs[g.next()%len(idxs)]
			switch g.next() % 6 {
			case 4: // the read on the edge where the condition fails: a wrong edge proves it
				fmt.Fprintf(&sb, "\tif i >= 0 && i %s %s {\n\t\tr = r +%% 1\n\t} else {\n\t\tr = r +%% int(s[%s])\n\t}\n",
					ops[g.next()%2], b, x)
			case 5: // the read where a negated && holds
				fmt.Fprintf(&sb, "\tif !(i >= 0 && i %s %s) {\n\t\tr = r +%% int(s[%s])\n\t}\n", ops[g.next()%2], b, x)
			case 0:
				fmt.Fprintf(&sb, "\tfor i %s %s && s[%s] != %d {\n\t\tr = r +%% int(s[%s])\n\t\ti += 1\n\t}\n",
					ops[g.next()%2], b, x, g.next()%16*13&255, idxs[g.next()%len(idxs)])
			case 1:
				fmt.Fprintf(&sb, "\tif i < 0 || i %s %s || s[%s] == 0 {\n\t\tr = r +%% 1\n\t} else {\n\t\tr = r +%% int(s[%s])\n\t}\n",
					[]string{">=", ">"}[g.next()%2], b, x, idxs[g.next()%len(idxs)])
			case 2:
				fmt.Fprintf(&sb, "\tif !(i >= 0 && i %s %s) {\n\t\tr = r +%% 1\n\t} else {\n\t\tr = r +%% int(s[%s])\n\t}\n",
					ops[g.next()%2], b, x)
			default:
				step := ""
				if g.next()%2 == 0 {
					step = "\ti += 1\n"
				}
				fmt.Fprintf(&sb, "\tc%[1]d := i >= 0 && i %[2]s %[3]s\n%[4]s\tif c%[1]d {\n\t\tr = r +%% int(s[%[5]s])\n\t}\n",
					t, ops[g.next()%2], b, step, x)
			}
		case 4: // rule 5: a counter under i + k <= b, stepped by 1 to 4 (now and then 0), reading s[i + d]; an i8 sum that overflows
			k := g.next()%5 + 1
			b := []string{"n", "n + 1", "s.Len()", "j", fmt.Sprint(g.next() % 20)}[g.next()%5]
			e := []string{fmt.Sprint(g.next()%9 + 1), "Cast[i8](s[0] & 7) + 1"}[g.next()%2]
			step := fmt.Sprint(g.next()%4 + 1)
			if g.next()%8 == 0 {
				step = "0"
			}
			fmt.Fprintf(&sb, "\tvar l%[1]d: i8 = %[2]d\n\tfor i + %[3]d <= %[4]s {\n\t\tl%[1]d += %[5]s\n\t\tr = r +%% int(s[i + %[6]d])\n\t\ti += %[7]s\n\t}\n\tr = r +%% int(l%[1]d)\n",
				t, 40+g.next()%88, k, b, e, g.next()%(k+1), step)
		case 5: // rule 5 back through t = i + k, k + i or i - k, a wrapping +% or -%, i = i + k (whose old value is gone), and i reassigned before the compare
			k := g.next()%9 + 1
			c := 100 + g.next()%60
			ops := []string{"i + %d", "%d + i", "i - %d", "i +%% %d", "i -%% %d"}
			o := g.next() % len(ops)
			def := fmt.Sprintf(ops[o], k)
			// z's add overflows u8 one past, at or one before the bound
			// rule 5 gives i (c - k, or c + k for i - k).
			bound := c - k
			if o == 2 || o == 4 {
				bound = c + k
			}
			d := 256 - bound + g.next()%3 - 1
			fmt.Fprintf(&sb, "\ti = (i & 127) + %d\n", g.next()%120)
			switch g.next() % 3 {
			case 0:
				fmt.Fprintf(&sb, "\tt%[1]d := %[2]s\n\tif t%[1]d <= %[3]d {\n\t\tz: u8 = Cast[u8](i & 255) + %[4]d\n\t\tr = r +%% int(z)\n\t}\n", t, def, c, d)
			case 1:
				fmt.Fprintf(&sb, "\ti = %s\n\tif i <= %d {\n\t\tz: u8 = Cast[u8](i & 255) + %d\n\t\tr = r +%% int(z)\n\t}\n", def, c, d)
			default:
				fmt.Fprintf(&sb, "\tt%[1]d := %[2]s\n\ti = j & 127\n\tif t%[1]d <= %[3]d {\n\t\tz: u8 = Cast[u8](i & 255) + %[4]d\n\t\tr = r +%% int(z)\n\t}\n", t, def, c, d)
			}
		case 0: // ascending, bounded by a length, j or a constant
			fmt.Fprintf(&sb, "\tfor i %s %s {\n\t\tr = r +%% int(s[%s])\n\t\ti += %d\n\t}\n",
				ops[g.next()%2], bounds[g.next()%len(bounds)], idxs[g.next()%len(idxs)], g.next()%2+1)
		case 1: // two pointers
			fmt.Fprintf(&sb, "\tfor i %s j {\n\t\tr = r +%% int(s[i]) *%% 3 +%% int(s[j])\n\t\ti += 1\n\t\tj -= 1\n\t}\n", ops[g.next()%2])
		case 2: // descending, reading s[j] and s[j - 1] (InsertionSort's shape)
			fmt.Fprintf(&sb, "\tfor j > i && s[j] >= s[j - 1] {\n\t\tr = r +%% int(s[j - 1])\n\t\tj -= 1\n\t}\n")
		default: // a counter bounding an accumulator: steps of 1 or 2, a step of 0, a reset, a wrapping u8
			l0 := 60 + g.next()%68
			switch g.next() % 4 {
			case 0:
				fmt.Fprintf(&sb, "\tvar l: i8 = %d\n\tfor i < n {\n\t\tif s[i] %% 2 == 0 {\n\t\t\ti += 1\n\t\t}\n\t\ti += 1\n\t\tl += 1\n\t}\n\tr = r +%% int(l)\n", l0)
			case 1:
				fmt.Fprintf(&sb, "\tvar l: i8 = %d\n\tfor i < 12 {\n\t\tif s[0] %% 2 == 0 {\n\t\t\ti += 1\n\t\t} else {\n\t\t\ti += 0\n\t\t}\n\t\tl += 1\n\t}\n\tr = r +%% int(l)\n", l0)
			case 2:
				fmt.Fprintf(&sb, "\tvar l: i8 = %d\n\tfor i < 12 {\n\t\ti += 1\n\t\tl += 1\n\t\tif s[0] %% 3 == %d {\n\t\t\ti = 0\n\t\t}\n\t}\n\tr = r +%% int(l)\n", l0, g.next()%3)
			default:
				fmt.Fprintf(&sb, "\tvar l: i8 = %d\n\tvar c: u8 = %d\n\tfor c != %d {\n\t\tl += 1\n\t\tc +%%= 1\n\t}\n\tr = r +%% int(l)\n", l0, 240+g.next()%16, g.next()%40)
			}
		}
		fmt.Fprintf(&sb, "\ti = %s\n\tj = %s\n", start(), start())
	}
	sb.WriteString("\treturn r\n}\n")
	return sb.String()
}

// TestPC_RelAgrees runs 300 programs of genRelProg (seeds from
// splitmix64): the pass, the zone and rule 8's counters included, never
// change a value or a panic, and most programs build.
func TestPC_RelAgrees(t *testing.T) {
	if testing.Short() {
		t.Skip("300 programs take a few seconds")
	}
	x := uint64(0x2e1a)
	built, folded, panics := 0, 0, 0
	const n = 300
	for i := 0; i < n; i++ {
		data := make([]byte, 40)
		for j := range data {
			x += 0x9e3779b97f4a7c15
			z := x
			z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
			z = (z ^ z>>27) * 0x94d049bb133111eb
			data[j] = byte(z ^ z>>31)
		}
		src := genRelProg(data)
		ok, d := rangeAgrees(t, src)
		if ok {
			built++
			on, _ := pcBuild(t, src, pcOpt{fold: true})
			base, _ := pcBuild(t, src, pcOpt{fold: true, tweak: func(c *Config) { c.rangeMut = mutZoneOff }})
			if on != nil && base != nil {
				if on.counts("H").boundsLeft < base.counts("H").boundsLeft {
					folded++
				}
				if strings.Contains(pcRun(on), " panic ") {
					panics++
				}
			}
		}
		if d != "" {
			t.Errorf("program %d: %s", i, d)
		}
	}
	t.Logf("%d of %d programs built, %d with a check only the zone folds, %d that panic", built, n, folded, panics)
	if built < n/2 || folded < n/4 || panics < n/10 {
		t.Errorf("%d of %d generated programs built, %d with a zone fold, %d panic: the generator drifted", built, n, folded, panics)
	}
}

// TestPC_StepsUnchanged: the range pass leaves eval's step count alone:
// eval's step count is the same with the pass on and its bounds folds (the
// body a build keeps), with the zone off, and with the pass off, on every
// fact and edge program and on 300 of genRelProg's programs. A fold keeps
// its compare and a goto costs what a switch does, so no fold may move it.
func TestPC_StepsUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("300 programs take a few seconds")
	}
	var srcs []string
	for _, c := range pcFacts {
		// Fact programs are F with parameters; only H(): int runs.
		if strings.Contains(c.src, "H(): int") {
			srcs = append(srcs, c.src)
		}
	}
	for _, e := range pcEdge {
		srcs = append(srcs, e.src)
	}
	x := uint64(0x51e9)
	for i := 0; i < 300; i++ {
		data := make([]byte, 40)
		for j := range data {
			x += 0x9e3779b97f4a7c15
			z := x
			z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
			z = (z ^ z>>27) * 0x94d049bb133111eb
			data[j] = byte(z ^ z>>31)
		}
		srcs = append(srcs, genRelProg(data))
	}
	same, folded := 0, 0
	for i, src := range srcs {
		on, _ := pcBuild(t, src, pcOpt{fold: true})
		zoff, _ := pcBuild(t, src, pcOpt{fold: true, tweak: func(c *Config) { c.rangeMut = mutZoneOff }})
		off, _ := pcBuild(t, src, pcOpt{tweak: func(c *Config) { c.NoRanges = true }})
		if on == nil || zoff == nil || off == nil {
			continue
		}
		if on.counts("H").boundsLeft < off.counts("H").boundsLeft {
			folded++
		}
		a, sa := pcRunSteps(on)
		b, sb := pcRunSteps(zoff)
		c, sc := pcRunSteps(off)
		if a != c || b != c || sa != sc || sb != sc {
			t.Errorf("program %d: on %s in %d steps, zone off %s in %d, pass off %s in %d\n%s", i, a, sa, b, sb, c, sc, src)
			continue
		}
		same++
	}
	t.Logf("%d programs with equal steps, %d with a bounds check folded", same, folded)
	if same < len(srcs)/2 || folded < 50 {
		t.Errorf("%d of %d programs ran, %d folded a check: too few to test the fold", same, len(srcs), folded)
	}
}

// FuzzRelAgrees is TestPC_RelAgrees's property on fuzzed choices.
func FuzzRelAgrees(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{2, 7, 1, 3, 0, 0, 1, 2, 0, 1, 0, 0, 1, 3, 2, 1, 4})
	f.Fuzz(func(t *testing.T, data []byte) {
		if _, d := rangeAgrees(t, genRelProg(data)); d != "" {
			t.Error(d)
		}
	})
}
