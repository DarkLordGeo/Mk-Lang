// Why: the DNS stub resolver (io_dns.mk) parses untrusted bytes from the
// network and must handle every answer shape a real nameserver sends.
// What: the RT-Os-Dns and RT-Os-Hosts blocks, a fake nameserver (DServe, DUdpServer,
// DTcpServer) and the helpers DAnswer, DLook, DUp and DDown.
// How: a fake nameserver on loopback (UDP and TCP on one port, the
// DnsTest switches pointing the resolver at it) answers by name: two A and
// one AAAA, a CNAME chain written with compression pointers, NXDOMAIN,
// SERVFAIL, a truncated UDP answer completed over TCP, a reply with a wrong
// id before the right one, a query dropped once (the retry), and replies
// whose names loop or point out of bounds (rejected, never followed). The
// parser also meets crafted messages directly. Both targets (the resolver
// is Mk code on both; Windows' NativeResolveAll asks GetAddrInfoW instead).

import std.core
import std.raw

// DPut16 writes v big-endian at m[i].
DPut16(m: mut []u8, i: int, v: int) {
    m[i] = Trunc[u8](v >> 8)
    m[i + 1] = Trunc[u8](v)
}

// DAnswer appends one record at m[p]: owner a compression pointer to own,
// type t, class IN, TTL 60, rdata d. The new end.
DAnswer(m: mut []u8, p: int, own: int, t: int, d: []u8): int {
    DPut16(m, p, 0xC000 | own)
    DPut16(m, p + 2, t)
    DPut16(m, p + 4, 1)
    DPut16(m, p + 6, 0)
    DPut16(m, p + 8, 60)
    DPut16(m, p + 10, d.Len())
    for k in 0..d.Len() {
        m[p + 12 + k] = d[k]
    }
    return p + 12 + d.Len()
}

// DIs: name is s.
DIs(name: []u8, s: str): bool {
    return SameName(name, s.AsBytes())
}

// DCounts are the fake server's per-name query counters (Add returns the
// count before it): drop.test, then six.test's AAAA queries, then every
// query the server saw (the blocks that must ask nothing read it).
struct DCounts {
    drops: raw.Atomic[u32]
    six: raw.Atomic[u32]
    seen: raw.Atomic[u32]
}

