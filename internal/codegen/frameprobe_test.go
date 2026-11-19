// Why: on x86_64 Windows LLVM's prologue realigns a frame after its __chkstk
// call, so a frame with a slot aligned over a page skips probe pages.
// What: tests that such a frame names mk_chkstk_65536, which also touches
// the pages the realignment skips, for any alignment up to 65536, and
// TestPrologueShape, which pins the prologue that routine assumes.
// How: one routine serves every alignment, since an inlined callee's
// alignment can exceed its caller's while the caller's probe-stack is kept.
// tests/programs/alignframe is the run test.

package codegen

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// alignedLocalUnit is Use(x: u64): u64 { var l := Block(x: x); return l.x },
// Block an align(n) struct of one u64.
func alignedLocalUnit(e *testEnv, n uint32) *Unit {
	bi := e.item("Block", types.KindStruct, &types.StructDecl{Align: n,
		Fields: []types.FieldDecl{{Name: e.in.Intern("x"), Type: types.U64}}})
	blk := e.tt.Named(bi, nil)
	f := e.body(e.fn("Use", []types.TypeID{types.U64}, types.U64, 0))
	l := f.local(blk)
	f.assign(place(l), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: bi, Type: blk,
		Ops: f.b.AddOperands(mir.Copy(place(1)))}, f.at(2, 1))
	f.assign(place(0), use(mir.Copy(f.b.Place(l, mir.Projection{Kind: mir.ProjField, A: 0, Type: types.U64}))), f.at(3, 1))
	f.ret(f.at(3, 1))
	return unitOf(e, []*mir.Body{f.b})
}

// TestFrameProbeOverPage: a local aligned over a page, at 8192 as at
// 65536, gives its function "probe-stack"="mk_chkstk_65536" on x86_64
// Windows, in both profiles, never at a call site, and the object's
// prologue calls it; a page or less, and every other target, keeps the
// default probe.
func TestFrameProbeOverPage(t *testing.T) {
	const attr = `"probe-stack"="mk_chkstk_65536"`
	for _, n := range []uint32{4096, 8192, 65536} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
				name := target.Name + "/" + strconv.Itoa(int(n)) + "/" + map[types.Profile]string{types.Dev: "dev", types.Release: "release"}[prof]
				t.Run(name, func(t *testing.T) {
					e := newTestEnv()
					ll := emit(t, e, alignedLocalUnit(e, n), target, prof)
					head, _, _ := strings.Cut(fnText(ll, "mk.main.Use"), "\n")
					want := n > 4096 && target.OS == types.OSWindows
					if strings.Contains(head, attr) != want {
						t.Errorf("%s on the definition: %v, want %v:\n%s", attr, !want, want, head)
					}
					if n := strings.Count(ll, "mk_chkstk_"); n != map[bool]int{true: 1, false: 0}[want] {
						t.Errorf("mk_chkstk_ named %d times (want only the definition's attribute):\n%s", n, ll)
					}
					if !want || prof != types.Dev {
						return
					}
					obj := clangC(t, target, t.TempDir(), "probe", ll, "-O0")
					dis := runTool(t, filepath.Join(needLLVM(t), "llvm-objdump"), "-dr", "--no-show-raw-insn", obj)
					if !strings.Contains(dis, "IMAGE_REL_AMD64_REL32\tmk_chkstk_65536") {
						t.Errorf("the prologue does not call mk_chkstk_65536:\n%s", dis)
					}
					if strings.Contains(dis, "__chkstk") {
						t.Errorf("the prologue still calls __chkstk:\n%s", dis)
					}
				})
			}
		}
	}
}

// TestAlignedProbeRoutine: the Windows support pack defines
// mk_chkstk_65536, __chkstk's pushes, then rax plus (R - rax) mod 65536
// with R the caller's rsp (so it probes down to (R - rax) & -65536), then
// its loop; no other target defines it. TestFuncTableRows checks its
// kind-1 row, as __chkstk's (the caller's return address at [sp + 16]).
func TestAlignedProbeRoutine(t *testing.T) {
	const add = "\npushq %rcx\npushq %rax\nleaq 24(%rsp), %rcx\nsubq %rax, %rcx\nandq $0xffff, %rcx\naddq %rcx, %rax\n"
	for _, target := range types.Targets() {
		ll := supportLL(t, target)
		has := strings.Contains(ll, `module asm "mk_chkstk_65536:"`)
		if has != (target.OS == types.OSWindows) || target.OS != types.OSWindows && strings.Contains(ll, "mk_chkstk") {
			t.Errorf("%s: mk_chkstk_65536 defined %v", target.Name, has)
		}
		if has && !strings.Contains(ll, `module asm "\09andq $0xffff, %rcx"`) {
			t.Errorf("%s: mk_chkstk_65536 does not probe down to the 64 KiB rounding", target.Name)
		}
	}
	// The pushes carry their unwind codes.
	if win64ChkstkAligned != win64ChkstkPushes+strings.TrimPrefix(add, "\npushq %rcx\npushq %rax")+win64ChkstkLoop {
		t.Errorf("mk_chkstk_65536 is not __chkstk's pushes, the addition and its loop:\n%s", win64ChkstkAligned)
	}
}

