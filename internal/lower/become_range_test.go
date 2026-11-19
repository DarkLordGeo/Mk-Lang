// Why: `become` is a guaranteed tail call and a range pattern is a single
// compare; both must hold in lowered code and in the evaluator.
// What: TestLangBecomeLowersToTailCall, TestLangRangeLowersToCompares,
// TestLangEvalChain.
// How: become lowers to TermTailCall with no exit chain, for a direct,
// generic, method and fn-table callee; a range test is one wrapping subtract
// and one unsigned compare; and the evaluator runs a chain of 10,000,000
// tail calls in one frame, charging fuel per call.

package lower

import (
	"encoding/binary"
	"strings"
	"testing"

	"mk/internal/eval"
	"mk/internal/mir"
	"mk/internal/types"
)

const becomeChains = `struct Vm {
	k: int
}

Count(n: int, acc: int): int {
	if n == 0 {
		return acc
	}
	become Count(n - 1, acc + 1)
}

Step(vm: &Vm, n: int, acc: int): int {
	if n == 0 {
		return acc
	}
	become Table[n % 2](vm, n - 1, acc + vm.k)
}

Odd(vm: &Vm, n: int, acc: int): int {
	become Step(vm, n, acc)
}

const Table: [2]fn(&Vm, int, int): int = [Step, Odd]

Gen[T](x: T, n: int): T {
	if n == 0 {
		return x
	}
	become Gen[T](x, n - 1)
}

Vm.Down(&self, n: int, acc: int): int {
	if n == 0 {
		return acc
	}
	become self.Down(n - 1, acc + self.k)
}

Run(n: int): int {
	vm := Vm(k: 1)
	return Step(&vm, n, 0)
}

Down(n: int): int {
	vm := Vm(k: 2)
	return vm.Down(n, 0)
}
`

// TestLangBecomeLowersToTailCall: each become is one TermTailCall that
// ends its block, of a direct callee (generic arguments kept), a method, or
// a fn value read from the table, with no Return and no StorageDead or
// cleanup after its operands.
func TestLangBecomeLowersToTailCall(t *testing.T) {
	l := lowerSrc(t, becomeChains)
	for _, c := range []struct{ body, callee string }{
		{"Count", "Count("},
		{"Step", "tailcall (copy "},
		{"Odd", "Step("},
		{"Gen", "Gen["},
		{"Vm:Down", "Down("},
	} {
		b := l.body(c.body)
		n := 0
		for _, bl := range b.Blocks {
			if bl.Term.Kind == mir.TermTailCall {
				n++
				if bl.Term.Dest != (mir.Place{}) || bl.Term.Target != 0 || bl.Term.HasErr || bl.Term.Spawn != mir.NoLocal {
					t.Errorf("%s: tail call with a destination, target, error edge or spawn", c.body)
				}
				if c.body == "Step" && bl.Term.Callee.Kind != mir.CallValue {
					t.Errorf("Step: callee kind %d, want a fn value", bl.Term.Callee.Kind)
				}
				if c.body != "Step" && bl.Term.Callee.Kind != mir.CallDirect {
					t.Errorf("%s: callee kind %d, want direct", c.body, bl.Term.Callee.Kind)
				}
			}
		}
		text := l.print(c.body)
		if n != 1 || !strings.Contains(text, "tailcall") || !strings.Contains(text, c.callee) {
			t.Errorf("%s: %d tail calls, want 1 naming %q:\n%s", c.body, n, c.callee, text)
		}
	}
}