// DServe builds the reply to query q (n bytes) into m for the transport
// (tcp true over TCP): its length, 0 to send nothing.
DServe(q: []u8, m: mut []u8, tcp: bool, cs: &DCounts): int {
    _ = cs.seen.Add(1)
    drops := &cs.drops
    var nb: [256]u8 = [0; 256]
    nm := DnsName(q, 12, mut nb)
    if nm.next < 0 || nm.next + 4 > q.Len() {
        return 0
    }
    name := nb[0..nm.n]
    qt := int(Get16At(q, nm.next))
    qend := nm.next + 4
    for k in 0..qend {
        m[k] = q[k]
    }
    var flags := 0x8180
    var an := 0
    var p := qend
    var a1: [4]u8 = [10, 0, 0, 1]
    var a2: [4]u8 = [10, 0, 0, 2]
    var six: [16]u8 = [0x20, 0x01, 0x0D, 0xB8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1]
    if DIs(name, "a.test") {
        if qt == 1 {
            p = DAnswer(m, p, 12, 1, a1[..])
            p = DAnswer(m, p, 12, 1, a2[..])
            an = 2
        } else {
            p = DAnswer(m, p, 12, 28, six[..])
            an = 1
        }
    } else if DIs(name, "c.test") {
        // c.test CNAME x.c.test (its labels "x" then a pointer to the
        // question's "c.test"), then x.c.test's A by a pointer to the
        // CNAME's rdata; a record of another owner in between is skipped.
        var t: [4]u8 = [1, 120, 0xC0, 12]
        rd := p + 12
        p = DAnswer(m, p, 12, 5, t[..])
        var other: [4]u8 = [10, 9, 9, 9]
        p = DAnswer(m, p, qend - 4 - 0, 1, other[..])
        an = 2
        if qt == 1 {
            var a9: [4]u8 = [10, 0, 0, 9]
            p = DAnswer(m, p, rd, 1, a9[..])
            an = 3
        }
    } else if DIs(name, "tc.test") && !tcp {
        flags |= 0x0200
    } else if DIs(name, "tc.test") {
        if qt == 1 {
            var a7: [4]u8 = [10, 0, 0, 7]
            p = DAnswer(m, p, 12, 1, a7[..])
            an = 1
        }
    } else if DIs(name, "servfail.test") {
        flags |= 2
    } else if DIs(name, "drop.test") {
        if drops.Add(1) < 2 {
            return 0
        }
        if qt == 1 {
            var a3: [4]u8 = [10, 0, 0, 3]
            p = DAnswer(m, p, 12, 1, a3[..])
            an = 1
        }
    } else if DIs(name, "loop.test") {
        // An owner name that points at itself: never followed.
        DPut16(m, p, 0xC000 | p)
        DPut16(m, p + 2, 1)
        DPut16(m, p + 4, 1)
        DPut16(m, p + 6, 0)
        DPut16(m, p + 8, 60)
        DPut16(m, p + 10, 4)
        for k in 0..4 {
            m[p + 12 + k] = a1[k]
        }
        p += 16
        an = 1
    } else if DIs(name, "short.example.test") {
        if qt == 1 {
            var a5: [4]u8 = [10, 0, 0, 5]
            p = DAnswer(m, p, 12, 1, a5[..])
            an = 1
        }
    } else if DIs(name, "aonly.test") || DIs(name, "adrop.test") {
        // A answers, AAAA fails (SERVFAIL) or is dropped.
        if qt == 1 {
            var a6: [4]u8 = [192, 0, 2, 1]
            p = DAnswer(m, p, 12, 1, a6[..])
            an = 1
        } else if DIs(name, "adrop.test") {
            return 0
        } else {
            flags |= 2
        }
    } else if DIs(name, "six.test") {
        // A has no records; AAAA fails once (SERVFAIL), then answers: only
        // AAAA is asked again.
        if qt == 28 {
            if cs.six.Add(1) == 0 {
                flags |= 2
            } else {
                var s6: [16]u8 = [0x20, 0x01, 0x0D, 0xB8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 6]
                p = DAnswer(m, p, 12, 28, s6[..])
                an = 1
            }
        }
    } else if (DIs(name, "huge.test") || DIs(name, "huge2.test")) && !tcp {
        flags |= 0x0200
    } else if DIs(name, "dropall.test") {
        return 0
    } else {
        flags |= 3
    }
    DPut16(m, 2, flags)
    DPut16(m, 6, an)
    DPut16(m, 8, 0)
    DPut16(m, 10, 0)
    return p
}

// DUdpServer answers queries on u until cancelled; for id.test it first
// sends a reply with the id flipped (dropped by the resolver).
DUdpServer(io: Io, u: uint, drops: &DCounts) {
    var q: [512]u8 = [0; 512]
    var m: [512]u8 = [0; 512]
    for {
        var from := core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0)
        r := NativeRecvFrom(io, u, mut q[..], mut from)
        if r.err != core.SysErr.None {
            return
        }
        n := Trunc[int](r.value)
        if n < 12 {
            continue
        }
        var nb: [256]u8 = [0; 256]
        nm := DnsName(q[0..n], 12, mut nb)
        if nm.next > 0 && DIs(nb[0..nm.n], "id.test") {
            var bad: [512]u8 = [0; 512]
            for k in 0..n {
                bad[k] = q[k]
            }
            bad[0] ^= 0x55
            DPut16(mut bad[..], 2, 0x8180)
            _ = NativeSendTo(io, u, bad[0..n], from)
            var good: [512]u8 = [0; 512]
            for k in 0..n {
                good[k] = q[k]
            }
            DPut16(mut good[..], 2, 0x8180)
            p := n
            if Get16At(q[0..n], nm.next) == 1 {
                var a4: [4]u8 = [10, 0, 0, 4]
                e := DAnswer(mut good[..], p, 12, 1, a4[..])
                DPut16(mut good[..], 6, 1)
                _ = NativeSendTo(io, u, good[0..e], from)
            } else {
                _ = NativeSendTo(io, u, good[0..p], from)
            }
            continue
        }
        k := DServe(q[0..n], mut m[..], false, drops)
        if k > 0 {
            _ = NativeSendTo(io, u, m[0..k], from)
        }
    }
}

