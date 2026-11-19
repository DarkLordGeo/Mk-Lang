// Why: mir.AddNoWrap must print `add nsw` on a signed type and `add nuw` on
// an unsigned one, never both, and no overflow check.
// What: TestAddNoWrap checks the IR; TestAddNoWrapRun runs counter
// loops natively at -O0 and -O2 on Windows and WSL Linux.
// How: the golden is testdata/nowrap.ll.golden, also covered by
// TestEmitGolden and TestEmitCompiles (clang 21, four targets and WSL).

package codegen

import (
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

func init() {
	emitCases = append(emitCases, emitCase{"nowrap", func(e *testEnv) ([]*mir.Body, []types.ItemID) {
		return nowrapBodies(e), nil
	}})
}

// countFn is lower's range-loop shape over a counter of type ct:
//
//	fn name(a: i64, n: i64): i64 {
//	    i := Trunc[ct](a); m := Trunc[ct](n)
//	    while i < m { i = AddNoWrap(i, 1) }   // c < m before the add proves it
//	    return Lossless[i64](i)
//	}
//
// For i64 the truncations are plain copies.
func countFn(e *testEnv, name string, ct types.TypeID) *mir.Body {
	f := e.body(e.fn(name, []types.TypeID{types.I64, types.I64}, types.I64, 0))
	i, m, c := f.local(ct), f.local(ct), f.local(types.Bool)
	conv := func(from mir.LocalID, to types.TypeID, kind mir.CastKind) mir.Rvalue {
		if to == types.I64 && ct == types.I64 {
			return use(mir.Copy(place(from)))
		}
		return mir.Rvalue{Kind: mir.RvCast, Cast: kind, A: mir.Copy(place(from)), Type: to}
	}
	f.assign(place(i), conv(1, ct, mir.CastTrunc), f.at(2, 5))
	f.assign(place(m), conv(2, ct, mir.CastTrunc), f.at(2, 25))
	hdr, body, exit := f.newBlock(), f.newBlock(), f.newBlock()
	f.term(mir.Terminator{Kind: mir.TermGoto, Target: hdr, Span: f.at(3, 5)})
	f.cur = hdr
	f.assign(place(c), bin(mir.Lt, mir.Copy(place(i)), mir.Copy(place(m))), f.at(3, 11))
	f.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(c)), Cases: f.b.AddCases(
		mir.SwitchCase{Value: 1, Target: body}, mir.SwitchCase{Value: 0, Target: exit}), Default: mir.NoBlock, Span: f.at(3, 5)})
	f.cur = body
	f.assign(place(i), bin(mir.AddNoWrap, mir.Copy(place(i)), mir.Imm(ct, 1)), f.at(3, 21))
	f.term(mir.Terminator{Kind: mir.TermGoto, Target: hdr, Span: f.at(3, 21)})
	f.cur = exit
	back := mir.CastLossless // i8, u8 widen exactly
	if ct == types.U64 {
		back = mir.CastTrunc // same width, a reinterpretation
	}
	f.assign(place(0), conv(i, types.I64, back), f.at(4, 5))
	f.ret(f.at(4, 5))
	return f.b
}

func nowrapBodies(e *testEnv) []*mir.Body {
	return []*mir.Body{
		countFn(e, "CountI8", types.I8),
		countFn(e, "CountU8", types.U8),
		countFn(e, "CountI64", types.I64),
		countFn(e, "CountU64", types.U64),
	}
}

var nowrapWant = map[string]string{
	"mk.main.CountI8":  "add nsw i8",
	"mk.main.CountU8":  "add nuw i8",
	"mk.main.CountI64": "add nsw i64",
	"mk.main.CountU64": "add nuw i64",
}

// TestAddNoWrap: each counter's increment carries exactly the flag of
// its type's signedness, never both, and no with.overflow call; for every
// target, dev and release, compiled by clang at -O2.
func TestAddNoWrap(t *testing.T) {
	both := regexp.MustCompile(`add (nuw nsw|nsw nuw) `)
	for _, target := range types.Targets() {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(target.Name+"/"+prof.String(), func(t *testing.T) {
				e := newTestEnv()
				ll := emit(t, e, unitOf(e, nowrapBodies(e)), target, prof)
				for fn, want := range nowrapWant {
					body := fnText(ll, fn)
					if strings.Count(body, want+" ") != 1 {
						t.Errorf("%s: want one %q in\n%s", fn, want, body)
					}
					if both.MatchString(body) || strings.Contains(body, "with.overflow") {
						t.Errorf("%s: both flags or an overflow check in\n%s", fn, body)
					}
				}
				clangC(t, target, t.TempDir(), "pack", ll, "-O2")
			})
		}
	}
}

const nowrapDecls = `
declare i64 @mk.main.CountI8(i64, i64)
declare i64 @mk.main.CountU8(i64, i64)
declare i64 @mk.main.CountI64(i64, i64)
declare i64 @mk.main.CountU64(i64, i64)

define internal void @h_pn(i64 %v) nounwind {
  call void @h_num(i64 %v)
  call void @h_write(ptr @s.nl, i64 1)
  ret void
}
`

// TestAddNoWrapRun runs the counters natively: a signed i8 counter from
// -3 to 2 (it crosses -1 to 0, which nuw would call a wrap), an unsigned u8
// one from 0 to 255 (it crosses 127 to 128, which nsw would call an
// overflow), i64 from -1 to 5, and u64 from 2^63-2 to 2^63 (crossing the
// signed boundary), at -O0 and -O2 on Windows and WSL Linux.
func TestAddNoWrapRun(t *testing.T) {
	body := pn(0, "@mk.main.CountI8(i64 -3, i64 2)") + pn(1, "@mk.main.CountU8(i64 0, i64 255)") +
		pn(2, "@mk.main.CountI64(i64 -1, i64 5)") +
		pn(3, "@mk.main.CountU64(i64 9223372036854775806, i64 -9223372036854775808)") +
		pn(4, "@mk.main.CountI8(i64 7, i64 2)")
	want := "2\n255\n5\n-9223372036854775808\n7\n"
	for _, target := range runTargets {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target.Name+opt, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				e := newTestEnv()
				ll := emit(t, e, unitOf(e, nowrapBodies(e)), target, types.Release)
				dir := t.TempDir()
				osIR, kernel := harnessOS(target)
				h := osIR + harnessRT + nowrapDecls + `
define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
` + body + `
  call void @h_exit(i32 0)
  unreachable
}
`
				objs := []string{
					clangC(t, target, dir, "support", supportLL(t, target), "-O2"),
					clangC(t, target, dir, "pack", ll, opt),
					clangC(t, target, dir, "harness", h, "-O0"),
				}
				exe := linkExe(t, target, dir, objs, linkOpts{kernel: kernel})
				out, code := runExe(t, target, exe, "")
				if code != 0 || out != want {
					t.Fatalf("exit %d, output\n%s\nwant\n%s", code, out, want)
				}
			})
		}
	}
}
