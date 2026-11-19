// Why: net.Resolve on Linux has no libc resolver to call, so a name that
// /etc/hosts does not hold must be asked of the nameservers directly.
//
// What: DnsLookup, the stub resolver; the resolv.conf parser (DnsConf,
// DnsParseConf); the message builder and parser (DnsQuery, DnsName,
// DnsParse); the UDP and TCP exchanges (DnsUdp, DnsTcp); the search-list
// candidates (DnsCandidate); and the test switches DnsTest and JobDnsHold.
//
// How: the nameservers of /etc/resolv.conf are asked over UDP, as Go's
// pure-Go resolver does (net/dnsclient, net/dnsconfig): RFC 1035 messages, an A and an AAAA query
// sent together on one socket per server, the answers matched by id, source
// and question; the search list and ndots in Go's order (an absolute name or
// one with at least ndots dots first); `timeout:` and `attempts:`; TCP when
// an answer is truncated (RFC 7766). The parser follows compression pointers
// only backwards, so a loop or an out-of-bounds pointer is a bad message,
// never a hang. Every buffer is in the frame but the TCP answer (64 KiB from
// the heap, freed on every path). The code is the same on both targets
// (Windows resolves through GetAddrInfoW; its blocks call DnsLookup
// directly).

import std.core
import std.raw
import std.rt
import std.sys

// DnsTest holds the test switches: port, when non-zero, replaces 53, and
// conf, when its length is non-zero, the address and length of the
// resolv.conf text to use instead of reading /etc/resolv.conf. hold, when
// 1, makes the next lookup run a pool job before its configuration (as
// JobReadConf runs) that sets it to 2 and waits until the test stores 0:
// the window where a cancel lands while the lookup's job runs.
// Set before the test's lookups run. Edge: none for port and conf (test
// switches, written before the lookups start); hold's 2 and 0 are
// sequentially consistent stores read by spinning loads, so each side sees
// the other's step.
struct DnsTestState {
    port: raw.Atomic[u32]
    conf_at: raw.Atomic[u64]
    conf_len: raw.Atomic[u64]
    hold: raw.Atomic[u32]
}

var DnsTest: DnsTestState = DnsTestState(port: raw.Atomic[u32].New(0), conf_at: raw.Atomic[u64].New(0),
    conf_len: raw.Atomic[u64].New(0), hold: raw.Atomic[u32].New(0))

// JobDnsHold is the hold switch's pool job: it announces itself (2) and
// waits for the test to release it (0).
JobDnsHold(op: *sys.Op) {
    // Edge: the test's switch: the store of 2 tells the test this job is
    // in its call; the test's store of 0 ends the wait (seq_cst both ways).
    DnsTest.hold.Store(2)
    for DnsTest.hold.Load() == 2 {
        sys.ThreadYield()
    }
    unsafe "op is the caller's" {
        (*op).res = sys.OsResult(value: 0, err: core.SysErr.None, os: 0)
    }
}

const DnsMaxServers: int = 3
const DnsMaxSearch: int = 6
const DnsConfBytes: int = 4096
const DnsUdpBytes: int = 1232
const DnsTcpBytes: int = 65536
const DnsMaxAnswers: int = 32
const TypeA: u16 = 1
const TypeAAAA: u16 = 28
const TypeCname: u16 = 5
const RcodeNx: int = 3

// DnsConf is resolv.conf as the lookup uses it (glibc's defaults: the
// local server, timeout 5 s, 2 attempts, ndots 1). The search domains are
// in sbuf, domain i at soff[i]..soff[i + 1].
struct DnsConf {
    servers: [3]core.IoAddr
    nservers: int
    sbuf: [256]u8
    soff: [7]int
    nsearch: int
    timeout_ns: i64
    attempts: int
    ndots: int
}

NewDnsConf(): DnsConf {
    return DnsConf(servers: [core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0); 3], nservers: 0, sbuf: [0; 256],
        soff: [0; 7], nsearch: 0, timeout_ns: 5_000_000_000, attempts: 2, ndots: 1)
}