// DHuge answers huge.test over TCP with the largest message a prefix can
// name: FF FF, then 65,535 bytes (the reply, padded with zeros: an A of
// 10.0.0.8, no AAAA). huge2.test gets the prefix FF FE and 100 bytes, then
// the close. Send failures are ignored (the client may give up).
DHuge(io: Io, c: uint, q: []u8, qend: int, qt: int, short: bool) {
    var m: [600]u8 = [0; 600]
    if short {
        m[0] = 0xFF
        m[1] = 0xFE
        _ = NSendAll(io, c, m[0..102])
        return
    }
    m[0] = 0xFF
    m[1] = 0xFF
    for k in 0..qend {
        m[2 + k] = q[k]
    }
    var p := 2 + qend
    DPut16(mut m[..], 4, 0x8180)
    DPut16(mut m[..], 8, 0)
    DPut16(mut m[..], 10, 0)
    DPut16(mut m[..], 12, 0)
    if qt == 1 {
        var a8: [4]u8 = [10, 0, 0, 8]
        p = 2 + DAnswer(mut m[2..], p - 2, 12, 1, a8[..])
        DPut16(mut m[..], 8, 1)
    }
    if !NSendAll(io, c, m[0..p]) {
        return
    }
    var left := 65535 - (p - 2)
    var zero: [4096]u8 = [0; 4096]
    for left > 0 {
        var k := left
        if k > 4096 {
            k = 4096
        }
        if !NSendAll(io, c, zero[0..k]) {
            return
        }
        left -= k
    }
}

// DTcpServer answers one length-prefixed query per connection on l until
// cancelled.
DTcpServer(io: Io, l: uint, drops: &DCounts) {
    var q: [514]u8 = [0; 514]
    var m: [514]u8 = [0; 514]
    for {
        a := NativeAccept(io, l)
        if a.err != core.SysErr.None {
            return
        }
        c := Trunc[uint](a.value)
        var got := 0
        var want := 2
        var good := true
        for got < want && good {
            r := NativeRecv(io, c, mut q[got..want])
            if r.err != core.SysErr.None || r.value <= 0 {
                good = false
            } else {
                got += Trunc[int](r.value)
                if want == 2 && got == 2 {
                    want = 2 + (int(q[0]) << 8 | int(q[1]))
                    if want > 514 {
                        good = false
                    }
                }
            }
        }
        if good {
            var nb: [256]u8 = [0; 256]
            nm := DnsName(q[2..want], 12, mut nb)
            if nm.next > 0 && nm.next + 4 <= want - 2 && (DIs(nb[0..nm.n], "huge.test") ||
                DIs(nb[0..nm.n], "huge2.test")) {
                DHuge(io, c, q[2..want], nm.next + 4, int(Get16At(q[2..want], nm.next)),
                    DIs(nb[0..nm.n], "huge2.test"))
                good = false
            }
        }
        if good {
            k := DServe(q[2..want], mut m[2..], true, drops)
            DPut16(mut m[..], 0, k)
            _ = NSendAll(io, c, m[0..k + 2])
        }
        _ = NativeClose(io, c)
    }
}

// DConf is the fake server's resolv.conf (one server, short timeouts, a
// search domain).
const DConf: str = "# fake nameserver\nnameserver 127.0.0.1\nsearch example.test.\noptions timeout:1 attempts:2 ndots:1\n"

// DLook resolves name with DnsLookup into out; the result.
DLook(io: Io, name: str, out: mut [8]core.IoAddr): core.IoResult {
    return DnsLookup(io, name.AsBytes(), mut out[..])
}

DIp4(a: core.IoAddr, b0: u8, b1: u8, b2: u8, b3: u8): bool {
    return !a.v6 && a.ip[0] == b0 && a.ip[1] == b1 && a.ip[2] == b2 && a.ip[3] == b3
}

