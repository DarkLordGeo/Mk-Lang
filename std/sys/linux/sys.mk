// Why: std.task, std.rt and the rest of std talk to one OS layer whose API
// is the same on every target; this file is the Linux side of its types and
// basic process calls.
//
// What: the exported types OsResult, ArgsInfo, Sem, ConsoleCarry, SockAddr,
// Op and FaultInfo, the Op kinds (OpRead to OpLock), NewOp, PageSize, Exit,
// Pid, ThreadId, CpuCount, and the error mapping (MapError, SockFail, IoFail,
// ConnectFail).
//
// How: raw system calls, no libc. Both variants export exactly the same API
// (a test compares them), and the struct declarations match
// std/sys/windows/sys.mk's except Sem, which has a sleepers word here
// (futex.mk).

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

/// Exit ends the process with code: exit_group ends every thread and runs
/// nothing else (no flush, no destructor). Never returns; allocates nothing.
export Exit(code: i32): never {
    unsafe "a system call that passes no memory, only values and handles" {
        for {
            _ = Syscall(Nrs().exit_group, Trunc[uint](i64(code)), 0, 0, 0, 0, 0)
        }
    }
}

/// Pid is the process id.
/// O(1); allocates nothing.
export Pid(): u64 {
    unsafe "a system call that passes no memory, only values and handles" {
        return Trunc[u64](Syscall(Nrs().getpid, 0, 0, 0, 0, 0, 0))
    }
}

/// ThreadId is the calling thread's OS id.
/// O(1); allocates nothing.
export ThreadId(): u64 {
    unsafe "a system call that passes no memory, only values and handles" {
        return Trunc[u64](Syscall(Nrs().gettid, 0, 0, 0, 0, 0, 0))
    }
}

/// CpuCount is the number of processors in this process's affinity mask, at
/// least 1.
/// O(1) system calls; allocates nothing.
export CpuCount(): int {
    // 8,192 CPUs' worth (1 KiB), Go's buffer (runtime/os_linux.go): the
    // call fails with EINVAL when the mask is smaller than the kernel's
    // possible CPUs, and 128 bytes would answer 1 above 1,024 (no machine
    // here has that many, so this is by reading). The kernel
    // writes r bytes; the rest stay zero.
    var mask: [128]u64 = [0; 128]
    unsafe "system call; mask holds 1,024 bytes" {
        r := Syscall(Nrs().sched_getaffinity, 0, 1024, raw.Addr(raw.DataMut(mut mask[..])), 0, 0, 0)
        if r <= 0 {
            return 1
        }
    }
    var n := 0
    for w in mask {
        n += raw.Popcount64(w)
    }
    return if n < 1 { 1 } else { n }
}

// Ok and Fail build OsResults.
Ok(v: i64): OsResult {
    return OsResult(value: v, err: core.SysErr.None, os: 0)
}

Fail(errno: i64): OsResult {
    return OsResult(value: 0, err: MapError(errno), os: errno)
}

FailWith(e: core.SysErr): OsResult {
    return OsResult(value: 0, err: e, os: 0)
}

// Result turns a raw system call return into an OsResult: -4095..-1 is
// -errno.
Result(r: int): OsResult {
    return if r < 0 && r >= -4095 { Fail(i64(-r)) } else { Ok(i64(r)) }
}

// IsErr reports whether a raw return is -errno.
IsErr(r: int): bool {
    return r < 0 && r >= -4095
}

// The error mapping, Linux side. EINTR is Canceled: every
// handler std.sys installs restarts system calls (SA_RESTART) except
// SIGURG's, which only ThreadInterrupt sends, so a call that reports EINTR
// was interrupted for a cancel; the pool repeats it when its own job was
// not cancelled (the late window), so "only after a cancel" holds where
// the job is known (an interrupted open is not Other). EPIPE is
// Closed on a pipe and Reset on a socket (SockFail, IoFail).
MapError(errno: i64): core.SysErr {
    return match errno {
        2 { core.SysErr.NotFound }
        17 { core.SysErr.Exists }
        1 | 13 { core.SysErr.Denied }
        21 { core.SysErr.IsDirectory }
        20 { core.SysErr.NotDirectory }
        28 | 122 { core.SysErr.NoSpace }
        36 | 40 { core.SysErr.InvalidPath }
        125 | 4 { core.SysErr.Canceled }
        32 { core.SysErr.Closed }
        111 { core.SysErr.Refused }
        104 | 103 { core.SysErr.Reset }
        98 { core.SysErr.AddrInUse }
        101 | 113 | 100 { core.SysErr.Unreachable }
        99 | 97 { core.SysErr.InvalidAddress }
        _ { core.SysErr.Other }
    }
}

// SockFail is Fail for a socket operation: EPIPE there is the peer's reset.
SockFail(errno: i64): OsResult {
    return if errno == 32 { OsResult(value: 0, err: core.SysErr.Reset, os: errno) } else { Fail(errno) }
}

// IoFail maps errno from an operation of kind on handle h, the one mapping
// both poller backends and the synchronous calls use: the
// socket kinds through SockFail; a read or write's EPIPE by what h is
// (Closed on a pipe, Reset on a socket; one fstat, on this error path
// only); anything else through MapError.
IoFail(h: uint, kind: u32, errno: i64): OsResult {
    if kind == OpAccept || kind == OpRecv || kind == OpSend || kind == OpSendTo || kind == OpRecvFrom {
        return SockFail(errno)
    }
    if errno == 32 && (kind == OpRead || kind == OpWrite) && HandleKind(h) == 4 {
        return OsResult(value: 0, err: core.SysErr.Reset, os: errno)
    }
    return Fail(errno)
}

// ConnectFail is SockFail for a connect: a reset that answers the SYN is
// the peer's refusal. io_uring's CONNECT reports a refused loopback
// connect as ECONNRESET where epoll's SO_ERROR says ECONNREFUSED (WSL
// kernel); both must give Refused.
ConnectFail(errno: i64): OsResult {
    if errno == 104 {
        return OsResult(value: 0, err: core.SysErr.Refused, os: errno)
    }
    return SockFail(errno)
}

// AtFdcwd is AT_FDCWD (-100) as a system call argument.
const AtFdcwd: uint = 0xFFFF_FFFF_FFFF_FF9C

/// NewSem is the zero Sem, for std.rt and std.task, whose fields are
/// private; SemInit still runs before it is used.
/// O(1); allocates nothing.
export NewSem(): Sem {
    return Sem(word: raw.Atomic[u32].New(0), sleepers: raw.Atomic[u32].New(0), h: 0)
}

/// NewCarry is the empty ConsoleCarry.
/// O(1); allocates nothing.
export NewCarry(): ConsoleCarry {
    return ConsoleCarry(bytes: [0; 4], n: 0)
}

// AfInet and AfInet6 are the OS values of the two families.
const AfInet: u16 = 2
const AfInet6: u16 = 10
