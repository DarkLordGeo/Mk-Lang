// Why: match compiles to a decision tree, with a sequential fallback when
// the tree would grow too large; both must pick the arm the language does.
// What: TestMatch_DecisionTree, TestMatch_SizeGuardFallback,
// TestMatch_BindingModes, and the MIR walker (walk).
// How: a small walker runs the MIR of a match over a table of inputs, so the
// tree and the sequential fallback are compared by the arm they choose.

package lower

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
)

// lowerWith is lowerSrc with a Config adjusted by set.
func lowerWith(t *testing.T, src string, set func(*Config)) *lowered {
	t.Helper()
	w := newWorld(t, true)
	b := w.add("m", opt{}, src)
	b.wantClean(t)
	out := &lowered{w: w, b: b, bodies: map[string]*mir.Body{}}
	cfg := w.lowerCfg()
	set(cfg)
	for _, id := range b.m.Units() {
		it := w.items.Get(id)
		body := w.c.Check(b.m, id)
		if len(body.Diags) > 0 {
			t.Fatalf("%s: check diagnostics:\n%s", it.Key, dump(w, body.Diags))
		}
		var mb *mir.Body
		if ie := source.Guard(it.Key.String(), it.Span, func() { mb = Lower(cfg, FromCheck(cfg, w.c, body)) }); ie != nil {
			t.Fatalf("lower ICE in %s: %v", it.Key, ie)
		}
		k := b.key(id)
		out.bodies[k] = mb
		out.keys = append(out.keys, k)
	}
	return out
}

// mval is a walker value: a scalar, or a tag with fields (an enum variant
// by discriminant, or ?T with tag 1 and the payload as field 0).
type mval struct {
	bits   uint64
	fields []mval
}

func some(v mval) mval { return mval{bits: 1, fields: []mval{v}} }
func variant(d uint64, f ...uint64) mval {
	v := mval{bits: d}
	for _, x := range f {
		v.fields = append(v.fields, mval{bits: x})
	}
	return v
}

// walk runs body b with parameter 1 bound to arg and returns the int it
// returns. It knows only what a match over enums and ints lowers to.
func walk(t *testing.T, b *mir.Body, arg mval) uint64 {
	t.Helper()
	locals := map[mir.LocalID]mval{1: arg}
	read := func(p mir.Place) mval {
		v := locals[p.Local]
		for _, s := range b.Proj(p) {
			switch s.Kind {
			case mir.ProjField:
				if int(s.A) >= len(v.fields) {
					t.Fatalf("walk: field %d of a value with %d", s.A, len(v.fields))
				}
				v = v.fields[s.A]
			case mir.ProjDowncast, mir.ProjDeref:
			default:
				t.Fatalf("walk: projection %v", s.Kind)
			}
		}
		return v
	}
	op := func(o mir.Operand) mval {
		switch o.Kind {
		case mir.OpImm:
			return mval{bits: o.Bits}
		case mir.OpCopy, mir.OpMove:
			return read(o.Place)
		}
		t.Fatalf("walk: operand kind %v", o.Kind)
		return mval{}
	}
	bb := mir.BlockID(0)
	for steps := 0; steps < 10000; steps++ {
		blk := b.Blocks[bb]
		for _, st := range blk.Stmts {
			if st.Kind != mir.StAssign {
				continue
			}
			rv := st.Rvalue
			var v mval
			switch rv.Kind {
			case mir.RvUse:
				v = op(rv.A)
			case mir.RvRef:
				v = read(rv.Place)
			case mir.RvDiscriminant:
				v = mval{bits: read(rv.Place).bits}
			case mir.RvBinary:
				a, c := op(rv.A).bits, op(rv.B).bits
				switch rv.Bin {
				case mir.Eq:
					if a == c {
						v.bits = 1
					}
				case mir.Add:
					v.bits = a + c
				case mir.Sub:
					v.bits = a - c
				default:
					t.Fatalf("walk: binary %v", rv.Bin)
				}
			default:
				t.Fatalf("walk: rvalue %v", rv.Kind)
			}
			if st.Place.Proj.Len != 0 {
				t.Fatalf("walk: store through a projection")
			}
			locals[st.Place.Local] = v
		}
		switch tm := blk.Term; tm.Kind {
		case mir.TermGoto:
			bb = tm.Target
		case mir.TermSwitch:
			on := op(tm.On).bits
			next := tm.Default
			for _, c := range b.Cases[tm.Cases.Start : tm.Cases.Start+tm.Cases.Len] {
				if c.Value == on {
					next = c.Target
				}
			}
			if next == mir.NoBlock {
				t.Fatalf("walk: switch on %d with no case and no default", on)
			}
			bb = next
		case mir.TermReturn:
			return locals[0].bits
		default:
			t.Fatalf("walk: terminator %v in bb%d", tm.Kind, bb)
		}
	}
	t.Fatal("walk: no return")
	return 0
}

const shapeMatch = `enum Shape {
	Circle(r: int)
	Rect(w: int, h: int)
	Dot
}
F(s: ?Shape): int {
	match s {
		some(.Circle(r: 0)) {
			return 1
		}
		some(.Circle(r)) {
			return 2 + r - r
		}
		some(.Rect(w: 1 | 2, h: 3)) {
			return 3
		}
		some(.Rect(w, h: 3)) {
			return 4 + w - w
		}
		some(_) {
			return 5
		}
		none {
			return 6
		}
	}
}
`

