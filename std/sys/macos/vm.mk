// Why: std.vm (JIT code, guarded memory) needs protection changes and
// executable memory that is never writable and executable at once, and
// std.linux's raw system call must answer on every target that names it.
//
// What: VmProtect, VmCode with VmCodeNew, VmCodeCommit and VmCodeFree,
// VmFlush and RawSyscall.
//
// How: VmProtect is mprotect with Darwin's protections. A code region is
// not made: std.vm's contract is two views of one memory, one read-write
// and one read-execute, live at once on every thread. arm64 macOS runs
// generated code only from a MAP_JIT mapping, which is one view whose
// writability is switched per thread (pthread_jit_write_protect_np), not
// two views, and a second mapping of a MAP_JIT region is not a documented
// way to get the other view; x86_64 macOS has no such
// limit, but its fault path has no code-region filter (filter.mk), so a
// fault in generated code could not reach std.vm's filters. VmCodeNew
// therefore fails with ENOSYS (78) on both. macOS has no stable system-call
// interface (libSystem is the only one), so RawSyscall refuses. Each call
// returns the errno (positive) or 0, so the caller can make vm.Error.Os(code).

import std.raw

const VmProtRead: i32 = 1
// VmEnosys is Darwin's ENOSYS.
const VmEnosys: i64 = 78

/// VmProtect sets the protection of [p, p + size), page-aligned and inside
/// a reservation the caller owns: prot 0 none, 1 read, 2 read-write (never
/// executable: std.vm's Prot has no such value). 0, or the errno.
/// O(1): one system call; allocates nothing.
export VmProtect(p: *u8, size: int, prot: u32): i64 {
    pr: i32 = if prot == 2 { ProtRW } else if prot == 1 { VmProtRead } else { ProtNone }
    unsafe "mprotect of [p, p + size), which lies in a reservation the caller owns" {
        return if mprotect(p, Trunc[uint](size), pr) == 0 { 0 } else { Errno() }
    }
}

/// VmCode is a code region's two views of one memory: w read-write, x
/// read-execute, each size bytes; err the errno when it could not be made
/// (then both are null).
export struct VmCode {
    export w: *u8
    export x: *u8
    export err: i64
}

/// VmCodeNew makes no code region on macOS: err is ENOSYS (78) and both
/// views are null, for every size and commit (the header says why).
/// O(1); allocates nothing.
export VmCodeNew(size: int, commit: bool): VmCode {
    return VmCode(w: raw.Null[u8](), x: raw.Null[u8](), err: VmEnosys)
}

/// VmCodeCommit has no region to commit on macOS (VmCodeNew makes none):
/// ENOSYS (78). O(1); allocates nothing.
export VmCodeCommit(c: &VmCode, offset: int, size: int, prot: u32): i64 {
    return VmEnosys
}

/// VmCodeFree has nothing to unmap on macOS (VmCodeNew makes no region).
/// O(1); allocates nothing.
export VmCodeFree(c: &VmCode, size: int) {
}

/// VmFlush makes code written at [x, x + n) visible to execution there. On
/// arm64 it is libSystem's sys_icache_invalidate (<libkern/OSCacheControl.h>),
/// which cleans the data cache and invalidates the instruction cache over
/// the range; on x86_64 the instruction cache is coherent with stores
/// (Intel SDM vol. 3, 11.6), so a fence suffices.
/// O(n / line); allocates nothing.
export VmFlush(x: *u8, n: int) {
    if Target.arch == Arch.Arm64 {
        if n <= 0 {
            return
        }
        unsafe "cache maintenance over [x, x + n), which the caller's region holds; it writes no memory" {
            sys_icache_invalidate(x, Trunc[uint](n))
        }
    } else {
        raw.Fence()
    }
}

/// RawSyscall makes no system call on macOS, whose system-call numbers are
/// private to libSystem and change between releases: it returns -78
/// (-ENOSYS) for every nr. std.linux, its only caller, builds for Linux
/// targets alone. O(1); allocates nothing.
export RawSyscall(nr: uint, a0: uint, a1: uint, a2: uint, a3: uint, a4: uint, a5: uint): int {
    return -78
}
