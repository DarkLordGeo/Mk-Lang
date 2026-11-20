// Why: std.net's TCP listeners and streams must wait for the network
// without holding a carrier, and a cancel must never drop an accepted
// socket or outlive a caller's buffer.
//
// What: the native table's socket entries NativeListen, NativeAccept,
// NativeDial, NativeRecv and NativeSend, the shared Transfer, Cancellable,
// and the dial-time name lookup Resolve with its test switch DialResolve.
//
// How: every entry is one OS step or one Await on the poller (the address
// parser is std.rt's, rt/addr.mk); partial sends and receives return their
// count (std.net's loops finish them). Every socket an entry made is closed
// on each of its error paths.

import std.core
import std.raw
import std.rt
import std.sys

// MaxChunk bounds one recv or send: WSARecv and WSASend take a u32 length,
// and a larger buffer is a partial transfer, which the caller repeats.
const MaxChunk: int = 0x4000_0000

/// Cancellable: an operation on io registers its wait on the running task's
/// inner scope: io is not masked, or the inner scope is past the
/// mask (MaskedPast). O(1); allocates nothing.
export Cancellable(io: Io): bool {
    if (core.IoFlags(io) & 1) == 0 {
        return true
    }
    return MaskedPast(Current(), io)
}

// NativeListen binds addr and listens with the OS's largest backlog. On
// success value is the runtime handle and os the bound port, so
// port 0 tells the caller which port the OS chose.
NativeListen(io: Io, addr: str): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    var a := sys.SockAddr(family: sys.Inet4, port: 0, addr: [0; 16], scope_id: 0)
    if !rt.ParseAddr(addr, true, mut a) {
        return ErrOf(core.SysErr.InvalidAddress)
    }
    s := sys.SocketOpen(a.family, true)
    if s.err != core.SysErr.None {
        return rt.IoOf(s)
    }
    sh := Trunc[uint](s.value)
    l := sys.SocketListen(sh, &a, 0)
    if l.err != core.SysErr.None {
        _ = sys.SocketShutdownClose(sh)
        return rt.IoOf(l)
    }
    var bound := a
    b := sys.SocketName(sh, mut bound)
    if b.err != core.SysErr.None {
        _ = sys.SocketShutdownClose(sh)
        return rt.IoOf(b)
    }
    h := rt.NewSocketHandle(sh, a.family)
    if h == 0 {
        _ = sys.SocketShutdownClose(sh)
        return ErrOf(core.SysErr.Other)
    }
    return core.IoResult(value: Trunc[i64](h), err: core.SysErr.None, os: i64(bound.port))
}

// NativeAccept waits for one connection on the listener h. The accepted
// socket is attached by std.sys (its contract); a completion with a
// socket wins over a cancel that came after it, so no
// accepted socket is dropped.
NativeAccept(io: Io, h: uint): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    r := rt.Rec(h)
    // A Unix listener's accept takes its address area from here (Windows
    // AcceptEx wants 2 x 128 bytes, more than op.os holds; Linux ignores it).
    var area: [32]u64 = [0; 32]
    unsafe "h is a live listener record; area outlives the op (Await returns after its completion)" {
        var op := sys.NewOp(sys.OpAccept, (*r).os)
        for {
            op = sys.NewOp(sys.OpAccept, (*r).os)
            op.addr.family = (*r).family
            if (*r).family == sys.Unix {
                op.buf = raw.PtrCast[u64, u8](raw.DataMut(mut area[..]))
                op.len = sys.AcceptUnixArea
            }
            // A listener another process holds too takes the shared-port
            // path. On Windows its registered wait is freed by
            // PollRelease, and a peer that reset before the accept comes
            // back as Reset to be asked again (for any other listener sys
            // issues it again itself). On Linux under io_uring it waits
            // non-exclusively, so a killed sibling cannot take a wake.
            op.pos = (*r).pos
            e := Await(raw.OfMut(mut op), Cancellable(io))
            sys.PollRelease(raw.OfMut(mut op))
            if e != core.SysErr.None {
                return ErrOf(e)
            }
            // An accept issued before the listener turned shared (a child
            // started meanwhile, rt.PassOn) can fail when the child took its
            // connection under the new O_NONBLOCK; it is asked again on the
            // shared path.
            if op.res.err != core.SysErr.None && op.pos != sys.SharedPort && (*r).pos == sys.SharedPort {
                continue
            }
            if (*r).pos != sys.SharedPort || op.res.err != core.SysErr.Reset {
                break
            }
        }
        if op.res.err != core.SysErr.None {
            return rt.IoOf(op.res)
        }
        s := Trunc[uint](op.res.value)
        nh := rt.NewSocketHandle(s, (*r).family)
        if nh == 0 {
            _ = sys.SocketShutdownClose(s)
            return ErrOf(core.SysErr.Other)
        }
        // The peer's address the accept op returned (std.sys's contract:
        // op.addr is the accept's result on every backend), kept in the
        // record so std.net's NewStream makes no getpeername. A
        // family sys could not decode stays NoPeer, and SockAddress asks
        // the OS then, as for a dialled socket.
        (*rt.Rec(nh)).peer = AcceptedPeer(op.addr)
        return NoErr(Trunc[i64](nh))
    }
}