test "RT-Os-Dns the stub resolver against a fake nameserver on loopback" (sys: &System) {
    (u: uh, l: lh) := DUp(sys.io)
    drops := DCounts(drops: raw.Atomic[u32].New(0), six: raw.Atomic[u32].New(0), seen: raw.Atomic[u32].New(0))
    var out: [8]core.IoAddr = [core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0); 8]
    scope s := sys.io.Scope() {
        s.go DUdpServer(sys.io, uh, &drops)
        s.go DTcpServer(sys.io, lh, &drops)
        // A then AAAA, in the answer's order; a final dot is the same name.
        r := DLook(sys.io, "a.test", mut out)
        Assert(r.err == core.SysErr.None && r.value == 3)
        Assert(DIp4(out[0], 10, 0, 0, 1) && DIp4(out[1], 10, 0, 0, 2))
        Assert(out[2].v6 && out[2].ip[0] == 0x20 && out[2].ip[15] == 1)
        Assert(DLook(sys.io, "A.Test.", mut out).value == 3)
        // A shorter out takes the first answers.
        var two: [2]core.IoAddr = [core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0); 2]
        Assert(DnsLookup(sys.io, "a.test".AsBytes(), mut two[..]).value == 2 && DIp4(two[1], 10, 0, 0, 2))
        // The CNAME chain, through compression pointers.
        r2 := DLook(sys.io, "c.test", mut out)
        Assert(r2.err == core.SysErr.None && r2.value == 1 && DIp4(out[0], 10, 0, 0, 9))
        // NXDOMAIN on the name and on its search form: NotFound.
        Assert(DLook(sys.io, "nx.test", mut out).err == core.SysErr.NotFound)
        // Truncated over UDP, answered over TCP.
        r3 := DLook(sys.io, "tc.test", mut out)
        Assert(r3.err == core.SysErr.None && r3.value == 1 && DIp4(out[0], 10, 0, 0, 7))
        // A wrong id first: dropped, the right reply taken.
        r4 := DLook(sys.io, "id.test", mut out)
        Assert(r4.err == core.SysErr.None && r4.value == 1 && DIp4(out[0], 10, 0, 0, 4))
        // The search list first for a name with fewer than ndots dots.
        r5 := DLook(sys.io, "short", mut out)
        Assert(r5.err == core.SysErr.None && r5.value == 1 && DIp4(out[0], 10, 0, 0, 5))
        // SERVFAIL passes the server over; the search form is NXDOMAIN.
        Assert(DLook(sys.io, "servfail.test", mut out).err == core.SysErr.NotFound)
        // Both queries dropped once: the second attempt (after 1 s) answers.
        r6 := DLook(sys.io, "drop.test", mut out)
        Assert(r6.err == core.SysErr.None && r6.value == 1 && DIp4(out[0], 10, 0, 0, 3))
        // A looping owner name: the reply is bad, so no server answered
        // for the name (2 attempts of 1 s), and the search form is NXDOMAIN.
        Assert(DLook(sys.io, "loop.test", mut out).err == core.SysErr.NotFound)
        // RFC 6761: .invalid is NotFound with no query (the server would
        // answer any name).
        Assert(DLook(sys.io, "x.INVALID.", mut out).err == core.SysErr.NotFound)
        Assert(DLook(sys.io, "notinvalid", mut out).err == core.SysErr.NotFound)
        s.Cancel()
    }
    DDown(sys.io, uh, lh)
}

