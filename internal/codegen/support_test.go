// Why: the support pack is hand-written IR (entry, memory functions, task
// switch, stack probe, clone) that every program links, with no C runtime.
// What: TestSupportGolden, TestSupportCompiles, TestEntryExitCode,
// TestSupportMemFunctions, TestMemSizeClasses, TestSwitchPingPong,
// TestChkstkLargeFrame, TestCloneTrampolineArgOrder and
// TestMemmoveBackwardBlocks.
// How: hand-written IR callers are linked with the pack and run natively;
// memory results are compared with Go's copy.

package codegen

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

func TestSupportGolden(t *testing.T) {
	for _, target := range types.Targets() {
		t.Run(target.Name, func(t *testing.T) {
			got := supportLL(t, target)
			if again := supportLL(t, target); again != got {
				t.Fatal("two emissions of the support pack differ")
			}
			path := filepath.Join("testdata", "support."+target.Name+".ll.golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if string(want) != got {
				t.Errorf("support pack for %s differs from %s (run with -update and read the diff)", target.Name, path)
			}
		})
	}
}

// TestSupportCompiles compiles the support pack of every target with clang
// at -O2, and the x86_64-linux one with WSL's clang 21 (the LLVM floor).
func TestSupportCompiles(t *testing.T) {
	for _, target := range types.Targets() {
		t.Run(target.Name, func(t *testing.T) {
			clangC(t, target, t.TempDir(), "support", supportLL(t, target), "-O2")
		})
	}
	t.Run("wsl-clang-x86_64-linux", func(t *testing.T) {
		wslClangRun(t, supportLL(t, types.X86_64Linux), "-O2")
	})
}

// buildHarness compiles the support pack at -O2 and the harness at -O0 and
// links them CRT-free.
func buildHarness(t *testing.T, target types.Target, harness string, o linkOpts) string {
	t.Helper()
	dir := t.TempDir()
	osIR, kernel := harnessOS(target)
	sup := clangC(t, target, dir, "support", supportLL(t, target), "-O2")
	h := clangC(t, target, dir, "harness", osIR+harness, "-O0")
	o.kernel = append(kernel, o.kernel...)
	return linkExe(t, target, dir, []string{sup, h}, o)
}

// TestEntryExitCode: the entry stub calls std.task.Start with null on
// Windows and the initial stack pointer on Linux, where argc is 1.
func TestEntryExitCode(t *testing.T) {
	for _, target := range runTargets {
		t.Run(target.Name, func(t *testing.T) {
			check := "%ok = icmp eq ptr %sp, null"
			if target.OS == types.OSLinux {
				check = "%argc = load i64, ptr %sp, align 8\n  %ok = icmp eq i64 %argc, 1"
			}
			h := `
define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
  ` + check + `
  %c = select i1 %ok, i32 42, i32 1
  call void @h_exit(i32 %c)
  unreachable
}
`
			exe := buildHarness(t, target, h, linkOpts{})
			if _, code := runExe(t, target, exe, ""); code != 42 {
				t.Fatalf("exit status %d, want 42", code)
			}
		})
	}
}

// memOp is one call of the memory-function test: op 0 memcpy, 1 memmove,
// 2 memset, 3 memcmp, 4 bcmp, at offsets a and b of the work buffer.
type memOp struct{ op, a, b, n, c int }

// TestSupportMemFunctions runs each of the five functions against Go's
// results over random sizes, offsets and overlaps, some of them over a
// kilobyte (many words).
func TestSupportMemFunctions(t *testing.T) {
	const size = 8192
	rng := rand.New(rand.NewSource(7))
	work := make([]byte, size)
	rng.Read(work)
	var ops []memOp
	for i := 0; i < 400; i++ {
		n := rng.Intn(70)
		if i%10 == 0 {
			n = rng.Intn(600)
		}
		if i%7 == 3 {
			n = 1016 + rng.Intn(3072)
		}
		o := memOp{op: i % 5, n: n, c: rng.Intn(256)}
		o.a, o.b = rng.Intn(size-n+1), rng.Intn(size-n+1)
		if o.op == 0 { // memcpy: disjoint ranges
			for o.a < o.b+n && o.b < o.a+n {
				o.a, o.b = rng.Intn(size-n+1), rng.Intn(size-n+1)
			}
		}
		if (o.op == 3 || o.op == 4) && rng.Intn(3) == 0 && n > 0 { // make equal ranges common
			ops = append(ops, memOp{op: 1, a: o.a, b: o.b, n: n})
			if rng.Intn(2) == 0 {
				ops = append(ops, memOp{op: 2, a: o.a + rng.Intn(n), n: 1, c: rng.Intn(256)})
			}
		}
		ops = append(ops, o)
	}
	checkMemOps(t, work, ops)
}