// AcceptedPeer is the HandleRec.peer an accept whose op returned a keeps:
// a itself when it is an Inet4 or Inet6 address with a port, else
// rt.NoPeer. NativeAccept presets op.addr.family to the listener's, so the
// family alone would pass an op that wrote no address; a TCP peer never
// has port 0, which NewOp's address has. O(1); allocates nothing.
AcceptedPeer(a: sys.SockAddr): sys.SockAddr {
    if (a.family == sys.Inet4 || a.family == sys.Inet6) && a.port != 0 {
        return a
    }
    return rt.NoPeer
}

// NativeDial connects to addr. A host name is resolved first: on Windows
// to one address (Resolve); elsewhere to every answer of the resolve entry
// (NativeResolveAll), dialled in order until one connects, else the first
// one's error (as Go's dialer), or Canceled. A cancel that wins closes the
// socket.
NativeDial(io: Io, addr: str): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    var a := sys.SockAddr(family: sys.Inet4, port: 0, addr: [0; 16], scope_id: 0)
    if DialResolveMine() || !rt.ParseAddr(addr, false, mut a) {
        if PollBackend() != 1 {
            return DialAll(io, addr)
        }
        e := Resolve(io, addr, mut a)
        if e != core.SysErr.None {
            return ErrOf(e)
        }
    }
    return Connect(io, a)
}

// DialAll dials every address the resolve entry gives addr's host, in
// order, with addr's port (Resolve's checks of the text first).
DialAll(io: Io, addr: str): core.IoResult {
    var a := sys.SockAddr(family: sys.Inet4, port: 0, addr: [0; 16], scope_id: 0)
    colon := HostPort(addr, mut a)
    if colon < 0 {
        return ErrOf(core.SysErr.InvalidAddress)
    }
    var out: [MaxAnswers]core.IoAddr = [core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0); MaxAnswers]
    r := NativeResolveAll(io, str.From(addr.AsBytes()[0..colon]).Must(), mut out[..])
    if r.err != core.SysErr.None {
        return r
    }
    var first := ErrOf(core.SysErr.NotFound)
    for i in 0..Trunc[int](r.value) {
        var s := ToSock(out[i])
        s.port = a.port
        c := Connect(io, s)
        if c.err == core.SysErr.None || c.err == core.SysErr.Canceled {
            return c
        }
        if i == 0 {
            first = c
        }
    }
    return first
}

