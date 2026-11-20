// Why: std.task's socket entries park a task on the poller, and a completion
// can race the park, a cancel, or another carrier.
// What: RT-Net-* (echo, errors, leaks, ParseAddr), RT-F1-ParkHandshake,
// RT-Full-ParkHandshake, RT-Io-CancelInFlight, RT-15-Resolve and the accept-peer block.
// How: loopback echo pairs with checked locals and patterns, run by the
// scheduler harness and mk test at 1, 2, 4 and 8 carriers on IOCP, io_uring and epoll.

import std.core
import std.raw
import std.rt
import std.sys

// NAddr writes "127.0.0.1:<port>" into b.
NAddr(b: mut [32]u8, port: i64): str from b {
    pre := "127.0.0.1:".AsBytes()
    for i in 0..pre.Len() {
        b[i] = pre[i]
    }
    var d: [8]u8 = [0; 8]
    var n := 0
    var x := Trunc[int](port)
    for {
        d[n] = Trunc[u8](48 + x % 10)
        n += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    for i in 0..n {
        b[pre.Len() + i] = d[n - 1 - i]
    }
    return str.From(b[0..pre.Len() + n]).Must()
}

// NListen listens on a loopback port the OS picks; the port is in os.
NListen(io: Io): core.IoResult {
    r := NativeListen(io, "127.0.0.1:0")
    Assert(r.err == core.SysErr.None && r.value != 0 && r.os > 0 && r.os < 65536)
    return r
}

// NSendAll sends every byte of data.
NSendAll(io: Io, s: uint, data: []u8): bool {
    var off := 0
    for off < data.Len() {
        w := NativeSend(io, s, data[off..data.Len()])
        if w.err != core.SysErr.None || w.value <= 0 || Trunc[int](w.value) > data.Len() - off {
            return false
        }
        off += Trunc[int](w.value)
    }
    return true
}

// NEcho accepts one connection on l and echoes it until end of stream;
// done counts the bytes echoed, and 1 << 40 is added on a clean end.
NEcho(io: Io, l: uint, done: &raw.Atomic[u64]) {
    a := NativeAccept(io, l)
    if a.err != core.SysErr.None {
        return
    }
    s := Trunc[uint](a.value)
    var buf: [256]u8 = [0; 256]
    for {
        r := NativeRecv(io, s, mut buf[..])
        if r.err != core.SysErr.None {
            rt.Abandon(s)
            return
        }
        if r.value == 0 {
            break
        }
        n := Trunc[int](r.value)
        if !NSendAll(io, s, buf[0..n]) {
            rt.Abandon(s)
            return
        }
        _ = done.Add(Trunc[u64](n))
    }
    Assert(rt.CloseRuntimeHandle(s).err == core.SysErr.None)
    _ = done.Add(1 << 40)
}

// NRoundTrips dials port and makes rounds round trips of a 1 to 200 byte
// message with a pattern from seed; true when every byte came back.
NRoundTrips(io: Io, port: i64, rounds: int, seed: u64): bool {
    var ab: [32]u8 = [0; 32]
    d := NativeDial(io, NAddr(mut ab, port))
    if d.err != core.SysErr.None {
        return false
    }
    c := Trunc[uint](d.value)
    var out: [200]u8 = [0; 200]
    var back: [200]u8 = [0; 200]
    // A local set before every read and checked after it (the task
    // resumes with its frame intact).
    var x := seed | 1
    for k in 0..rounds {
        x = Mix(x)
        n := 1 + Trunc[int](x % 200)
        for i in 0..n {
            out[i] = Trunc[u8](x) ^ Trunc[u8](i)
        }
        guard := x ^ 0x5A5A
        if !NSendAll(io, c, out[0..n]) {
            rt.Abandon(c)
            return false
        }
        var got := 0
        for got < n {
            r := NativeRecv(io, c, mut back[got..n])
            if r.err != core.SysErr.None || r.value <= 0 {
                rt.Abandon(c)
                return false
            }
            got += Trunc[int](r.value)
            if guard != x ^ 0x5A5A {
                rt.Abandon(c)
                return false
            }
        }
        for i in 0..n {
            if back[i] != out[i] {
                rt.Abandon(c)
                return false
            }
        }
        _ = k
    }
    // End of stream for the server, then our own end of stream back.
    unsafe "c is a live socket record" {
        _ = sys.SocketShutdown((*rt.Rec(c)).os, 1)
    }
    r := NativeRecv(io, c, mut back[..])
    ok := r.err == core.SysErr.None && r.value == 0
    Assert(rt.CloseRuntimeHandle(c).err == core.SysErr.None)
    return ok
}

// NClient is one dialing task: ok gets 1 when every round trip matched.
NClient(io: Io, port: i64, rounds: int, seed: u64, ok: &raw.Atomic[u32]) {
    if NRoundTrips(io, port, rounds, seed) {
        _ = ok.Add(1)
    }
}

test "RT-Net-Echo listen, dial, accept and 500 round trips of varied sizes" (sys: &System) {
    l := NListen(sys.io)
    lh := Trunc[uint](l.value)
    done := raw.Atomic[u64].New(0)
    ok := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go NEcho(sys.io, lh, &done)
        s.go NClient(sys.io, l.os, 500, 7, &ok)
    }
    Assert(ok.Load() == 1)
    Assert(done.Load() >> 40 == 1)
    Assert(rt.CloseRuntimeHandle(lh).err == core.SysErr.None)
}