// TestMemSizeClasses: memcpy and memmove of every length from 0 to 300
// (each size class, its edges, and the 32-byte loop with its
// overlapping tail), at unaligned offsets, and memmove over ranges that
// overlap by every small distance in both directions, against Go's copy.
func TestMemSizeClasses(t *testing.T) {
	work := make([]byte, 8192)
	rand.New(rand.NewSource(9)).Read(work)
	var ops []memOp
	for n := 0; n <= 300; n++ {
		ops = append(ops, memOp{op: 0, a: 3 + n%5, b: 4096 + n%7, n: n})
		for _, dist := range []int{1, 3, 8, 9, 16, 31, 33, 64} {
			if n%3 == dist%3 || n <= 70 {
				ops = append(ops, memOp{op: 1, a: 1000 + dist, b: 1000, n: n}, memOp{op: 1, a: 2000, b: 2000 + dist, n: n})
			}
		}
	}
	checkMemOps(t, work, ops)
}

// checkMemOps runs ops in the support pack's functions over a buffer that
// starts as work, and compares the buffer and the compare results with
// Go's, on each run target.
func checkMemOps(t *testing.T, work []byte, ops []memOp) {
	t.Helper()
	size := len(work)
	init := append([]byte(nil), work...)
	var results []int32
	var body strings.Builder
	k := 0
	for _, o := range ops {
		pa := fmt.Sprintf("getelementptr inbounds (i8, ptr @work, i64 %d)", o.a)
		pb := fmt.Sprintf("getelementptr inbounds (i8, ptr @work, i64 %d)", o.b)
		switch o.op {
		case 0:
			copy(work[o.a:o.a+o.n], work[o.b:o.b+o.n])
			fmt.Fprintf(&body, "  call ptr @memcpy(ptr %s, ptr %s, i64 %d)\n", pa, pb, o.n)
		case 1:
			copy(work[o.a:o.a+o.n], work[o.b:o.b+o.n])
			fmt.Fprintf(&body, "  call ptr @memmove(ptr %s, ptr %s, i64 %d)\n", pa, pb, o.n)
		case 2:
			for i := 0; i < o.n; i++ {
				work[o.a+i] = byte(o.c)
			}
			fmt.Fprintf(&body, "  call ptr @memset(ptr %s, i32 %d, i64 %d)\n", pa, o.c|0x7700, o.n)
		case 3, 4:
			var r int32
			for i := 0; i < o.n; i++ {
				if work[o.a+i] != work[o.b+i] {
					r = int32(work[o.a+i]) - int32(work[o.b+i])
					break
				}
			}
			if o.op == 4 && r != 0 {
				r = 1
			}
			results = append(results, r)
			name := map[int]string{3: "memcmp", 4: "bcmp"}[o.op]
			fmt.Fprintf(&body, "  %%r%d = call i32 @%s(ptr %s, ptr %s, i64 %d)\n", k, name, pa, pb, o.n)
			fmt.Fprintf(&body, "  store i32 %%r%d, ptr getelementptr inbounds (i8, ptr @res, i64 %d), align 4\n", k, 4*k)
			k++
		}
	}
	h := fmt.Sprintf(`
@work = internal global [%d x i8] c"%s", align 16
@res = internal global [%d x i32] zeroinitializer, align 16

declare ptr @memcpy(ptr, ptr, i64)
declare ptr @memmove(ptr, ptr, i64)
declare ptr @memset(ptr, i32, i64)
declare i32 @memcmp(ptr, ptr, i64)
declare i32 @bcmp(ptr, ptr, i64)

define void @"mk.std.task.Start"(ptr %%sp) noreturn nounwind {
%s  call void @h_write(ptr @work, i64 %d)
  call void @h_write(ptr @res, i64 %d)
  call void @h_exit(i32 0)
  unreachable
}
`, size, llBytes(init), max(k, 1), body.String(), size, 4*k)
	want := append([]byte(nil), work...)
	for _, r := range results {
		want = binary.LittleEndian.AppendUint32(want, uint32(r))
	}
	for _, target := range runTargets {
		t.Run(target.Name, func(t *testing.T) {
			exe := buildHarness(t, target, h, linkOpts{})
			out, code := runExe(t, target, exe, "")
			if code != 0 {
				t.Fatalf("exit status %d", code)
			}
			got := []byte(out)
			if !bytes.Equal(got[:min(len(got), size)], want[:size]) {
				for i := 0; i < size && i < len(got); i++ {
					if got[i] != want[i] {
						t.Fatalf("work buffer differs from Go's at byte %d: %#x, want %#x", i, got[i], want[i])
					}
				}
				t.Fatalf("output is %d bytes, want %d", len(got), len(want))
			}
			for i := range results {
				off := size + 4*i
				if off+4 > len(got) {
					t.Fatalf("output is %d bytes, want %d", len(got), len(want))
				}
				if r := int32(binary.LittleEndian.Uint32(got[off:])); r != results[i] {
					t.Errorf("compare %d: %d, want %d", i, r, results[i])
				}
			}
		})
	}
}