// probeShapeLL is a module whose functions name mk_chkstk_65536: f8 and f64
// hold a slot aligned 8192 and 65536, and caller (aligned 8192) inlines
// callee (aligned 65536), the merge the one routine is for.
const probeShapeLL = `declare void @sink(ptr)
define void @f8() "probe-stack"="mk_chkstk_65536" {
  %a = alloca [64 x i8], align 8192
  call void @sink(ptr %a)
  ret void
}
define void @f64() "probe-stack"="mk_chkstk_65536" {
  %a = alloca [64 x i8], align 65536
  call void @sink(ptr %a)
  ret void
}
define internal void @callee() alwaysinline "probe-stack"="mk_chkstk_65536" {
  %a = alloca [64 x i8], align 65536
  call void @sink(ptr %a)
  ret void
}
define void @caller() "probe-stack"="mk_chkstk_65536" {
  %a = alloca [64 x i8], align 8192
  call void @sink(ptr %a)
  call void @callee()
  ret void
}
`

// TestPrologueShape pins what mk_chkstk_65536 assumes of LLVM's Win64
// prologue: after the probe's call, the only change of rsp before the
// realigning andq is subq %rax, %rsp, so the rounding acts on exactly the
// caller's rsp less rax, at or above the probe's (R - rax) & -65536. At -O0
// and -O2 (clang 21.1.8), f8 rounds to 8192 and f64 and caller to 65536.
func TestPrologueShape(t *testing.T) {
	want := map[string]string{"f8": "$-0x2000", "f64": "$-0x10000", "caller": "$-0x10000"}
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			obj := clangC(t, types.X86_64Windows, t.TempDir(), "shape", probeShapeLL, opt)
			dis := runTool(t, filepath.Join(needLLVM(t), "llvm-objdump"), "-dr", "--no-show-raw-insn", obj)
			seen := map[string]bool{}
			for _, fn := range strings.Split(dis, "\n\n") {
				name, _, ok := strings.Cut(strings.TrimSpace(fn), ">:")
				if !ok {
					continue
				}
				name = name[strings.LastIndexByte(name, '<')+1:]
				if want[name] == "" {
					continue
				}
				seen[name] = true
				_, after, ok := strings.Cut(fn, "IMAGE_REL_AMD64_REL32\tmk_chkstk_65536")
				if !ok {
					t.Errorf("%s: no call of mk_chkstk_65536:\n%s", name, fn)
					continue
				}
				var subs int
				var and string
				for _, ln := range strings.Split(after, "\n")[1:] {
					f := strings.Fields(ln)
					if len(f) < 2 || strings.Contains(ln, "IMAGE_REL") {
						continue
					}
					ins := strings.Join(f[1:], " ")
					if strings.HasPrefix(ins, "andq") && strings.Contains(ins, ", %rsp") {
						and = f[2]
						break
					}
					switch {
					case ins == "subq %rax, %rsp":
						subs++
					case strings.Contains(ins, "%rsp") && !strings.Contains(ins, "(%rsp)") || strings.HasPrefix(ins, "push") || strings.HasPrefix(ins, "pop") || strings.HasPrefix(ins, "call"):
						t.Errorf("%s: %q changes rsp between the probe and the rounding:\n%s", name, ins, fn)
					}
				}
				if subs != 1 || and != want[name]+"," {
					t.Errorf("%s: %d subq %%rax, %%rsp and rounding %q, want 1 and %s:\n%s", name, subs, and, want[name], fn)
				}
			}
			if len(seen) != len(want) {
				t.Errorf("functions found %v, want %v:\n%s", seen, want, dis)
			}
		})
	}
}
