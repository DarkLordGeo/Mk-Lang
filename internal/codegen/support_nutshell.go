// Why: the x86_64-nutshell target (the Nutshell operating system) shares
// x86_64 Linux's calling convention but starts its threads its own way.
//
// What: nutshellELFStart, its _start. The System V task switch, task start
// and system-stack call are x86_64 Linux's (support_x64linux.go).
//
// How: there is no mk_clone and no mk_sigreturn: Nutshell starts threads
// and reports faults through its own system calls, which std.sys makes.

package codegen

// nutshellELFStart is _start of x86_64-nutshell. A Nutshell thread enters
// as a C function would be called: rdi and
// rsi hold the two start words (the start record's address and length),
// rsp is 8 mod 16 above a zero return
// slot, and every other register is zero. It pushes the two words, so the
// stack pointer it passes to mk_start points at them, as Linux's points at
// argc: std.task.Start hands that pointer to std.sys, whose Nutshell variant
// reads the start words there. Its unwind rule ends a walk here (rip
// undefined, as every outermost frame's). Then it clears the frame pointer, aligns the
// stack to 16 and calls mk_start, which never returns.
const nutshellELFStart = `
.cfi_undefined %rip
pushq %rsi
pushq %rdi
xorl %ebp, %ebp
movq %rsp, %rdi
andq $-16, %rsp
callq mk_start
ud2
`