// ppSwitch is the test's register checker around mk_switch, per target:
// pp_switch(from, to, in, out) saves its caller's callee-saved state, loads
// every callee-saved register (and MXCSR and the x87 control word) from in,
// calls mk_switch(from, to), stores the registers it comes back with into
// out, and restores its caller's state. The other task runs the same with
// other values, so out equals in exactly when mk_switch preserved them.
func ppSwitch(target types.Target) (asm string, size int) {
	if target.OS == types.OSWindows {
		var s strings.Builder
		s.WriteString(".text\n.globl pp_switch\npp_switch:\n")
		w := func(f string, a ...any) { fmt.Fprintf(&s, "\t"+f+"\n", a...) }
		for _, r := range []string{"rbp", "rbx", "rdi", "rsi", "r12", "r13", "r14", "r15"} {
			w("pushq %%%s", r)
		}
		w("subq $184, %%rsp")
		for i := 6; i <= 15; i++ {
			w("movups %%xmm%d, %d(%%rsp)", i, 16*(i-6))
		}
		w("stmxcsr 160(%%rsp)")
		w("fnstcw 164(%%rsp)")
		w("movq %%r9, 168(%%rsp)")
		gprs := []string{"rbx", "rbp", "rdi", "rsi", "r12", "r13", "r14", "r15"}
		for i, r := range gprs {
			w("movq %d(%%r8), %%%s", 8*i, r)
		}
		for i := 6; i <= 15; i++ {
			w("movups %d(%%r8), %%xmm%d", 64+16*(i-6), i)
		}
		w("ldmxcsr 224(%%r8)")
		w("fldcw 228(%%r8)")
		w("callq mk_switch")
		w("movq 168(%%rsp), %%r9")
		for i, r := range gprs {
			w("movq %%%s, %d(%%r9)", r, 8*i)
		}
		for i := 6; i <= 15; i++ {
			w("movups %%xmm%d, %d(%%r9)", i, 64+16*(i-6))
		}
		w("stmxcsr 224(%%r9)")
		w("fnstcw 228(%%r9)")
		for i := 6; i <= 15; i++ {
			w("movups %d(%%rsp), %%xmm%d", 16*(i-6), i)
		}
		w("ldmxcsr 160(%%rsp)")
		w("fldcw 164(%%rsp)")
		w("addq $184, %%rsp")
		for _, r := range []string{"r15", "r14", "r13", "r12", "rsi", "rdi", "rbx", "rbp"} {
			w("popq %%%s", r)
		}
		w("retq")
		return s.String(), 230
	}
	var s strings.Builder
	s.WriteString(".text\n.globl pp_switch\npp_switch:\n")
	w := func(f string, a ...any) { fmt.Fprintf(&s, "\t"+f+"\n", a...) }
	for _, r := range []string{"rbp", "rbx", "r12", "r13", "r14", "r15"} {
		w("pushq %%%s", r)
	}
	w("subq $24, %%rsp")
	w("stmxcsr (%%rsp)")
	w("fnstcw 4(%%rsp)")
	w("movq %%rcx, 8(%%rsp)")
	gprs := []string{"rbx", "rbp", "r12", "r13", "r14", "r15"}
	for i, r := range gprs {
		w("movq %d(%%rdx), %%%s", 8*i, r)
	}
	w("ldmxcsr 48(%%rdx)")
	w("fldcw 52(%%rdx)")
	w("callq mk_switch")
	w("movq 8(%%rsp), %%rcx")
	for i, r := range gprs {
		w("movq %%%s, %d(%%rcx)", r, 8*i)
	}
	w("stmxcsr 48(%%rcx)")
	w("fnstcw 52(%%rcx)")
	w("ldmxcsr (%%rsp)")
	w("fldcw 4(%%rsp)")
	w("addq $24, %%rsp")
	for _, r := range []string{"r15", "r14", "r13", "r12", "rbx", "rbp"} {
		w("popq %%%s", r)
	}
	w("retq")
	return s.String(), 54
}