// DnsWord: t[i..] starts with the word w followed by a space, a tab or the
// end.
DnsWord(t: []u8, i: int, w: str): bool {
    b := w.AsBytes()
    if i + b.Len() > t.Len() {
        return false
    }
    for k in 0..b.Len() {
        if t[i + k] != b[k] {
            return false
        }
    }
    e := i + b.Len()
    return e == t.Len() || t[e] == 32 || t[e] == 9
}

// DnsNum parses the decimal at t[i..e] (at most 2 digits used, as glibc
// caps these options); -1 when there is none.
DnsNum(t: []u8, i: int, e: int): int {
    var v := 0
    var any := false
    for k in i..e {
        c := t[k]
        if c < 48 || c > 57 {
            break
        }
        if v < 100 {
            v = v * 10 + int(c - 48)
        }
        any = true
    }
    return if !any { -1 } else { v }
}

// DnsParseConf reads resolv.conf text t into c: nameserver (at most 3,
// numeric only, as glibc), search and domain (the last one wins, at most 6
// domains in 255 bytes), options timeout:n (1 to 30 s), attempts:n (1 to
// 5), ndots:n (0 to 15). # and ; start a comment. No server means the
// local one.
DnsParseConf(t: []u8, c: mut DnsConf) {
    var i := 0
    for i < t.Len() {
        var e := i
        for e < t.Len() && t[e] != 10 {
            e += 1
        }
        var le := e
        for k in i..e {
            if t[k] == 35 || t[k] == 59 || t[k] == 13 {
                le = k
                break
            }
        }
        line := t[i..le]
        if DnsWord(line, 0, "nameserver") {
            var f := 10
            for f < line.Len() && (line[f] == 32 || line[f] == 9) {
                f += 1
            }
            var g := f
            for g < line.Len() && line[g] != 32 && line[g] != 9 {
                g += 1
            }
            var a := sys.SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0)
            if c.nservers < DnsMaxServers && g > f && ParseHostIp(line[f..g], mut a) {
                c.servers[c.nservers] = FromSock(&a)
                c.nservers += 1
            }
        } else if DnsWord(line, 0, "search") || DnsWord(line, 0, "domain") {
            c.nsearch = 0
            var o := 0
            var f := 6
            for f < line.Len() {
                for f < line.Len() && (line[f] == 32 || line[f] == 9) {
                    f += 1
                }
                var g := f
                for g < line.Len() && line[g] != 32 && line[g] != 9 {
                    g += 1
                }
                var n := g - f
                if n > 0 && line[g - 1] == 46 {
                    n -= 1
                }
                if n > 0 && c.nsearch < DnsMaxSearch && o + n <= 255 {
                    for k in 0..n {
                        c.sbuf[o + k] = line[f + k]
                    }
                    c.soff[c.nsearch] = o
                    o += n
                    c.nsearch += 1
                    c.soff[c.nsearch] = o
                }
                f = g
            }
        } else if DnsWord(line, 0, "options") {
            var f := 7
            for f < line.Len() {
                for f < line.Len() && (line[f] == 32 || line[f] == 9) {
                    f += 1
                }
                var g := f
                for g < line.Len() && line[g] != 32 && line[g] != 9 {
                    g += 1
                }
                w := line[f..g]
                if w.Len() > 8 && DnsPrefix(w, "timeout:") {
                    v := DnsNum(w, 8, w.Len())
                    if v >= 1 {
                        c.timeout_ns = i64(DnsClamp(v, 1, 30)) * 1_000_000_000
                    }
                } else if w.Len() > 9 && DnsPrefix(w, "attempts:") {
                    v := DnsNum(w, 9, w.Len())
                    if v >= 1 {
                        c.attempts = DnsClamp(v, 1, 5)
                    }
                } else if w.Len() > 6 && DnsPrefix(w, "ndots:") {
                    v := DnsNum(w, 6, w.Len())
                    if v >= 0 {
                        c.ndots = DnsClamp(v, 0, 15)
                    }
                }
                f = g
            }
        }
        i = e + 1
    }
    if c.nservers == 0 {
        var lo: [16]u8 = [0; 16]
        lo[0] = 127
        lo[3] = 1
        c.servers[0] = core.IoAddr(ip: lo, port: 0, v6: false, zone: 0)
        c.nservers = 1
    }
}

