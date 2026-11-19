; Smoke program for x86_64-windows (TestSmokeWindowsNoCRT). No CRT: its own
; entry mk_start, memcpy, memset, __chkstk and _fltused; only kernel32
; through an import library made from kernel32.def. The test prepends the
; probed target datalayout and triple. It prints "hello from mk\n" and exits
; 7. The 8 KiB frame makes LLVM call __chkstk; the lengths are volatile
; loads so memcpy and memset stay calls at -O3 and in ThinLTO.

source_filename = "smoke_windows.mk"

@msg = private unnamed_addr constant [14 x i8] c"hello from mk\0A"
@msg_len = internal global i64 14
@buf_len = internal global i64 8192
@_fltused = dso_local global i32 0

declare dllimport ptr @GetStdHandle(i32) nounwind
declare dllimport i32 @WriteFile(ptr, ptr, i32, ptr, ptr) nounwind
declare dllimport void @ExitProcess(i32) nounwind noreturn

declare void @llvm.memcpy.p0.p0.i64(ptr, ptr, i64, i1)
declare void @llvm.memset.p0.i64(ptr, i8, i64, i1)

module asm ".text"
module asm ".globl __chkstk"
module asm "__chkstk:"
module asm "  pushq %rcx"
module asm "  pushq %rax"
module asm "  cmpq $0x1000, %rax"
module asm "  leaq 24(%rsp), %rcx"
module asm "  jb 1f"
module asm "2:"
module asm "  subq $0x1000, %rcx"
module asm "  testq %rcx, (%rcx)"
module asm "  subq $0x1000, %rax"
module asm "  cmpq $0x1000, %rax"
module asm "  ja 2b"
module asm "1:"
module asm "  subq %rax, %rcx"
module asm "  testq %rcx, (%rcx)"
module asm "  popq %rax"
module asm "  popq %rcx"
module asm "  retq"

define dso_local void @mk_start() #0 {
entry:
  %buf = alloca [8192 x i8], align 16
  %written = alloca i32, align 4
  %blen = load volatile i64, ptr @buf_len
  call void @llvm.memset.p0.i64(ptr align 16 %buf, i8 0, i64 %blen, i1 false)
  %mlen = load volatile i64, ptr @msg_len
  call void @llvm.memcpy.p0.p0.i64(ptr align 16 %buf, ptr align 1 @msg, i64 %mlen, i1 false)
  %h = call ptr @GetStdHandle(i32 -11)
  %n = trunc i64 %mlen to i32
  %ok = call i32 @WriteFile(ptr %h, ptr %buf, i32 %n, ptr %written, ptr null)
  %w = load i32, ptr %written
  %good = icmp eq i32 %w, 14
  %code = select i1 %good, i32 7, i32 1
  call void @ExitProcess(i32 %code)
  unreachable
}

define dso_local ptr @memcpy(ptr %d, ptr %s, i64 %n) #1 {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %i1, %body ]
  %done = icmp eq i64 %i, %n
  br i1 %done, label %exit, label %body
body:
  %sp = getelementptr i8, ptr %s, i64 %i
  %b = load i8, ptr %sp
  %dp = getelementptr i8, ptr %d, i64 %i
  store i8 %b, ptr %dp
  %i1 = add i64 %i, 1
  br label %loop
exit:
  ret ptr %d
}

define dso_local ptr @memset(ptr %d, i32 %c, i64 %n) #1 {
entry:
  %v = trunc i32 %c to i8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %i1, %body ]
  %done = icmp eq i64 %i, %n
  br i1 %done, label %exit, label %body
body:
  %dp = getelementptr i8, ptr %d, i64 %i
  store i8 %v, ptr %dp
  %i1 = add i64 %i, 1
  br label %loop
exit:
  ret ptr %d
}

attributes #0 = { nounwind noreturn }
attributes #1 = { nounwind "no-builtins" }