// shapeInputs pairs each input of F with the arm the language picks: the
// first that matches, in source order.
var shapeInputs = []struct {
	in   mval
	want uint64
}{
	{mval{}, 6},
	{some(variant(0, 0)), 1},
	{some(variant(0, 5)), 2},
	{some(variant(1, 1, 3)), 3},
	{some(variant(1, 2, 3)), 3},
	{some(variant(1, 7, 3)), 4},
	{some(variant(1, 1, 4)), 5},
	{some(variant(2)), 5},
}

func TestMatch_DecisionTree(t *testing.T) {
	l := lowerSrc(t, shapeMatch+`G(s: str): int {
	match s {
		"a" {
			return 1
		}
		"b" | "c" {
			return 2
		}
		"a" | "d" {
			return 3
		}
		_ {
			return 4
		}
	}
}
H(b: bool, n: int): int {
	match n {
		0 | 1 {
			return 0
		}
		-1 {
			return 1
		}
		_ {
			return 2
		}
	}
}
`)
	b := l.body("F")
	for _, c := range shapeInputs {
		if got := walk(t, b, c.in); got != c.want {
			t.Errorf("F(%+v) = %d, want %d\n%s", c.in, got, c.want, l.print("F"))
		}
	}
	f := l.print("F")
	// The ?T switch covers some and none: Default = NoBlock. The Shape
	// switch does not cover Dot: its default goes to some(_).
	wantIn(t, f, "Discriminant(_2)", "[1: bb", ", 0: bb", "]")
	if strings.Count(f, "Discriminant(") != 2 {
		t.Fatalf("one discriminant test per column (?T, Shape):\n%s", f)
	}
	g := l.print("G")
	// Each str literal is one Eq test; "a" is tested once (the third arm's
	// "a" is unreachable), and every edge reaches an arm.
	for _, s := range []string{`"a"`, `"b"`, `"c"`, `"d"`} {
		if strings.Count(g, "Eq(copy _2, const str "+s+")") != 1 {
			t.Fatalf("one Eq test of %s:\n%s", s, g)
		}
	}
	h := l.print("H")
	wantIn(t, h, "switch copy _3 [0: bb", ", 1: bb", ", 18446744073709551615: bb", ", _: bb")
}

func TestMatch_SizeGuardFallback(t *testing.T) {
	src := `enum T {
	V(a: int, b: int, c: int)
	W
}
F(x: T): int {
	match x {
		.V(a: 1 | 2 | 3 | 4 | 5, b: 1 | 2 | 3 | 4 | 5, c: 1 | 2 | 3 | 4 | 5) {
			return 1
		}
		_ {
			return 2
		}
	}
}
`
	l := lowerSrc(t, src)
	seq := lowerWith(t, src, func(c *Config) { c.matchLeaves = -1 })
	tree := l.body("F")
	// The guard: the tree would have about 5 x 5 x 5 leaves, past 4 x the
	// 17 pattern nodes, so the default build falls back as the forced one.
	if l.print("F") != seq.print("F") {
		t.Fatalf("the pathological match did not fall back:\n%s", l.print("F"))
	}
	small := `enum T {
	V(a: int, b: int)
	W
}
F(x: T): int {
	match x {
		.V(a: 1, b: 2) {
			return 1
		}
		_ {
			return 2
		}
	}
}
`
	if len(lowerSrc(t, small).body("F").Blocks) == len(lowerWith(t, small, func(c *Config) { c.matchLeaves = -1 }).body("F").Blocks) {
		t.Fatalf("a small match is not compiled to a tree")
	}
	for _, a := range []uint64{0, 1, 3, 5, 6} {
		for _, b := range []uint64{1, 4, 9} {
			for _, c := range []uint64{2, 5, 7} {
				in := variant(0, a, b, c)
				want := uint64(2)
				if a >= 1 && a <= 5 && b >= 1 && b <= 5 && c >= 1 && c <= 5 {
					want = 1
				}
				if got := walk(t, seq.body("F"), in); got != want {
					t.Fatalf("fallback F(%d, %d, %d) = %d, want %d", a, b, c, got, want)
				}
				if got := walk(t, tree, in); got != want {
					t.Fatalf("default F(%d, %d, %d) = %d, want %d", a, b, c, got, want)
				}
			}
		}
	}
	if got := walk(t, seq.body("F"), variant(1)); got != 2 {
		t.Fatalf("F(.W) = %d", got)
	}
	// The shape match chooses the same arm under the tree and the fallback.
	fb := lowerWith(t, shapeMatch, func(c *Config) { c.matchLeaves = -1 }).body("F")
	for _, c := range shapeInputs {
		if got := walk(t, fb, c.in); got != c.want {
			t.Errorf("fallback F(%+v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestMatch_BindingModes(t *testing.T) {
	l := lowerSrc(t, `enum E {
	A(n: int)
	B
}
ByValue(e: E): int {
	match e {
		.A(n) {
			return n
		}
		.B {
			return 0
		}
	}
}
ByRef(e: E): int {
	match &e {
		.A(n) {
			return *n
		}
		.B {
			return 0
		}
	}
}
ByMut(var e: E): int {
	match mut e {
		.A(n) {
			*n += 1
			return *n
		}
		.B {
			return 0
		}
	}
}
`)
	// By value: the scrutinee is copied into a temporary and n copied out.
	wantIn(t, l.print("ByValue"), "_2 = copy _1", "Discriminant(_2)", "= copy (_2 as variant0).0")
	// & and mut: no copy; n is a shared or exclusive borrow of the field.
	wantIn(t, l.print("ByRef"), "Discriminant(_1)", "= &(_1 as variant0).0")
	wantIn(t, l.print("ByMut"), "Discriminant(_1)", "= mut (_1 as variant0).0")
}
