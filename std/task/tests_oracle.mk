// Why: std.rt's file, path, time-zone and environment calls (std/rt/call.mk,
// proc.mk) must answer what an independent implementation answers.
// What: RT8-Oracle-Wtf8, -Calls, -Zone, -Env and -ReadLink, with helpers OrJoin,
// OrHex and OrI32; each block prints lines and does nothing when its
// variable (MK_RT8_ORACLE, MK_RT8_WTF8, MK_RT8_ZONE, MK_RT8_ENVORACLE) is unset.
// How: std/rt_oracle_test.go, rt_wtf8_test.go and rt_oraclelinux_test.go
// set the variables and check each printed line against Go's os,
// path/filepath, time and os/exec.

import std.core
import std.rt

// OrJoin writes dir, the separator and name into b.
OrJoin(b: mut [512]u8, dir: str, name: str): []u8 from b {
    var n := 0
    for x in dir.AsBytes() {
        b[n] = x
        n += 1
    }
    b[n] = if PWin() { 92 } else { 47 }
    n += 1
    for x in name.AsBytes() {
        b[n] = x
        n += 1
    }
    return b[0..n]
}

// OrHex writes b as lower-case hex into out: the hex.
OrHex(out: mut [1024]u8, b: []u8): str from out {
    var n := 0
    for x in b {
        hi := x >> 4
        lo := x & 15
        out[n] = if hi < 10 { 48 + hi } else { 87 + hi }
        out[n + 1] = if lo < 10 { 48 + lo } else { 87 + lo }
        n += 2
    }
    return str.From(out[0..n]).Must()
}

test "RT8-Oracle-Wtf8 names listed as WTF-8 reopen: stat, a directory opened inside its parent, a file removed by its listed name (MK_RT8_WTF8, set by TestRt_Wtf8Names)" (sys: &System) {
    io := sys.io
    dir := sys.Env("MK_RT8_WTF8") else { "" }
    if dir.Len() == 0 || dir.Len() > 400 {
        return
    }
    o := rt.DirOpenAt(io, 0, dir.AsBytes())
    Assert(o.err == core.SysErr.None)
    d := Trunc[uint](o.value)
    var name: [256]u8 = [0; 256]
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    for {
        r := NativeReadDir(io, d, mut name[..], mut st)
        Assert(r.err == core.SysErr.None)
        if r.value == 0 {
            break
        }
        nm := name[0..Trunc[int](r.value)]
        var hb: [1024]u8 = [0; 1024]
        h := OrHex(mut hb, nm)
        sys.PrintErr("N {h} {k}\n", (h: h, k: st.kind)) else {}
        // The whole path in bytes: the directory, the separator, the name.
        var pb: [1024]u8 = [0; 1024]
        var n := 0
        for x in dir.AsBytes() {
            pb[n] = x
            n += 1
        }
        pb[n] = 92
        n += 1
        for x in nm {
            pb[n] = x
            n += 1
        }
        var w := core.IoWords(a: 0, b: 0, c: 0, d: 0)
        s := rt.StatEx(io, pb[0..n], true, mut w)
        ok := if s.err == core.SysErr.None && w.a == i64(st.kind) { 1 } else { 0 }
        sys.PrintErr("S {h} {ok}\n", (h: h, ok: ok)) else {}
        if st.kind == core.IoKindDir {
            c := rt.DirOpenAt(io, d, nm)
            Assert(c.err == core.SysErr.None)
            ch := Trunc[uint](c.value)
            var inner: [256]u8 = [0; 256]
            var ist := core.IoStat(kind: 0, size: 0, modified: 0)
            for {
                q := NativeReadDir(io, ch, mut inner[..], mut ist)
                Assert(q.err == core.SysErr.None)
                if q.value == 0 {
                    break
                }
                var ib: [1024]u8 = [0; 1024]
                ih := OrHex(mut ib, inner[0..Trunc[int](q.value)])
                sys.PrintErr("I {h} {ih}\n", (h: h, ih: ih)) else {}
            }
            Assert(NativeClose(io, ch).err == core.SysErr.None)
        } else {
            // OpenBytes reopens the file from its whole path's bytes.
            ob := rt.OpenBytes(io, pb[0..n], 0)
            ook := if ob.err == core.SysErr.None && NativeClose(io, Trunc[uint](ob.value)).err == core.SysErr.None { 1 } else { 0 }
            sys.PrintErr("O {h} {ook}\n", (h: h, ook: ook)) else {}
            rm := rt.RemoveAt(io, d, nm, false)
            rok := if rm.err == core.SysErr.None { 1 } else { 0 }
            sys.PrintErr("R {h} {rok}\n", (h: h, rok: rok)) else {}
        }
    }
    Assert(NativeClose(io, d).err == core.SysErr.None)
}

