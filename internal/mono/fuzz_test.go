// Why: Collect runs on every program the compiler accepts, so no generated
// program may make it crash, hang or vary between runs.
// What: FuzzCollect: on any well-formed generated program, Collect raises no
// ICE, does not hang (the limits bound it), reports only E0708, gives closed
// instance bodies that validate, and is deterministic.
// How: fuzzProgram turns the bytes into up to 6 functions of generic calls;
// the fuzzer collects it twice under small limits and compares the results.

package mono

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// fuzzProgram builds up to 6 functions from data: function 0 is a
// non-generic root, the others have 0 or 1 generic; each body makes up to 4
// calls, each argument a small type over int, bool and (in a generic body)
// T, wrapped in [], &, ? or [1] by the bytes.
func fuzzProgram(data []byte) (*world, types.ItemID) {
	w := newWorld()
	tab := w.tab
	pos := 0
	next := func() byte {
		if pos >= len(data) {
			return 0
		}
		pos++
		return data[pos-1]
	}
	one := types.ConstArg{Kind: types.CAValue, Value: intConst(w.consts, 1)}
	n := int(next()%6) + 1
	gens := make([]int, n)
	fns := make([]types.ItemID, n)
	for i := range fns {
		if i > 0 {
			gens[i] = int(next() % 2)
		}
		fns[i] = w.fn(string(rune('A'+i)), gens[i], nil, types.Void)
	}
	typ := func(generic bool) types.TypeID {
		var t types.TypeID
		switch b := next() % 3; {
		case b == 0 && generic:
			t = tab.Param(0)
		case b == 1:
			t = types.Bool
		default:
			t = types.Int
		}
		for k := next() % 4; k > 0; k-- {
			switch next() % 4 {
			case 0:
				t = tab.Slice(t)
			case 1:
				t = tab.Ref(t)
			case 2:
				t = tab.Optional(t)
			default:
				t = tab.Array(one, t)
			}
		}
		return t
	}
	for i, f := range fns {
		fb := w.body(f)
		v := fb.temp(types.Void)
		for c := next() % 5; c > 0; c-- {
			callee := int(next()) % n
			var args []types.GenericArg
			for g := 0; g < gens[callee]; g++ {
				args = append(args, ty(typ(gens[i] > 0)))
			}
			fb.call(fns[callee], args, v)
		}
		fb.ret()
	}
	return w, fns[0]
}

func FuzzCollect(f *testing.F) {
	f.Add([]byte{3, 1, 1, 2, 1, 0, 2, 1, 2, 1, 1, 0, 3, 2, 0, 3})
	f.Add([]byte{2, 1, 1, 1, 0, 1, 1, 1, 0, 3, 3})
	f.Add([]byte{5, 1, 0, 1, 1, 4, 1, 2, 2, 3, 0, 0, 1, 4, 3, 3, 2, 1, 1, 2, 0, 3, 2})
	f.Fuzz(func(t *testing.T, data []byte) {
		run := func() (string, []*diag.Diagnostic) {
			w, root := fuzzProgram(data)
			var in *Instances
			var ds []*diag.Diagnostic
			opt := Options{MaxDepth: 6, MaxCount: 300, Validate: true}
			if ie := source.Guard("fuzz", source.Span{}, func() { in, ds = Collect(w, []Root{mainRoot(root)}, opt) }); ie != nil {
				t.Fatalf("ICE: %s", ie)
			}
			for _, i := range in.List {
				if i.Body == nil {
					continue
				}
				for _, l := range i.Body.Locals {
					if !closed(w.tab, l.Type) {
						t.Fatalf("%s keeps an open local", i.Name)
					}
				}
				if i.Body.GenericCount != 0 && i.Body != w.bodies[i.Item] {
					t.Fatalf("%s: generic count %d", i.Name, i.Body.GenericCount)
				}
			}
			if len(ds) == 0 {
				closedOver(t, w, in)
			}
			return summary(in), ds
		}
		a, da := run()
		b, db := run()
		if a != b || len(da) != len(db) {
			t.Fatalf("not deterministic")
		}
		if len(da) > 1 {
			t.Fatalf("%d diagnostics, at most one per build root", len(da))
		}
		for _, d := range da {
			if d.Code != diag.E0708 {
				t.Fatalf("diagnostic %s", d.Code)
			}
		}
	})
}