DnsPrefix(w: []u8, p: str): bool {
    b := p.AsBytes()
    if w.Len() < b.Len() {
        return false
    }
    for k in 0..b.Len() {
        if w[k] != b[k] {
            return false
        }
    }
    return true
}

DnsClamp(v: int, lo: int, hi: int): int {
    if v < lo {
        return lo
    }
    return if v > hi { hi } else { v }
}

// DnsQuery writes a query for name (dotted, no final dot) and qtype into
// m: its length, or 0 when the name cannot be one (an empty label, a label
// past 63 bytes, a name past 253). Recursion desired, one question.
DnsQuery(m: mut []u8, id: u16, name: []u8, qtype: u16): int {
    if name.Len() == 0 || name.Len() > 253 || m.Len() < name.Len() + 18 {
        return 0
    }
    m[0] = Trunc[u8](id >> 8)
    m[1] = Trunc[u8](id)
    m[2] = 1
    m[3] = 0
    for k in 4..12 {
        m[k] = 0
    }
    m[5] = 1
    var p := 12
    var s := 0
    for s <= name.Len() {
        var e := s
        for e < name.Len() && name[e] != 46 {
            e += 1
        }
        l := e - s
        if l == 0 || l > 63 {
            return 0
        }
        m[p] = Trunc[u8](l)
        p += 1
        for k in s..e {
            m[p] = name[k]
            p += 1
        }
        s = e + 1
    }
    m[p] = 0
    m[p + 1] = Trunc[u8](qtype >> 8)
    m[p + 2] = Trunc[u8](qtype)
    m[p + 3] = 0
    m[p + 4] = 1
    return p + 5
}

Get16At(m: []u8, i: int): u16 {
    return (u16(m[i]) << 8) | u16(m[i + 1])
}

// DnsName reads the name at m[at..] into out, dotted with no final dot:
// (next, n), next the offset after the name where it is written (after its
// first pointer, if any) and n its length; next -1 for a bad name: a
// pointer that does not point strictly before itself (so every jump goes
// back and a loop cannot exist; RFC 1035 4.1.4 points only at prior
// occurrences), a label type 0x40 or 0x80, a name past 255 bytes, or bytes
// past the message's end.
DnsName(m: []u8, at: int, out: mut [256]u8): (next: int, n: int) {
    var p := at
    var next := -1
    var n := 0
    for {
        if p >= m.Len() {
            return (next: -1, n: 0)
        }
        l := int(m[p])
        if l == 0 {
            if next < 0 {
                next = p + 1
            }
            return (next: next, n: n)
        }
        if (l & 0xC0) == 0xC0 {
            if p + 1 >= m.Len() {
                return (next: -1, n: 0)
            }
            t := ((l & 0x3F) << 8) | int(m[p + 1])
            if t >= p {
                return (next: -1, n: 0)
            }
            if next < 0 {
                next = p + 2
            }
            p = t
        } else if (l & 0xC0) != 0 {
            return (next: -1, n: 0)
        } else {
            if p + 1 + l > m.Len() {
                return (next: -1, n: 0)
            }
            var need := l
            if n > 0 {
                need += 1
            }
            if n + need > 255 {
                return (next: -1, n: 0)
            }
            if n > 0 {
                out[n] = 46
                n += 1
            }
            for k in 0..l {
                out[n + k] = m[p + 1 + k]
            }
            n += l
            p += 1 + l
        }
    }
}

// DnsReply is one parsed answer: ok a well-formed reply to the query
// (id, question), tc truncated, rcode, and n addresses in addrs.
struct DnsReply {
    ok: bool
    tc: bool
    rcode: int
    n: int
    addrs: [32]core.IoAddr
}

NewDnsReply(): DnsReply {
    return DnsReply(ok: false, tc: false, rcode: 0, n: 0, addrs: [core.IoAddr(ip: [0; 16], port: 0, v6: false,
        zone: 0); 32])
}