test "RT-F1-ParkHandshake 8 pairs, 2,000 round trips each, completions racing the park" (sys: &System) {
    // Each client writes the moment it has read, so the peer's recv often
    // completes on another carrier before its task has switched out
    // (MK_TEST_SEED adds a spin between PollSubmit and ParkUntil). The
    // scheduler stops the process on a second ready or a resume of a task
    // that is not runnable (a task is readied once per park); each client checks a local
    // after every read.
    l := NListen(sys.io)
    lh := Trunc[uint](l.value)
    done := raw.Atomic[u64].New(0)
    ok := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for k in 0..8 {
            s.go NEcho(sys.io, lh, &done)
            s.go NClient(sys.io, l.os, 2_000, Trunc[u64](k) + 11, &ok)
        }
    }
    Assert(ok.Load() == 8)
    Assert(done.Load() >> 40 == 8)
    Assert(rt.CloseRuntimeHandle(lh).err == core.SysErr.None)
}

test "RT-Full-ParkHandshake RT-F1 at section 7's size: 8 pairs, 62,500 round trips each (10^6 recvs) at 8 carriers" (sys: &System) {
    // RT-F1-ParkHandshake at full size: every round trip is two
    // recvs (the echo's and the client's), so 8 x 62,500 x 2 = 1,000,000
    // poller reads whose data is written by another task at once. The full
    // size wants MK_CARRIERS=8; with fewer carriers a tenth of it (10^5 reads)
    // keeps each run inside the harness's 60 s watchdog.
    rounds := if NWantCarriers() >= 8 { 62_500 } else { 6_250 }
    l := NListen(sys.io)
    lh := Trunc[uint](l.value)
    done := raw.Atomic[u64].New(0)
    ok := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for k in 0..8 {
            s.go NEcho(sys.io, lh, &done)
            s.go NClient(sys.io, l.os, rounds, Trunc[u64](k) + 31, &ok)
        }
    }
    Assert(ok.Load() == 8)
    Assert(done.Load() >> 40 == 8)
    Assert(rt.CloseRuntimeHandle(lh).err == core.SysErr.None)
}

// NWantCarriers is the configured carrier count (MK_CARRIERS).
NWantCarriers(): u32 {
    unsafe "the TaskState page Start mapped, which lives until exit" {
        return (*TS()).want.Load()
    }
}

