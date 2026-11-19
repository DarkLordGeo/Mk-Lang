// Why: unbounded instantiation (Nest[T] calling Nest[[1]T]) must stop with
// E0708 instead of running out of memory.
// What: TestE0708_Depth, TestE0708_Count and TestE0708_ChainDeterministic.
// How: builds the recursive program by hand (nestProgram) and checks the
// depth and count limits, the error's span, and that its text does not
// depend on GOMAXPROCS.

package mono

import (
	"fmt"
	"runtime"
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// nestProgram is tests/conformance/codes/E0708.mk by hand: Nest[T] calls
// Nest[[1]T]; Start calls Nest[int]; Main calls Start. It returns Main and
// the span of Start's call.
func nestProgram(w *world) (types.ItemID, mir.SpanID, *mir.Body) {
	tab := w.tab
	one := types.ConstArg{Kind: types.CAValue, Value: intConst(w.consts, 1)}
	nest := w.fn("Nest", 1, nil, types.Void)
	nb := w.body(nest)
	nb.call(nest, []types.GenericArg{ty(tab.Array(one, tab.Param(0)))}, nb.temp(types.Void))
	nb.ret()
	start := w.fn("Start", 0, nil, types.Void)
	sb := w.body(start)
	site := sb.call(nest, []types.GenericArg{ty(types.Int)}, sb.temp(types.Void))
	sb.ret()
	main := w.fn("Main", 0, nil, types.Void)
	mb := w.body(main)
	mb.call(start, nil, mb.temp(types.Void))
	mb.ret()
	return main, site, sb.b
}

func factText(d *diag.Diagnostic, key string) string {
	f, ok := d.FactValue(key)
	if !ok {
		return "<none>"
	}
	return fmt.Sprint(f)
}

// TestE0708_Depth: a generic recursing on [1]T is refused past depth 64, at
// the outermost concrete call (Start's call of Nest, not Main's of Start),
// with the chain from Start outermost first.
func TestE0708_Depth(t *testing.T) {
	w := newWorld()
	main, site, sb := nestProgram(w)
	in, ds := w.collect(t, Options{}, mainRoot(main))
	if len(ds) != 1 {
		t.Fatalf("want one E0708, got %d", len(ds))
	}
	d := ds[0]
	if d.Code != diag.E0708 || d.At != sb.Span(site) {
		t.Fatalf("got %s at %v, want E0708 at %v", d.Code, d.At, sb.Span(site))
	}
	lim, _ := d.FactValue("limit")
	kind, _ := d.FactValue("kind")
	chain, _ := d.FactValue("chain")
	if lim.Int != 64 || kind.Str != "depth" {
		t.Fatalf("facts limit %v kind %v", lim, kind)
	}
	if len(chain.List) != 66 || chain.List[0].Str != "m.Start" || chain.List[1].Str != "m.Nest[int]" || chain.List[2].Str != "m.Nest[[1]int]" {
		t.Fatalf("chain of %d: %v", len(chain.List), chain.List[:min(3, len(chain.List))])
	}
	// Collection stopped after the reporting level: Main, Start and 64 Nests.
	if len(in.List) != 66 {
		t.Fatalf("want 66 built instances, got %d", len(in.List))
	}
	for _, i := range in.List {
		if i.Depth > 64 {
			t.Fatalf("%s at depth %d", i.Name, i.Depth)
		}
	}
	// A deeper limit accepts the same chain's prefix and reports later.
	w2 := newWorld()
	m2, _, _ := nestProgram(w2)
	_, ds2 := w2.collect(t, Options{MaxDepth: 3}, mainRoot(m2))
	if len(ds2) != 1 {
		t.Fatalf("MaxDepth 3: %d diagnostics", len(ds2))
	}
	if c, _ := ds2[0].FactValue("chain"); len(c.List) != 5 {
		t.Fatalf("MaxDepth 3: chain %v", c.List)
	}
}

// TestE0708_Count: past MaxCount instances for one build root, one E0708 of
// kind count at the call that asked for one too many.
func TestE0708_Count(t *testing.T) {
	w := newWorld()
	g := w.fn("G", 1, nil, types.Void)
	w.body(g).ret()
	main := w.fn("Main", 0, nil, types.Void)
	mb := w.body(main)
	v := mb.temp(types.Void)
	var sites []mir.SpanID
	for _, a := range []types.TypeID{types.I8, types.I16, types.I32, types.I64, types.U8, types.U16} {
		sites = append(sites, mb.call(g, []types.GenericArg{ty(a)}, v))
	}
	mb.ret()
	in, ds := w.collect(t, Options{MaxCount: 5}, mainRoot(main))
	if len(ds) != 1 {
		t.Fatalf("want one E0708, got %d", len(ds))
	}
	d := ds[0]
	kind, _ := d.FactValue("kind")
	lim, _ := d.FactValue("limit")
	if kind.Str != "count" || lim.Int != 5 || d.At != mb.b.Span(sites[4]) {
		t.Fatalf("got kind %v limit %v at %v, want count 5 at %v", kind, lim, d.At, mb.b.Span(sites[4]))
	}
	if chain, _ := d.FactValue("chain"); len(chain.List) != 2 || chain.List[0].Str != "m.Main" || chain.List[1].Str != "m.G[u8]" {
		t.Fatalf("chain %v", chain.List)
	}
	if len(in.List) != 1 {
		t.Fatalf("only Main was built before the stop, got %d", len(in.List))
	}
	// Exactly at the limit is accepted.
	_, ds = w.collect(t, Options{MaxCount: 7}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("MaxCount 7: %v", ds)
	}
}

// TestE0708_ChainDeterministic: GOMAXPROCS 1 and 8, and two runs, give the
// same diagnostic.
func TestE0708_ChainDeterministic(t *testing.T) {
	render := func(procs int) string {
		defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(procs))
		w := newWorld()
		main, _, _ := nestProgram(w)
		_, ds := w.collect(t, Options{}, mainRoot(main))
		if len(ds) != 1 {
			t.Fatalf("want one E0708, got %d", len(ds))
		}
		d := ds[0]
		return fmt.Sprint(d.Code, d.At, d.Message, factText(d, "limit"), factText(d, "kind"), factText(d, "chain"))
	}
	a, b, c := render(1), render(8), render(8)
	if a != b || b != c {
		t.Fatalf("diagnostics differ:\n%s\n%s\n%s", a, b, c)
	}
}