// DnsParse checks that m answers the query (id, name, qtype) and collects
// the qtype addresses of name and of the CNAME chain from it, in the
// answer's order (glibc's and Go's rule: a record of another owner is
// skipped). A malformed message leaves r.ok false.
DnsParse(m: []u8, id: u16, name: []u8, qtype: u16, r: mut DnsReply) {
    r.ok = false
    r.n = 0
    if m.Len() < 12 || Get16At(m, 0) != id {
        return
    }
    f := Get16At(m, 2)
    if (f & 0x8000) == 0 || ((f >> 11) & 15) != 0 {
        return
    }
    if Get16At(m, 4) != 1 {
        return
    }
    var nb: [256]u8 = [0; 256]
    q := DnsName(m, 12, mut nb)
    if q.next < 0 || q.next + 4 > m.Len() || !SameName(nb[0..q.n], name) || Get16At(m, q.next) != qtype ||
        Get16At(m, q.next + 2) != 1 {
        return
    }
    r.tc = (f & 0x0200) != 0
    r.rcode = int(f & 15)
    if r.tc || r.rcode != 0 {
        r.ok = true
        return
    }
    // cur is the name whose records count: the query's, then each CNAME's
    // target in turn.
    var cur: [256]u8 = [0; 256]
    var cn := name.Len()
    for k in 0..cn {
        cur[k] = name[k]
    }
    var p := q.next + 4
    an := int(Get16At(m, 6))
    for _ in 0..an {
        o := DnsName(m, p, mut nb)
        if o.next < 0 || o.next + 10 > m.Len() {
            return
        }
        t := Get16At(m, o.next)
        c := Get16At(m, o.next + 2)
        rl := int(Get16At(m, o.next + 8))
        d := o.next + 10
        if d + rl > m.Len() {
            return
        }
        if c == 1 && SameName(nb[0..o.n], cur[0..cn]) {
            if t == qtype && r.n < DnsMaxAnswers && ((t == TypeA && rl == 4) || (t == TypeAAAA && rl == 16)) {
                var ip: [16]u8 = [0; 16]
                for k in 0..rl {
                    ip[k] = m[d + k]
                }
                r.addrs[r.n] = core.IoAddr(ip: ip, port: 0, v6: t == TypeAAAA, zone: 0)
                r.n += 1
            } else if t == TypeCname {
                var tb: [256]u8 = [0; 256]
                g := DnsName(m, d, mut tb)
                if g.next < 0 || g.next > d + rl {
                    return
                }
                cur = tb
                cn = g.n
            }
        }
        p = d + rl
    }
    r.ok = true
}

// SameIp: a and b are the same address (family and bytes).
SameIp(a: core.IoAddr, b: core.IoAddr): bool {
    if a.v6 != b.v6 {
        return false
    }
    for k in 0..16 {
        if a.ip[k] != b.ip[k] {
            return false
        }
    }
    return true
}

// DnsTimer cancels s after ns unless s is cancelled first (the exchange
// finished).
DnsTimer(s: &Scope, io: Io, ns: i64) {
    r := NativeSleep(io, ns)
    if r.err == core.SysErr.None {
        s.Cancel()
    }
}

// DnsXchg is one UDP exchange's state: the queries (A then AAAA; want[k]
// says which are asked this time), their replies, how many are wanted and
// how many are in.
struct DnsXchg {
    sock: uint
    server: core.IoAddr
    name: [256]u8
    nlen: int
    ids: [2]u16
    want: [2]bool
    got: [2]bool
    replies: [2]DnsReply
    need: int
    done: int
}

// DnsRecv reads datagrams on x.sock until every wanted reply is in, then
// cancels s (so the timer stops). A datagram from another source, with
// another id, or malformed is dropped, as Go's resolver drops it.
DnsRecv(s: &Scope, io: Io, x: mut DnsXchg) {
    var m: [1232]u8 = [0; 1232]
    for x.done < x.need {
        var from := core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0)
        r := NativeRecvFrom(io, x.sock, mut m[..], mut from)
        if r.err != core.SysErr.None {
            return
        }
        if !SameIp(from, x.server) || from.port != x.server.port {
            continue
        }
        n := Trunc[int](r.value)
        for k in 0..2 {
            if x.want[k] && !x.got[k] && n >= 2 && Get16At(m[0..n], 0) == x.ids[k] {
                qt := if k == 1 { TypeAAAA } else { TypeA }
                var rep := NewDnsReply()
                DnsParse(m[0..n], x.ids[k], x.name[0..x.nlen], qt, mut rep)
                if rep.ok {
                    x.replies[k] = rep
                    x.got[k] = true
                    x.done += 1
                }
            }
        }
    }
    s.Cancel()
}

