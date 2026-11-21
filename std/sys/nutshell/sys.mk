// Why: Mk carries os "nutshell" (x86_64-nutshell and aarch64-nutshell) as a
// placeholder target, so std.rt, std.task and every module above them check
// and build for it before the Nutshell backend exists.
//
// What: std.sys's exported types (OsResult, ArgsInfo, Sem, ConsoleCarry,
// SockAddr, Op, FaultInfo), the Op kinds, NewOp, PageSize, Exit, Pid,
// ThreadId, CpuCount, Unsupported, NewSem and NewCarry.
//
// How: this directory exports exactly what std/sys/linux exports, with the
// same signatures, and its struct declarations are byte for byte those of
// std/sys/linux/sys.mk. It makes no system call. Every operation that
// returns an OsResult fails with SysErr.Other and os 0 (Unsupported), the
// nearest that interface has to "not supported"; the others return the value
// their result type uses for failure or absence (false, null, 0, -1, an
// empty ArgsInfo), and Exit traps. Nutshell's real backend, developed in the
// Nutshell repository until its system-call ABI freezes, replaces this
// directory through MK_STD.

import std.core
import std.raw

/// A call that can fail: err is SysErr.None and value the result, or err the
/// mapped error and os the OS's own code.
export struct OsResult {
    /// The call's result when err is None (a count, a handle, a size).
    export value: i64
    /// None, or the mapped error.
    export err: core.SysErr
    /// The OS's own error code when err is not None, else 0.
    export os: i64
}

/// What ReadArgs and ReadEnv needed; the caller retries with larger buffers.
export struct ArgsInfo {
    /// How many strings there are.
    export count: int
    /// How many bytes of UTF-8 they take.
    export bytes: int
}

/// A counting semaphore for carriers and pool threads; SemInit first.
export struct Sem {
    word: raw.Atomic[u32]
    sleepers: raw.Atomic[u32]
    h: uint
}

/// An incomplete UTF-8 sequence kept between console writes.
export struct ConsoleCarry {
    bytes: [4]u8
    n: u32
}

/// A socket address on every OS: family (Inet4 or Inet6), the port in host
/// order, the address bytes (IPv4 in addr[0..4]), and the IPv6 scope id.
export struct SockAddr {
    /// Inet4 or Inet6.
    export family: u16
    /// The port, in host order.
    export port: u16
    /// The address bytes; IPv4 in addr[0..4].
    export addr: [16]u8
    /// The IPv6 scope id; 0 for IPv4.
    export scope_id: u32
}

/// One poller operation, in the waiting task's frame; never moves while
/// pending. state and waiter are std.task's park handshake and
/// std.sys never touches them; os is std.sys's own area. The one-owner rule:
/// from a PollSubmit that returns true until the PollWait that returns the
/// op, std.sys and the OS own the op and buf[0..len]; the task touches only
/// cancel and may call PollCancel.
export struct Op {
    /// One of the Op kind constants.
    export kind: u32
    /// The handle the operation is on.
    export h: uint
    /// The buffer a read fills or a write sends, owned by std.sys and the OS
    /// while the op is pending.
    export buf: *u8
    /// buf's length in bytes.
    export len: int
    /// The file offset of a read or write; a stream ignores it, and an
    /// attached disk file refuses a negative one (the caller keeps the
    /// position).
    export pos: i64
    /// The address a dial or a datagram send goes to, or a receive fills.
    export addr: SockAddr
    /// The result, written by std.sys before the op completes.
    export res: OsResult
    /// The waiting task, std.task's; std.sys never touches it.
    export waiter: *u8
    /// std.task's park handshake word; std.sys never touches it.
    export state: raw.Atomic[u32]
    /// 1 once the waiter asked for a cancel; the only field the waiter
    /// writes while the op is pending.
    export cancel: raw.Atomic[u32]
    /// The path of an operation that takes one (open, stat, mkdir, ...).
    export path: str
    /// The second path of a rename.
    export path2: str
    os: [160]u8
}

/// A fault the OS reported, as std.rt's report reads it.
export struct FaultInfo {
    /// 1 stack overflow, 2 access violation, 3 other, 4 a fault in a
    /// std.vm code region that no fault filter resumed (std.rt reports
    /// it as such, unless its address is a guard page: a stack overflow).
    export kind: u32
    /// The faulting data address, when the OS reports one.
    export addr: uint
    /// The faulting instruction's address.
    export pc: uint
    /// The stack pointer at the fault.
    export sp: uint
}

