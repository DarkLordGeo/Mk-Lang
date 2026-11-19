// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: the Syscall intrinsic, bodiless and private to std.sys as on Linux.
//
// How: codegen implements it as on x86_64-linux (`syscall`, rax and rdi rsi
// rdx r10 r8 r9) and eval refuses it. This placeholder never calls it;
// Nutshell's backend does. There is no Clone and no Restorer: the target's
// support pack has no mk_clone or mk_sigreturn.

// Syscall is `syscall` on x86_64 and `svc #0` on aarch64; the result is the
// raw rax (x0).
Syscall(n: uint, a0: uint, a1: uint, a2: uint, a3: uint, a4: uint, a5: uint): int
