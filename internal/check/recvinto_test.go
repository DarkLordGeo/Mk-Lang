// Why: a receiver is the first parameter, so its `into` must reach every
// reader of a signature's flows as a parameter's does.
// What: TestReceiverIntoIsParamFlow and TestReceiverIntoGenFlows.
// How: checks a method with `&self into arena` beside the free function
// whose first parameter is the receiver, and compares their signatures
// flow for flow; then reads both through the stage-4 view, from the AST in
// the same module and from the checked signature in another.

package check

import (
	"reflect"
	"testing"

	"mk/internal/types"
)

const recvIntoSrc = "struct Names {\n\tall: List[str]\n}\n\n" +
	"Names.Keys(&self into arena, arena: &Arena) {\n}\n\n" +
	"Keys(n: &Names into arena, arena: &Arena) {\n}\n\n" +
	"Names.Own(mut self into self) {\n}\n"

// TestReceiverIntoIsParamFlow: the method's Params[0].Into names arena (2),
// as the function's n does, and every other flow of the two signatures is
// equal; `mut self into self` names the receiver itself.
func TestReceiverIntoIsParamFlow(t *testing.T) {
	w := newWorld(t, true)
	b := checkCodes(t, w, "m", opt{}, "", recvIntoSrc)
	m, f := b.sigOf("Names:Keys"), b.sigOf("Keys")
	if len(m.Params) != 2 || len(f.Params) != 2 {
		t.Fatalf("params %+v and %+v", m.Params, f.Params)
	}
	for i := range m.Params {
		if m.Params[i].Into != f.Params[i].Into {
			t.Errorf("param %d: method into %d, function into %d", i+1, m.Params[i].Into, f.Params[i].Into)
		}
		if m.Params[i].Type != f.Params[i].Type {
			t.Errorf("param %d: types differ", i+1)
		}
	}
	if m.Params[0].Into != 2 {
		t.Errorf("receiver into = %d, want 2", m.Params[0].Into)
	}
	if !reflect.DeepEqual(m.Flows, f.Flows) {
		t.Errorf("flows differ:\n%+v\n%+v", m.Flows, f.Flows)
	}
	if own := b.sigOf("Names:Own"); own.Params[0].Into != 1 {
		t.Errorf("mut self into self: into = %d, want 1", own.Params[0].Into)
	}
	if got := w.c.sigHead(m); got != "Keys(&self into arena, arena: &Arena)" {
		t.Errorf("sigHead = %q", got)
	}
}

// TestReceiverIntoGenFlows: m.Flows() lists the receiver's clause as an
// Into with Param the target and Path `self`, read from the AST of a module
// still before step 5 and from the checked signature of another module.
func TestReceiverIntoGenFlows(t *testing.T) {
	want := []GenFlow{{Kind: FlowInto, Param: "arena", Path: "self"}}
	w := newWorld(t, true)
	lib := checkCodes(t, w, "lib", opt{}, "", recvIntoSrc)
	b := w.add("m", opt{stopBefore: true, parseErrors: true}, recvIntoSrc)
	v := w.c.GenView(&ModuleInput{Module: b.id, Files: b.files, Info: b.info})
	for _, c := range []struct {
		name string
		item types.ItemID
	}{{"same module", b.item("Names:Keys")}, {"other module", lib.item("Names:Keys")}} {
		g, ok := v.Func(c.item)
		if !ok {
			t.Fatalf("%s: no view of Names.Keys", c.name)
		}
		if !reflect.DeepEqual(g.Flows, want) {
			t.Errorf("%s: flows %+v, want %+v", c.name, g.Flows, want)
		}
		if g.Sig.Params[0].Into != 2 {
			t.Errorf("%s: receiver into = %d, want 2", c.name, g.Sig.Params[0].Into)
		}
	}
}
