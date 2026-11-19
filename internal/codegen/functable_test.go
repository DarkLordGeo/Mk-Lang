// Why: fault reports and stack traces find a function's name and site
// through FuncTable, so every user function needs a row.
// What: TestFuncTableRows (every pack's descriptor and rows, the support
// pack's FuncTable over all of them) and TestFuncTableRun (the rows a fault
// report scans, read by a native program).
// How: funcTableUnit's packs are emitted and their rows read from the IR,
// then linked with funcTableHarness and run.

package codegen

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// funcTableUnit is two packs: Twice(n) = n *% 2 at line 3 col 1, Local to
// pack 0 (so internal), and Thrice(n) = n *% 3 at line 7 col 1 in pack 1,
// plus the bodiless std.rt.FuncTable.
func funcTableUnit(e *testEnv) *Unit {
	site := e.structIn("std.rt", "Site", types.Str, types.U32, types.U32)
	row := e.structIn("std.rt", "FuncRow", e.tt.RawPtr(types.U8), e.tt.RawPtr(site), types.U32)
	ft := e.stdFn("std.rt", "", "FuncTable", 0, nil, e.tt.Slice(row))
	mk := func(name string, k uint64, line uint32) *mir.Body {
		id := e.fn(name, []types.TypeID{types.Int}, types.Int, 0)
		e.items.Get(id).Span = source.Span{File: 1, Start: (line - 1) * 100, End: (line-1)*100 + uint32(len(name))}
		f := e.body(id)
		f.assign(place(0), bin(mir.MulWrap, mir.Copy(place(1)), mir.Imm(types.Int, k)), f.at(line+1, 5))
		f.ret(f.at(line+1, 5))
		return f.b
	}
	tw, th := mk("Twice", 2, 3), mk("Thrice", 3, 7)
	u := &Unit{Packs: [][]int{{0}, {1}}}
	u.Funcs = append(u.Funcs,
		Func{Symbol: "mk.main.Twice", Key: "main.Twice", Item: tw.Item, Body: tw, Pack: 0, Local: true},
		Func{Symbol: "mk.main.Thrice", Key: "main.Thrice", Item: th.Item, Body: th, Pack: 1},
		Func{Symbol: "mk.std.rt.FuncTable", Key: "std.rt.FuncTable", Item: ft, Pack: -1})
	return u
}

