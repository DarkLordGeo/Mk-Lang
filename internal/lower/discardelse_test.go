// Why: shapes std code writes (a discard whose else falls through, a
// namespace function's implicit bounds, a spawn at an `s.go`) must lower to
// valid MIR with the facts and spans borrow and codegen read.
// What: TestDiscardElseFallsThrough, TestImplicitPlainReachesFacts,
// TestSpawnTermAtGoExpr.
// How: lowers each shape, validates it and checks the MIR or the facts.

package lower

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestDiscardElseFallsThrough: `_ = e else { }` with a non-void `e` whose
// handler falls through drops the value on the success edge and leaves none
// on the handler's (check accepts it). It was an ICE:
// "record aggregate of void with 0 operands" from Validate.
func TestDiscardElseFallsThrough(t *testing.T) {
	l := lowerSrc(t, "error Bad\n\nF(): Bad!int {\n\treturn 1\n}\n\nG(o: ?int) {\n\t_ = F() else { }\n\t_ = o else { }\n\t_ = F() else e {\n\t\t_ = e\n\t}\n}\n")
	wantIn(t, l.print("G"), "F()", "return")
}

// TestImplicitPlainReachesFacts (`Slice[T].Fill` and `CopyFrom` as safe
// loops): the implicit `T: Plain` in those bodies is check's
// (TestSliceImplicitBounds), and lower's Input.Facts, which borrow reads
// through the driver, must answer from the same bounds. Before, lower built
// its env from Sig.Generics alone, so T was Linear and borrow rejected every
// copy of x (E0402, E0405, E0409). A namespace function with no implicit
// bound keeps T Linear.
func TestImplicitPlainReachesFacts(t *testing.T) {
	const loop = "(mut self, x: T into self) {\n\ts := self.ItemsMut()\n\tfor i in 0..s.Len() {\n\t\ts[i] = x\n\t}\n}\n"
	core := fakePrelude() + "export Slice[T].Fill" + loop + "export Slice[T].Other" + loop
	w := newWorldCore(t, true, core)
	b := w.mods["std.core"]
	cfg := w.lowerCfg()
	for _, tc := range []struct {
		name string
		want types.Kind
	}{{"Slice:Fill", types.Plain}, {"Slice:Other", types.Linear}} {
		id := b.item(tc.name)
		body := w.c.Check(b.m, id)
		if len(body.Diags) > 0 {
			t.Fatalf("%s: check diagnostics:\n%s", tc.name, dump(w, body.Diags))
		}
		in := FromCheck(cfg, w.c, body)
		tp := w.tab.Param(0)
		f, ok := in.Facts(tp)
		if !ok || f.Kind != tc.want {
			t.Errorf("%s: Facts(T).Kind = %v (ok %v), want %v", tc.name, f.Kind, ok, tc.want)
		}
		mb := Lower(cfg, in)
		p := &mir.Printer{Types: w.tab, Items: w.items}
		text := p.Print(mb)
		if hasCopy := strings.Contains(text, "copy _2"); hasCopy != (tc.want == types.Plain) {
			t.Errorf("%s: copy of x is %v, want %v:\n%s", tc.name, hasCopy, tc.want == types.Plain, text)
		}
	}
}

// TestSpawnTermAtGoExpr is the spawn site of spawn_huge.mk (its
// "panic: stack limit at spawn_huge.mk:29:13"): the TermCall with Spawn
// set carries the `s.go` expression's span, which codegen's SpawnAlloc site
// and a spawn's stack-limit panic report; a plain call's TermCall keeps the
// call's own span. It was the inner call's span (`Work(io)`, 3:8).
func TestSpawnTermAtGoExpr(t *testing.T) {
	l := lowerSrc(t, "Work(io: Io) {\n}\nF(io: Io) {\n\tscope s := io.Scope() {\n\t\ts.go Work(io)\n\t\t  Work(io)\n\t}\n}\n")
	b := l.body("F")
	var spawns, plain []string
	for _, blk := range b.Blocks {
		tm := blk.Term
		if tm.Kind != mir.TermCall || tm.Callee.Kind != mir.CallDirect || !strings.HasSuffix(l.w.items.Get(tm.Callee.Fn.Item).Key.String(), ".Work") {
			continue
		}
		if tm.Spawn != mir.NoLocal {
			spawns = append(spawns, l.w.lc(b.Span(tm.Span)))
		} else {
			plain = append(plain, l.w.lc(b.Span(tm.Span)))
		}
	}
	if strings.Join(spawns, ",") != "5:3" || strings.Join(plain, ",") != "6:5" {
		t.Fatalf("spawn TermCall at %v, plain at %v; want [5:3] and [6:5]:\n%s", spawns, plain, l.print("F"))
	}
}
