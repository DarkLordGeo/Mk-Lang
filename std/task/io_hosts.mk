// Why: net.Resolve must turn a host name into addresses without blocking a
// carrier, and on Linux without libc's resolver.
//
// What: NativeResolveAll, the native table's resolve entry, the hosts-file
// job and its frame record HostsArgs, MaxAnswers, HostsBytes and the test
// switch HostsTest.
//
// How: a numeric address answers itself and "localhost" is fixed; otherwise
// the hosts file is scanned a block at a time, then the system's resolver
// is asked (GetAddrInfoW on Windows, the nameservers of /etc/resolv.conf
// through DnsLookup on Linux), each as a job on the blocking pool.

import std.core
import std.raw
import std.rt
import std.sys

// MaxAnswers bounds the addresses one resolve returns (the frame array the
// pool job fills); HostsBytes is the block /etc/hosts is read through (the
// whole file is scanned, a block at a time).
const MaxAnswers: int = 32
const HostsBytes: int = 65536

// HostsTest is the hosts file's test switch: when path_len is non-zero, the
// file at that address and length is read instead of /etc/hosts. Edge:
// none; a test switch written before the test's lookups start.
struct HostsTestState {
    path_at: raw.Atomic[u64]
    path_len: raw.Atomic[u64]
}

var HostsTest: HostsTestState = HostsTestState(path_at: raw.Atomic[u64].New(0), path_len: raw.Atomic[u64].New(0))

// HostsArgs is a hosts job's frame record (op.buf points at it): the block,
// the name, and the caller's out with the count found.
struct HostsArgs {
    block: *u8
    name: *u8
    nlen: int
    out: *core.IoAddr
    olen: int
    n: int
}

// ResolveArgs is a resolve job's frame record (op.buf points at it).
struct ResolveArgs {
    addrs: [MaxAnswers]sys.SockAddr
    n: int
}

JobResolveAll(op: *sys.Op) {
    unsafe "op.buf is the caller's ResolveArgs" {
        a := raw.PtrCast[u8, ResolveArgs]((*op).buf)
        (*op).res = sys.ResolveAll((*op).path, mut (*a).addrs[0..(*a).n])
    }
}

// JobHosts scans the hosts file op.path for the name of the HostsArgs at
// op.buf, the whole file a HostsBytes block at a time (glibc's files backend
// reads every line; a file of blocklist size is megabytes): each read fills
// the block after the partial line the last one left, the complete lines
// are scanned, and the partial line moves to the block's front. A line
// longer than the block is dropped up to its newline (no name can be in
// it that a 64 KiB line would hold usefully). value the addresses found;
// a missing file is its open error. Linux only in NativeResolveAll
// (Windows' resolver reads its own hosts file); HostsLookup runs it on both.
JobHosts(op: *sys.Op) {
    unsafe "op is the caller's; op.buf its HostsArgs, whose block, name and out are live" {
        a := raw.PtrCast[u8, HostsArgs]((*op).buf)
        o := sys.OpenFile((*op).path, 0, false)
        if o.err != core.SysErr.None {
            (*op).res = o
            return
        }
        h := Trunc[uint](o.value)
        b := raw.SliceMut((*a).block, HostsBytes)
        name := raw.Slice((*a).name, (*a).nlen)
        out := raw.SliceMut((*a).out, (*a).olen)
        var n := 0
        var keep := 0
        var skip := false
        for n < out.Len() {
            r := sys.ReadSync(h, mut b[keep..], -1)
            if r.err != core.SysErr.None || r.value == 0 {
                // The end (a read error ends the scan as the end does): a
                // last line without its newline still counts.
                if keep > 0 && !skip {
                    n += HostsScan(b[0..keep], name, mut out[n..])
                }
                break
            }
            fill := keep + Trunc[int](r.value)
            var e := fill
            for e > 0 && b[e - 1] != 10 {
                e -= 1
            }
            if e == 0 {
                keep = fill
                if fill == HostsBytes {
                    // One line fills the block: drop it to its newline.
                    skip = true
                    keep = 0
                }
                continue
            }
            var from := 0
            if skip {
                for b[from] != 10 {
                    from += 1
                }
                from += 1
                skip = false
            }
            n += HostsScan(b[from..e], name, mut out[n..])
            for k in e..fill {
                b[k - e] = b[k]
            }
            keep = fill - e
        }
        _ = sys.Close(h)
        (*a).n = n
        (*op).res = sys.OsResult(value: i64(n), err: core.SysErr.None, os: 0)
    }
}

