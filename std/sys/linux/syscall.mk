// Why: std.sys on Linux needs a few operations Mk cannot express: the
// system-call instruction, a thread clone, the signal restorer and the
// build's libc mode.
//
// What: the intrinsics Syscall, Clone, Restorer and LibcBuild, bodiless and
// private to std.sys.
//
// How: codegen implements each call directly; eval refuses them, so no
// compile-time result depends on one.

// Syscall is `syscall` on x86_64 and `svc #0` on aarch64; the result is the
// raw return, -4095..-1 being -errno.
Syscall(n: uint, a0: uint, a1: uint, a2: uint, a3: uint, a4: uint, a5: uint): int

// Clone calls mk_clone: clone with the child on stack, calling entry(arg)
// with rbp zero, then exit (the thread only).
Clone(flags: uint, stack: *u8, ptid: *u32, ctid: *u32, tls: uint, entry: fn(*u8), arg: *u8): int

// Restorer is the address of mk_sigreturn, every handler's sa_restorer.
Restorer(): *u8

// LibcBuild is true when this bin is built in system-libc mode (its
// package graph holds a dynamic link for x86_64-linux),
// a constant of the build. mono folds every call, so the branch the mode
// cannot take is not compiled; codegen never sees a call. It is not a
// Target field and eval refuses it, so no acceptance or constant depends
// on it. The process's own answer is
// the same: the libc-mode entry fills Libc before mk_start (libc.mk).
LibcBuild(): bool
