// Why: asm operands can only be judged once an instance is built, against the
// target's register table and the instance's cpu features.
// What: tests of E1104 (registers, classes, features), E1108 in
// general-regs-only bins, sym operands rooting their item, and const
// parameters as const operands.
// How: collects small asm programs per target and compares the sorted
// (reason, register) pairs of the diagnostics.

package mono

import (
	"sort"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

const asmSrc = `Handler(): u64 {
    return 7
}

Regs(x: u64, f: f64): u64 {
    var r := x
    unsafe "the table" {
        asm("nop", inout(foo_reg) r)
        asm("nop", in(ymm_reg) f)
        asm("nop", in(xmm_reg) x)
        asm("nop", in("xmm20") f)
        asm("nop", in(kreg) x)
        asm("nop", in("nope") x)
        asm("nop", in(xmm_reg) f, in(reg) x, in("eax") x, in("r9d") x, inout(reg) r)
    }
    return r
}

Wide(f: f64): f64 cpu "avx512f" {
    var r := f
    unsafe "avx-512 context" {
        asm("nop", inout(xmm_reg) r, in("xmm20") f, in("zmm3") f)
    }
    return r
}

Jump() {
    unsafe "names Handler" {
        asm("call {h}", h: sym Handler)
    }
}
`

// asmCodes collects (reason, register) of every E1104, sorted.
func asmCodes(t *testing.T, ds []*diag.Diagnostic) []string {
	t.Helper()
	var out []string
	for _, d := range ds {
		if d.Code != diag.E1104 {
			t.Fatalf("unexpected %v: %s", d.Code, d.Message)
		}
		var reason, reg string
		for _, f := range d.Facts {
			switch f.Key {
			case "reason":
				reason = f.Value.Str
			case "register":
				reg = f.Value.Str
			}
		}
		out = append(out, reason+" "+reg)
	}
	sort.Strings(out)
	return out
}

// TestAsmRegisters: an unknown class or register is E1104 register; a
// class or register whose feature the context lacks is E1104 feature (the
// baseline holds sse; a cpu clause adds avx512f); a type that does not fit
// its class is E1104 class; the accepted forms draw nothing.
func TestAsmRegisters(t *testing.T) {
	sw := newSrcWorld(t, asmSrc)
	_, ds := Collect(sw, sw.limitRoots(), Options{Target: types.X86_64Windows, Validate: true})
	got := strings.Join(asmCodes(t, ds), "; ")
	for _, w := range []string{"register foo_reg", "register nope", "feature ymm_reg", "feature xmm20", "feature kreg", "class xmm_reg"} {
		if !strings.Contains(got, w) {
			t.Errorf("no E1104 %q in %q", w, got)
		}
	}
	if n := len(ds); n != 6 {
		t.Errorf("%d E1104, want 6 (Wide's operands are all in its avx512f context and fit): %q", n, got)
	}
}

// TestNoRegisterTable: on aarch64 any asm is E1104 no-register-table.
func TestNoRegisterTable(t *testing.T) {
	sw := newSrcWorld(t, `Nop() {
    unsafe "test" {
        asm("nop")
    }
}
`)
	_, ds := Collect(sw, sw.limitRoots(), Options{Target: types.Aarch64Linux, Validate: true})
	if got := asmCodes(t, ds); len(got) != 1 || got[0] != "no-register-table " {
		t.Errorf("got %q, want one no-register-table", got)
	}
}

// TestSymRootsItem: a function named only by a sym operand is
// collected, address taken, so it is never internal or dropped.
func TestSymRootsItem(t *testing.T) {
	sw := newSrcWorld(t, `Handler(): u64 {
    return 7
}

Jump() {
    unsafe "names Handler" {
        asm("call {h}", h: sym Handler)
    }
}
`)
	jump := sw.item(t, "Jump")
	in, ds := Collect(sw, []Root{{Kind: RootExport, Item: jump}}, Options{Target: types.X86_64Windows, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %s", ds[0].Message)
	}
	hs := instancesOf(in, sw.item(t, "Handler"))
	if len(hs) != 1 || !hs[0].AddrTaken {
		t.Fatalf("Handler instances %d, want one, address taken", len(hs))
	}
}

// TestGeneralRegsOnly: in a general-regs-only bin a float value is
// E1108 with the call path from the root; a function whose cpu set holds
// sse2 may compute in floats inside but take or return none; integer code
// and a plain bin draw nothing.
func TestGeneralRegsOnly(t *testing.T) {
	src := `Mix(a: u64): u64 {
    var x: f64 = 1.5
    return a + Cast[u64](x)
}

Saved(a: u64): u64 cpu "sse2" {
    var x: f64 = 2.5
    return a + Cast[u64](x)
}

Takes(x: f64): u64 cpu "sse2" {
    return Cast[u64](x)
}

Ints(a: u64): u64 {
    return a * 3
}

Root(a: u64): u64 {
    return Mix(a) + Saved(a) + Ints(a)
}
`
	sw := newSrcWorld(t, src)
	roots := []Root{{Kind: RootExport, Item: sw.item(t, "Root")}, {Kind: RootExport, Item: sw.item(t, "Takes")}}
	if _, ds := Collect(sw, roots, Options{Target: types.X86_64Freestanding, Validate: true}); len(ds) != 0 {
		t.Fatalf("a plain bin: %s", ds[0].Message)
	}
	_, ds := Collect(sw, roots, Options{Target: types.X86_64Freestanding, Validate: true, GeneralRegsOnly: true, Bin: "kernel"})
	var got []string
	for _, d := range ds {
		if d.Code != diag.E1108 {
			t.Fatalf("unexpected %v: %s", d.Code, d.Message)
		}
		var path, bin string
		for _, f := range d.Facts {
			switch f.Key {
			case "path":
				path = f.Value.Str
			case "bin":
				bin = f.Value.Str
			}
		}
		if bin != "kernel" {
			t.Errorf("E1108 bin fact %q", bin)
		}
		got = append(got, path)
	}
	sort.Strings(got)
	if len(got) != 2 || !strings.HasSuffix(got[0], "Mix") || !strings.Contains(got[0], "Root") || !strings.HasSuffix(got[1], "Takes") {
		t.Errorf("E1108 paths %q, want Root -> Mix and Takes", got)
	}
}

// TestConstParamOperand: a const parameter as a const operand keeps its
// hole in the generic body, with the parameter in In; each instance has the
// value pasted into its template, Const set to it and In cleared. Generic
// const parameters and const value parameters both.
func TestConstParamOperand(t *testing.T) {
	src := `ShlN[const N: int](x: u64): u64 {
    var r := x
    unsafe "shift by N" {
        asm("shl {r}, {n}", inout(reg) r, n: const N, options(pure, nomem, nostack))
    }
    return r
}

AddK(x: u64, const k: i8): u64 {
    var r := x
    unsafe "add k" {
        asm("add {r}, {k}", inout(reg) r, k: const k, options(pure, nomem, nostack))
    }
    return r
}

Root(x: u64): u64 {
    return ShlN[3](x) + ShlN[12](x) + AddK(x, -8)
}
`
	sw := newSrcWorld(t, src)
	if gb := sw.bodies[sw.item(t, "ShlN")]; gb.Asms[0].Template != "shl {0}, {1}" {
		t.Errorf("generic template %q, want the hole kept", gb.Asms[0].Template)
	}
	in, ds := Collect(sw, []Root{{Kind: RootExport, Item: sw.item(t, "Root")}}, Options{Target: types.X86_64Linux, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("unexpected %v: %s", ds[0].Code, ds[0].Message)
	}
	got := map[string]string{}
	for i := range in.List {
		inst := &in.List[i]
		if inst.Body == nil || len(inst.Body.Asms) == 0 {
			continue
		}
		a := inst.Body.Asms[0]
		got[inst.Name] = a.Template
		c := a.Operands[1]
		if c.Kind != mir.AsmConst || c.In != (mir.Operand{}) {
			t.Errorf("%s: const operand %+v, want In cleared", inst.Name, c)
		}
	}
	for name, want := range map[string]string{"m.ShlN[3]": "shl {0}, 3", "m.ShlN[12]": "shl {0}, 12", "m.AddK[-8]": "add {0}, -8"} {
		if got[name] != want {
			t.Errorf("%s template %q, want %q (all: %q)", name, got[name], want, got)
		}
	}
	if gb := sw.bodies[sw.item(t, "ShlN")]; gb.Asms[0].Template != "shl {0}, {1}" {
		t.Errorf("an instance changed the generic body's template to %q", gb.Asms[0].Template)
	}
}