// HostsPath is the hosts file to read: /etc/hosts, or the test switch's.
HostsPath(): str {
    // Edge: the test stores path_at before path_len (seq_cst), so a length
    // seen here names a path that is already in place.
    l := Trunc[int](HostsTest.path_len.Load())
    if l > 0 {
        unsafe "the test's path lives for the test" {
            return raw.Str(raw.FromAddr[u8](Trunc[uint](HostsTest.path_at.Load())), l)
        }
    }
    return "/etc/hosts"
}

// HostsLookup finds name in the hosts file at path into out on the pool
// (JobHosts; the block is HostsBytes from the heap, freed before it
// returns): value the count, 0 when the file does not name it or is
// missing; Canceled when the job was cancelled before it ran.
HostsLookup(io: Io, path: str, name: []u8, out: mut []core.IoAddr): core.IoResult {
    t := rt.HeapAlloc(HostsBytes, 16)
    if raw.IsNull(t) {
        return ErrOf(core.SysErr.Other)
    }
    var a := HostsArgs(block: t, name: raw.Null[u8](), nlen: name.Len(), out: raw.Null[core.IoAddr](),
        olen: out.Len(), n: 0)
    var op := sys.NewOp(sys.OpOpen, 0)
    op.path = path
    unsafe "a, name and out live until OnPool returns; t until the HeapFree" {
        a.name = raw.Data(name)
        a.out = raw.DataMut(out)
        op.buf = raw.PtrCast[HostsArgs, u8](raw.OfMut(mut a))
    }
    r := OnPool(io, JobHosts, mut op)
    rt.HeapFree(t, HostsBytes, 16)
    if r.err == core.SysErr.Canceled {
        return r
    }
    return if r.err != core.SysErr.None { NoErr(0) } else { NoErr(i64(a.n)) }
}

// Lower is c in ASCII lower case.
Lower(c: u8): u8 {
    return if c >= 65 && c <= 90 { c + 32 } else { c }
}

// SameName compares host names in ASCII case-insensitively (RFC 4343).
SameName(a: []u8, b: []u8): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        if Lower(a[i]) != Lower(b[i]) {
            return false
        }
    }
    return true
}

// ParseHostIp parses a numeric IPv4 or IPv6 address (no brackets, an
// optional %zone) into a.
ParseHostIp(b: []u8, a: mut sys.SockAddr): bool {
    var ip: [16]u8 = [0; 16]
    if rt.ParseIp4(b, mut ip, 0) {
        *a = sys.SockAddr(family: sys.Inet4, port: 0, addr: ip, scope_id: 0)
        return true
    }
    var six := sys.SockAddr(family: sys.Inet6, port: 0, addr: [0; 16], scope_id: 0)
    if rt.ParseIp6(b, mut six) {
        *a = six
        return true
    }
    return false
}

