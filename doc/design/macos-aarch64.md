# macOS and AArch64 targets

This note says how mk builds for `aarch64-linux`, `aarch64-macos` and `x86_64-macos`, why `aarch64-windows` is not a target, and in what order the work is done. It describes the design; [reference.md](../reference.md#targets) states what each target does today.

## What AArch64 started from

Much of AArch64 existed in the compiler unexercised:

- `internal/abi` classifies C values under AAPCS64 (`aapcs64.go`) with Apple's differences (`darwin.go`), tested against clang's choices.
- `internal/codegen` emits AArch64 IR: the AAPCS64 signatures, the crypto and carry intrinsics, and the support routines `mk_switch`, `mk_task_start`, `mk_call_system`, `_start`, `mk_clone` and `mk_sigreturn` in AArch64 assembly (`support_aarch64.go`).
- `std/sys/linux` carries both system-call tables (`nr.mk`) and the AArch64 layouts it reads (`stat`, `epoll_event`, `ucontext`).
- Inline `asm` had no aarch64 register table, so any live `asm` on aarch64 was E1104. The table (`internal/mono/asmregs_aarch64.go`) and the lowering (no Intel dialect, `cc` for the flags, AAPCS64's caller-saved set for `clobber_abi("C")`) are part of this work.

## 1. The ABI

Mk's own functions use LLVM's C calling convention, so on AArch64 they follow AAPCS64 as clang lowers it. The C boundary (`extern`, `export extern`, `fn` values passed to C) goes through `internal/abi`:

| Rule | AAPCS64 (Linux, Windows) | Apple arm64 |
|---|---|---|
| Homogeneous float aggregate of up to 4 members | float registers v0 to v7 | the same |
| Other composite of up to 16 bytes | x0 to x7, as one or two `i64` (an `[2 x i64]` array) | the same |
| Larger composite | by reference to a caller-made copy | the same |
| Result over 16 bytes | through memory addressed by x8 | the same |
| Integer argument under 32 bits | callee extends | caller extends (`signext`/`zeroext` on the argument) |
| Stack argument alignment | 8, an HFA 8 or 16 | natural alignment, packed |
| Variadic arguments | in registers as fixed ones | every variadic argument on the stack |

The last row matters to std: `open`, `openat`, `fcntl` and `ioctl` are variadic in libSystem. std declares them `extern ... , ...)`, so the call is emitted variadic and LLVM puts the mode or argument on the stack. Declaring them with fixed parameters would link and pass garbage.

`x86_64-macos` is System V, the same classification as `x86_64-linux`.

## 2. Code generation

The target triples are `aarch64-unknown-linux-gnu`, `arm64-apple-macosx11.0.0` and `x86_64-apple-macosx11.0.0`. Mach-O differs from ELF in the support pack only:

- Assembly symbols take a leading `_` (`asmName`), and routines carry no `.type` or `.size`.
- The entry is `main(argc, argv, envp, apple)`, called by dyld through `LC_MAIN`. It stores its four arguments in a block on the main thread's stack and calls `std.task.Start` with the block's address as `sp`. `main` never returns, so the block lives as long as the process; `std/sys/macos` reads argv and envp from it, where Linux reads them from the kernel's initial stack.
- There is no `_start`, `mk_clone` or `mk_sigreturn` on macOS: threads come from `pthread_create`, and the kernel's signal trampoline is libSystem's.
- `x86_64-macos` uses the System V routines (`mk_switch`, `mk_task_start`, `mk_call_system`) with Mach-O directives. Apple forbids nothing they do.

No target but Windows needs a stack probe routine: every function off Windows carries `probe-stack=inline-asm` with a 4 KiB probe interval, so LLVM touches each 4 KiB of a large frame in order, which reaches the guard page on 4 KiB and 16 KiB pages alike.

## 3. Linking

| Target | Linker | Command shape |
|---|---|---|
| `aarch64-linux` | `ld.lld` | the static `x86_64-linux` link: `-static -nostdlib -e _start`; ld.lld takes the machine from the objects |
| `aarch64-macos`, `x86_64-macos` | `ld64.lld` | `-arch arm64` (or `x86_64`) `-platform_version macos 11.0 11.0 -e _main`, the objects, and `libSystem.tbd` |

macOS has no static linking: every program loads `/usr/lib/libSystem.B.dylib`, and system calls go through it, never through `svc` or `syscall` (Apple changes the numbers between releases). There is no SDK on the build machine and none is needed: the driver writes a text-based stub, `libSystem.tbd` (TAPI version 4), from std's name list `std/sys/macos/libSystem.def`, one exported symbol per line, the way it writes Windows import libraries from `kernel32.def`. ld64.lld links against the stub exactly as against Apple's, and the program binds to the real libSystem at load. No C runtime of Mk's own is linked: `crt1.o` is not needed with `LC_MAIN`.

ld64.lld signs arm64 executables ad hoc, which arm64 macOS requires of every executable. The output is deterministic: ld64.lld derives `LC_UUID` from the content.

System-libc mode on Linux, libraries (`pkg.Lib`) and dynamic links stay x86_64-only for now: the mode's loader path and its `.def` version tags are x86_64's.

## 4. The macOS system layer

`std/sys/macos` exports what `std/sys/linux` exports, with the same signatures, as every system layer does; `std/pkg.mk` selects it for os `macos` and lists it in `unsafe_modules`. Every call goes to libSystem through an `extern` in one file, `libsystem.mk`, each name listed once in `libSystem.def`. Constants and structure layouts come from Go's `syscall` and `runtime` tables for darwin (`zerrors_darwin_*.go`, `ztypes_darwin_*.go`, `defs_darwin_*.go`), which are generated from Apple's headers; this machine has no headers to check them against.

| Area | libSystem calls |
|---|---|
| errors | `__error()` gives the calling thread's errno |
| arguments, environment | the entry block; `_NSGetExecutablePath` for the executable's path |
| memory | `mmap`, `mprotect`, `munmap`, `madvise` |
| files, directories | `open`, `read`, `write`, `pread`, `pwrite`, `close`, `fstat`, `stat`, `lstat`, `fsync`, `ftruncate`, `lseek`, `mkdir`, `rmdir`, `unlink`, `rename`, `readlink`, `symlink`, `opendir`, `readdir`, `closedir`, `getcwd`, `chdir` |
| threads | `pthread_create` on a stack std maps, detached |
| sync | `__ulock_wait` and `__ulock_wake` for the futex-style sleep and wake, which libc++ and Go's runtime use the same way |
| time | `clock_gettime` (`CLOCK_REALTIME`, `CLOCK_UPTIME_RAW`), `nanosleep` |
| processes | `posix_spawn`, `waitpid`, `kill`, `pipe`, `getpid` |
| signals, faults | `sigaction` with `SA_SIGINFO | SA_ONSTACK`, `sigaltstack`; the fault's pc and sp come from `uc_mcontext` |
| net, readiness | `socket`, `connect`, `bind`, `listen`, `accept`, `setsockopt`, `kqueue`, `kevent`, `getaddrinfo` |
| random | `getentropy` |

The page size is 16 KiB on arm64 macOS, where it is 4 KiB everywhere else mk builds. `mprotect` and `munmap` refuse a range that is not page-aligned, so std's guard and commit granularity is the target's page, a constant of the build, not a literal 4096.

Deep libSystem calls (`getaddrinfo`, `posix_spawn`) run on the carrier's system stack, as Windows calls do, so a task's small stack does not overflow inside the library.

## 5. Order of the work

1. **aarch64-linux, built.** Lift `not-built` for it, refuse system-libc mode and libraries for it, and run the program tests under qemu-user (Docker's `linux/arm64` emulation). It is the only new target this machine can run.
2. **Targets.** Add `x86_64-macos`. `aarch64-windows` is left out: `std/sys/windows` reads the TEB through x86-64's `gs` and calls x86-64 stack probes, the link names `/machine:x64`, and the import libraries are made for x64; each needs its arm64 form, and nothing here could run the result.
3. **Mach-O.** `toolchain.MachO`, ld64.lld discovery, the `.tbd` writer, the `main` entry in the support pack.
4. **`std/sys/macos`.** First the module with every export (`mk check` for std and the corpus passes on both macOS targets), then the real calls area by area, in the order of the table above.
5. **Documentation.** The targets table in the reference, with what was run, what was only compiled, and what is left.

## What this machine cannot check

There is no Mac and no ARM hardware here. `aarch64-linux` programs run under qemu-user, which emulates the instruction set and passes system calls to the x86_64 kernel; it does not check memory ordering on real ARM cores, and its signal delivery is qemu's. The macOS targets are compiled, linked and inspected (`llvm-objdump --macho`: load commands, the libSystem import, the build version, the code signature on arm64) but never run, and the macOS layer's constants are checked against Go's tables only.
