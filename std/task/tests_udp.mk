// Why: std.net's UDP rests on the runtime's datagram entries, which must cut,
// wait, cancel and fail the same way on every backend.
// What: the RT-Os-Udp-* blocks over NativeUdp, NativeSendTo and NativeRecvFrom,
// plus RT-Os-Resolve and RT-Os-Hosts (numeric names and the hosts-file scan).
// How: loopback on IPv4 and IPv6: a cut datagram, an empty one, a receive that
// waits for another task, a cancel of a pending receive, a send to a closed
// port. Run by the scheduler harness at 1 to 8 carriers, dev and release.

import std.core
import std.raw
import std.sys

// ULoop is the loopback address of the family, port p.
ULoop(v6: bool, p: u16): core.IoAddr {
    var ip: [16]u8 = [0; 16]
    if v6 {
        ip[15] = 1
    } else {
        ip[0] = 127
        ip[3] = 1
    }
    return core.IoAddr(ip: ip, port: p, v6: v6, zone: 0)
}

// UBind is a UDP socket on loopback port 0: the handle and the port the OS
// chose.
UBind(io: Io, v6: bool): (h: uint, port: u16) {
    r := NativeUdp(io, ULoop(v6, 0))
    Assert(r.err == core.SysErr.None && r.value != 0 && r.os > 0 && r.os < 65536)
    return (h: Trunc[uint](r.value), port: Trunc[u16](r.os))
}

UClose(io: Io, h: uint) {
    r := NativeClose(io, h)
    Assert(r.err == core.SysErr.None)
}

test "RT-Os-Udp echo on IPv4 and IPv6 loopback, a cut datagram and an empty one" (sys: &System) {
    for k in 0..2 {
        v6 := k == 1
        (h: a, port: pa) := UBind(sys.io, v6)
        (h: b, port: pb) := UBind(sys.io, v6)
        Assert(pa != pb)
        var msg: [5]u8 = [104, 101, 108, 108, 111]
        s := NativeSendTo(sys.io, a, msg[..], ULoop(v6, pb))
        Assert(s.err == core.SysErr.None && s.value == 5)
        var buf: [64]u8 = [0; 64]
        var from := ULoop(false, 0)
        r := NativeRecvFrom(sys.io, b, mut buf[..], mut from)
        Assert(r.err == core.SysErr.None && r.value == 5 && r.os == 0)
        Assert(buf[0] == 104 && buf[4] == 111)
        Assert(from.port == pa && from.v6 == v6)
        if v6 {
            Assert(from.ip[15] == 1 && from.ip[0] == 0)
        } else {
            Assert(from.ip[0] == 127 && from.ip[3] == 1)
        }
        // A datagram longer than the buffer is cut to it, os 1; the rest of
        // it is gone, so the next receive gets the next datagram.
        var big: [1000]u8 = [7; 1000]
        s2 := NativeSendTo(sys.io, a, big[..], ULoop(v6, pb))
        Assert(s2.err == core.SysErr.None && s2.value == 1000)
        r2 := NativeRecvFrom(sys.io, b, mut buf[0..10], mut from)
        Assert(r2.err == core.SysErr.None && r2.value == 10 && r2.os == 1 && buf[9] == 7)
        // An empty datagram is a datagram of 0 bytes, not an end of stream.
        s3 := NativeSendTo(sys.io, a, big[0..0], ULoop(v6, pb))
        Assert(s3.err == core.SysErr.None && s3.value == 0)
        r3 := NativeRecvFrom(sys.io, b, mut buf[..], mut from)
        Assert(r3.err == core.SysErr.None && r3.value == 0 && r3.os == 0 && from.port == pa)
        UClose(sys.io, a)
        UClose(sys.io, b)
    }
}