// TestFuncTableRows: each member gets a private row { ptr @f,
// ptr site, i32 0 } in @llvm.compiler.used, in section mk_funcs with
// !associated !{ptr @f} on ELF, or in .mkfn$M in f's comdat (and f in it)
// on COFF; its site is main/t.mk:<line>:1 of the item name; a Local
// function stays internal. The support pack names no pack: FuncTable is the
// slice between the row bounds (__start_mk_funcs/__stop_mk_funcs; on COFF
// its own rows in .mkfn$B up to the .mkfn$Z sentinel), with kind-2 rows for
// its routines and kind 1 for __chkstk.
func TestFuncTableRows(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux, types.Aarch64Linux, types.Aarch64Macos} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(target.Name+"/"+prof.String(), func(t *testing.T) {
				e := newTestEnv()
				u := funcTableUnit(e)
				cfg := Config{Target: target, Profile: prof}
				coff, elf := target.OS == types.OSWindows, target.OS == types.OSLinux
				for p, want := range []struct{ sym, line string }{{"mk.main.Twice", "3"}, {"mk.main.Thrice", "7"}} {
					var b strings.Builder
					if _, err := EmitPack(u, p, e, cfg, &b); err != nil {
						t.Fatal(err)
					}
					ll := b.String()
					sym := regexp.QuoteMeta(want.sym)
					tail := `, section "__DATA,__mk_funcs", align 8$`
					switch {
					case coff:
						tail = `, section "\.mkfn\$M", comdat\(\$` + sym + `\), align 8$`
					case elf:
						tail = `, section "mk_funcs", align 8, !associated (![0-9]+)$`
					}
					d := regexp.MustCompile(`(?m)^@mk\.fr\.` + sym + ` = private constant \{ ptr, ptr, i32 \} \{ ptr @` + sym +
						`, ptr (@[^,]+), i32 0 \}` + tail).FindStringSubmatch(ll)
					if d == nil {
						t.Fatalf("pack %d: no row for %s in\n%s", p, want.sym, ll)
					}
					if elf && !strings.Contains(ll, d[2]+" = !{ptr @"+want.sym+"}") {
						t.Errorf("pack %d: %s is not !{ptr @%s}", p, d[2], want.sym)
					}
					if coff && (!strings.Contains(ll, "$"+want.sym+" = comdat nodeduplicate") ||
						!regexp.MustCompile(`(?m)^define .*@`+sym+`\(.*\) .*comdat\(\$`+sym+`\)`).MatchString(ll)) {
						t.Errorf("pack %d: %s is not in its comdat", p, want.sym)
					}
					if !strings.Contains(ll, "@llvm.compiler.used = appending global [1 x ptr] [ptr @mk.fr."+want.sym+"]") {
						t.Errorf("pack %d: the row is not in llvm.compiler.used", p)
					}
					if strings.Contains(ll, "mk.funcs.") {
						t.Errorf("pack %d: an old-format descriptor is left", p)
					}
					// An FnSite: the Site, then the
					// name's length and bytes, without its module path.
					short := strings.TrimPrefix(want.sym, "mk.main.")
					pos := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(d[1]) + ` = private unnamed_addr constant \{ ptr, i64, i32, i32, i32, \[\d+ x i8\] \} \{ ptr @[^,]+, i64 9, i32 ` +
						want.line + `, i32 1, i32 ` + strconv.Itoa(len(short)) + `, \[\d+ x i8\] c"` + short + `" \}`)
					if !pos.MatchString(ll) {
						t.Errorf("pack %d: the row's site %s is not main/t.mk:%s:1 in\n%s", p, d[1], want.line, ll)
					}
					if p == 0 && !strings.Contains(ll, "define internal noundef i64 @mk.main.Twice(") {
						t.Errorf("Twice is Local but not internal:\n%s", fnText(ll, "mk.main.Twice"))
					}
					clangC(t, target, t.TempDir(), "pack", ll, "-O2")
				}
				var sb strings.Builder
				if err := EmitSupport(u, e, cfg, &sb); err != nil {
					t.Fatal(err)
				}
				sl := sb.String()
				if strings.Contains(sl, "Twice") || strings.Contains(sl, "Thrice") || strings.Contains(sl, "mk.funcs.") {
					t.Errorf("the support pack names a pack:\n%s", sl)
				}
				var bounds []string
				switch {
				case coff:
					bounds = []string{`@mk.support.funcs = private constant \[\d+ x \{ ptr, ptr, i32 \}\] .*, section "\.mkfn\$B", align 8`,
						`@mk.support.funcend = private constant i64 0, section "\.mkfn\$Z", align 8`}
				case elf:
					bounds = []string{`@mk.support.funcs = private constant \[\d+ x \{ ptr, ptr, i32 \}\] .*, section "mk_funcs", align 8`,
						`@__start_mk_funcs = external hidden global i64, align 8`, `@__stop_mk_funcs = external hidden global i64, align 8`,
						`@llvm.used = appending global \[\d+ x ptr\] \[.*ptr @mk.support.funcs`}
				default:
					bounds = []string{`section "__DATA,__mk_funcs"`, `section\$start\$__DATA\$__mk_funcs`}
				}
				for _, re := range bounds {
					if !regexp.MustCompile(`(?m)` + re).MatchString(sl) {
						t.Errorf("want /%s/ in the support pack", re)
					}
				}
				for _, sym := range []string{"mk.support.InitContext", "mk_switch", "mk.std.rt.FuncTable"} {
					if sym == "mk_switch" && !coff && !elf {
						continue // Mach-O: no mk_switch row checked (assembled, not run)
					}
					if !regexp.MustCompile(`\{ ptr @` + regexp.QuoteMeta(sym) + `, ptr null, i32 2 \}`).MatchString(sl) {
						t.Errorf("no kind-2 support row for %s", sym)
					}
				}
				// The memory functions C code calls too are kind 3.
				for _, sym := range []string{"memcpy", "memmove", "memset", "memcmp", "bcmp"} {
					if !strings.Contains(sl, "{ ptr @"+sym+", ptr null, i32 3 }") {
						t.Errorf("no kind-3 row for %s", sym)
					}
				}
				if coff && !strings.Contains(sl, "{ ptr @__chkstk, ptr null, i32 1 }") {
					t.Error("no kind-1 row for __chkstk")
				}
				if coff && !strings.Contains(sl, "{ ptr @mk_chkstk_65536, ptr null, i32 1 }") {
					t.Error("no kind-1 row for mk_chkstk_65536")
				}
				clangC(t, target, t.TempDir(), "support", sl, "-O2")
			})
		}
	}
}

