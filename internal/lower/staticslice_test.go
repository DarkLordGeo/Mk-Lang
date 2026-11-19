// Why: in a const initializer an array literal whose expected type is []T
// is a static slice, and both lowering and evaluation must produce it.
// What: TestLowersToStaticSlice, TestEvaluatesAsStaticData.
// How: the literal lowers to its array and a shared slice of it, and the
// evaluator's result holds the elements as static data (relocations to [N]T
// values), as a string literal holds its bytes.

package lower

import (
	"strings"
	"testing"

	"mk/internal/eval"
	"mk/internal/types"
)

const staticSliceSrc = `struct Route {
	path: str
	methods: []str
}

const Routes: []Route = [
	Route(path: "/", methods: ["GET"]),
	Route(path: "/notes", methods: ["GET", "POST"]),
	Route(path: "/health", methods: []),
]

H(): int {
	return Routes.Len() * 1000 + Routes[1].methods.Len() * 100 + Routes[2].methods.Len() * 10 + Routes[1].methods[1].Len()
}
`

// TestLowersToStaticSlice: the const body holds each literal's elements
// and validates (Validate runs in lowerSrc: no [N]T reaches a []T place).
// Today each literal is its array and a shared slice of it; it may become
// one array aggregate of type []T. Both forms pass.
func TestLowersToStaticSlice(t *testing.T) {
	l := lowerSrc(t, staticSliceSrc)
	got := l.print("Routes")
	for _, want := range []string{"str(const str \"GET\", const str \"POST\")", "str()", "Route(copy ", "_0 = copy "} {
		if !strings.Contains(got, want) {
			t.Errorf("Routes body lacks %q:\n%s", want, got)
		}
	}
}

// TestEvaluatesAsStaticData: reading the const from a function gives
// the elements (3 routes; the second has 2 methods, the third none; "POST"
// has 4 bytes), and the const's value relocates each slice to an interned
// [N]T value, the empty one to a [0]str.
func TestEvaluatesAsStaticData(t *testing.T) {
	if v, err := evalH(t, staticSliceSrc); err != nil || v != 3204 {
		t.Fatalf("H() = %d, %v; want 3204", v, err)
	}
	l := lowerSrc(t, staticSliceSrc)
	p := &evalProg{l: l, byItem: map[types.ItemID]string{}}
	var routes types.ItemID
	for _, id := range l.b.m.Units() {
		k := l.b.key(id)
		p.byItem[id] = k
		if k == "Routes" {
			routes = id
		}
	}
	ev := eval.New(eval.Config{Types: l.w.tab, Items: l.w.items, Consts: l.w.consts, Interner: l.w.in, Program: p, Known: *l.w.cfg.Known})
	r := ev.Eval(&types.EvalRequest{What: types.EvalConst, Item: routes, Fuel: 1 << 20, Stage: types.StageBodies, Module: l.b.id})
	if r.Err != nil {
		t.Fatalf("Routes: %v", r.Err)
	}
	tab := l.w.tab
	top := l.w.consts.Get(r.Value)
	if tab.Kind(top.Type) != types.KSlice || len(top.Relocs) != 1 {
		t.Fatalf("Routes is %s with %d relocs, want a []Route with 1", tab.TypeString(top.Type, nil), len(top.Relocs))
	}
	arr := l.w.consts.Get(top.Relocs[0].Data)
	if at := tab.Get(arr.Type); at.Kind != types.KArray {
		t.Fatalf("Routes points at %s, want [3]Route", tab.TypeString(arr.Type, nil))
	}
	// Each Route holds a str and a []str: 2 pointer words, so 6 relocs.
	if len(arr.Relocs) != 6 {
		t.Fatalf("[3]Route has %d relocs, want 6", len(arr.Relocs))
	}
	var lens []string
	for _, rl := range arr.Relocs {
		v := l.w.consts.Get(rl.Data)
		lens = append(lens, tab.TypeString(v.Type, nil))
	}
	got := strings.Join(lens, " ")
	for _, want := range []string{"[1]str", "[2]str", "[0]str"} {
		if !strings.Contains(got, want) {
			t.Errorf("pointees %q lack %s", got, want)
		}
	}
}