// DnsUdp asks server for name (the queries x.want names, A and AAAA at
// once) within ns: x holds what came back. false when the socket or a send
// failed.
DnsUdp(io: Io, server: core.IoAddr, name: []u8, ns: i64, x: mut DnsXchg): bool {
    var wild := core.IoAddr(ip: [0; 16], port: 0, v6: server.v6, zone: 0)
    u := NativeUdp(io, wild)
    if u.err != core.SysErr.None {
        return false
    }
    x.sock = Trunc[uint](u.value)
    x.server = server
    x.nlen = name.Len()
    for k in 0..name.Len() {
        x.name[k] = name[k]
    }
    x.got = [false, false]
    x.done = 0
    x.need = 0
    var rnd: [4]u8 = [0; 4]
    _ = NativeRandom(io, mut rnd[..])
    x.ids[0] = (u16(rnd[0]) << 8) | u16(rnd[1])
    x.ids[1] = x.ids[0] ^ 0x8000 ^ (u16(rnd[2]) & 0x7FFF)
    if x.ids[1] == x.ids[0] {
        x.ids[1] = x.ids[0] ^ 1
    }
    var ok := true
    var q: [300]u8 = [0; 300]
    for k in 0..2 {
        if x.want[k] {
            qt := if k == 1 { TypeAAAA } else { TypeA }
            n := DnsQuery(mut q[..], x.ids[k], name, qt)
            if n == 0 || NativeSendTo(io, x.sock, q[0..n], server).err != core.SysErr.None {
                ok = false
            }
            x.need += 1
        }
    }
    sock := x.sock
    if ok {
        // The timer and the receiver end each other by cancelling their
        // scope, so they wait cancellably (NativeIo has no NoCancel flag)
        // whatever io is; for a NoCancel caller the scope is a root
        // (RootFor), so a cancel of the caller's scopes reaches neither, and
        // the exchange still ends at ns or at its last reply.
        qi := NativeIo()
        (on: on, inner: inner) := RootFor(io)
        scope s := qi.Scope() {
            s.go DnsTimer(&s, qi, ns)
            s.go DnsRecv(&s, qi, x)
        }
        EndRoot(on, inner)
    }
    _ = NativeClose(io, sock)
    return ok
}

// DnsTcp asks server for name and qtype over TCP (RFC 7766: a two-byte
// length before each message), within ns: true with r filled when an
// answer came. The answer buffer is 64 KiB from the heap, which holds the
// longest message the prefix can name (65,535 bytes; the prefix is read
// apart).
DnsTcp(io: Io, server: core.IoAddr, name: []u8, qtype: u16, ns: i64, r: mut DnsReply): bool {
    var ab: [64]u8 = [0; 64]
    an := DnsAddrText(server, mut ab)
    if an == 0 {
        return false
    }
    buf := rt.HeapAlloc(DnsTcpBytes, 16)
    if raw.IsNull(buf) {
        return false
    }
    var t := DnsTcpState(ok: false, len: 0)
    var q: [302]u8 = [0; 302]
    var rnd: [2]u8 = [0; 2]
    _ = NativeRandom(io, mut rnd[..])
    id := (u16(rnd[0]) << 8) | u16(rnd[1])
    n := DnsQuery(mut q[2..], id, name, qtype)
    if n > 0 {
        q[0] = Trunc[u8](n >> 8)
        q[1] = Trunc[u8](n)
        // As DnsUdp's exchange: cancellable inside, a root for NoCancel.
        qi := NativeIo()
        (on: on, inner: inner) := RootFor(io)
        unsafe "buf holds DnsTcpBytes until the HeapFree below; ab is this frame's" {
            scope s := qi.Scope() {
                s.go DnsTimer(&s, qi, ns)
                s.go DnsTcpAsk(&s, qi, raw.Str(raw.Data(ab[..]), an), q[0..n + 2], raw.SliceMut(buf, DnsTcpBytes), mut t)
            }
            EndRoot(on, inner)
            if t.ok {
                DnsParse(raw.Slice(buf, t.len), id, name, qtype, r)
            }
        }
    }
    rt.HeapFree(buf, DnsTcpBytes, 16)
    return t.ok && r.ok
}