// UServe answers each datagram with the same bytes until one starts with
// 255 (answered too, then it returns); bad counts the failures.
UServe(io: Io, h: uint, bad: &raw.Atomic[u32]) {
    var buf: [256]u8 = [0; 256]
    var from := ULoop(false, 0)
    for {
        r := NativeRecvFrom(io, h, mut buf[..], mut from)
        if r.err != core.SysErr.None || r.value == 0 {
            _ = bad.Add(1)
            return
        }
        n := Trunc[int](r.value)
        s := NativeSendTo(io, h, buf[0..n], from)
        if s.err != core.SysErr.None || s.value != r.value {
            _ = bad.Add(1)
        }
        if buf[0] == 255 {
            return
        }
    }
}

test "RT-Os-Udp-Wait 2,000 round trips with another task, whose receives wait in the poller" (sys: &System) {
    (h: srv, port: ps) := UBind(sys.io, false)
    (h: cli, port: pc) := UBind(sys.io, false)
    bad := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go UServe(sys.io, srv, &bad)
        var out: [64]u8 = [0; 64]
        var back: [64]u8 = [0; 64]
        var from := ULoop(false, 0)
        for i in 0..2_000 {
            n := 1 + i % 60
            out[0] = Trunc[u8](i % 251)
            out[n - 1] = Trunc[u8](i % 13)
            w := NativeSendTo(sys.io, cli, out[0..n], ULoop(false, ps))
            Assert(w.err == core.SysErr.None && w.value == i64(n))
            r := NativeRecvFrom(sys.io, cli, mut back[..], mut from)
            Assert(r.err == core.SysErr.None && r.value == i64(n) && from.port == ps)
            Assert(back[0] == out[0] && back[n - 1] == out[n - 1])
        }
        var stop: [1]u8 = [255]
        w := NativeSendTo(sys.io, cli, stop[..], ULoop(false, ps))
        Assert(w.err == core.SysErr.None)
        r := NativeRecvFrom(sys.io, cli, mut back[..], mut from)
        Assert(r.err == core.SysErr.None && r.value == 1)
    }
    Assert(bad.Load() == 0 && pc != 0)
    UClose(sys.io, srv)
    UClose(sys.io, cli)
}

// URecvCancel blocks in a receive nobody answers; got: 1 Canceled, 2 other.
URecvCancel(io: Io, h: uint, got: &raw.Atomic[u32]) {
    var buf: [16]u8 = [0; 16]
    var from := ULoop(false, 0)
    r := NativeRecvFrom(io, h, mut buf[..], mut from)
    if r.err == core.SysErr.Canceled {
        got.Store(1)
    } else {
        got.Store(2)
    }
}

test "RT-Os-Udp-Cancel a pending receive ends with Canceled, twenty times, and the socket still works" (sys: &System) {
    (h: a, port: pa) := UBind(sys.io, false)
    (h: b, port: pb) := UBind(sys.io, false)
    for round in 0..20 {
        g := raw.Atomic[u32].New(0)
        scope s := sys.io.Scope() {
            s.go URecvCancel(sys.io, a, &g)
            if round % 2 == 0 {
                CAwaitBlocked(&s, 1)
            }
            s.Cancel()
        }
        Assert(g.Load() == 1)
    }
    var msg: [3]u8 = [1, 2, 3]
    w := NativeSendTo(sys.io, b, msg[..], ULoop(false, pa))
    Assert(w.err == core.SysErr.None && w.value == 3)
    var buf: [8]u8 = [0; 8]
    var from := ULoop(false, 0)
    r := NativeRecvFrom(sys.io, a, mut buf[..], mut from)
    Assert(r.err == core.SysErr.None && r.value == 3 && buf[2] == 3 && from.port == pb)
    UClose(sys.io, a)
    UClose(sys.io, b)
}