test "RT-Os-Dns-Parse crafted messages: loops, pointers out of bounds, truncation, ids" (sys: &System) {
    // A query for a.test, id 0x1234, and replies built on it.
    var q: [64]u8 = [0; 64]
    n := DnsQuery(mut q[..], 0x1234, "a.test".AsBytes(), 1)
    Assert(n == 12 + 8 + 4)
    // Bad query names: an empty label, a label of 64, a name past 253.
    var big: [300]u8 = [97; 300]
    var bq: [64]u8 = [0; 64]
    Assert(DnsQuery(mut bq[..], 1, "a..b".AsBytes(), 1) == 0)
    Assert(DnsQuery(mut bq[..], 1, big[0..64], 1) == 0)
    for i in 0..5 {
        big[i * 60 + 59] = 46
    }
    var huge: [600]u8 = [0; 600]
    Assert(DnsQuery(mut huge[..], 1, big[0..254], 1) == 0)
    var m: [128]u8 = [0; 128]
    for k in 0..n {
        m[k] = q[k]
    }
    DPut16(mut m[..], 2, 0x8180)
    var a1: [4]u8 = [10, 0, 0, 1]
    e := DAnswer(mut m[..], n, 12, 1, a1[..])
    DPut16(mut m[..], 6, 1)
    var r := NewDnsReply()
    DnsParse(m[0..e], 0x1234, "a.test".AsBytes(), 1, mut r)
    Assert(r.ok && r.n == 1 && DIp4(r.addrs[0], 10, 0, 0, 1))
    // Another id, another name, another type, a query not a reply: not ok.
    DnsParse(m[0..e], 0x1235, "a.test".AsBytes(), 1, mut r)
    Assert(!r.ok)
    DnsParse(m[0..e], 0x1234, "b.test".AsBytes(), 1, mut r)
    Assert(!r.ok)
    DnsParse(m[0..e], 0x1234, "a.test".AsBytes(), 28, mut r)
    Assert(!r.ok)
    DPut16(mut m[..], 2, 0x0180)
    DnsParse(m[0..e], 0x1234, "a.test".AsBytes(), 1, mut r)
    Assert(!r.ok)
    DPut16(mut m[..], 2, 0x8180)
    // Every cut of the message is refused (no read past its end).
    for cut in 0..e {
        DnsParse(m[0..cut], 0x1234, "a.test".AsBytes(), 1, mut r)
        Assert(!r.ok || r.n == 0)
    }
    // An answer owner that points forward, at itself, or past the end.
    for t in [e, n, 127] {
        DPut16(mut m[..], n, 0xC000 | t)
        DnsParse(m[0..e], 0x1234, "a.test".AsBytes(), 1, mut r)
        Assert(!r.ok)
    }
    // Two pointers that point at each other: never followed.
    var lp: [32]u8 = [0; 32]
    DPut16(mut lp[..], 12, 0xC00E)
    DPut16(mut lp[..], 14, 0xC00C)
    var nb: [256]u8 = [0; 256]
    Assert(DnsName(lp[..], 12, mut nb).next < 0 && DnsName(lp[..], 14, mut nb).next < 0)
    // Label types 0x40 and 0x80 are refused; a name past 255 bytes too.
    lp[12] = 0x41
    Assert(DnsName(lp[..], 12, mut nb).next < 0)
    lp[12] = 0x81
    Assert(DnsName(lp[..], 12, mut nb).next < 0)
    var long: [300]u8 = [0; 300]
    for i in 0..5 {
        long[i * 60] = 59
        for k in 1..60 {
            long[i * 60 + k] = 97
        }
    }
    Assert(DnsName(long[..], 0, mut nb).next < 0)
    // Four such labels (239 bytes) are a good name.
    long[240] = 0
    w := DnsName(long[..], 0, mut nb)
    Assert(w.next == 241 && w.n == 239)
    // Truncated and NXDOMAIN replies are well formed, with no addresses.
    DPut16(mut m[..], n, 0xC00C)
    DPut16(mut m[..], 2, 0x8380)
    DnsParse(m[0..e], 0x1234, "a.test".AsBytes(), 1, mut r)
    Assert(r.ok && r.tc && r.n == 0)
    DPut16(mut m[..], 2, 0x8183)
    DnsParse(m[0..e], 0x1234, "a.test".AsBytes(), 1, mut r)
    Assert(r.ok && r.rcode == 3 && r.n == 0)
    // The configuration: three servers at most, numeric only, options
    // clamped, the last search line wins.
    var c := NewDnsConf()
    DnsParseConf("nameserver 10.0.0.1\nnameserver ::1 # c\nnameserver host.name\nnameserver 10.0.0.3\nnameserver 10.0.0.4\nsearch a.b c\ndomain d.e\noptions timeout:99 attempts:0 ndots:3\n".AsBytes(), mut c)
    Assert(c.nservers == 3 && DIp4(c.servers[0], 10, 0, 0, 1) && c.servers[1].v6 && DIp4(c.servers[2], 10, 0, 0, 3))
    Assert(c.nsearch == 1 && c.timeout_ns == 30_000_000_000 && c.attempts == 2 && c.ndots == 3)
    var e2 := NewDnsConf()
    DnsParseConf("".AsBytes(), mut e2)
    Assert(e2.nservers == 1 && DIp4(e2.servers[0], 127, 0, 0, 1) && e2.ndots == 1 && e2.attempts == 2)
}