// NRecvCancel blocks in a recv nobody answers; got: 1 Canceled, 2 other.
NRecvCancel(io: Io, c: uint, got: &raw.Atomic[u32]) {
    var buf: [16]u8 = [0; 16]
    r := NativeRecv(io, c, mut buf[..])
    if r.err == core.SysErr.Canceled {
        got.Store(1)
    } else {
        got.Store(2)
    }
}

// NAcceptCancel blocks in an accept nobody dials.
NAcceptCancel(io: Io, l: uint, got: &raw.Atomic[u32]) {
    r := NativeAccept(io, l)
    if r.err == core.SysErr.Canceled {
        got.Store(1)
    } else {
        if r.err == core.SysErr.None {
            rt.Abandon(Trunc[uint](r.value))
        }
        got.Store(2)
    }
}

// NPair is a connected pair: c dialed, a accepted.
struct NPair {
    c: uint
    a: uint
}

NAcceptInto(io: Io, l: uint, out: &raw.Atomic[u64]) {
    r := NativeAccept(io, l)
    if r.err == core.SysErr.None {
        out.Store(Trunc[u64](r.value))
    }
}

NMakePair(io: Io, l: core.IoResult): NPair {
    acc := raw.Atomic[u64].New(0)
    var c: uint = 0
    scope s := io.Scope() {
        s.go NAcceptInto(io, Trunc[uint](l.value), &acc)
        var ab: [32]u8 = [0; 32]
        d := NativeDial(io, NAddr(mut ab, l.os))
        Assert(d.err == core.SysErr.None)
        c = Trunc[uint](d.value)
    }
    Assert(acc.Load() != 0)
    return NPair(c: c, a: Trunc[uint](acc.Load()))
}

test "RT-Io-CancelInFlight a pending recv and a pending accept end with Canceled, and the sockets still work" (sys: &System) {
    l := NListen(sys.io)
    lh := Trunc[uint](l.value)
    p := NMakePair(sys.io, l)
    for round in 0..20 {
        g1 := raw.Atomic[u32].New(0)
        g2 := raw.Atomic[u32].New(0)
        t0 := BenchNow()
        scope s := sys.io.Scope() {
            s.go NRecvCancel(sys.io, p.c, &g1)
            s.go NAcceptCancel(sys.io, lh, &g2)
            // Both are parked in the poller (on the blocked list) before
            // the cancel, except on some rounds, where the cancel races the
            // submission.
            if round % 2 == 0 {
                CAwaitBlocked(&s, 2)
            }
            s.Cancel()
        }
        Assert(BenchNow() - t0 < 2_000_000_000)
        Assert(g1.Load() == 1 && g2.Load() == 1)
    }
    // The pair and the listener still work after twenty cancels.
    var msg: [3]u8 = [1, 2, 3]
    Assert(NSendAll(sys.io, p.a, msg[..]))
    var buf: [8]u8 = [0; 8]
    var got := 0
    for got < 3 {
        r := NativeRecv(sys.io, p.c, mut buf[got..8])
        Assert(r.err == core.SysErr.None && r.value > 0)
        got += Trunc[int](r.value)
    }
    Assert(got == 3 && buf[0] == 1 && buf[2] == 3)
    q := NMakePair(sys.io, l)
    rt.Abandon(q.a)
    rt.Abandon(q.c)
    rt.Abandon(p.a)
    rt.Abandon(p.c)
    rt.Abandon(lh)
}