test "RT-Os-Udp-Closed a send to a closed port fails no later receive" (sys: &System) {
    (h: a, port: pa) := UBind(sys.io, false)
    (h: b, port: pb) := UBind(sys.io, false)
    (h: c, port: pc) := UBind(sys.io, false)
    UClose(sys.io, c)
    var msg: [4]u8 = [9, 9, 9, 9]
    // The ICMP port-unreachable this draws must not reach a's next receive
    // (Windows reports it as WSAECONNRESET unless SIO_UDP_CONNRESET is off).
    for _ in 0..3 {
        w := NativeSendTo(sys.io, a, msg[..], ULoop(false, pc))
        Assert(w.err == core.SysErr.None && w.value == 4)
    }
    w := NativeSendTo(sys.io, b, msg[0..2], ULoop(false, pa))
    Assert(w.err == core.SysErr.None)
    var buf: [8]u8 = [0; 8]
    var from := ULoop(false, 0)
    r := NativeRecvFrom(sys.io, a, mut buf[..], mut from)
    Assert(r.err == core.SysErr.None && r.value == 2 && from.port == pb)
    // A bound port is AddrInUse for a second socket.
    x := NativeUdp(sys.io, ULoop(false, pa))
    Assert(x.err == core.SysErr.AddrInUse)
    UClose(sys.io, a)
    UClose(sys.io, b)
}

test "RT-Os-Resolve numeric addresses answer themselves, localhost is both loopbacks, an unknown name is NotFound" (sys: &System) {
    var out: [8]core.IoAddr = [ULoop(false, 0); 8]
    r := NativeResolveAll(sys.io, "192.0.2.7", mut out[..])
    Assert(r.err == core.SysErr.None && r.value == 1 && !out[0].v6 && out[0].ip[0] == 192 && out[0].ip[3] == 7)
    r2 := NativeResolveAll(sys.io, "::1", mut out[..])
    Assert(r2.err == core.SysErr.None && r2.value == 1 && out[0].v6 && out[0].ip[15] == 1)
    r3 := NativeResolveAll(sys.io, "LocalHost", mut out[..])
    Assert(r3.err == core.SysErr.None && r3.value == 2 && !out[0].v6 && out[1].v6)
    r4 := NativeResolveAll(sys.io, "127.0.0.1", mut out[0..0])
    Assert(r4.err == core.SysErr.None && r4.value == 0)
    // On Windows GetAddrInfoW asks DNS for it (about 12 s a run here: the
    // eight runs of this block took 103.8 s), so the NotFound mapping of 11001 is
    // judged on Linux only, whose hosts scan answers at once.
    if PollBackend() == 1 {
        // Through GetAddrInfoW (IsLocalhost does not take the trailing dot).
        r7 := NativeResolveAll(sys.io, "localhost.", mut out[..])
        Assert(r7.err == core.SysErr.None && r7.value >= 1)
        Assert((out[0].v6 && out[0].ip[15] == 1) || (!out[0].v6 && out[0].ip[0] == 127))
    } else {
        r5 := NativeResolveAll(sys.io, "no-such-host.invalid", mut out[..])
        Assert(r5.err == core.SysErr.NotFound)
    }
    r6 := NativeResolveAll(sys.io, "", mut out[..])
    Assert(r6.err == core.SysErr.InvalidAddress)
}

test "RT-Os-Hosts the hosts-file scan: comments, tabs, several names, case, file order, a short out" (sys: &System) {
    t := "# comment\n10.0.0.1\talpha beta # trailing\n\n::2 Beta\n10.0.0.3 gamma\r\n10.0.0.4 alphabet\nbad.addr beta\n10.0.0.5 beta\n"
    var out: [8]core.IoAddr = [ULoop(false, 0); 8]
    n := HostsScan(t.AsBytes(), "BETA".AsBytes(), mut out[..])
    Assert(n == 3)
    Assert(!out[0].v6 && out[0].ip[3] == 1)
    Assert(out[1].v6 && out[1].ip[15] == 2)
    Assert(!out[2].v6 && out[2].ip[3] == 5)
    Assert(HostsScan(t.AsBytes(), "gamma".AsBytes(), mut out[..]) == 1 && out[0].ip[3] == 3)
    Assert(HostsScan(t.AsBytes(), "alpha".AsBytes(), mut out[..]) == 1)
    Assert(HostsScan(t.AsBytes(), "trailing".AsBytes(), mut out[..]) == 0)
    Assert(HostsScan(t.AsBytes(), "beta".AsBytes(), mut out[0..1]) == 1)
}