// Connect opens a TCP socket and connects it to a.
Connect(io: Io, a: sys.SockAddr): core.IoResult {
    s := sys.SocketOpen(a.family, true)
    if s.err != core.SysErr.None {
        return rt.IoOf(s)
    }
    sh := Trunc[uint](s.value)
    var op := sys.NewOp(sys.OpConnect, sh)
    op.addr = a
    unsafe "op is in this frame until Await returns" {
        e := Await(raw.OfMut(mut op), Cancellable(io))
        if e != core.SysErr.None {
            _ = sys.SocketShutdownClose(sh)
            return ErrOf(e)
        }
    }
    if op.res.err != core.SysErr.None {
        _ = sys.SocketShutdownClose(sh)
        return rt.IoOf(op.res)
    }
    h := rt.NewSocketHandle(sh, a.family)
    if h == 0 {
        _ = sys.SocketShutdownClose(sh)
        return ErrOf(core.SysErr.Other)
    }
    return NoErr(Trunc[i64](h))
}

// NativeRecv reads at most buf.Len() bytes (at most MaxChunk); 0 is end of
// stream. An empty buf returns 0 with no call.
NativeRecv(io: Io, h: uint, buf: mut []u8): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    return if buf.Len() == 0 { NoErr(0) } else { Transfer(io, sys.OpRecv, h, raw.DataMut(buf), buf.Len()) }
}

// NativeSend writes at most data.Len() bytes (at most MaxChunk) and returns
// the count, which may be short. An empty data returns 0 with no call.
NativeSend(io: Io, h: uint, data: []u8): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    if data.Len() == 0 {
        return NoErr(0)
    }
    unsafe "the poller only reads a send buffer" {
        return Transfer(io, sys.OpSend, h, raw.Data(data), data.Len())
    }
}

// Transfer is one recv or send on the poller. The buffer belongs to
// std.sys from the submit until the completion (the one-owner rule); Await
// returns only after that, so the caller's slice outlives the operation.
Transfer(io: Io, kind: u32, h: uint, p: *u8, n: int): core.IoResult {
    r := rt.Rec(h)
    unsafe "h is a live socket record" {
        var op := sys.NewOp(kind, (*r).os)
        op.buf = p
        op.len = if n > MaxChunk { MaxChunk } else { n }
        if Target.os == Os.Windows {
            return PortTransfer(io, h, mut op)
        }
        e := Await(raw.OfMut(mut op), Cancellable(io))
        return if e != core.SysErr.None { ErrOf(e) } else { rt.IoOf(op.res) }
    }
}

// PortTransfer is Transfer's wait on the Windows port: op is linked into h's
// record while submitted, so a local Shutdown can cancel it (rt.ShutWake).
// A cancel the caller's scope did not make (995 with op.cancel 0) came
// from such a Shutdown, and answers as Linux's shutdown(2) does: a receive the end of
// the stream (0), a send Reset. A receive after a read shutdown answers
// 0 as well, where Windows says WSAESHUTDOWN (10058).
PortTransfer(io: Io, h: uint, op: mut sys.Op): core.IoResult {
    unsafe "op is in the caller's frame and stays linked only until Await returns" {
        var link := rt.Parked(op: raw.OfMut(op), next: raw.Null[rt.Parked]())
        rt.SockPark(h, raw.OfMut(mut link))
        e := Await(raw.OfMut(op), Cancellable(io))
        shut := rt.SockUnpark(h, raw.OfMut(mut link))
        recv := op.kind == sys.OpRecv
        if e != core.SysErr.None && op.cancel.Load() == 0 && op.res.os == 995 {
            if recv && shut & rt.ShutRead != 0 {
                return NoErr(0)
            }
            if !recv && shut & rt.ShutWrite != 0 {
                return ErrOf(core.SysErr.Reset)
            }
        }
        if e != core.SysErr.None {
            return ErrOf(e)
        }
        if recv && op.res.err != core.SysErr.None && op.res.os == 10058 {
            return NoErr(0)
        }
        return rt.IoOf(op.res)
    }
}

// DialResolve is a test switch: the address of the one task whose dials
// all go to the resolver, "localhost" and numeric hosts included, so a
// block can reach GetAddrInfoW with a name that needs no network
// (test "RT-15-Resolve"); 0 off. Task-scoped, so blocks running in parallel in one
// process (mk test) keep their own dials.
var DialResolve: raw.Atomic[u64] = raw.Atomic[u64].New(0)

