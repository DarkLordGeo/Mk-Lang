// Why: std.rt and std.task are written once against std.sys, so the
// Windows and Linux variants must export exactly the same API.
//
// What: the exported types of the OS layer (OsResult, SockAddr, Op, the
// Op* kinds and the rest) and the process calls, for Windows.
//
// How: the exported struct declarations match std/sys/linux/sys.mk's,
// except Sem's private words; only the functions differ, and the error
// mapping below turns Win32 and Winsock codes into core.SysErr.

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
/// std.sys never touches them; os is std.sys's own area (the OVERLAPPED, the
/// AcceptEx addresses, the io_uring generation, the epoll wait link). The
/// one-owner rule: from a PollSubmit that returns true until the PollWait
/// that returns the op, std.sys and the OS own the op and buf[0..len]; the
/// task touches only cancel and may call PollCancel.
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
    /// The address a dial or a datagram send goes to, or a receive fills,
    /// or an accept's peer.
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

/// Op kind: read at most len bytes from h into buf.
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
/// OpResolve looks up a name: path the name, buf and len the
/// caller's SockAddr array; the result is the count written. Windows does
/// it on the port (GetAddrInfoExW with a completion routine, cancelled by
/// GetAddrInfoExCancel); Linux has no poller form (PollSupports false), its
/// stub resolver is std.task's.
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

/// Address families of SockAddr, the same numbers on every OS (the OS's own
/// AF_ values differ: AF_INET6 is 23 on Windows and 10 on Linux).
export const Inet4: u16 = 4
/// SockAddr family of an IPv6 address.
export const Inet6: u16 = 6

/// NewOp is an idle operation of kind on handle h; the caller sets the other
/// fields its kind reads.
/// O(1); allocates nothing.
export NewOp(kind: u32, h: uint): Op {
    return Op(kind: kind, h: h, buf: raw.Null[u8](), len: 0, pos: -1,
        addr: SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0), res: Ok(0), waiter: raw.Null[u8](),
        state: raw.Atomic[u32].New(0), cancel: raw.Atomic[u32].New(0), path: "", path2: "", os: [0; 160])
}

/// PageSize is the OS page size in bytes, 4,096 on every target.
/// O(1); allocates nothing.
export PageSize(): int {
    return 4096
}

/// Exit ends the process with code: ExitProcess runs nothing else of Mk's
/// (no flush, no destructor). Never returns; allocates nothing.
export Exit(code: i32): never {
    unsafe "a kernel32 call that passes no memory, only values and handles" {
        ExitProcess(Trunc[u32](code))
    }
}

/// Pid is the process id.
/// O(1); allocates nothing.
export Pid(): u64 {
    unsafe "a kernel32 call that passes no memory, only values and handles" {
        return u64(GetCurrentProcessId())
    }
}

/// ThreadId is the calling thread's OS id.
/// O(1); allocates nothing.
export ThreadId(): u64 {
    unsafe "a kernel32 call that passes no memory, only values and handles" {
        return u64(GetCurrentThreadId())
    }
}

/// CpuCount is the number of processors this process may run on: the
/// popcount of a restricting affinity mask, else every active processor of
/// every group (ALL_PROCESSOR_GROUPS), at least 1.
/// O(1) system calls; allocates nothing.
export CpuCount(): int {
    unsafe "kernel32; pm and sm are words in this frame" {
        // The processors this process may run on (Go's getproccount): the
        // popcount of its affinity mask when that mask restricts it (a
        // start /affinity, tools/bench's one-CPU mask, a job object), so
        // the runtime does not start a carrier or spin a thread per CPU it
        // cannot use (the pool's spins wait for another CPU's thread).
        // An unrestricted mask, or a process in several processor groups
        // (both masks 0), is every active processor of every group.
        var pm: uint = 0
        var sm: uint = 0
        if GetProcessAffinityMask(GetCurrentProcess(), raw.OfMut(mut pm), raw.OfMut(mut sm)) != 0 && pm != 0 && pm != sm {
            return raw.Popcount64(u64(pm))
        }
        n := int(GetActiveProcessorCount(0xFFFF))
        return if n < 1 { 1 } else { n }
    }
}

// Ok and Fail build OsResults.
Ok(v: i64): OsResult {
    return OsResult(value: v, err: core.SysErr.None, os: 0)
}

Fail(code: u32): OsResult {
    return OsResult(value: 0, err: MapError(code), os: i64(code))
}

FailWith(e: core.SysErr): OsResult {
    return OsResult(value: 0, err: e, os: 0)
}

LastError(): OsResult {
    unsafe "a kernel32 call that passes no memory, only values and handles" {
        return Fail(GetLastError())
    }
}

// The error mapping, Windows side, for Win32 codes and the
// Winsock codes of the same failures (a socket operation reports WSAE*
// through WSAGetLastError and the Win32 code through GetOverlappedResult).
// 5 on a directory path is IsDirectory, decided by the caller that knows the
// path; 995 is Canceled, which only a cancel produces on a handle of the
// port (the issuing carrier never exits). WSAESHUTDOWN (10058, a send
// after this socket's own write shutdown) is Reset, the answer Linux gives
// for the same failure (EPIPE on a socket, SockFail), so both targets agree;
// 232 (ERROR_NO_DATA, a write to a pipe whose reader closed) is Closed,
// Linux's EPIPE on a pipe.
MapError(code: u32): core.SysErr {
    return match code {
        2 | 3 { core.SysErr.NotFound }
        80 | 183 { core.SysErr.Exists }
        5 | 32 | 10013 { core.SysErr.Denied }
        267 { core.SysErr.NotDirectory }
        39 | 112 { core.SysErr.NoSpace }
        123 | 161 | 206 { core.SysErr.InvalidPath }
        995 { core.SysErr.Canceled }
        109 | 232 { core.SysErr.Closed }
        10061 | 1225 { core.SysErr.Refused }
        10054 | 10053 | 10058 | 64 | 1236 { core.SysErr.Reset }
        10048 { core.SysErr.AddrInUse }
        10051 | 10065 | 10050 | 1231 | 1232 { core.SysErr.Unreachable }
        10049 | 11001 | 10047 | 1214 { core.SysErr.InvalidAddress }
        _ { core.SysErr.Other }
    }
}

// InvalidHandle is INVALID_HANDLE_VALUE.
const InvalidHandle: uint = 0xFFFF_FFFF_FFFF_FFFF

/// NewSem is the zero Sem, for std.rt and std.task, whose fields are
/// private; SemInit still runs before it is used.
/// O(1); allocates nothing.
export NewSem(): Sem {
    return Sem(word: raw.Atomic[u32].New(0), h: 0)
}

/// NewCarry is the empty ConsoleCarry.
/// O(1); allocates nothing.
export NewCarry(): ConsoleCarry {
    return ConsoleCarry(bytes: [0; 4], n: 0)
}

// AfInet and AfInet6 are the OS values of the two families.
const AfInet: u16 = 2
const AfInet6: u16 = 23