test "RT8-Oracle-Calls StatEx, ReadLink, Cwd, Absolute and SetModified print what Go's os and filepath check (MK_RT8_ORACLE, set by TestRt_CallOracle)" (sys: &System) {
    io := sys.io
    dir := sys.Env("MK_RT8_ORACLE") else { "" }
    if dir.Len() == 0 || dir.Len() > 400 {
        return
    }
    // g is SetModified's, checked by its T line after the set.
    var names: [5]str = ["f", "ro", "d", "l", "missing"]
    for name in names[..] {
        for follow in 0..2 {
            var b: [512]u8 = [0; 512]
            p := OrJoin(mut b, dir, name)
            var w := core.IoWords(a: 0, b: 0, c: 0, d: 0)
            r := rt.StatEx(io, p, follow == 0, mut w)
            ok := if r.err == core.SysErr.None { 1 } else { 0 }
            sys.PrintErr("S {name} {follow} {ok} {kind} {size} {mtime} {mode}\n",
                (name: name, follow: follow, ok: ok, kind: w.a, size: w.b, mtime: w.c, mode: w.d)) else {}
        }
    }
    var b: [512]u8 = [0; 512]
    var out: [512]u8 = [0; 512]
    r := rt.ReadLink(io, OrJoin(mut b, dir, "l"), mut out[..])
    if r.err == core.SysErr.None {
        t := str.From(out[0..Trunc[int](r.value)]) else { "?" }
        sys.PrintErr("L {t}\n", (t: t)) else {}
    }
    c := rt.Cwd(io, mut out[..])
    Assert(c.err == core.SysErr.None && Trunc[int](c.value) < out.Len())
    cw := str.From(out[0..Trunc[int](c.value)]) else { "?" }
    sys.PrintErr("C {cw}\n", (cw: cw)) else {}
    var ins: [3]str = ["rel", ".", dir]
    for input in ins[..] {
        var o2: [512]u8 = [0; 512]
        a := rt.Absolute(io, input.AsBytes(), mut o2[..])
        Assert(a.err == core.SysErr.None && Trunc[int](a.value) < o2.Len())
        ab := str.From(o2[0..Trunc[int](a.value)]) else { "?" }
        sys.PrintErr("A {input} {ab}\n", (input: input, ab: ab)) else {}
    }
    // 2020-02-03T04:05:06.7Z, a multiple of Windows' 100 ns.
    nanos: i64 = 1_580_702_706_700_000_000
    Assert(rt.SetModified(io, OrJoin(mut b, dir, "g"), nanos).err == core.SysErr.None)
    sys.PrintErr("T g {nanos}\n", (nanos: nanos)) else {}
}

// OrI32 is the little-endian i32 at b[at..at + 4].
OrI32(b: []u8, at: int): i64 {
    u := u32(b[at]) | (u32(b[at + 1]) << 8) | (u32(b[at + 2]) << 16) | (u32(b[at + 3]) << 24)
    return i64(Trunc[i32](u))
}