test "RT-Net-Errors refused, address in use, invalid addresses, end of stream" (sys: &System) {
    // A port that was bound and closed: nothing listens there now.
    l := NListen(sys.io)
    port := l.os
    Assert(rt.CloseRuntimeHandle(Trunc[uint](l.value)).err == core.SysErr.None)
    var ab: [32]u8 = [0; 32]
    d := NativeDial(sys.io, NAddr(mut ab, port))
    Assert(d.err == core.SysErr.Refused)
    m := NListen(sys.io)
    var ab2: [32]u8 = [0; 32]
    dup := NativeListen(sys.io, NAddr(mut ab2, m.os))
    Assert(dup.err == core.SysErr.AddrInUse)
    Assert(NativeListen(sys.io, "example.com:80").err == core.SysErr.InvalidAddress)
    Assert(NativeListen(sys.io, "127.0.0.1").err == core.SysErr.InvalidAddress)
    Assert(NativeDial(sys.io, "127.0.0.1:65536").err == core.SysErr.InvalidAddress)
    Assert(NativeDial(sys.io, "::1:80").err == core.SysErr.InvalidAddress)
    // End of stream after the peer closes; a recv into an empty buffer is 0.
    p := NMakePair(sys.io, m)
    Assert(rt.CloseRuntimeHandle(p.a).err == core.SysErr.None)
    var buf: [8]u8 = [0; 8]
    r := NativeRecv(sys.io, p.c, mut buf[..])
    Assert(r.err == core.SysErr.None && r.value == 0)
    Assert(NativeRecv(sys.io, p.c, mut buf[0..0]).value == 0)
    rt.Abandon(p.c)
    rt.Abandon(Trunc[uint](m.value))
}

test "RT-Net-Leaks failing and cancelled socket entries leave no OS handle behind" (sys: &System) {
    // Warm-up: start every carrier (each is a thread handle), sleep on all
    // of them at once, then 100 rounds, three with a refused dial (std.sys
    // loads Winsock once and makes its high-resolution timer slots lazily).
    NStartCarriers(sys.io)
    scope s := sys.io.Scope() {
        for _ in 0..16 {
            s.go NNap(sys.io)
        }
    }
    for k in 0..100 {
        NLeakRound(sys.io, k < 3)
    }
    before := NHandles()
    if before < 0 {
        return
    }
    // 200 rounds without the refused dial (about 2 s each on Windows, which
    // retries the reset SYN). A leak on any path of a round shows as at
    // least 200. What may still appear is bounded: std.sys's lazily made
    // timer slots, two handles each, 64 at most (128); seen: +4 at 8
    // carriers in one run, more under load.
    for _ in 0..200 {
        NLeakRound(sys.io, false)
    }
    Assert(NHandles() - before <= 128)
}

// NAbort closes the socket h with a reset (linger on, 0 seconds), so it
// leaves no TIME_WAIT entry: a client that has read everything it waits
// for closes this way, and the peer's own close then ends at the reset
// too. A block that dials in a loop would otherwise hold a TIME_WAIT entry
// per connection for minutes, against the machine's socket budget.
NAbort(io: Io, h: uint) {
    _ = rt.SockOption(io, h, core.IoSockLinger, 1, 0)
    _ = NativeClose(io, h)
}

// NNap sleeps 1 ms twenty times.
NNap(io: Io) {
    for _ in 0..20 {
        io.Sleep(core.DurationOf(1_000_000)) else {
            return
        }
    }
}

// NStartCarriers spreads busy tasks until every carrier MK_CARRIERS allows
// has started (at most 50 tries).
NStartCarriers(io: Io) {
    for _ in 0..50 {
        unsafe "the TaskState page Start mapped, which lives until exit" {
            if (*TS()).ncarriers.Load() >= (*TS()).want.Load() {
                return
            }
        }
        mask := raw.Atomic[u64].New(0)
        count := raw.Atomic[u32].New(0)
        scope s := io.Scope() {
            for _ in 0..32 {
                s.go MnMark(io, &mask, &count)
            }
        }
    }
}

// NHandles is the process's open OS handles, or -1 (sys.HandleCount).
NHandles(): int {
    return sys.HandleCount()
}