// DUp starts the fake nameserver's sockets on loopback and points the
// resolver at them (DConf): the UDP and TCP handles, on one port. The TCP
// listener comes first, on a port the OS picks, and the UDP socket binds
// the same port. Windows hands ports out in order, and Hyper-V and WSL
// exclude ranges of 100 ports for one protocol only (netsh int ipv4 show
// excludedportrange; seen on a development machine, where the seeded stress
// runs walked into them), so a refused port is tried again with the next
// listener, the refused ones held open meanwhile so each try gets a new
// port, up to 256 tries (past any one range).
DUp(io: Io): (u: uint, l: uint) {
    var lo: [16]u8 = [0; 16]
    lo[0] = 127
    lo[3] = 1
    var l := NListen(io)
    Assert(l.err == core.SysErr.None)
    var u := NativeUdp(io, core.IoAddr(ip: lo, port: Trunc[u16](l.os), v6: false, zone: 0))
    var spare: [256]uint = [0; 256]
    var tries := 0
    for u.err != core.SysErr.None && tries < 256 {
        spare[tries] = Trunc[uint](l.value)
        l = NListen(io)
        Assert(l.err == core.SysErr.None)
        u = NativeUdp(io, core.IoAddr(ip: lo, port: Trunc[u16](l.os), v6: false, zone: 0))
        tries += 1
    }
    for k in 0..tries {
        PClose(io, spare[k])
    }
    Assert(u.err == core.SysErr.None)
    DnsTest.port.Store(Trunc[u32](l.os))
    unsafe "DConf is static text" {
        DnsTest.conf_at.Store(u64(raw.Addr(raw.StrData(DConf))))
    }
    DnsTest.conf_len.Store(Trunc[u64](DConf.Len()))
    return (u: Trunc[uint](u.value), l: Trunc[uint](l.value))
}

// DDown undoes DUp.
DDown(io: Io, u: uint, l: uint) {
    DnsTest.conf_len.Store(0)
    DnsTest.port.Store(0)
    PClose(io, u)
    PClose(io, l)
}

test "RT-Os-Dns-Families a family that fails or is silent never hides the other's addresses" (sys: &System) {
    (u: u, l: l) := DUp(sys.io)
    cs := DCounts(drops: raw.Atomic[u32].New(0), six: raw.Atomic[u32].New(0), seen: raw.Atomic[u32].New(0))
    var out: [8]core.IoAddr = [core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0); 8]
    scope s := sys.io.Scope() {
        s.go DUdpServer(sys.io, u, &cs)
        s.go DTcpServer(sys.io, l, &cs)
        // A answers, AAAA is SERVFAIL on every attempt: the A address, at once.
        t0 := NativeNow(sys.io).value
        r := DLook(sys.io, "aonly.test", mut out)
        Assert(r.err == core.SysErr.None && r.value == 1 && DIp4(out[0], 192, 0, 2, 1))
        Assert(NativeNow(sys.io).value - t0 < 900_000_000)
        // A answers, AAAA is dropped: the A address after one timeout (1 s),
        // not after every attempt (2 s) and the search form.
        t1 := NativeNow(sys.io).value
        r2 := DLook(sys.io, "adrop.test", mut out)
        Assert(r2.err == core.SysErr.None && r2.value == 1 && DIp4(out[0], 192, 0, 2, 1))
        Assert(NativeNow(sys.io).value - t1 < 1_900_000_000)
        // A has no records, AAAA fails once and then answers: only AAAA is
        // asked again, and its address is the answer.
        r3 := DLook(sys.io, "six.test", mut out)
        Assert(r3.err == core.SysErr.None && r3.value == 1 && out[0].v6 && out[0].ip[15] == 6)
        Assert(cs.six.Load() == 2)
        s.Cancel()
    }
    DDown(sys.io, u, l)
}

test "RT-Os-Dns-TcpMax a TCP answer of the largest length, and a prefix past the body" (sys: &System) {
    (u: u, l: l) := DUp(sys.io)
    cs := DCounts(drops: raw.Atomic[u32].New(0), six: raw.Atomic[u32].New(0), seen: raw.Atomic[u32].New(0))
    var out: [8]core.IoAddr = [core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0); 8]
    scope s := sys.io.Scope() {
        s.go DUdpServer(sys.io, u, &cs)
        s.go DTcpServer(sys.io, l, &cs)
        // Prefix FF FF and 65,535 bytes: read whole (it was a bounds panic).
        r := DLook(sys.io, "huge.test", mut out)
        Assert(r.err == core.SysErr.None && r.value == 1 && DIp4(out[0], 10, 0, 0, 8))
        // Prefix FF FE, then 100 bytes and the close: no answer from the
        // name; its search form is NXDOMAIN.
        r2 := DLook(sys.io, "huge2.test", mut out)
        Assert(r2.err == core.SysErr.NotFound)
        s.Cancel()
    }
    DDown(sys.io, u, l)
}