test "RT8-Oracle-Zone TimeZone's biases and DST months print what Go's time.Local checks (MK_RT8_ZONE, set by TestRt_ZoneOracle)" (sys: &System) {
    io := sys.io
    year := sys.Env("MK_RT8_ZONE") else { "" }
    if year.Len() != 4 || Target.os != Os.Windows {
        return
    }
    var y: i64 = 0
    for c in year.AsBytes() {
        y = y * 10 + i64(c - 48)
    }
    var ys: [2]i64 = [0, y]
    for want in ys[..] {
        var tz: [432]u8 = [0; 432]
        z := rt.TimeZone(io, want, mut tz[..])
        Assert(z.err == core.SysErr.None)
        // TIME_ZONE_INFORMATION: Bias at 0, StandardDate at 68 (wMonth at
        // 70), StandardBias at 84, DaylightDate at 152 (wMonth at 154),
        // DaylightBias at 168; DYNAMIC_ adds DynamicDaylightTimeDisabled at 428.
        off := if want == 0 { tz[428] } else { 0 }
        sys.PrintErr("Z {want} {b} {sb} {db} {sm} {dm} {off}\n", (want: want, b: OrI32(tz[..], 0),
            sb: OrI32(tz[..], 84), db: OrI32(tz[..], 168), sm: u32(tz[70]) | (u32(tz[71]) << 8),
            dm: u32(tz[154]) | (u32(tz[155]) << 8), off: off)) else {}
    }
}

test "RT8-Oracle-Env an extended environment's child sees what Go's os/exec gives (MK_RT8_ENVORACLE, set by TestRt_EnvOracle)" (sys: &System) {
    io := sys.io
    on := sys.Env("MK_RT8_ENVORACLE") else { "" }
    if on.Len() == 0 {
        return
    }
    // The same list TestRt_EnvOracle (std/rt_oracle_test.go) appends to
    // the block's own environment for os/exec: a new name, an override of
    // PATH in another case on Windows, an empty value, and a name given
    // twice.
    var add: [6]str = ["MK_RT8_X=1", "PATH=/rt8:/usr/bin:/bin", "MK_RT8_EMPTY=", "MK_RT8_DUP=a",
        "MK_RT8_DUP=b", "mk_rt8_x=lower"]
    if PWin() {
        add[1] = "path=C:\\rt8;C:\\Windows\\System32"
        add[5] = "MK_RT8_Y=2"
    }
    var s := CSpec(core.IoStdNull, core.IoStdPipe, core.IoStdJoin)
    s.env_mode = core.IoEnvExtend
    r := CSh(io, "set", "env", add[..], "", mut s)
    Assert(r.err == core.SysErr.None)
    h := Trunc[uint](r.value)
    var chunk: [512]u8 = [0; 512]
    for {
        q := NativeRead(io, s.pipes[1], mut chunk[..], -1)
        Assert(q.err == core.SysErr.None)
        if q.value == 0 {
            break
        }
        var hb: [1024]u8 = [0; 1024]
        hx := OrHex(mut hb, chunk[0..Trunc[int](q.value)])
        sys.PrintErr("E {hx}\n", (hx: hx)) else {}
    }
    Assert(rt.ChildWait(io, h, s.job).value == 0)
    PClose(io, s.pipes[1])
    PClose(io, h)
}

test "RT8-Oracle-ReadLink ReadLink answers a link's substitute name, never its print name: a junction with a decoy print name, absolute and relative symbolic links (MK_RT8_READLINK, set by TestRt_ReadLinkSubstitute)" (sys: &System) {
    io := sys.io
    dir := sys.Env("MK_RT8_READLINK") else { "" }
    if dir.Len() == 0 || dir.Len() > 400 {
        return
    }
    var names: [3]str = ["j", "abs", "rel"]
    for name in names[..] {
        var b: [512]u8 = [0; 512]
        var out: [1024]u8 = [0; 1024]
        r := rt.ReadLink(io, OrJoin(mut b, dir, name), mut out[..])
        if r.err == core.SysErr.None {
            var hb: [1024]u8 = [0; 1024]
            h := OrHex(mut hb, out[0..Trunc[int](r.value)])
            sys.PrintErr("R {name} {h}\n", (name: name, h: h)) else {}
        } else {
            sys.PrintErr("R {name} -\n", (name: name)) else {}
        }
    }
}