// NLeakRound runs every failure path of the socket entries once.
NLeakRound(io: Io, refuse: bool) {
    l := NListen(io)
    port := l.os
    var ab: [32]u8 = [0; 32]
    Assert(NativeListen(io, NAddr(mut ab, port)).err == core.SysErr.AddrInUse)
    g := raw.Atomic[u32].New(0)
    scope s := io.Scope() {
        s.go NAcceptCancel(io, Trunc[uint](l.value), &g)
        CAwaitBlocked(&s, 1)
        s.Cancel()
    }
    Assert(g.Load() == 1)
    // A dial cancelled before it starts makes no socket.
    scope s2 := io.Scope() {
        s2.Cancel()
        var ab2: [32]u8 = [0; 32]
        Assert(NativeDial(io, NAddr(mut ab2, port)).err == core.SysErr.Canceled)
    }
    Assert(rt.CloseRuntimeHandle(Trunc[uint](l.value)).err == core.SysErr.None)
    if refuse {
        var ab3: [32]u8 = [0; 32]
        Assert(NativeDial(io, NAddr(mut ab3, port)).err == core.SysErr.Refused)
    }
}

// NParse is ParseAddr's verdict, family, port and first and last address
// bytes, for the table below.
NParse(s: str, listen: bool, ok: bool, v6: bool, port: u16, first: u8, last: u8) {
    var a := sys.SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0)
    got := rt.ParseAddr(s, listen, mut a)
    Assert(got == ok)
    if ok {
        fam := if v6 { sys.Inet6 } else { sys.Inet4 }
        Assert(a.family == fam && a.port == port && a.addr[0] == first)
        if !v6 {
            Assert(a.addr[3] == last)
        } else {
            Assert(a.addr[15] == last)
        }
    }
}

test "RT-Net-ParseAddr IPv4, bracketed IPv6, localhost, empty hosts and malformed input" (sys: &System) {
    NParse("127.0.0.1:80", false, true, false, 80, 127, 1)
    NParse("0.0.0.0:0", true, true, false, 0, 0, 0)
    NParse("255.255.255.255:65535", false, true, false, 65535, 255, 255)
    NParse(":8080", true, true, false, 8080, 0, 0)
    NParse(":8080", false, true, false, 8080, 127, 1)
    NParse("LocalHost:1", false, true, false, 1, 127, 1)
    NParse("[::1]:443", false, true, true, 443, 0, 1)
    NParse("[::]:0", true, true, true, 0, 0, 0)
    NParse("[fe80::1%7]:9", false, true, true, 9, 0xFE, 1)
    NParse("[1:2:3:4:5:6:7:8]:1", false, true, true, 1, 0, 8)
    NParse("[1:2:3:4:5:6:7::]:1", false, true, true, 1, 0, 0)
    NParse("[::ffff:10.0.0.9]:5", false, true, true, 5, 0, 9)
    NParse("[ABCD::Ef]:5", false, true, true, 5, 0xAB, 0xEF)
    NParse("127.0.0.1:080", false, true, false, 80, 127, 1)
    // Rejected.
    NParse("", false, false, false, 0, 0, 0)
    NParse(":", false, false, false, 0, 0, 0)
    NParse("127.0.0.1", false, false, false, 0, 0, 0)
    NParse("127.0.0.1:", false, false, false, 0, 0, 0)
    NParse("127.0.0.1:65536", false, false, false, 0, 0, 0)
    NParse("127.0.0.1:123456", false, false, false, 0, 0, 0)
    NParse("127.0.0.1:-1", false, false, false, 0, 0, 0)
    NParse("127.0.0.01:1", false, false, false, 0, 0, 0)
    NParse("256.0.0.1:1", false, false, false, 0, 0, 0)
    NParse("1.2.3:1", false, false, false, 0, 0, 0)
    NParse("1.2.3.4.5:1", false, false, false, 0, 0, 0)
    NParse("1..3.4:1", false, false, false, 0, 0, 0)
    NParse("1.2.3.4 :1", false, false, false, 0, 0, 0)
    NParse("example.com:80", false, false, false, 0, 0, 0)
    NParse("::1:80", false, false, false, 0, 0, 0)
    NParse("[::1]", false, false, false, 0, 0, 0)
    NParse("[::1]80", false, false, false, 0, 0, 0)
    NParse("[::1:80", false, false, false, 0, 0, 0)
    NParse("[1.2.3.4]:80", false, false, false, 0, 0, 0)
    NParse("[1:2:3:4:5:6:7:8:9]:1", false, false, false, 0, 0, 0)
    NParse("[1:2:3:4:5:6:7:8::]:1", false, false, false, 0, 0, 0)
    NParse("[1::2::3]:1", false, false, false, 0, 0, 0)
    NParse("[:1::2]:1", false, false, false, 0, 0, 0)
    NParse("[1::2:]:1", false, false, false, 0, 0, 0)
    NParse("[12345::]:1", false, false, false, 0, 0, 0)
    NParse("[fe80::1%]:1", false, false, false, 0, 0, 0)
    NParse("[fe80::1%eth0]:1", false, false, false, 0, 0, 0)
    NParse("[fe80::1%4294967296]:1", false, false, false, 0, 0, 0)
    NParse("[::ffff:1.2.3]:1", false, false, false, 0, 0, 0)
    NParse("[1:2:3:4:5:6:7:1.2.3.4]:1", false, false, false, 0, 0, 0)
}

