// Why: the trapped call (trapform.go) must make small functions frameless on
// x86_64-windows and change nothing anywhere else.
// What: TestTrapForm_NeverCall over trapUnitOf's unit, with and without
// Config.TrapForm, on x86_64-windows and the other targets.
// How: checks the prefetchnta and ubsantrap(127) pair in the IR, frameless leaves
// in the machine code, mk_trap_call and TrapEntry per unit, and the pack key.

package codegen

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// trapUnitOf builds:
//
//	Fail(): never { Panic }
//	Check(k: u64): u64 { if k != 0x1234 { Fail() }; return k }   // small: alwaysinline
//	Adapt(k: u64, x: u64): u64 { return Check(k) +% x }
//	Main(k: u64): u64 { if k == 9 { Fail() }; return Adapt(k, 1) +% Adapt(k, 2) }
//	Entry(): *u8 { return sys.TrapEntry() }
//
// Adapt is the std.dyn adapter's shape: a key check inlined, then the
// work. Main calls Fail itself and, as the Main the report wrapper calls
// (Config.Main another function, func.go wrappedMain), carries neither
// alwaysinline mark.
func trapUnitOf(e *testEnv) *Unit {
	fail := e.fn("Fail", nil, types.Never, 0)
	fb := e.body(fail)
	fb.term(mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicExplicit, Span: fb.at(1, 1)})

	check := e.fn("Check", []types.TypeID{types.U64}, types.U64, 0)
	cb := e.body(check)
	ok := cb.local(types.Bool)
	cb.assign(place(ok), bin(mir.Eq, mir.Copy(place(1)), mir.Imm(types.U64, 0x1234)), cb.at(2, 5))
	bad, good := cb.newBlock(), cb.newBlock()
	cb.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(ok)),
		Cases: cb.b.AddCases(mir.SwitchCase{Value: 0, Target: bad}), Default: good, Span: cb.at(2, 5)})
	cb.cur = bad
	cb.call(fail, nil, types.Never)
	cb.term(mir.Terminator{Kind: mir.TermUnreachable, Span: cb.at(3, 1)})
	cb.cur = good
	cb.assign(place(0), use(mir.Copy(place(1))), cb.at(4, 5))
	cb.ret(cb.at(4, 5))

	adapt := e.fn("Adapt", []types.TypeID{types.U64, types.U64}, types.U64, 0)
	ab := e.body(adapt)
	k := ab.call(check, nil, types.U64, mir.Copy(place(1)))
	ab.assign(place(0), bin(mir.AddWrap, mir.Copy(place(k)), mir.Copy(place(2))), ab.at(6, 5))
	ab.ret(ab.at(6, 5))

	twice := e.fn("Main", []types.TypeID{types.U64}, types.U64, 0)
	tb := e.body(twice) // Main
	nine := tb.local(types.Bool)
	tb.assign(place(nine), bin(mir.Eq, mir.Copy(place(1)), mir.Imm(types.U64, 9)), tb.at(8, 5))
	tbad, tgood := tb.newBlock(), tb.newBlock()
	tb.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(nine)),
		Cases: tb.b.AddCases(mir.SwitchCase{Value: 0, Target: tgood}), Default: tbad, Span: tb.at(8, 5)})
	tb.cur = tbad
	tb.call(fail, nil, types.Never)
	tb.term(mir.Terminator{Kind: mir.TermUnreachable, Span: tb.at(9, 1)})
	tb.cur = tgood
	a1 := tb.call(adapt, nil, types.U64, mir.Copy(place(1)), mir.Imm(types.U64, 1))
	a2 := tb.call(adapt, nil, types.U64, mir.Copy(place(1)), mir.Imm(types.U64, 2))
	tb.assign(place(0), bin(mir.AddWrap, mir.Copy(place(a1)), mir.Copy(place(a2))), tb.at(10, 5))
	tb.ret(tb.at(10, 5))

	pu8 := e.tt.RawPtr(types.U8)
	te := e.stdFn("std.sys", "", "TrapEntry", 0, nil, pu8)
	entry := e.fn("Entry", nil, pu8, 0)
	eb := e.body(entry)
	r := eb.call(te, nil, pu8)
	eb.assign(place(0), use(mir.Copy(place(r))), eb.at(12, 5))
	eb.ret(eb.at(12, 5))
	return unitOf(e, []*mir.Body{fb.b, cb.b, ab.b, tb.b, eb.b}, te)
}

