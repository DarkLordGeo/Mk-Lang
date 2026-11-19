; Smoke program for x86_64-linux (TestSmokeLinuxStatic). No libc: _start in
; module assembly hands the initial stack pointer to mk_start, which reads
; argc from it; write and exit_group are raw system calls through inline
; assembly; its own memcpy and memset. The test prepends the probed target
; datalayout and triple. It prints "hello from mk\n" and exits 6 + argc, so
; 7 with no arguments. The 8 KiB frame is probed inline
; ("probe-stack"="inline-asm").

source_filename = "smoke_linux.mk"

@msg = private unnamed_addr constant [14 x i8] c"hello from mk\0A"
@msg_len = internal global i64 14
@buf_len = internal global i64 8192

declare void @llvm.memcpy.p0.p0.i64(ptr, ptr, i64, i1)
declare void @llvm.memset.p0.i64(ptr, i8, i64, i1)

module asm ".text"
module asm ".globl _start"
module asm ".type _start,@function"
module asm "_start:"
module asm "  xorl %ebp, %ebp"
module asm "  movq %rsp, %rdi"
module asm "  andq $-16, %rsp"
module asm "  callq mk_start"
module asm "  ud2"

define dso_local void @mk_start(ptr %sp) #0 {
entry:
  %buf = alloca [8192 x i8], align 16
  %argc = load i64, ptr %sp
  %blen = load volatile i64, ptr @buf_len
  call void @llvm.memset.p0.i64(ptr align 16 %buf, i8 0, i64 %blen, i1 false)
  %mlen = load volatile i64, ptr @msg_len
  call void @llvm.memcpy.p0.p0.i64(ptr align 16 %buf, ptr align 1 @msg, i64 %mlen, i1 false)
  %w = call i64 asm sideeffect "syscall", "={rax},{rax},{rdi},{rsi},{rdx},~{rcx},~{r11},~{memory}"(i64 1, i64 1, ptr %buf, i64 %mlen)
  %good = icmp eq i64 %w, 14
  %base = select i1 %good, i64 6, i64 0
  %code = add i64 %base, %argc
  call void asm sideeffect "syscall", "{rax},{rdi},~{rcx},~{r11},~{memory}"(i64 231, i64 %code)
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

attributes #0 = { nounwind noreturn "probe-stack"="inline-asm" }
attributes #1 = { nounwind "no-builtins" "probe-stack"="inline-asm" }