// NLocal writes "localhost:<port>" into b.
NLocal(b: mut [32]u8, port: i64): str from b {
    return NNamed(b, "localhost:", port)
}

// NNamed writes host (with its colon) and port into b.
NNamed(b: mut [32]u8, host: str, port: i64): str from b {
    pre := host.AsBytes()
    for i in 0..pre.Len() {
        b[i] = pre[i]
    }
    var d: [8]u8 = [0; 8]
    var n := 0
    var x := Trunc[int](port)
    for {
        d[n] = Trunc[u8](48 + x % 10)
        n += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    for i in 0..n {
        b[pre.Len() + i] = d[n - 1 - i]
    }
    return str.From(b[0..pre.Len() + n]).Must()
}

// NResolveAll sets the resolver test switch (DialResolve) for the calling
// task.
NResolveAll(on: bool) {
    if on {
        DialResolve.Store(u64(raw.Addr(Current())))
    } else {
        DialResolve.Store(0)
    }
}

test "RT-15-Resolve a dial by name goes through the resolver on Windows and Linux, and a bad name is refused with no call" (sys: &System) {
    // No lookup that needs the network: on Windows, "example.invalid"
    // (RFC 6761) took 11 s here (a DNS query; std.sys's Resolve probe covers
    // the refusal). Not a host name, or no port: refused before the resolver.
    Assert(NativeDial(sys.io, "bad name:80").err == core.SysErr.InvalidAddress)
    Assert(NativeDial(sys.io, "host:x").err == core.SysErr.InvalidAddress)
    Assert(NativeDial(sys.io, ":").err == core.SysErr.InvalidAddress)
    l := NListen(sys.io)
    // "localhost" through the resolver (the switch), so no network is needed.
    NResolveAll(true)
    var ab: [32]u8 = [0; 32]
    d := NativeDial(sys.io, NLocal(mut ab, l.os))
    NResolveAll(false)
    Assert(d.err == core.SysErr.None)
    rt.Abandon(Trunc[uint](d.value))
    rt.Abandon(Trunc[uint](l.value))
}

test "RT-15-DialAll on Linux a dial by name tries each answer of the resolve entry in order: ::1 is refused, then 127.0.0.1 connects" (sys: &System) {
    if PWin() {
        return
    }
    name := "mkx-dial-hosts.txt"
    o := NativeOpen(sys.io, name, 1)
    Assert(o.err == core.SysErr.None)
    h := Trunc[uint](o.value)
    _ = DPutText(sys.io, h, 0, "::1 mkx-dial.invalid\n127.0.0.1 mkx-dial.invalid\n")
    Assert(NativeClose(sys.io, h).err == core.SysErr.None)
    unsafe "name is static text" {
        HostsTest.path_at.Store(u64(raw.Addr(raw.StrData(name))))
    }
    HostsTest.path_len.Store(Trunc[u64](name.Len()))
    // A name under .invalid: should the file be missed, DnsLookup answers
    // NotFound with no query.
    defer HostsTest.path_len.Store(0)
    l := NListen(sys.io)
    var ab: [32]u8 = [0; 32]
    d := NativeDial(sys.io, NNamed(mut ab, "mkx-dial.invalid:", l.os))
    Assert(d.err == core.SysErr.None)
    // The connection is the listener's: its peer is 127.0.0.1.
    p := NAddrOf(sys.io, Trunc[uint](d.value), true)
    Assert(p.addr[0] == 127 && p.addr[3] == 1 && i64(p.port) == l.os)
    rt.Abandon(Trunc[uint](d.value))
    rt.Abandon(Trunc[uint](l.value))
    Assert(NativeRemove(sys.io, name).err == core.SysErr.None)
}

// NAddrOf is SockAddress of h (peer or local) as a sys.SockAddr.
NAddrOf(io: Io, h: uint, peer: bool): sys.SockAddr {
    var ip: [16]u8 = [0; 16]
    var w := core.IoWords(a: 0, b: 0, c: 0, d: 0)
    r := rt.SockAddress(io, h, peer, mut ip[..], mut w)
    Assert(r.err == core.SysErr.None)
    return sys.SockAddr(family: if w.b == 1 { sys.Inet6 } else { sys.Inet4 }, port: Trunc[u16](w.a), addr: ip,
        scope_id: Trunc[u32](w.c))
}

// NSameAddr: a and b name the same family, port and address bytes.
NSameAddr(a: sys.SockAddr, b: sys.SockAddr): bool {
    if a.family != b.family || a.port != b.port {
        return false
    }
    for i in 0..16 {
        if a.addr[i] != b.addr[i] {
            return false
        }
    }
    return true
}

test "the native accept keeps the peer's address its op returned, and SockAddress answers it with no getpeername" (sys: &System) {
    NPeerKept(sys.io)
}

// NPeerKept is the accept-peer block's body (a helper, so the module sys is in
// scope, which the block's sys parameter would hide).
NPeerKept(io: Io) {
    l := NListen(io)
    p := NMakePair(io, l)
    local := NAddrOf(io, p.c, false)
    Assert(local.family == sys.Inet4 && local.port != 0 && local.addr[0] == 127)
    unsafe "p.a and p.c are live runtime handles of this block" {
        // The accepted socket's record holds the dialler's address; a
        // dialled socket's holds none.
        Assert(NSameAddr((*rt.Rec(p.a)).peer, local))
        Assert((*rt.Rec(p.c)).peer.family == 0)
        Assert(NSameAddr(NAddrOf(io, p.a, true), local))
        // SockAddress answers from the record, not from the OS: a value
        // planted there comes back, which getpeername would not give.
        (*rt.Rec(p.a)).peer = sys.SockAddr(family: sys.Inet4, port: 7,
            addr: [10, 9, 8, 7, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0], scope_id: 0)
        planted := NAddrOf(io, p.a, true)
        Assert(planted.port == 7 && planted.addr[0] == 10 && planted.addr[3] == 7)
    }
    // A dialled socket's peer is still getpeername's: the listener's port.
    Assert(i64(NAddrOf(io, p.c, true).port) == l.os)
    // The local address of the accepted socket is never the record's.
    Assert(i64(NAddrOf(io, p.a, false).port) == l.os)
    NAbort(io, p.c)
    NAbort(io, p.a)
    _ = NativeClose(io, Trunc[uint](l.value))
    // An op that wrote no address keeps no peer: NativeAccept presets the
    // listener's family, so only the port tells it from an answer.
    Assert(NSameAddr(AcceptedPeer(local), local))
    Assert(AcceptedPeer(sys.SockAddr(family: sys.Inet4, port: 0, addr: [0; 16], scope_id: 0)).family == 0)
    Assert(AcceptedPeer(sys.SockAddr(family: sys.Inet6, port: 0, addr: [0; 16], scope_id: 0)).family == 0)
    Assert(AcceptedPeer(sys.SockAddr(family: sys.Unix, port: 9, addr: [0; 16], scope_id: 0)).family == 0)
}
