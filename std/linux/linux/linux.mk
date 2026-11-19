// Why: some Linux programs need a system call std has no wrapper for, and
// std.linux gives them the raw call without C.
//
// What: Syscall, the raw x86_64 Linux system call with six arguments.
//
// How: x86_64-linux only: std/pkg.mk's targets select this directory as
// std.linux for os "linux" (std/linux/other, an empty placeholder,
// elsewhere), and an import on any other target is E0202 at the import
// (internal/driver's stdTargetOnly), so a package that also builds for
// Windows names it only in a module its own targets select for linux.
// Unsafe only (E1101): what each argument
// means is the call's.

import std.sys

/// Makes the Linux system call nr with the arguments a0 to a5, through the
/// x86_64 `syscall` instruction; pass 0 for arguments the call does not
/// use. r is the kernel's result as it came back. When that result, read
/// as a signed number, is from -4095 to -1, the call failed and errno is
/// its negation (9 for EBADF); otherwise errno is 0. Needs unsafe
/// (E1101): what each argument means is the call's.
///
/// It is one system call and nothing more: no retry on EINTR, and no task
/// suspension, so a call that blocks blocks the carrier thread under it.
/// Run such a call through runtime.Blocking, which takes it off the
/// carriers. O(1) besides the call; allocates nothing.
export Syscall(nr: uint, a0: uint, a1: uint, a2: uint, a3: uint, a4: uint, a5: uint): (r: uint, errno: int) {
    v := sys.RawSyscall(nr, a0, a1, a2, a3, a4, a5)
    e: int = if v < 0 && v >= -4095 { -v } else { 0 }
    return (r: Trunc[uint](v), errno: e)
}