func TestTrapForm_NeverCall(t *testing.T) {
	const trap = "call void @llvm.ubsantrap(i8 127) nomerge"
	const pf = "call void @llvm.prefetch.p0(ptr @mk.main.Fail, i32 0, i32 0, i32 1)"
	for _, c := range []struct {
		name  string
		cfg   Config
		traps bool
	}{
		{"windows-trapform", Config{Target: types.X86_64Windows, Profile: types.Release, TrapForm: true}, true},
		{"windows", Config{Target: types.X86_64Windows, Profile: types.Release}, false},
		{"windows-dev-trapform", Config{Target: types.X86_64Windows, Profile: types.Dev, TrapForm: true}, false},
		{"linux-trapform", Config{Target: types.X86_64Linux, Profile: types.Release, TrapForm: true}, false},
	} {
		c.cfg.RootModule, c.cfg.Main = "main", types.ItemKey("main", "Run")
		t.Run(c.name, func(t *testing.T) {
			e := newTestEnv()
			u := trapUnitOf(e)
			ll := emitCfg(t, e, u, 0, c.cfg)
			check, twice, entry := fnText(ll, "mk.main.Check"), fnText(ll, "mk.main.Main"), fnText(ll, "mk.main.Entry")
			if got := strings.Contains(check, trap) && strings.Contains(check, pf); got != c.traps {
				t.Errorf("Check traps %v, want %v:\n%s", got, c.traps, check)
			}
			if c.traps && strings.Contains(check, "call void @mk.main.Fail(") {
				t.Errorf("Check keeps the call beside the trap:\n%s", check)
			}
			if !c.traps && !strings.Contains(check, "call void @mk.main.Fail(") {
				t.Errorf("Check lost the call:\n%s", check)
			}
			// Main carries no mark: its own call of Fail stays a call.
			if !strings.Contains(twice, "call void @mk.main.Fail(") || strings.Contains(twice, "ubsantrap") {
				t.Errorf("Twice (no alwaysinline) does not keep the call:\n%s", twice)
			}
			wantEntry := "store ptr null"
			if c.traps {
				wantEntry = "store ptr @mk_trap_call"
			}
			if !strings.Contains(entry, wantEntry) {
				t.Errorf("Entry has no %q:\n%s", wantEntry, entry)
			}
			var sb bytes.Buffer
			if err := EmitSupport(u, e, c.cfg, &sb); err != nil {
				t.Fatal(err)
			}
			sup := sb.String()
			if got := strings.Contains(sup, "mk_trap_call:") && strings.Contains(sup, ".seh_pushframe"); got != c.traps {
				t.Errorf("support pack defines mk_trap_call %v, want %v", got, c.traps)
			}
			if !c.traps {
				return
			}
			// The machine code: Check and Adapt are frameless leaves whose
			// cold path is the prefetchnta and ud1 pair; with the call
			// form (Config without TrapForm) they keep a frame.
			off := c.cfg
			off.TrapForm = false
			callLL := emitCfg(t, e, u, 0, off)
			bin := needLLVM(t)
			dir := t.TempDir()
			dis := func(name, ll, fn string) string {
				d := runTool(t, tool(bin, "llvm-objdump"), "-d", "--no-show-raw-insn", clangC(t, c.cfg.Target, dir, name, ll, "-O2"))
				i := strings.Index(d, "<"+fn+">:")
				if i < 0 {
					t.Fatalf("no %s in:\n%s", fn, d)
				}
				d = d[i+len(fn)+3:]
				if j := strings.Index(d, ">:"); j >= 0 {
					d = d[:j]
				}
				return d
			}
			for _, fn := range []string{"mk.main.Check", "mk.main.Adapt"} {
				d := dis("trap", ll, fn)
				if strings.Contains(d, "%rsp") || strings.Contains(d, "push") || strings.Contains(d, "callq") {
					t.Errorf("trapped %s has a frame or a call:\n%s", fn, d)
				}
				if !regexp.MustCompile(`prefetchnta\t[^\n]*\(%rip\)[^\n]*\n[^\n]*ud1l\t0x7f\(%eax\), %eax`).MatchString(d) {
					t.Errorf("trapped %s has no prefetchnta then ud1l 0x7f:\n%s", fn, d)
				}
				if d := dis("call", callLL, fn); !strings.Contains(d, "subq\t$0x28, %rsp") {
					t.Errorf("call-form %s has no frame (the shape this form removes):\n%s", fn, d)
				}
			}
			// The setting and the unit's trap fact are in the key.
			var keys []source.Hash
			for _, cfg := range []Config{c.cfg, off} {
				h := source.NewHasher("t")
				if !PackFacts(u, 0, e, cfg, h) {
					t.Fatal("no pack facts")
				}
				keys = append(keys, h.Sum())
			}
			if keys[0] == keys[1] {
				t.Error("TrapForm does not change the pack key")
			}
		})
	}
}
