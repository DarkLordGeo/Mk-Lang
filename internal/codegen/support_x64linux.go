// Why: the task switch, task start, thread creation, signal return and
// process entry need exact register and stack control, so on x86_64 Linux
// they are written in System V assembly.
//
// What: sysvELFStart (_start), sysvSwitch (mk_switch), sysvTaskStart
// (mk_task_start), sysvClone (mk_clone), sysvSigreturn (mk_sigreturn) and
// sysvCallSystem (mk_call_system), as module assembly; and the system-libc
// entry (LibcEntryAsm, LibcSlotNames, LibcTableSymbol, LibcEntry), which
// the driver assembles for a bin built to link the system libc.
//
// How: each routine is a Go string that support.go puts in the support
// pack with its unwind directives (asmFunc).

package codegen

import (
	"strconv"
	"strings"

	"mk/internal/types"
)

// sysvELFStart passes the initial stack pointer (argc, argv, envp) to
// mk_start with the frame pointer cleared and the stack 16-aligned.
const sysvELFStart = `
.cfi_undefined %rip
xorl %ebp, %ebp
movq %rsp, %rdi
andq $-16, %rsp
callq mk_start
ud2
`

// sysvSwitch is mk_switch(from: rdi, to: rsi): rbp, rbx, r12 to r15,
// MXCSR and the x87 control word on the stack (64 bytes with the return
// address); from[0] receives the stack pointer, to[0] gives the other's.
const sysvSwitch = `
pushq %rbp
.cfi_def_cfa_offset 16
.cfi_offset %rbp, -16
pushq %rbx
.cfi_def_cfa_offset 24
.cfi_offset %rbx, -24
pushq %r12
.cfi_def_cfa_offset 32
.cfi_offset %r12, -32
pushq %r13
.cfi_def_cfa_offset 40
.cfi_offset %r13, -40
pushq %r14
.cfi_def_cfa_offset 48
.cfi_offset %r14, -48
pushq %r15
.cfi_def_cfa_offset 56
.cfi_offset %r15, -56
subq $8, %rsp
.cfi_def_cfa_offset 64
stmxcsr (%rsp)
fnstcw 4(%rsp)
movq %rsp, (%rdi)
movq (%rsi), %rsp
ldmxcsr (%rsp)
fldcw 4(%rsp)
addq $8, %rsp
.cfi_def_cfa_offset 56
popq %r15
.cfi_def_cfa_offset 48
popq %r14
.cfi_def_cfa_offset 40
popq %r13
.cfi_def_cfa_offset 32
popq %r12
.cfi_def_cfa_offset 24
popq %rbx
.cfi_def_cfa_offset 16
popq %rbp
.cfi_def_cfa_offset 8
retq
`

// sysvTaskStart calls entry (r12) with its argument (r13); never returns.
const sysvTaskStart = `
.cfi_undefined %rip
movq %r13, %rdi
callq *%r12
ud2
`

// sysvClone is mk_clone(flags: rdi, stack: rsi, ptid: rdx, ctid: rcx,
// tls: r8, fn: r9, arg: 8(%rsp)): it stores fn and arg at the top of
// the child stack and issues clone (56) with the kernel's x86_64 order, rdi
// flags, rsi stack, rdx ptid, r10 ctid, r8 tls. The parent returns the
// result; the child pops fn and arg, clears the frame pointer, calls
// fn(arg) and exits the thread (60) with status 0.
const sysvClone = `
movq 8(%rsp), %rax
subq $16, %rsi
movq %r9, (%rsi)
movq %rax, 8(%rsi)
movq %rcx, %r10
movl $56, %eax
syscall
testq %rax, %rax
jnz 1f
.cfi_remember_state
.cfi_undefined %rip
xorl %ebp, %ebp
popq %rax
popq %rdi
callq *%rax
xorl %edi, %edi
movl $60, %eax
syscall
ud2
1:
.cfi_restore_state
retq
`

// sysvSigreturn is the SA_RESTORER of rt_sigaction: rt_sigreturn (15).
const sysvSigreturn = `
.cfi_undefined %rip
movl $15, %eax
syscall
ud2
`

// sysvCallSystem is mk_call_system(fn: rdi, args: rsi, ss: rdx):
// fn(args) on the system stack ss.sp, then back.
const sysvCallSystem = `
pushq %rbx
.cfi_def_cfa_offset 16
.cfi_offset %rbx, -16
movq %rsp, %rbx
.cfi_def_cfa_register %rbx
movq (%rdx), %rsp
movq %rdi, %rax
movq %rsi, %rdi
callq *%rax
movq %rbx, %rsp
.cfi_def_cfa_register %rsp
popq %rbx
.cfi_def_cfa_offset 8
retq
`