test "RT-Os-Dns-NoCancel a NoCancel lookup ends at its timeout, answers at once, and answers in a cancelled scope" (sys: &System) {
    (u: u, l: l) := DUp(sys.io)
    cs := DCounts(drops: raw.Atomic[u32].New(0), six: raw.Atomic[u32].New(0), seen: raw.Atomic[u32].New(0))
    var out: [8]core.IoAddr = [core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0); 8]
    nc := sys.io.NoCancel()
    scope s := sys.io.Scope() {
        s.go DUdpServer(sys.io, u, &cs)
        s.go DTcpServer(sys.io, l, &cs)
        // Every query dropped: the per-server timeout still ends each
        // exchange, and the result is the cancellable call's (the search
        // form is NXDOMAIN). Before the fix the receive waited for ever.
        r0 := DLook(sys.io, "dropall.test", mut out)
        r1 := DLook(nc, "dropall.test", mut out)
        Assert(r0.err == core.SysErr.NotFound && r1.err == r0.err && r1.os == r0.os)
        // An answered lookup returns when its replies are in, not after the
        // timer's whole second.
        t0 := NativeNow(sys.io).value
        r2 := DLook(nc, "a.test", mut out)
        Assert(r2.err == core.SysErr.None && r2.value == 3)
        Assert(NativeNow(sys.io).value - t0 < 900_000_000)
        // Truncated over UDP, answered over TCP (DnsTcp's exchange), at once.
        t1 := NativeNow(sys.io).value
        r4 := DLook(nc, "tc.test", mut out)
        Assert(r4.err == core.SysErr.None && r4.value == 1 && DIp4(out[0], 10, 0, 0, 7))
        Assert(NativeNow(sys.io).value - t1 < 900_000_000)
        // In a cancelled scope, a NoCancel lookup still answers: its
        // exchange's own scope is not the cancelled one's child.
        scope c := sys.io.Scope() {
            c.Cancel()
            r3 := DLook(nc, "a.test", mut out)
            Assert(r3.err == core.SysErr.None && r3.value == 3 && DIp4(out[0], 10, 0, 0, 1))
            r5 := DLook(nc, "tc.test", mut out)
            Assert(r5.err == core.SysErr.None && r5.value == 1 && DIp4(out[0], 10, 0, 0, 7))
            // A cancellable lookup there is Canceled.
            Assert(DLook(sys.io, "a.test", mut out).err == core.SysErr.Canceled)
        }
        s.Cancel()
    }
    DDown(sys.io, u, l)
}

// DLookCancel resolves dropall.test (every query dropped) and records the
// result: 1 Canceled, 2 anything else; took the nanoseconds it took.
DLookCancel(io: Io, got: &raw.Atomic[u32], took: &raw.Atomic[u64]) {
    var out: [8]core.IoAddr = [core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0); 8]
    t0 := NativeNow(io).value
    r := DnsLookup(io, "dropall.test".AsBytes(), mut out[..])
    took.Store(Trunc[u64](NativeNow(io).value - t0))
    if r.err == core.SysErr.Canceled {
        got.Store(1)
    } else {
        got.Store(2)
    }
}

test "RT-Os-Dns-Cancel a cancel at each point of a lookup ends it as Canceled, at once" (sys: &System) {
    (u: u, l: l) := DUp(sys.io)
    cs := DCounts(drops: raw.Atomic[u32].New(0), six: raw.Atomic[u32].New(0), seen: raw.Atomic[u32].New(0))
    scope srv := sys.io.Scope() {
        srv.go DUdpServer(sys.io, u, &cs)
        srv.go DTcpServer(sys.io, l, &cs)
        // 0: cancelled while it waits for a reply; 1: after a 1 ms sleep;
        // 2: while its pool job runs (the hold switch), before any query.
        for mode in 0..3 {
            for round in 0..4 {
                got := raw.Atomic[u32].New(0)
                took := raw.Atomic[u64].New(0)
                if mode == 2 {
                    DnsTest.hold.Store(1)
                }
                seen := cs.seen.Load()
                scope s := sys.io.Scope() {
                    s.go DLookCancel(sys.io, &got, &took)
                    if mode == 0 {
                        // The server saw the query: the lookup waits for a
                        // reply that never comes.
                        for cs.seen.Load() == seen {
                            _ = NativeSleep(sys.io, 1_000_000)
                        }
                        _ = NativeSleep(sys.io, 2_000_000)
                    } else if mode == 1 {
                        _ = NativeSleep(sys.io, 1_000_000 * i64(round))
                    } else {
                        for DnsTest.hold.Load() != 2 {
                            _ = NativeSleep(sys.io, 1_000_000)
                        }
                    }
                    s.Cancel()
                    DnsTest.hold.Store(0)
                }
                Assert(got.Load() == 1)
                // Well inside one timeout (1 s): the walk over every server,
                // attempt and candidate would take 4 s.
                Assert(took.Load() < 900_000_000)
            }
        }
        srv.Cancel()
    }
    DDown(sys.io, u, l)
}