// HostsScan finds name in the hosts text t (glibc's files backend: a line
// is an address then names, # starts a comment) and writes each address of
// a line naming it to out, in file order; the count written.
HostsScan(t: []u8, name: []u8, out: mut []core.IoAddr): int {
    var n := 0
    var i := 0
    for i < t.Len() && n < out.Len() {
        var e := i
        for e < t.Len() && t[e] != 10 {
            e += 1
        }
        var line_end := e
        for k in i..e {
            if t[k] == 35 {
                line_end = k
                break
            }
        }
        // Fields of t[i..line_end], split on spaces and tabs.
        var f := i
        var first := true
        var addr := sys.SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0)
        var ok := false
        for f < line_end {
            for f < line_end && (t[f] == 32 || t[f] == 9 || t[f] == 13) {
                f += 1
            }
            var g := f
            for g < line_end && t[g] != 32 && t[g] != 9 && t[g] != 13 {
                g += 1
            }
            if g > f {
                if first {
                    ok = ParseHostIp(t[f..g], mut addr)
                    first = false
                } else if ok && SameName(t[f..g], name) {
                    out[n] = FromSock(&addr)
                    n += 1
                    break
                }
            }
            f = g
        }
        i = e + 1
    }
    return n
}

// NativeResolveAll answers host's addresses into out: a numeric address
// answers itself; "localhost" 127.0.0.1 then ::1 (RFC 6761, Go's order);
// Windows asks GetAddrInfoW on the pool; Linux reads /etc/hosts on the pool,
// then asks the nameservers of /etc/resolv.conf (DnsLookup). value the
// count written; a name with no answer is NotFound.
NativeResolveAll(io: Io, host: str, out: mut []core.IoAddr): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    b := host.AsBytes()
    if b.Len() == 0 || b.Len() > 253 {
        return ErrOf(core.SysErr.InvalidAddress)
    }
    var one := sys.SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0)
    if ParseHostIp(b, mut one) {
        if out.Len() == 0 {
            return NoErr(0)
        }
        out[0] = FromSock(&one)
        return NoErr(1)
    }
    if rt.IsLocalhost(b) {
        var lo: [16]u8 = [0; 16]
        lo[0] = 127
        lo[3] = 1
        var six: [16]u8 = [0; 16]
        six[15] = 1
        var n := 0
        if out.Len() > 0 {
            out[0] = core.IoAddr(ip: lo, port: 0, v6: false, zone: 0)
            n = 1
        }
        if out.Len() > 1 {
            out[1] = core.IoAddr(ip: six, port: 0, v6: true, zone: 0)
            n = 2
        }
        return NoErr(i64(n))
    }
    var op := sys.NewOp(sys.OpOpen, 0)
    if PollBackend() == 1 {
        var a := ResolveArgs(addrs: [sys.SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0); MaxAnswers], n: 0)
        a.n = out.Len()
        if a.n > MaxAnswers {
            a.n = MaxAnswers
        }
        var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
        if sys.PollSupports(sys.OpResolve) {
            // On the port: a waiting lookup holds no thread,
            // and a cancel ends it at once (GetAddrInfoExCancel), where the
            // pool's GetAddrInfoW ran to the system's timeout.
            var rop := sys.NewOp(sys.OpResolve, 0)
            rop.path = host
            rop.len = a.n
            unsafe "a is in this frame until OnPoller returns (the one-owner rule)" {
                rop.buf = raw.PtrCast[sys.SockAddr, u8](raw.DataMut(mut a.addrs[..]))
            }
            r = OnPoller(io, mut rop)
        } else {
            op.path = host
            unsafe "a is in this frame until OnPool returns" {
                op.buf = raw.PtrCast[ResolveArgs, u8](raw.OfMut(mut a))
            }
            r = OnPool(io, JobResolveAll, mut op)
        }
        if r.err != core.SysErr.None {
            return r
        }
        n := Trunc[int](r.value)
        for i in 0..n {
            out[i] = FromSock(&a.addrs[i])
        }
        return r
    }
    r := HostsLookup(io, HostsPath(), b, out)
    if r.err != core.SysErr.None {
        return r
    }
    n := Trunc[int](r.value)
    if n == 0 {
        // Not in /etc/hosts: the nameservers (io_dns.mk).
        return DnsLookup(io, b, out)
    }
    return NoErr(i64(n))
}