struct DnsTcpState {
    ok: bool
    len: int
}

// DnsTcpAsk dials addr, sends q and reads one length-prefixed answer into
// buf (the two-byte prefix into a frame word first, then exactly that many
// bytes, so no prefix can name a slice past buf); then cancels s.
DnsTcpAsk(s: &Scope, io: Io, addr: str, q: []u8, buf: mut []u8, t: mut DnsTcpState) {
    d := NativeDial(io, addr)
    if d.err == core.SysErr.None {
        c := Trunc[uint](d.value)
        var off := 0
        var good := true
        for off < q.Len() && good {
            w := NativeSend(io, c, q[off..])
            if w.err != core.SysErr.None || w.value <= 0 {
                good = false
            } else {
                off += Trunc[int](w.value)
            }
        }
        var pre: [2]u8 = [0, 0]
        var pg := 0
        for good && pg < 2 {
            r := NativeRecv(io, c, mut pre[pg..2])
            if r.err != core.SysErr.None || r.value <= 0 {
                good = false
            } else {
                pg += Trunc[int](r.value)
            }
        }
        want := (int(pre[0]) << 8) | int(pre[1])
        if want > buf.Len() {
            good = false
        }
        var got := 0
        for good && got < want {
            r := NativeRecv(io, c, mut buf[got..want])
            if r.err != core.SysErr.None || r.value <= 0 {
                good = false
            } else {
                got += Trunc[int](r.value)
            }
        }
        _ = NativeClose(io, c)
        if good && want > 0 {
            t.ok = true
            t.len = want
        }
    }
    s.Cancel()
}

// DnsAddrText writes server as "a.b.c.d:port" or "[v6]:port" (the port the
// test switch or 53): its length.
DnsAddrText(a: core.IoAddr, b: mut [64]u8): int {
    var n := 0
    if a.v6 {
        b[0] = 91
        n = 1
        for g in 0..8 {
            if g > 0 {
                b[n] = 58
                n += 1
            }
            v := (int(a.ip[g * 2]) << 8) | int(a.ip[g * 2 + 1])
            for sh in [12, 8, 4, 0] {
                d := (v >> sh) & 15
                if d < 10 {
                    b[n] = Trunc[u8](48 + d)
                } else {
                    b[n] = Trunc[u8](87 + d)
                }
                n += 1
            }
        }
        b[n] = 93
        n += 1
    } else {
        for i in 0..4 {
            if i > 0 {
                b[n] = 46
                n += 1
            }
            n = DnsDec(b, n, int(a.ip[i]))
        }
    }
    b[n] = 58
    n += 1
    return DnsDec(b, n, int(a.port))
}