// DPutText writes s at off of the open file h: the offset after it.
DPutText(io: Io, h: uint, off: int, s: str): int {
    w := NativeWrite(io, h, s.AsBytes(), i64(off))
    Assert(w.err == core.SysErr.None && w.value == i64(s.Len()))
    return off + s.Len()
}

// DHostsFile writes the big hosts file: 2,000 filler lines of 47 bytes
// (94,000 bytes, past one 64 KiB block, so lines split at block edges),
// far.example, one line of 70,000 bytes naming inner.example (longer than the block,
// dropped), after.long, and last.example with no final newline.
DHostsFile(io: Io, name: str) {
    o := NativeOpen(io, name, 1)
    Assert(o.err == core.SysErr.None)
    h := Trunc[uint](o.value)
    var off := 0
    for _ in 0..2000 {
        off = DPutText(io, h, off, "10.9.9.9 filler-name-of-thirty-chars alias-two\n")
    }
    off = DPutText(io, h, off, "10.1.2.3 far.example\n")
    off = DPutText(io, h, off, "10.5.5.5 inner.example ")
    var pad: [1000]u8 = [120; 1000]
    for _ in 0..70 {
        w := NativeWrite(io, h, pad[..], i64(off))
        Assert(w.err == core.SysErr.None && w.value == 1000)
        off += 1000
    }
    off = DPutText(io, h, off, "\n10.4.5.6 after.long\n10.7.8.9 last.example")
    Assert(NativeClose(io, h).err == core.SysErr.None)
}

test "RT-Os-Hosts-Big a hosts file past one block is read to its end" (sys: &System) {
    name := "mkx-hosts-big.txt"
    DHostsFile(sys.io, name)
    var out: [8]core.IoAddr = [core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0); 8]
    r := HostsLookup(sys.io, name, "far.example".AsBytes(), mut out[..])
    Assert(r.err == core.SysErr.None && r.value == 1 && DIp4(out[0], 10, 1, 2, 3))
    r2 := HostsLookup(sys.io, name, "AFTER.long".AsBytes(), mut out[..])
    Assert(r2.err == core.SysErr.None && r2.value == 1 && DIp4(out[0], 10, 4, 5, 6))
    r3 := HostsLookup(sys.io, name, "last.example".AsBytes(), mut out[..])
    Assert(r3.err == core.SysErr.None && r3.value == 1 && DIp4(out[0], 10, 7, 8, 9))
    // The line longer than the block is dropped whole.
    Assert(HostsLookup(sys.io, name, "inner.example".AsBytes(), mut out[..]).value == 0)
    // Every filler line names alias-two: out fills up and the scan stops.
    Assert(HostsLookup(sys.io, name, "alias-two".AsBytes(), mut out[..]).value == 8)
    Assert(HostsLookup(sys.io, "mkx-no-such-hosts", "far.example".AsBytes(), mut out[..]).value == 0)
    if !PWin() {
        // Linux: resolve reads the switch's file, and no query is sent.
        (u: u, l: l) := DUp(sys.io)
        cs := DCounts(drops: raw.Atomic[u32].New(0), six: raw.Atomic[u32].New(0), seen: raw.Atomic[u32].New(0))
        unsafe "name is static text" {
            HostsTest.path_at.Store(u64(raw.Addr(raw.StrData(name))))
        }
        HostsTest.path_len.Store(Trunc[u64](name.Len()))
        scope s := sys.io.Scope() {
            s.go DUdpServer(sys.io, u, &cs)
            s.go DTcpServer(sys.io, l, &cs)
            n := NativeResolveAll(sys.io, "far.example", mut out[..])
            Assert(n.err == core.SysErr.None && n.value == 1 && DIp4(out[0], 10, 1, 2, 3))
            Assert(cs.seen.Load() == 0)
            s.Cancel()
        }
        HostsTest.path_len.Store(0)
        DDown(sys.io, u, l)
    }
    Assert(NativeRemove(sys.io, name).err == core.SysErr.None)
}