// ppValues are 8 register blocks of 240 bytes for one side: random GPR and
// xmm bytes, a valid MXCSR (rounding mode varied) and x87 control word.
func ppValues(rng *rand.Rand, target types.Target, size int) []byte {
	out := make([]byte, 8*240)
	for k := 0; k < 8; k++ {
		blk := out[240*k : 240*(k+1)]
		rng.Read(blk[:size-6])
		mx, cw := size-6, size-2
		binary.LittleEndian.PutUint32(blk[mx:], 0x1F80|uint32(rng.Intn(4))<<13)
		binary.LittleEndian.PutUint16(blk[cw:], 0x037F|uint16(rng.Intn(4))<<10)
	}
	return out
}

// TestSwitchPingPong: two contexts, one made by InitContext on a 64 KiB
// stack, switch 1,000 times; every callee-saved register, xmm6 to xmm15 on
// Windows, MXCSR and the x87 control word survive each switch.
func TestSwitchPingPong(t *testing.T) {
	for _, target := range runTargets {
		t.Run(target.Name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(11))
			asm, size := ppSwitch(target)
			va, vb := ppValues(rng, target, size), ppValues(rng, target, size)
			tib := ""
			if target.OS == types.OSWindows {
				tib = `
  store ptr getelementptr inbounds (i8, ptr @stackB, i64 65536), ptr getelementptr inbounds (i8, ptr @ctxB, i64 8), align 8
  store ptr @stackB, ptr getelementptr inbounds (i8, ptr @ctxB, i64 16), align 8
  store ptr @stackB, ptr getelementptr inbounds (i8, ptr @ctxB, i64 24), align 8
`
			}
			var lines []string
			for _, l := range strings.Split(strings.TrimSpace(asm), "\n") {
				lines = append(lines, fmt.Sprintf("module asm %q", strings.TrimSpace(l)))
			}
			h := strings.Join(lines, "\n") + fmt.Sprintf(`

@ctxA = internal global [8 x i64] zeroinitializer, align 16
@ctxB = internal global [8 x i64] zeroinitializer, align 16
@stackB = internal global [65536 x i8] zeroinitializer, align 16
@valsA = internal constant [1920 x i8] c"%s", align 16
@valsB = internal constant [1920 x i8] c"%s", align 16
@outA = internal global [240 x i8] zeroinitializer, align 16
@outB = internal global [240 x i8] zeroinitializer, align 16

declare void @pp_switch(ptr, ptr, ptr, ptr) nounwind
declare void @"mk.support.InitContext"(ptr, ptr, ptr, ptr) nounwind
declare i32 @memcmp(ptr, ptr, i64)

define internal void @round(ptr %%from, ptr %%to, ptr %%vals, ptr %%out, i64 %%i, i32 %%code) nounwind {
  %%k = urem i64 %%i, 8
  %%off = mul i64 %%k, 240
  %%in = getelementptr inbounds i8, ptr %%vals, i64 %%off
  call void @pp_switch(ptr %%from, ptr %%to, ptr %%in, ptr %%out)
  %%r = call i32 @memcmp(ptr %%out, ptr %%in, i64 %d)
  %%bad = icmp ne i32 %%r, 0
  br i1 %%bad, label %%fail, label %%ok
fail:
  call void @h_exit(i32 %%code)
  unreachable
ok:
  ret void
}

define internal void @taskB(ptr %%arg) nounwind {
entry:
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  call void @round(ptr @ctxB, ptr @ctxA, ptr @valsB, ptr @outB, i64 %%iv, i32 3)
  %%n = add i64 %%iv, 1
  store i64 %%n, ptr %%i, align 8
  br label %%loop
}

define void @"mk.std.task.Start"(ptr %%sp) noreturn nounwind {
entry:
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
%s
  call void @"mk.support.InitContext"(ptr @ctxB, ptr getelementptr inbounds (i8, ptr @stackB, i64 65536), ptr @taskB, ptr null)
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  call void @round(ptr @ctxA, ptr @ctxB, ptr @valsA, ptr @outA, i64 %%iv, i32 2)
  %%n = add i64 %%iv, 1
  store i64 %%n, ptr %%i, align 8
  %%done = icmp eq i64 %%n, 1000
  br i1 %%done, label %%exit, label %%loop
exit:
  call void @h_exit(i32 42)
  unreachable
}
`, llBytes(va), llBytes(vb), size, tib)
			exe := buildHarness(t, target, h, linkOpts{})
			if _, code := runExe(t, target, exe, ""); code != 42 {
				t.Fatalf("exit status %d, want 42 (2: a register of the main side changed, 3: of the task)", code)
			}
		})
	}
}

