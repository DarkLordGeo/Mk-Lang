// Why: inline asm, naked functions and extern vars lower to MIR shapes the
// back end trusts: operand holes, a terminating block, an address.
// What: TestAsmLowers, TestNoreturnEndsBlock, TestRawTemplateVerbatim (asm statements), TestNakedBodyIsAsmAndUnreachable,
// TestExternVarAddress.
// How: lowers small programs and compares the MIR text: an asm statement is
// one StAsm with holes as mir operand indices and const operands pasted as
// decimal text; a noreturn asm and a naked body end in unreachable; &name of
// an extern var is its address as *T.

package lower

import (
	"strings"
	"testing"

	"mk/internal/mir"
)

// TestAsmLowers: labels and bare locals name operands by their mir
// index (clobber_abi and options take none), a const operand's value is
// text, inout reads then writes its place, "_" writes nothing, a sym names
// its item, and the options and ABI are the Asm's.
func TestAsmLowers(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{unsafe: true}, `const Shift: int = 3

Helper() {
}

Ops(x: u64): u64 {
	var r: u64 = 0
	var y := x
	unsafe "test" {
		asm("shl {y}, {k}\nmov {o}, {y}\nlea {s}, [rip]\n{{x}}", clobber_abi("C"), o: out(reg) r, inout(reg) y, k: const Shift + 2, out("rcx") _, s: sym Helper, options(nostack, att))
	}
	return r
}
`)
	b.wantClean(t)
	l := lowerModule(t, w, b)
	got := l.print("Ops")
	want := `asm "shl {1}, 5\nmov {0}, {1}\nlea {4}, [rip]\n{{x}}", out(reg) _2, inout(reg) copy _3 => _3, const 5, out("rcx") _, sym m.Helper, clobber_abi("C"), options(nostack, att)`
	if !strings.Contains(got, want) {
		t.Errorf("Ops lowers to:\n%s\nwant the statement\n%s", got, want)
	}
	body := l.bodies["Ops"]
	var a *mir.Asm
	for i := range body.Blocks {
		for _, st := range body.Blocks[i].Stmts {
			if st.Kind == mir.StAsm {
				a = &body.Asms[st.Asm]
			}
		}
	}
	if a == nil {
		t.Fatal("no StAsm")
	}
	if op := a.Operands[3]; op.Out.Local != mir.NoLocal || !op.Explicit || op.Reg != "rcx" {
		t.Errorf("the discarded output: %+v", op)
	}
}

// TestNoreturnEndsBlock: options(noreturn) is the block's last
// statement before unreachable, and nothing after it is lowered; a function
// with a result needs no return after it.
func TestNoreturnEndsBlock(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{unsafe: true}, `Halt(code: u32): int {
	unsafe "test" {
		asm("hlt", in("edi") code, options(noreturn, nomem, nostack))
	}
}
`)
	b.wantClean(t)
	got := lowerModule(t, w, b).print("Halt")
	if !strings.Contains(got, `asm "hlt", in("edi") copy _1, options(nomem, nostack, noreturn)`+"\n    unreachable") {
		t.Errorf("Halt lowers to:\n%s", got)
	}
}

// TestRawTemplateVerbatim: with options(raw) the template is the
// literal as written: no hole is read and no brace is rewritten.
func TestRawTemplateVerbatim(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{unsafe: true}, `Raw() {
	unsafe "test" {
		asm("{not a hole} $x", options(raw, nomem, nostack))
	}
}
`)
	b.wantClean(t)
	got := lowerModule(t, w, b).print("Raw")
	if !strings.Contains(got, `asm "{not a hole} $x", options(nomem, nostack, raw)`) {
		t.Errorf("Raw lowers to:\n%s", got)
	}
}

// TestNakedBodyIsAsmAndUnreachable: a naked body is its StAsm (made
// noreturn) and unreachable, with no local beyond the parameters and no
// entry check.
func TestNakedBodyIsAsmAndUnreachable(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{unsafe: true}, `Handler() {
}

naked Stub(a: u64, b: u64): u64 {
	unsafe "entry" {
		asm("call {h}\nret", h: sym Handler)
	}
}
`)
	b.wantClean(t)
	l := lowerModule(t, w, b)
	got := l.print("Stub")
	body := l.bodies["Stub"]
	if len(body.Locals) != 3 || len(body.Blocks) != 1 {
		t.Errorf("Stub has %d locals and %d blocks, want 3 (result and two parameters) and 1:\n%s", len(body.Locals), len(body.Blocks), got)
	}
	if !strings.Contains(got, `asm "call {0}\nret", sym m.Handler, options(noreturn)`+"\n    unreachable") {
		t.Errorf("Stub lowers to:\n%s", got)
	}
}

// TestExternVarAddress: &name of an extern var is the static
// reference retyped as *T, and a read goes through *(&name); the extern var
// itself is no unit.
func TestExternVarAddress(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{unsafe: true}, `extern var End: u8

Addr(): *u8 {
	unsafe "linker" {
		return &End
	}
}

Peek(): u8 {
	unsafe "linker" {
		return End
	}
}

Take(p: *u8): int {
	return 0
}

Pass(): int {
	unsafe "linker" {
		return Take(&End)
	}
}
`)
	b.wantClean(t)
	l := lowerModule(t, w, b)
	if got := l.print("Addr"); !strings.Contains(got, "= &global m.End") || !strings.Contains(got, "Lossless[*u8]") {
		t.Errorf("Addr lowers to:\n%s", got)
	}
	if got := l.print("Peek"); !strings.Contains(got, "&global m.End") || !strings.Contains(got, "(*_") {
		t.Errorf("Peek lowers to:\n%s", got)
	}
	// &End passed straight as a *u8 argument is the same value, not a borrow
	// (it once lowered as a &u8 assigned to the *u8 argument, which mir
	// rejected).
	if got := l.print("Pass"); !strings.Contains(got, "= &global m.End") || !strings.Contains(got, "Lossless[*u8]") {
		t.Errorf("Pass lowers to:\n%s", got)
	}
	if _, ok := l.bodies["End"]; ok {
		t.Error("the extern var End was lowered as a unit: it has no initializer")
	}
}