// UServeN echoes exactly n datagrams, then returns; bad counts failures.
UServeN(io: Io, h: uint, n: int, bad: &raw.Atomic[u32]) {
    var buf: [64]u8 = [0; 64]
    var from := ULoop(false, 0)
    for _ in 0..n {
        r := NativeRecvFrom(io, h, mut buf[..], mut from)
        if r.err != core.SysErr.None {
            _ = bad.Add(1)
            return
        }
        k := Trunc[int](r.value)
        s := NativeSendTo(io, h, buf[0..k], from)
        if s.err != core.SysErr.None || s.value != r.value {
            _ = bad.Add(1)
        }
    }
}

// UClient makes rounds round trips to port ps from a socket of its own;
// each datagram carries the client's id and the round, and the echo must
// carry the same (no datagram is lost, crossed or duplicated: at most one
// per client is in flight, so the receive buffer never overflows).
UClient(io: Io, id: int, ps: u16, rounds: int, bad: &raw.Atomic[u32]) {
    r0 := NativeUdp(io, ULoop(false, 0))
    if r0.err != core.SysErr.None {
        _ = bad.Add(1)
        return
    }
    h := Trunc[uint](r0.value)
    var out: [8]u8 = [0; 8]
    var back: [16]u8 = [0; 16]
    var from := ULoop(false, 0)
    for i in 0..rounds {
        out[0] = Trunc[u8](id)
        out[1] = Trunc[u8](i)
        out[2] = Trunc[u8](i >> 8)
        w := NativeSendTo(io, h, out[0..3], ULoop(false, ps))
        r := NativeRecvFrom(io, h, mut back[..], mut from)
        if w.err != core.SysErr.None || r.err != core.SysErr.None || r.value != 3 || back[0] != out[0] ||
            back[1] != out[1] || back[2] != out[2] || from.port != ps {
            _ = bad.Add(1)
        }
    }
    _ = NativeClose(io, h)
}

test "RT-Os-Udp-Many 8 client tasks, 10,000 round trips through one server socket" (sys: &System) {
    (h: srv, port: ps) := UBind(sys.io, false)
    bad := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go UServeN(sys.io, srv, 10_000, &bad)
        for id in 0..8 {
            s.go UClient(sys.io, id, ps, 1_250, &bad)
        }
    }
    Assert(bad.Load() == 0)
    UClose(sys.io, srv)
}

// USkipOff is sys.TestSkipOff (the blocks' parameter is named sys).
USkipOff(): bool {
    return sys.TestSkipOff()
}

// URecvOne receives one datagram on h into a 64-byte buffer and checks it
// is the 5 bytes 1..5 from port p; bad counts a failure.
URecvOne(io: Io, h: uint, p: u16, bad: &raw.Atomic[u32]) {
    var buf: [64]u8 = [0; 64]
    var from := ULoop(false, 0)
    r := NativeRecvFrom(io, h, mut buf[..], mut from)
    if r.err != core.SysErr.None || r.value != 5 || r.os != 0 || from.port != p || buf[0] != 1 || buf[4] != 5 {
        _ = bad.Add(1)
    }
}

// UCutThenPending runs rounds of a cut datagram received at once (it is
// queued before the receive) and then a receive that waits in the poller
// for the next one: a stray packet of the cut receive would
// complete the waiting one with the old status, or touch a frame that is
// gone.
UCutThenPending(io: Io, rounds: int) {
    (h: a, port: pa) := UBind(io, false)
    (h: b, port: pb) := UBind(io, false)
    var big: [1000]u8 = [7; 1000]
    var five: [5]u8 = [1, 2, 3, 4, 5]
    var buf: [10]u8 = [0; 10]
    var from := ULoop(false, 0)
    bad := raw.Atomic[u32].New(0)
    for _ in 0..rounds {
        s := NativeSendTo(io, a, big[..], ULoop(false, pb))
        Assert(s.err == core.SysErr.None && s.value == 1000)
        r := NativeRecvFrom(io, b, mut buf[..], mut from)
        Assert(r.err == core.SysErr.None && r.value == 10 && r.os == 1 && from.port == pa)
        scope sc := io.Scope() {
            sc.go URecvOne(io, b, pa, &bad)
            CAwaitBlocked(&sc, 1)
            s2 := NativeSendTo(io, a, five[..], ULoop(false, pb))
            Assert(s2.err == core.SysErr.None && s2.value == 5)
        }
    }
    Assert(bad.Load() == 0)
    UClose(io, a)
    UClose(io, b)
}