// TestChkstkLargeFrame: a 1 MiB frame runs on a 2 MiB stack; on Windows a
// 512 KiB stack ends in STATUS_STACK_OVERFLOW (the guard page is hit in
// order, never jumped: a jump past it would be an access violation), and on
// Linux a 512 KiB stack limit ends in SIGSEGV.
func TestChkstkLargeFrame(t *testing.T) {
	for _, target := range runTargets {
		t.Run(target.Name, func(t *testing.T) {
			probe := ""
			if target.OS == types.OSLinux {
				probe = ` "probe-stack"="inline-asm" "stack-probe-size"="4096"`
			}
			h := `
define internal void @big() noinline nounwind` + probe + ` {
  %a = alloca [1048576 x i8], align 16
  store volatile i8 1, ptr %a, align 1
  %e = getelementptr inbounds i8, ptr %a, i64 1048575
  store volatile i8 2, ptr %e, align 1
  ret void
}

define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
  call void @big()
  call void @h_exit(i32 42)
  unreachable
}
`
			exe := buildHarness(t, target, h, linkOpts{stack: 2 << 20})
			if _, code := runExe(t, target, exe, ""); code != 42 {
				t.Fatalf("2 MiB stack: exit status %d, want 42", code)
			}
			if target.OS == types.OSWindows {
				exe := buildHarness(t, target, h, linkOpts{stack: 512 << 10})
				if _, code := runExe(t, target, exe, ""); uint32(code) != 0xC00000FD {
					t.Fatalf("512 KiB stack: exit status %#x, want STATUS_STACK_OVERFLOW 0xC00000FD", uint32(code))
				}
				return
			}
			if _, code := runExe(t, target, exe, "ulimit -s 512;"); code != 139 {
				t.Fatalf("512 KiB stack: exit status %d, want 139 (SIGSEGV)", code)
			}
		})
	}
}

// elfFunc returns the bytes of a defined function symbol of an ELF object.
func elfFunc(t *testing.T, obj, name string) []byte {
	t.Helper()
	f, err := elf.Open(obj)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range syms {
		if s.Name != name || s.Section == elf.SHN_UNDEF || int(s.Section) >= len(f.Sections) {
			continue
		}
		data, err := f.Sections[s.Section].Data()
		if err != nil {
			t.Fatal(err)
		}
		if s.Value+s.Size > uint64(len(data)) || s.Size == 0 {
			t.Fatalf("%s: symbol %s at %d+%d outside its section", obj, name, s.Value, s.Size)
		}
		return data[s.Value : s.Value+s.Size]
	}
	t.Fatalf("%s: no symbol %s", obj, name)
	return nil
}

