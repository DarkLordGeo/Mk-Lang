// Why: the task switch, task start, system-stack call and Linux entry
// points cannot be written in Mk or LLVM IR; each target needs them in its
// own assembly and calling convention.
//
// What: the aarch64 (AAPCS64) support routines as module assembly:
// arm64Switch (mk_switch), arm64TaskStart (mk_task_start), arm64CallSystem
// (mk_call_system), and on Linux arm64ELFStart (_start), arm64Clone
// (mk_clone) and arm64Sigreturn (mk_sigreturn).
//
// How: each is a Go string of assembly that support.go puts in the support
// pack. aarch64 is assembled and inspected, not run.

package codegen

// arm64ELFStart passes the initial stack pointer to mk_start.
const arm64ELFStart = `
mov x29, #0
mov x0, sp
and sp, x0, #-16
bl mk_start
brk #1
`

// arm64Switch is mk_switch(from: x0, to: x1): x19 to x30, d8 to d15
// and FPCR in a 176-byte frame; from[0] receives sp, to[0] gives the other's.
const arm64Switch = `
sub sp, sp, #176
stp x19, x20, [sp, #0]
stp x21, x22, [sp, #16]
stp x23, x24, [sp, #32]
stp x25, x26, [sp, #48]
stp x27, x28, [sp, #64]
stp x29, x30, [sp, #80]
stp d8, d9, [sp, #96]
stp d10, d11, [sp, #112]
stp d12, d13, [sp, #128]
stp d14, d15, [sp, #144]
mrs x9, fpcr
str x9, [sp, #160]
mov x9, sp
str x9, [x0]
ldr x9, [x1]
mov sp, x9
ldr x9, [sp, #160]
msr fpcr, x9
ldp d14, d15, [sp, #144]
ldp d12, d13, [sp, #128]
ldp d10, d11, [sp, #112]
ldp d8, d9, [sp, #96]
ldp x29, x30, [sp, #80]
ldp x27, x28, [sp, #64]
ldp x25, x26, [sp, #48]
ldp x23, x24, [sp, #32]
ldp x21, x22, [sp, #16]
ldp x19, x20, [sp, #0]
add sp, sp, #176
ret
`

// arm64TaskStart calls entry (x19) with its argument (x20); never returns.
const arm64TaskStart = `
mov x0, x20
blr x19
brk #1
`

// arm64CallSystem is mk_call_system(fn: x0, args: x1, ss: x2).
const arm64CallSystem = `
stp x19, x30, [sp, #-16]!
mov x19, sp
mov x9, x0
mov x0, x1
ldr x10, [x2]
mov sp, x10
blr x9
mov sp, x19
ldp x19, x30, [sp], #16
ret
`

// arm64Clone is mk_clone(flags: x0, stack: x1, ptid: x2, ctid: x3, tls: x4,
// fn: x5, arg: x6). arm64's clone (220) takes tls in x3 and ctid in
// x4 (CONFIG_CLONE_BACKWARDS), so x3 and x4 are swapped through x9 before
// svc. The child pops fn and arg, clears the frame and link registers,
// calls fn(arg) and exits the thread (93).
const arm64Clone = `
sub x1, x1, #16
stp x5, x6, [x1]
mov x9, x3
mov x3, x4
mov x4, x9
mov x8, #220
svc #0
cbnz x0, 1f
ldp x9, x0, [sp], #16
mov x29, #0
mov x30, #0
blr x9
mov x0, #0
mov x8, #93
svc #0
brk #1
1:
ret
`

// arm64Sigreturn is the SA_RESTORER of rt_sigaction: rt_sigreturn (139).
const arm64Sigreturn = `
mov x8, #139
svc #0
brk #1
`