// funcTableHarness calls std.rt.FuncTable twice; for each kind-0 row it
// prints its site's line, column and file length and the row's function
// called with 7; then 1 when the second call returns the same slice.
func funcTableHarness(target types.Target, decls, pre string) string {
	call := func(n string) string {
		if target.OS == types.OSWindows { // Win64: a 16-byte result through a hidden pointer
			return fmt.Sprintf("  %%out%[1]s = alloca { ptr, i64 }, align 8\n  call void @mk.std.rt.FuncTable(ptr %%out%[1]s)\n"+
				"  %%d%[1]s = load ptr, ptr %%out%[1]s, align 8\n  %%np%[1]s = getelementptr i8, ptr %%out%[1]s, i64 8\n"+
				"  %%n%[1]s = load i64, ptr %%np%[1]s, align 8\n", n)
		}
		return fmt.Sprintf("  %%s%[1]s = call { ptr, i64 } @mk.std.rt.FuncTable()\n  %%d%[1]s = extractvalue { ptr, i64 } %%s%[1]s, 0\n"+
			"  %%n%[1]s = extractvalue { ptr, i64 } %%s%[1]s, 1\n", n)
	}
	decl := "declare { ptr, i64 } @mk.std.rt.FuncTable()\n"
	if target.OS == types.OSWindows {
		decl = "declare void @mk.std.rt.FuncTable(ptr)\n"
	}
	return decl + decls + `
define internal void @h_pn(i64 %v) nounwind {
  call void @h_num(i64 %v)
  call void @h_write(ptr @s.nl, i64 1)
  ret void
}

define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
entry:
` + pre + call("1") + `  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %i1, %next ]
  %end = icmp eq i64 %i, %n1
  br i1 %end, label %done, label %body
body:
  %off = mul i64 %i, 24
  %row = getelementptr i8, ptr %d1, i64 %off
  %kp = getelementptr i8, ptr %row, i64 16
  %k = load i32, ptr %kp, align 8
  %mk = icmp eq i32 %k, 0
  br i1 %mk, label %fn, label %next
fn:
  %addr = load ptr, ptr %row, align 8
  %sp2 = getelementptr i8, ptr %row, i64 8
  %site = load ptr, ptr %sp2, align 8
  %lp = getelementptr i8, ptr %site, i64 16
  %ln = load i32, ptr %lp, align 8
  %cp = getelementptr i8, ptr %site, i64 20
  %cl = load i32, ptr %cp, align 4
  %fp = getelementptr i8, ptr %site, i64 8
  %fl = load i64, ptr %fp, align 8
  %ln64 = zext i32 %ln to i64
  %cl64 = zext i32 %cl to i64
  call void @h_pn(i64 %ln64)
  call void @h_pn(i64 %cl64)
  call void @h_pn(i64 %fl)
  %r = call i64 %addr(i64 7)
  call void @h_pn(i64 %r)
  br label %next
next:
  %i1 = add i64 %i, 1
  br label %loop
done:
` + call("2") + `  %same1 = icmp eq i64 %n1, %n2
  %same2 = icmp eq ptr %d1, %d2
  %same = and i1 %same1, %same2
  %s64 = zext i1 %same to i64
  call void @h_pn(i64 %s64)
  call void @h_exit(i32 0)
  unreachable
}
`
}

// TestFuncTableRun: natively, dev and release optimization, FuncTable
// returns a row for the internal Twice and the external Thrice with their
// names' sites (main/t.mk:3:1 and 7:1), each address calls its function,
// and a second call returns the same slice (the copy is made once).
func TestFuncTableRun(t *testing.T) {
	for _, target := range runTargets {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target.Name+opt, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				e := newTestEnv()
				u := funcTableUnit(e)
				prof := types.Dev
				if opt == "-O2" {
					prof = types.Release
				}
				cfg := Config{Target: target, Profile: prof}
				dir := t.TempDir()
				var objs []string
				for p := range u.Packs {
					var b strings.Builder
					if _, err := EmitPack(u, p, e, cfg, &b); err != nil {
						t.Fatal(err)
					}
					objs = append(objs, clangC(t, target, dir, fmt.Sprint("pack", p), b.String(), opt))
				}
				var sb strings.Builder
				if err := EmitSupport(u, e, cfg, &sb); err != nil {
					t.Fatal(err)
				}
				osIR, kernel := harnessOS(target)
				objs = append(objs, clangC(t, target, dir, "support", sb.String(), "-O2"),
					clangC(t, target, dir, "harness", osIR+harnessRT+funcTableHarness(target, "", ""), "-O0"))
				// Twice is reached only through its row: keep unreferenced
				// sections (lld-link discards them by default).
				out, code := runExe(t, target, linkExe(t, target, dir, objs, linkOpts{kernel: kernel, noref: true}), "")
				if want := "3\n1\n9\n14\n7\n1\n9\n21\n1\n"; code != 0 || out != want {
					t.Fatalf("exit %d, output\n%s\nwant\n%s", code, out, want)
				}
			})
		}
	}
}
