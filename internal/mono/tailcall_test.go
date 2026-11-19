// Why: `become` tail calls must be collected and instantiated like calls, or a
// generic callee reached only through `become` is never built.
// What: TestLangTailCallInstances, on MIR that lower builds from source.
// How: checks that every TermTailCall site is read as a call: generic callees
// collected with canonical arguments, a tail call through a fn-value table
// left a fn-value call, and one inside an unrolled fields loop copied with its
// operands renamed; every instance validates.

package mono

import (
	"testing"

	"mk/internal/mir"
)

func TestLangTailCallInstances(t *testing.T) {
	sw := newSrcWorld(t, `struct Vm {
    k: int
    ops: [2]fn(&Vm, int, int): int
}

struct Two {
    export a: int
    export b: int
}

Gen[T](x: T, n: int): T {
    if n == 0 {
        return x
    }
    become Gen[T](x, n - 1)
}

Hop[U](x: U, n: int): U {
    become Gen(x, n)
}

Step(vm: &Vm, n: int, acc: int): int {
    if n == 0 {
        return acc
    }
    become (vm.ops[n % 2])(vm, n - 1, acc + vm.k)
}

Walk[T: Fields[Plain] + Plain](x: T, n: int): int {
    for _, _ in fields(&x) {
        if n > 0 {
            become Walk[T](x, n - 1)
        }
    }
    return n
}

H(): int {
    vm := Vm(k: 1, ops: [Step, Step])
    t := Two(a: 1, b: 2)
    _ = Hop(true, 3)
    return Hop(4, 3) + Step(&vm, 5, 0) + Walk(t, 2)
}
`)
	in, ds := sw.collect(t, Options{Validate: true}, sw.limitRoots()...)
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	// Gen is reached only through Hop's tail call: one instance per T.
	for _, c := range []struct {
		name string
		n    int
	}{{"Gen", 2}, {"Hop", 2}, {"Walk", 1}} {
		if got := len(instancesOf(in, sw.item(t, c.name))); got != c.n {
			t.Errorf("%d instances of %s, want %d:\n%s", got, c.name, c.n, summary(in))
		}
	}
	gen := sw.item(t, "Gen")
	for _, inst := range instancesOf(in, sw.item(t, "Hop")) {
		found := false
		for _, bl := range inst.Body.Blocks {
			tm := bl.Term
			if tm.Kind != mir.TermTailCall {
				continue
			}
			found = true
			if tm.Callee.Kind != mir.CallDirect || tm.Callee.Fn.Item != gen || tm.Callee.Fn.Args.Len != 1 {
				t.Errorf("Hop's tail call: %+v", tm.Callee)
			}
			if a := inst.Body.GenericArgs[tm.Callee.Fn.Args.Start]; a.Type != inst.Args[0].Type {
				t.Errorf("Hop[%v] tail-calls Gen at %v", inst.Args[0].Type, a.Type)
			}
		}
		if !found {
			t.Errorf("an instance of Hop has no tail call")
		}
	}
	step := instancesOf(in, sw.item(t, "Step"))
	if len(step) != 1 {
		t.Fatalf("%d instances of Step", len(step))
	}
	calls := 0
	for _, bl := range step[0].Body.Blocks {
		if bl.Term.Kind == mir.TermTailCall {
			calls++
			if bl.Term.Callee.Kind != mir.CallValue {
				t.Errorf("Step's tail call is callee kind %d, want a fn value", bl.Term.Callee.Kind)
			}
		}
	}
	if calls != 1 {
		t.Errorf("Step has %d tail calls", calls)
	}
	// The fields loop over Two is unrolled twice: two tail calls, each
	// passing the instance's own parameters.
	fs := instancesOf(in, sw.item(t, "Walk"))
	tails := 0
	for _, bl := range fs[0].Body.Blocks {
		if bl.Term.Kind == mir.TermTailCall {
			tails++
			for _, a := range fs[0].Body.Args[bl.Term.Args.Start:bl.Term.Args.End()] {
				if int(a.Operand.Place.Local) >= len(fs[0].Body.Locals) {
					t.Errorf("a copied tail call reads local %d of %d", a.Operand.Place.Local, len(fs[0].Body.Locals))
				}
			}
		}
	}
	if tails != 2 {
		t.Errorf("Walk[Two] has %d tail calls, want 2 (one per field)", tails)
	}
}