test "RT-Os-Udp-CutPending cut datagrams, each followed by a receive that waits, 500 rounds" (sys: &System) {
    UCutThenPending(sys.io, 500)
}

test "RT-Os-Udp-NoSkip the datagram and stream paths with no skip-on-success (every success posts its packet)" (sys: &System) {
    // Before the first socket: every socket of this run waits for the
    // packet a synchronous success posts.
    Assert(USkipOff())
    for k in 0..2 {
        v6 := k == 1
        (h: a, port: pa) := UBind(sys.io, v6)
        (h: b, port: pb) := UBind(sys.io, v6)
        var msg: [5]u8 = [104, 101, 108, 108, 111]
        s := NativeSendTo(sys.io, a, msg[..], ULoop(v6, pb))
        Assert(s.err == core.SysErr.None && s.value == 5)
        var buf: [64]u8 = [0; 64]
        var from := ULoop(false, 0)
        r := NativeRecvFrom(sys.io, b, mut buf[..], mut from)
        Assert(r.err == core.SysErr.None && r.value == 5 && r.os == 0 && from.port == pa)
        // The cut datagram: value 10 and os 1 here too (it was an error).
        var big: [1000]u8 = [7; 1000]
        Assert(NativeSendTo(sys.io, a, big[..], ULoop(v6, pb)).value == 1000)
        r2 := NativeRecvFrom(sys.io, b, mut buf[0..10], mut from)
        Assert(r2.err == core.SysErr.None && r2.value == 10 && r2.os == 1 && buf[9] == 7)
        UClose(sys.io, a)
        UClose(sys.io, b)
    }
    UCutThenPending(sys.io, 200)
    // A stream: an accept, a dial and 200 round trips, each send and
    // receive a submission that waits for its packet.
    l := NListen(sys.io)
    Assert(l.err == core.SysErr.None)
    lh := Trunc[uint](l.value)
    bad := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go UTcpEcho(sys.io, lh, &bad)
        var ab: [32]u8 = [0; 32]
        d := NativeDial(sys.io, NAddr(mut ab, l.os))
        Assert(d.err == core.SysErr.None)
        c := Trunc[uint](d.value)
        var q: [16]u8 = [0; 16]
        var back: [16]u8 = [0; 16]
        for i in 0..200 {
            q[0] = Trunc[u8](i)
            Assert(NSendAll(sys.io, c, q[..]))
            var got := 0
            for got < 16 {
                r := NativeRecv(sys.io, c, mut back[got..])
                Assert(r.err == core.SysErr.None && r.value > 0)
                got += Trunc[int](r.value)
            }
            Assert(back[0] == Trunc[u8](i))
        }
        UClose(sys.io, c)
    }
    Assert(bad.Load() == 0)
    UClose(sys.io, lh)
}

// UTcpEcho accepts one connection on l and echoes it until its end.
UTcpEcho(io: Io, l: uint, bad: &raw.Atomic[u32]) {
    a := NativeAccept(io, l)
    if a.err != core.SysErr.None {
        _ = bad.Add(1)
        return
    }
    c := Trunc[uint](a.value)
    var buf: [64]u8 = [0; 64]
    for {
        r := NativeRecv(io, c, mut buf[..])
        if r.err != core.SysErr.None {
            _ = bad.Add(1)
            break
        }
        if r.value == 0 {
            break
        }
        if !NSendAll(io, c, buf[0..Trunc[int](r.value)]) {
            _ = bad.Add(1)
            break
        }
    }
    _ = NativeClose(io, c)
}