/// Op kind: read at most len bytes from h into buf (the numbers of every variant).
export const OpRead: u32 = 1
/// Op kind: write len bytes of buf to h.
export const OpWrite: u32 = 2
/// Op kind: accept one connection on the listener h.
export const OpAccept: u32 = 3
/// Op kind: connect the socket h to addr.
export const OpConnect: u32 = 4
/// Op kind: receive at most len bytes into buf from the socket h.
export const OpRecv: u32 = 5
/// Op kind: send len bytes of buf on the socket h.
export const OpSend: u32 = 6
/// Op kind: open the file at path.
export const OpOpen: u32 = 7
/// Op kind: rename path to path2.
export const OpRename: u32 = 8
/// Op kind: remove the file at path.
export const OpRemove: u32 = 9
/// Op kind: flush h's data to its device.
export const OpSync: u32 = 10
/// Op kind: reserved, never submitted (its number stays taken).
export const OpTimeoutWake: u32 = 11
/// Op kind: send one datagram of buf[0..len] on the socket h to addr.
export const OpSendTo: u32 = 12
/// Op kind: receive one datagram into buf; addr is filled with its source.
export const OpRecvFrom: u32 = 13
/// OpProcWait waits for a child process to end: h the process
/// (Linux the pidfd, Windows the process handle); the result is its exit
/// code (Linux 0 to 255, or minus the signal that ended it). Every backend
/// does it on the poller (Linux a readability wait on the pidfd, then
/// waitid; Windows a registered wait posting to the port), so a waiting
/// child holds no thread. The task calls PollRelease(op) once Await is over.
export const OpProcWait: u32 = 14
/// OpResolve is Windows' name lookup on the port; no Linux backend supports
/// it (PollSupports false): std.task's stub resolver asks the nameservers.
export const OpResolve: u32 = 15
/// OpReady waits until h has input to read, consuming nothing: no byte, no
/// console record (the console wait a cancel ends).
/// Linux: io_uring's POLL_ADD for POLLIN, epoll's edge after a ppoll that
/// found nothing (the descriptor is on epoll's list from its open); Windows:
/// the registered wait of OpProcWait on the console's input handle. The
/// result is 0. The task calls PollRelease(op) once Await is over.
export const OpReady: u32 = 16
/// OpLock takes the whole-file advisory lock of h, waiting for it on the
/// poller: len 1 exclusive, 0 shared; the result is 1. Windows: an
/// overlapped LockFileEx over every byte, without fail-immediately, on the
/// handle's completion port (the kernel hands the lock over at its
/// release; a cancel is CancelIoEx). Linux has no asynchronous lock wait
/// (PollSupports false): rt.FileLock polls there.
export const OpLock: u32 = 17

/// Address families of SockAddr, the same numbers on every OS.
export const Inet4: u16 = 4
/// SockAddr family of an IPv6 address.
export const Inet6: u16 = 6

/// NewOp is an idle operation of kind on handle h.
/// O(1); allocates nothing.
export NewOp(kind: u32, h: uint): Op {
    return Op(kind: kind, h: h, buf: raw.Null[u8](), len: 0, pos: -1,
        addr: SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0), res: Unsupported(), waiter: raw.Null[u8](),
        state: raw.Atomic[u32].New(0), cancel: raw.Atomic[u32].New(0), path: "", path2: "", os: [0; 160])
}

/// PageSize is the OS page size in bytes, 4,096 on every target.
/// O(1); allocates nothing.
export PageSize(): int {
    return 4096
}

/// Exit has no system call to make here: on x86_64 it ends the thread with
/// ud2, an undefined-instruction fault the kernel reports; elsewhere it
/// spins, since mk build refuses aarch64-nutshell.
/// O(1); allocates nothing.
export Exit(code: i32): never {
    if Target.arch == Arch.X64 {
        unsafe "ud2 raises #UD and never returns; it reads and writes no memory" {
            asm("ud2", options(noreturn, nostack))
        }
    }
    for {
    }
}

/// Pid has std/sys/linux's contract; this placeholder returns `0`.
/// O(1); allocates nothing.
export Pid(): u64 {
    return 0
}

/// FatalExit ends the process with code after a panic or fault report: Exit
/// (Windows' runs no DLL detach). Never returns; allocates nothing.
export FatalExit(code: i32): never {
    Exit(code)
}

/// ThreadId has std/sys/linux's contract; this placeholder returns `0`.
/// O(1); allocates nothing.
export ThreadId(): u64 {
    return 0
}

/// CpuCount: one processor, the least every caller accepts.
/// O(1); allocates nothing.
export CpuCount(): int {
    return 1
}

// Unsupported is every OsResult this placeholder answers.
Unsupported(): OsResult {
    return OsResult(value: 0, err: core.SysErr.Other, os: 0)
}

/// NewSem is the zero Sem, for std.rt and std.task, whose fields are
/// private.
/// O(1); allocates nothing.
export NewSem(): Sem {
    return Sem(word: raw.Atomic[u32].New(0), sleepers: raw.Atomic[u32].New(0), h: 0)
}

/// NewCarry is the empty ConsoleCarry.
/// O(1); allocates nothing.
export NewCarry(): ConsoleCarry {
    return ConsoleCarry(bytes: [0; 4], n: 0)
}