// TestLangRangeLowersToCompares: a closed range is a wrapping subtract
// and an unsigned compare in the unsigned type of the scrutinee's width,
// an open end one compare in the scrutinee's own type, and the match is
// tested arm by arm.
func TestLangRangeLowersToCompares(t *testing.T) {
	l := lowerSrc(t, `F(x: i8): int {
	return match x {
		-10..20 { 1 }
		20.. { 2 }
		..-10 { 3 }
	}
}

G(c: char): int {
	return match c {
		'a'..'{' { 1 }
		_ { 0 }
	}
}
`)
	b := l.body("F")
	var subs, ults, sge, slt int
	for _, bl := range b.Blocks {
		for _, st := range bl.Stmts {
			if st.Kind != mir.StAssign || st.Rvalue.Kind != mir.RvBinary {
				continue
			}
			rv := st.Rvalue
			at := b.OperandType(rv.A)
			switch {
			case rv.Bin == mir.SubWrap && at == types.U8:
				subs++
			case rv.Bin == mir.Lt && at == types.U8:
				ults++
				if rv.B.Kind != mir.OpImm || rv.B.Bits != 30 {
					t.Errorf("unsigned compare against %d, want hi - lo = 30", rv.B.Bits)
				}
			case rv.Bin == mir.Ge && at == types.I8:
				sge++
			case rv.Bin == mir.Lt && at == types.I8:
				slt++
			}
		}
	}
	if subs != 1 || ults != 1 || sge != 1 || slt != 1 {
		t.Errorf("F: %d subtracts and %d unsigned compares in u8, %d >= and %d < in i8; want 1 of each:\n%s", subs, ults, sge, slt, l.print("F"))
	}
	if g := l.print("G"); !strings.Contains(g, "u32") {
		t.Errorf("G: a char range compares in u32:\n%s", g)
	}
	for _, c := range []struct {
		x    int64
		want int64
	}{{-11, 3}, {-10, 1}, {19, 1}, {20, 2}, {127, 2}, {-128, 3}} {
		src := "H(): int {\n\treturn F(" + itoa64(c.x) + ")\n}\n" + `F(x: i8): int {
	return match x {
		-10..20 { 1 }
		20.. { 2 }
		..-10 { 3 }
	}
}
`
		if v, err := evalH(t, src); err != nil || v != c.want {
			t.Errorf("F(%d) = %d, %v; want %d", c.x, v, err, c.want)
		}
	}
}

func itoa64(v int64) string {
	if v < 0 {
		return "-" + itoa64(-v)
	}
	if v < 10 {
		return string(rune('0' + v))
	}
	return itoa64(v/10) + string(rune('0'+v%10))
}

// evalChain evaluates H() of becomeChains plus h with fuel, the result as
// an int.
func evalChain(t *testing.T, h string, fuel uint64) (int64, *types.EvalError) {
	t.Helper()
	l := lowerSrc(t, becomeChains+h)
	p := &evalProg{l: l, byItem: map[types.ItemID]string{}}
	var hid types.ItemID
	for _, id := range l.b.m.Units() {
		k := l.b.key(id)
		p.byItem[id] = k
		if k == "H" {
			hid = id
		}
	}
	ev := eval.New(eval.Config{Types: l.w.tab, Items: l.w.items, Consts: l.w.consts, Interner: l.w.in, Program: p, Known: *l.w.cfg.Known})
	r := ev.Eval(&types.EvalRequest{What: types.EvalCall, Item: hid, Fuel: fuel, Stage: types.StageBodies, Module: l.b.id})
	if r.Err != nil {
		return 0, r.Err
	}
	return int64(binary.LittleEndian.Uint64(l.w.consts.Get(r.Value).Bytes)), nil
}

// TestLangEvalChain: 10,000,000 tail calls, direct, through the table
// of fn values and of a method, run at compile time in one frame (a frame per call
// would pass the 1 MiB frame limit after about 30,000 calls), within their
// fuel; with too little fuel the same chain stops with EvalFuel, since each
// call is charged.
func TestLangEvalChain(t *testing.T) {
	const n = 10_000_000
	for _, c := range []struct{ name, h string }{
		{"direct", "H(): int {\n\treturn Count(10000000, 0)\n}\n"},
		{"fn table", "H(): int {\n\treturn Run(10000000)\n}\n"},
		{"method", "H(): int {\n\treturn Down(5000000)\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, err := evalChain(t, c.h, 2_000_000_000)
			if err != nil || v != n {
				t.Fatalf("H() = %d, %v; want %d", v, err, n)
			}
		})
	}
	if _, err := evalChain(t, "H(): int {\n\treturn Count(100000, 0)\n}\n", 100_000); err == nil || err.Kind != types.EvalFuel {
		t.Errorf("100,000 tail calls in 100,000 steps: %v, want EvalFuel", err)
	}
}