// LibcSlotNames are the libc functions the system-libc entry stores in
// std's libc table (std/sys/linux/libc.mk), slot i at 8 * i
// bytes from LibcTableSymbol: dlopen to dlerror for std.dl, the pthread
// calls for the carriers (std/sys/linux/pthread.mk), and
// __errno_location.
var LibcSlotNames = []string{"dlopen", "dlsym", "dlclose", "dlerror", "pthread_create", "pthread_attr_init",
	"pthread_attr_setstack", "pthread_attr_setdetachstate", "pthread_attr_destroy", "__errno_location"}

// LibcTableSymbol is std.sys's Libc, the table the entry fills.
const LibcTableSymbol = "mk.std.sys.Libc"

// LibcEntry is the system-libc entry's symbol, the ELF entry of a bin in
// that mode.
const LibcEntry = "mk_libc_start"

// LibcEntryAsm is the system-libc entry,
// one Linux assembly file for arch (x86_64 here, AArch64 in
// arm64LibcEntryAsm) the driver assembles into a bin in that mode, beside the support pack's static _start, which then stays unused.
// mk_libc_start is glibc's own start sequence: rdx is ld.so's rtld_fini,
// the stack holds argc, argv, envp and auxv; it calls __libc_start_main
// (GLIBC_2.34: init and fini are null, the library runs the executable's
// constructors itself) with mk_libc_main as main. mk_libc_main fills std's
// libc table through the GOT (each address the loader bound, -z now), then
// calls mk_start with argv - 8, the original stack pointer, where argc
// still lies, so std reads argc, argv, envp and auxv as on the static
// entry. mk_start never returns. The file also holds a weak, zero copy of
// the table, so a program that reaches no std code reading it still links.
func LibcEntryAsm(arch types.Arch) string {
	if arch == types.ArchArm64 {
		return arm64LibcEntryAsm()
	}
	var b strings.Builder
	b.WriteString("\t.text\n\t.globl " + LibcEntry + "\n\t.type " + LibcEntry + ",@function\n\t.p2align 4\n" + LibcEntry + ":\n")
	for _, l := range strings.Split(strings.Trim(libcStart, "\n"), "\n") {
		b.WriteString("\t" + l + "\n")
	}
	b.WriteString("\t.size " + LibcEntry + ", .-" + LibcEntry + "\n")
	b.WriteString("\t.globl mk_libc_main\n\t.type mk_libc_main,@function\n\t.p2align 4\nmk_libc_main:\n")
	b.WriteString("\tsubq $8, %rsp\n\tleaq \"" + LibcTableSymbol + "\"(%rip), %rax\n")
	for i, n := range LibcSlotNames {
		b.WriteString("\tmovq " + n + "@GOTPCREL(%rip), %rcx\n")
		b.WriteString("\tmovq %rcx, " + strconv.Itoa(8*i) + "(%rax)\n")
	}
	b.WriteString("\tleaq -8(%rsi), %rdi\n\tcallq " + symStart + "\n\tud2\n")
	b.WriteString("\t.size mk_libc_main, .-mk_libc_main\n")
	libcWeakTable(&b)
	return b.String()
}

// libcWeakTable writes the entry file's tail: a weak table for a program
// that never reads std's (no thread, no std.dl: mono does not instantiate
// std's Libc then), which std's own definition replaces whenever it
// exists, and the non-executable stack note.
func libcWeakTable(b *strings.Builder) {
	n := strconv.Itoa(8 * len(LibcSlotNames))
	b.WriteString("\t.bss\n\t.weak \"" + LibcTableSymbol + "\"\n\t.p2align 3\n\"" + LibcTableSymbol + "\":\n\t.zero " + n + "\n")
	b.WriteString("\t.size \"" + LibcTableSymbol + "\", " + n + "\n")
	b.WriteString("\t.section .note.GNU-stack,\"\",@progbits\n")
}

// libcStart is mk_libc_start's body: glibc's x86_64 start.S, with
// mk_libc_main as main.
const libcStart = `xorl %ebp, %ebp
movq %rdx, %r9
popq %rsi
movq %rsp, %rdx
andq $-16, %rsp
pushq %rax
pushq %rsp
xorl %r8d, %r8d
xorl %ecx, %ecx
leaq mk_libc_main(%rip), %rdi
callq *__libc_start_main@GOTPCREL(%rip)
hlt`