// DialResolveMine: the switch names the calling task.
DialResolveMine(): bool {
    // Edge: none; set and cleared by the same task that reads it here.
    d := DialResolve.Load()
    return d != 0 && d == u64(raw.Addr(Current()))
}

// MaxHostName is the longest host name the resolver is given (RFC 1035
// 2.3.4: 253 characters in text form).
const MaxHostName: int = 253

// HostPort checks "host:port" with a host name: the host is one to
// MaxHostName bytes of letters, digits, '-', '.' and '_', and the port as
// ParseAddr's, set in a. The colon's index (so the host ends at an ASCII
// byte, a str boundary), or -1.
HostPort(addr: str, a: mut sys.SockAddr): int {
    b := addr.AsBytes()
    n := b.Len()
    var colon := -1
    for i in 0..n {
        if b[i] == 58 {
            colon = i
        }
    }
    if colon < 1 || colon > MaxHostName || !rt.ParsePort(b[colon + 1..n], a) {
        return -1
    }
    for i in 0..colon {
        c := b[i]
        ok := (c >= 97 && c <= 122) || (c >= 65 && c <= 90) || (c >= 48 && c <= 57) || c == 45 || c == 46 || c == 95
        if !ok {
            return -1
        }
    }
    return colon
}

// Resolve reads "host:port" with a host name into a: on Windows
// the lookup runs on the port (sys.OpResolve, GetAddrInfoExW), so a
// cancel ends it at once, and the first IPv4 answer is taken, else the
// first IPv6 one; without sys.OpResolve it runs on the blocking pool
// through sys.ResolveHost. NativeDial calls it on Windows only (DialAll
// elsewhere). A text
// HostPort refuses is InvalidAddress with no call. No allocation.
Resolve(io: Io, addr: str, a: mut sys.SockAddr): core.SysErr {
    colon := HostPort(addr, a)
    if colon < 0 {
        return core.SysErr.InvalidAddress
    }
    host := str.From(addr.AsBytes()[0..colon]).Must()
    if sys.PollSupports(sys.OpResolve) {
        // Windows: the lookup on the port, so a cancel ends it at once; the
        // first IPv4 answer, else the first IPv6 one, as
        // ResolveHost picks.
        var all: [16]sys.SockAddr = [sys.SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0); 16]
        var rop := sys.NewOp(sys.OpResolve, 0)
        rop.path = host
        rop.len = 16
        unsafe "all is in this frame until OnPoller returns (the one-owner rule)" {
            rop.buf = raw.PtrCast[sys.SockAddr, u8](raw.DataMut(mut all[..]))
        }
        r := OnPoller(io, mut rop)
        if r.err != core.SysErr.None {
            return r.err
        }
        got := Trunc[int](r.value)
        if got == 0 {
            return core.SysErr.NotFound
        }
        var pick := 0
        for i in 0..got {
            if all[i].family == sys.Inet4 {
                pick = i
                break
            }
        }
        port := a.port
        a.family = all[pick].family
        a.addr = all[pick].addr
        a.scope_id = all[pick].scope_id
        a.port = port
        return core.SysErr.None
    }
    var op := sys.NewOp(0, 0)
    op.path = host
    op.pos = i64(a.port)
    unsafe "op is in this frame until Blocking returns" {
        e := Blocking(JobResolve, raw.OfMut(mut op), Cancellable(io))
        if e != core.SysErr.None {
            return e
        }
    }
    if op.res.err != core.SysErr.None {
        return op.res.err
    }
    a.family = op.addr.family
    a.port = op.addr.port
    a.addr = op.addr.addr
    a.scope_id = op.addr.scope_id
    return core.SysErr.None
}

// JobResolve is the pool's resolver job: op.path the name, op.pos the port,
// the answer in op.addr.
JobResolve(op: *sys.Op) {
    unsafe "op is the caller's, live until the job completes" {
        (*op).res = sys.ResolveHost((*op).path, Trunc[u16]((*op).pos), mut (*op).addr)
    }
}