DnsDec(b: mut [64]u8, at: int, v: int): int {
    var d: [8]u8 = [0; 8]
    var k := 0
    var x := v
    for {
        d[k] = Trunc[u8](48 + x % 10)
        k += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    for i in 0..k {
        b[at + i] = d[k - 1 - i]
    }
    return at + k
}

// DnsCandidate writes the i-th name to ask for host (Go's nameList): an
// absolute name (final dot) only itself; with at least ndots dots the name
// first, then name.domain for each search domain; with fewer the search
// forms first. Its length, 0 past the last or when too long.
DnsCandidate(c: &DnsConf, host: []u8, i: int, out: mut [256]u8): int {
    var h := host.Len()
    if h > 0 && host[h - 1] == 46 {
        if i > 0 {
            return 0
        }
        h -= 1
        for k in 0..h {
            out[k] = host[k]
        }
        return h
    }
    var dots := 0
    for b in host {
        if b == 46 {
            dots += 1
        }
    }
    var j := i
    if dots >= c.ndots {
        if i == 0 {
            for k in 0..h {
                out[k] = host[k]
            }
            return h
        }
        j = i - 1
    } else if i == c.nsearch {
        for k in 0..h {
            out[k] = host[k]
        }
        return h
    }
    if j >= c.nsearch {
        return 0
    }
    s := c.soff[j]
    e := c.soff[j + 1]
    if h + 1 + (e - s) > 253 {
        // Too long: an empty candidate the caller skips.
        return 0 - 1
    }
    for k in 0..h {
        out[k] = host[k]
    }
    out[h] = 46
    for k in s..e {
        out[h + 1 + k - s] = c.sbuf[k]
    }
    return h + 1 + e - s
}

// DnsUnder: host is the label tld or ends with "." tld (a final dot
// allowed), ASCII case ignored.
DnsUnder(host: []u8, tld: str): bool {
    var h := host.Len()
    if h > 0 && host[h - 1] == 46 {
        h -= 1
    }
    t := tld.AsBytes()
    if h < t.Len() || !SameName(host[h - t.Len()..h], t) {
        return false
    }
    return h == t.Len() || host[h - t.Len() - 1] == 46
}

// JobReadConf reads /etc/resolv.conf into op.buf (op.len bytes); value the
// bytes read (a missing file reads as empty: glibc's defaults).
JobReadConf(op: *sys.Op) {
    o := sys.OpenFile("/etc/resolv.conf", 0, false)
    unsafe "op is the caller's; op.buf holds op.len bytes" {
        if o.err != core.SysErr.None {
            (*op).res = sys.OsResult(value: 0, err: core.SysErr.None, os: 0)
            return
        }
        h := Trunc[uint](o.value)
        var got := 0
        for got < (*op).len {
            r := sys.ReadSync(h, raw.SliceMut(raw.Offset((*op).buf, got), (*op).len - got), -1)
            if r.err != core.SysErr.None || r.value == 0 {
                break
            }
            got += Trunc[int](r.value)
        }
        _ = sys.Close(h)
        (*op).res = sys.OsResult(value: i64(got), err: core.SysErr.None, os: 0)
    }
}

// DnsLookup answers host's addresses from the nameservers into out (A
// answers, then AAAA, at most out.Len()): value the count. NotFound when
// every candidate name had no address (NXDOMAIN or no records); Other with
// os 110 (ETIMEDOUT) when no server answered; Canceled when the caller was
// cancelled, at whatever point the cancel came (else a cancel after the
// entry check would make every later exchange fail at once and the lookup
// end as a timeout). Each candidate: each attempt, each server in turn,
// a UDP exchange of the queries not yet settled within the timeout; a
// truncated reply is asked again over TCP. Each family settles on its own
// (Go's goLookupIPCNAMEOrder without strict errors): NOERROR or
// NXDOMAIN settles it, SERVFAIL, REFUSED or a bad reply sends only it to
// the next server. When one family has addresses and the other got no
// reply for a whole timeout, the addresses found are the answer (Go would
// wait out every attempt and server first).
DnsLookup(io: Io, host: []u8, out: mut []core.IoAddr): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    // RFC 6761 6.4: a name under .invalid is NotFound with no query.
    if DnsUnder(host, "invalid") {
        return ErrOf(core.SysErr.NotFound)
    }
    // Edge: none; a test switch the test sets before the lookup starts.
    if DnsTest.hold.Load() == 1 {
        var hop := sys.NewOp(sys.OpOpen, 0)
        r := OnPool(io, JobDnsHold, mut hop)
        if r.err != core.SysErr.None {
            return r
        }
    }
    var c := NewDnsConf()
    var text: [4096]u8 = [0; 4096]
    var tn := 0
    // Edge: the test stores conf_at before conf_len (seq_cst), so a length
    // seen here names text that is already in place.
    tl := Trunc[int](DnsTest.conf_len.Load())
    if tl > 0 {
        tn = tl
        if tn > DnsConfBytes {
            tn = DnsConfBytes
        }
        unsafe "the test's text lives for the test" {
            // Edge: conf_len's load above (the test stored conf_at first).
            raw.Copy(raw.DataMut(mut text[..]), raw.FromAddr[u8](Trunc[uint](DnsTest.conf_at.Load())), tn)
        }
    } else {
        var op := sys.NewOp(sys.OpOpen, 0)
        unsafe "text is in this frame until OnPool returns" {
            op.buf = raw.DataMut(mut text[..])
        }
        op.len = DnsConfBytes
        r := OnPool(io, JobReadConf, mut op)
        if r.err != core.SysErr.None {
            return r
        }
        tn = Trunc[int](r.value)
    }
    DnsParseConf(text[0..tn], mut c)
    var port: u16 = 53
    // Edge: none; a test switch set before the lookup starts.
    tp := DnsTest.port.Load()
    if tp != 0 {
        port = Trunc[u16](tp)
    }
    var answered := false
    var x := DnsXchg(sock: 0, server: c.servers[0], name: [0; 256], nlen: 0, ids: [0, 0], want: [true, true],
        got: [false, false], replies: [NewDnsReply(), NewDnsReply()], need: 0, done: 0)
    var res: [2]DnsReply = [NewDnsReply(), NewDnsReply()]
    var cand: [256]u8 = [0; 256]
    for i in 0..DnsMaxSearch + 2 {
        cl := DnsCandidate(&c, host, i, mut cand)
        if cl == 0 {
            break
        }
        if cl < 0 {
            continue
        }
        // fin[k]: family k (A, AAAA) settled for this name, its reply in res[k].
        var fin: [2]bool = [false, false]
        var stop := false
        for _ in 0..c.attempts {
            for si in 0..c.nservers {
                // Every exchange starts with the cancel check, so one that
                // failed at once for a cancel (NativeUdp sees Stop) never
                // passes for a server that did not answer.
                if Stop(io) {
                    return ErrOf(core.SysErr.Canceled)
                }
                var srv := c.servers[si]
                srv.port = port
                x.want[0] = !fin[0]
                x.want[1] = !fin[1]
                if !DnsUdp(io, srv, cand[0..cl], c.timeout_ns, mut x) {
                    continue
                }
                if Stop(io) {
                    return ErrOf(core.SysErr.Canceled)
                }
                // A truncated reply is asked again over TCP.
                for k in 0..2 {
                    if x.want[k] && x.got[k] && x.replies[k].tc {
                        qt := if k == 1 { TypeAAAA } else { TypeA }
                        var rep := NewDnsReply()
                        if DnsTcp(io, srv, cand[0..cl], qt, c.timeout_ns, mut rep) && !rep.tc {
                            x.replies[k] = rep
                        } else {
                            x.got[k] = false
                        }
                        if Stop(io) {
                            return ErrOf(core.SysErr.Canceled)
                        }
                    }
                }
                // NOERROR and NXDOMAIN settle a family; a server that failed
                // it (SERVFAIL, REFUSED, a bad reply) or said nothing is
                // passed over for that family only.
                var silent := false
                for k in 0..2 {
                    if x.want[k] {
                        if x.got[k] && (x.replies[k].rcode == 0 || x.replies[k].rcode == RcodeNx) {
                            fin[k] = true
                            res[k] = x.replies[k]
                        } else if !x.got[k] {
                            silent = true
                        }
                    }
                }
                if fin[0] && fin[1] {
                    stop = true
                } else if silent && ((fin[0] && res[0].n > 0) || (fin[1] && res[1].n > 0)) {
                    stop = true
                }
                if stop {
                    break
                }
            }
            if stop {
                break
            }
        }
        if fin[0] || fin[1] {
            answered = true
            var n := 0
            var total := 0
            for k in 0..2 {
                if fin[k] {
                    total += res[k].n
                    for j in 0..res[k].n {
                        if n < out.Len() {
                            out[n] = res[k].addrs[j]
                            n += 1
                        }
                    }
                }
            }
            if total > 0 {
                return NoErr(i64(n))
            }
        }
    }
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    if answered {
        return ErrOf(core.SysErr.NotFound)
    }
    return core.IoResult(value: 0, err: core.SysErr.Other, os: 110)
}