// TestCloneTrampolineArgOrder decodes mk_clone in the compiled objects: on
// aarch64 the instructions before the first svc #0 swap x3 and x4 through x9
// (the kernel takes tls in x3 and ctid in x4); on x86_64 ctid moves from rcx
// to r10 and tls stays in r8.
func TestCloneTrampolineArgOrder(t *testing.T) {
	t.Run("aarch64-linux", func(t *testing.T) {
		obj := clangC(t, types.Aarch64Linux, t.TempDir(), "support", supportLL(t, types.Aarch64Linux), "-O2")
		code := elfFunc(t, obj, symClone)
		var words []uint32
		for i := 0; i+4 <= len(code); i += 4 {
			w := binary.LittleEndian.Uint32(code[i:])
			if w == 0xD4000001 { // svc #0
				break
			}
			words = append(words, w)
		}
		mov := func(d, m uint32) uint32 { return 0xAA0003E0 | m<<16 | d } // orr xd, xzr, xm
		want := []uint32{mov(9, 3), mov(3, 4), mov(4, 9)}
		at := -1
		for i := 0; i+len(want) <= len(words); i++ {
			if words[i] == want[0] && words[i+1] == want[1] && words[i+2] == want[2] {
				at = i
			}
		}
		if at < 0 {
			t.Fatalf("mk_clone before svc #0: %08x; want the swap %08x", words, want)
		}
	})
	t.Run("x86_64-linux", func(t *testing.T) {
		obj := clangC(t, types.X86_64Linux, t.TempDir(), "support", supportLL(t, types.X86_64Linux), "-O2")
		code := elfFunc(t, obj, symClone)
		sys := bytes.Index(code, []byte{0x0f, 0x05})
		if sys < 0 {
			t.Fatal("mk_clone has no syscall")
		}
		pre := code[:sys]
		if !bytes.Contains(pre, []byte{0x49, 0x89, 0xca}) { // movq %rcx, %r10
			t.Fatalf("mk_clone does not move ctid (rcx) to r10 before syscall: % x", pre)
		}
		for i := 0; i+2 < len(pre); i++ { // no mov of a register or immediate into r8
			rex, op, modrm := pre[i], pre[i+1], pre[i+2]
			if rex&0xF0 != 0x40 {
				continue
			}
			if op == 0x89 && rex&1 != 0 && modrm>>6 == 3 && modrm&7 == 0 ||
				op == 0x8b && rex&4 != 0 && modrm>>3&7 == 0 || op == 0xb8 && rex&1 != 0 {
				t.Fatalf("mk_clone writes r8 (tls) before syscall: % x", pre)
			}
		}
	})
}

// TestMemmoveBackwardBlocks: memmove's backward path (d above s,
// overlapping, n > 64), 32-byte blocks from the end with the head
// loaded first, at every length from 65 to 300 and every distance
// from 1 to 40, and at about 4 KiB (an insert near the front of a long
// buffer), against Go's copy; the forward overlap at the same distances.
func TestMemmoveBackwardBlocks(t *testing.T) {
	work := make([]byte, 8192)
	rand.New(rand.NewSource(5)).Read(work)
	var ops []memOp
	for n := 65; n <= 300; n++ {
		for dist := 1; dist <= 40; dist++ {
			ops = append(ops, memOp{op: 1, a: 1000 + dist + n%3, b: 1000 + n%3, n: n})
			if n%8 == dist%8 {
				ops = append(ops, memOp{op: 1, a: 3000, b: 3000 + dist, n: n})
			}
		}
	}
	for _, n := range []int{4064, 4095, 4096, 4097, 4127} {
		for _, dist := range []int{1, 7, 8, 31, 32, 33, 3000} {
			ops = append(ops, memOp{op: 1, a: 100 + dist, b: 100, n: n})
		}
	}
	checkMemOps(t, work, ops)
}
