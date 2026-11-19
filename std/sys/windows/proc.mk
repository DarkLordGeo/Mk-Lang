// Why: std.proc's Run and Supervise start child processes, and on Windows
// a careless CreateProcessW leaks every inheritable handle into the child
// and quotes arguments in ways a child can misread.
//
// What: Spawn, PipeRoom, ProcKill, Inherited, InheritSeal, HandlesValue
// and PassMax: starting a child with its pipes and named handles, and the
// handles a parent passed to this process.
//
// How: CreateProcessW with STARTUPINFOEXW and
// PROC_THREAD_ATTRIBUTE_HANDLE_LIST, so the child inherits exactly the listed
// handles and no other inheritable handle of this process (Go's
// os.StartProcess and Rust's Command since 1.x do the same); the parent's end
// of each pipe is a named pipe server opened for overlapped I/O and attached
// to the port (Rust's anon_pipe: an anonymous pipe cannot ride a completion
// port), the child's end a plain inheritable client handle; the command line
// is quoted by CommandLineToArgvW's rules (Go's syscall.EscapeArg); the
// program is found in PATH by SearchPathW with ".exe" appended to a name
// without an extension (Rust's rule; the current directory is not searched,
// Go 1.19's rule). Every handle made for the call is closed on every path;
// the pages mapped for it are given back.

import std.core
import std.raw

struct PipeState {
    // The pipe name counter: a Swap-free Add gives each pipe its own name;
    // nothing else is published by it (the name only has to be unique).
    next: raw.Atomic[u64]
    // query: the address of ntdll's NtQueryInformationFile, 0 before the
    // first PipeRoom, 1 when it cannot be found. A cache: every racing
    // loader stores the same value and ntdll is never unloaded, so the
    // store creates no edge a reader needs.
    query: raw.Atomic[u64]
}

var Pipes: PipeState = PipeState(next: raw.Atomic[u64].New(0), query: raw.Atomic[u64].New(0))

const StartfUseStdHandles: u32 = 0x100
const ExtendedStartupInfo: u32 = 0x0008_0000
const UnicodeEnvironment: u32 = 0x400
const AttrHandleList: uint = 0x0002_0002
const HandleFlagInherit: u32 = 1
const DuplicateSameAccess: u32 = 2
const PipeAccessInbound: u32 = 1
const PipeAccessDuplex: u32 = 3
// FilePipeLocalInformation is FILE_INFORMATION_CLASS 24.
const FilePipeLocalInformation: u32 = 24
const FirstPipeInstance: u32 = 0x0008_0000
const PipeRejectRemote: u32 = 8
const PipeBytes: u32 = 65536
// MaxCmdUnits is CreateProcessW's command line limit with its NUL.
const MaxCmdUnits: int = 32768

// The call's pages, offsets in bytes: the program path and PATH's value
// (MaxCmdUnits units each), the attribute list, the small records, then
// the quoted command line (UTF-8), its UTF-16 form and the environment.
const PgApp: int = 0
const PgPath: int = 65536
const PgAttr: int = 131072
const PgAttrBytes: int = 2048
const PgSi: int = 133120
const PgPi: int = 133232
const PgSa: int = 133256
const PgList: int = 133280
const PgName: int = 133312
const PgQuoted: int = 135168

// SelfBytes bounds this program's path (Spawn's empty args[0]).
const SelfBytes: int = 98304

/// Spawn starts a child (SpawnWith, below). An empty args[0] names this
/// program's own executable (ExePath: SelfBytes of UTF-8 at most), which
/// becomes the child's program and its args[0]: Supervise's re-exec
/// needs no path of its own. The substituted list and the path live
/// in pages mapped for the call.
/// O(n) in the bytes of args and the PATH search; the pages it maps are given
/// back before it returns.
export Spawn(sp: *u8, args: []str, child: mut core.IoChild, inherit: uint): OsResult {
    if args.Len() == 0 || args[0].Len() > 0 {
        return SpawnWith(sp, args, child, inherit)
    }
    list := args.Len() * SizeOf[str]()
    total := PageRound(list + SelfBytes)
    pg := Reserve(total, 4096)
    if raw.IsNull(pg) || !Commit(pg, total) {
        if !raw.IsNull(pg) {
            Release(pg, total)
        }
        return FailWith(core.SysErr.Other)
    }
    var r := FailWith(core.SysErr.Other)
    unsafe "the call's pages: the list, then the path" {
        path := raw.Offset(pg, list)
        n := ExePath(raw.SliceMut(path, SelfBytes))
        if n > 0 {
            l := raw.PtrCast[u8, str](pg)
            raw.Write(l, raw.Str(path, n))
            for i in 1..args.Len() {
                raw.Write(raw.Offset(l, i), args[i])
            }
            r = SpawnWith(sp, raw.Slice(l, args.Len()), child, inherit)
        }
    }
    Release(pg, total)
    return r
}

// SpawnWith starts args[0] with args, the standard streams as child.stdio
// says (core.IoStdInherit, IoStdNull, IoStdPipe), and, when inherit is a
// socket, that socket inherited and named by MK_INHERIT in the child's
// environment. value: the process handle; child.pipes[i] the parent's end
// of stream i (attached to the port), 0 where it is not a pipe; child.pid.
// The environment is this process's without MK_INHERIT. sp is unused.
SpawnWith(sp: *u8, args: []str, child: mut core.IoChild, inherit: uint): OsResult {
    if args.Len() == 0 {
        return FailWith(core.SysErr.InvalidPath)
    }
    var qbytes := 2
    for a in args {
        for b in a.AsBytes() {
            if b == 0 {
                return FailWith(core.SysErr.InvalidPath)
            }
        }
        qbytes += a.Len() * 2 + 3
    }
    if qbytes > MaxCmdUnits * 4 {
        return Fail(206)
    }
    for i in 0..3 {
        if child.stdio[i] > core.IoStdPipe {
            return FailWith(core.SysErr.Other)
        }
    }
    var envp := raw.Null[u16]()
    var env_units := 0
    unsafe "kernel32; the block ends with two NULs" {
        envp = GetEnvironmentStringsW()
        if raw.IsNull(envp) {
            return LastError()
        }
        for raw.Read(raw.Offset(envp, env_units)) != 0 {
            for raw.Read(raw.Offset(envp, env_units)) != 0 {
                env_units += 1
            }
            env_units += 1
        }
    }
    cmd_off := PgQuoted + PageRound(qbytes)
    env_off := cmd_off + PageRound((qbytes + 1) * 2)
    total := PageRound(env_off + (env_units + 40) * 2)
    pg := Reserve(total, 4096)
    if raw.IsNull(pg) || !Commit(pg, total) {
        FreeMap(pg, total)
        unsafe "kernel32; envp is the block GetEnvironmentStringsW returned, freed once here" {
            _ = FreeEnvironmentStringsW(envp)
        }
        return FailWith(core.SysErr.Other)
    }
    var r := Ok(0)
    // Fresh pages (Reserve and Commit above), zeroed by the OS: no fill.
    unsafe "the call's pages, zeroed by the OS at their commit" {
        BuildEnv(envp, raw.PtrCast[u8, u16](raw.Offset(pg, env_off)))
        _ = FreeEnvironmentStringsW(envp)
        r = SpawnIn(pg, args, child, inherit, qbytes, cmd_off, env_off)
    }
    Release(pg, total)
    return r
}

// SpawnIn is Spawn over its pages: the program and the command line
// (Line), then the handles and CreateProcessW (SpawnHandles); every handle
// it made for the child is closed before it returns, and the parent's pipe
// ends too on a failure.
SpawnIn(pg: *u8, args: []str, child: mut core.IoChild, inherit: uint, qbytes: int, cmd_off: int,
    env_off: int): OsResult {
    var cmd := raw.Null[u16]()
    var bm := raw.Null[u8]()
    var bsz := 0
    var r := Line(pg, args, cmd_off, mut cmd, mut bm, mut bsz)
    if r.err == core.SysErr.None {
        unsafe "the call's pages hold every region named by the Pg offsets" {
            r = SpawnHandles(pg, raw.PtrCast[u8, u16](raw.Offset(pg, PgApp)), cmd, child, inherit, env_off)
        }
    }
    if bsz > 0 {
        Release(bm, bsz)
    }
    return r
}

// Line finds args[0] (its path to PgApp) and builds the command line in the
// call's pages; cmd is the line CreateProcessW takes. A batch file's line
// lives in pages of its own, bm and bsz, which the caller gives back after
// the call (bsz 0 when none). Shared by Spawn and child.mk's SpawnEx.
Line(pg: *u8, args: []str, cmd_off: int, cmd: mut *u16, bm: mut *u8, bsz: mut int): OsResult {
    unsafe "the call's pages hold every region named by the Pg offsets" {
        app := raw.PtrCast[u8, u16](raw.Offset(pg, PgApp))
        f := FindProgram(args[0], app, raw.PtrCast[u8, u16](raw.Offset(pg, PgPath)))
        if f.err != core.SysErr.None {
            return f
        }
        // A batch file runs under cmd.exe, which parses its command line
        // by rules of its own: CommandLineToArgvW quoting lets an argument
        // end cmd's quotes and run a command (CVE-2024-24576, "BatBadBut").
        // So, as Rust's Command since 1.77.2 does: the program is cmd.exe
        // from the system directory, and the line is built by BatchLine in
        // pages of its own. PATH's value is not needed past FindProgram, so
        // its region holds the full path, which is what CreateProcessW
        // would judge (trailing dots and spaces gone).
        full := raw.PtrCast[u8, u16](raw.Offset(pg, PgPath))
        fl := int(GetFullPathNameW(app, Trunc[u32](MaxCmdUnits), full, raw.Null[u8]()))
        if fl > 0 && fl < MaxCmdUnits && IsBatchName(full, fl) {
            var abytes := 0
            for a in args {
                abytes += a.Len() * 8 + 4
            }
            line := PageRound(64 + 3 * fl + abytes)
            size := line + MaxCmdUnits * 2
            m := Reserve(size, 4096)
            if raw.IsNull(m) || !Commit(m, size) {
                FreeMap(m, size)
                return FailWith(core.SysErr.Other)
            }
            *bm = m
            *bsz = size
            bcmd := raw.PtrCast[u8, u16](raw.Offset(m, line))
            r := BatchLine(full, fl, args, m, bcmd)
            if r.err != core.SysErr.None {
                return r
            }
            sl := int(GetSystemDirectoryW(app, Trunc[u32](MaxCmdUnits - 16)))
            if sl == 0 || sl >= MaxCmdUnits - 16 {
                return LastError()
            }
            PutUnits(raw.Offset(app, sl), "\\cmd.exe")
            raw.Write(raw.Offset(app, sl + 8), 0)
            *cmd = bcmd
            return Ok(0)
        }
        q := raw.Offset(pg, PgQuoted)
        var qn := 0
        for i, a in args {
            if i > 0 {
                raw.Write(raw.Offset(q, qn), 32)
                qn += 1
            }
            qn = QuoteArg(a.AsBytes(), q, qn)
        }
        c := raw.PtrCast[u8, u16](raw.Offset(pg, cmd_off))
        if Wide(raw.Str(q, qn), raw.SliceMut(c, MaxCmdUnits)) < 0 {
            return Fail(206)
        }
        *cmd = c
        return Ok(0)
    }
}

// SpawnHandles makes the child's handles for SpawnIn and calls Create with
// the program app and the command line cmd: every handle it made for the
// child is closed before it returns, and the parent's pipe ends too on a
// failure.
SpawnHandles(pg: *u8, app: *u16, cmd: *u16, child: mut core.IoChild, inherit: uint, env_off: int): OsResult {
    unsafe "the call's pages hold every region named by the Pg offsets" {
        sa := raw.Offset(pg, PgSa)
        Put32(sa, 0, 24)
        Put32(sa, 16, 1)
        // The child's handles (theirs[3] the socket duplicate), each
        // inheritable; the parent's pipe ends in child.pipes.
        var theirs: [4]uint = [0; 4]
        var mine: [3]uint = [0; 3]
        var r := Ok(0)
        for i in 0..3 {
            r = ChildStream(i, child.stdio[i], sa, raw.PtrCast[u8, u16](raw.Offset(pg, PgName)), mut theirs[i],
                mut mine[i])
            if r.err != core.SysErr.None {
                break
            }
        }
        if r.err == core.SysErr.None && inherit != 0 {
            var d: uint = 0
            if DuplicateHandle(GetCurrentProcess(), inherit, GetCurrentProcess(), raw.OfMut(mut d), 0, 1,
                DuplicateSameAccess) == 0 {
                r = LastError()
            } else {
                theirs[3] = d
                AppendInherit(raw.PtrCast[u8, u16](raw.Offset(pg, env_off)), d)
            }
        }
        if r.err == core.SysErr.None {
            r = Create(pg, app, cmd, raw.PtrCast[u8, u16](raw.Offset(pg, env_off)), theirs)
        }
        for i in 0..4 {
            if theirs[i] != 0 {
                _ = CloseHandle(theirs[i])
            }
        }
        if r.err != core.SysErr.None {
            for i in 0..3 {
                if mine[i] != 0 {
                    _ = CloseHandle(mine[i])
                }
            }
            return r
        }
        for i in 0..3 {
            child.pipes[i] = mine[i]
        }
        child.pid = i64(Get32(raw.Offset(pg, PgPi), 16))
        return r
    }
}

// Create fills the attribute list with the non-zero handles of theirs and
// calls CreateProcessW; value the process handle (the thread handle is
// closed at once).
Create(pg: *u8, app: *u16, cmd: *u16, env: *u16, theirs: [4]uint): OsResult {
    unsafe "pg is the call's committed pages, laid out for si, pi and the list" {
        si := raw.Offset(pg, PgSi)
        pi := raw.Offset(pg, PgPi)
        list := raw.Offset(pg, PgList)
        var n := 0
        for i in 0..4 {
            if theirs[i] != 0 {
                Put64(list, n * 8, u64(theirs[i]))
                n += 1
            }
        }
        Put32(si, 0, 112)
        Put32(si, 60, StartfUseStdHandles)
        Put64(si, 80, u64(theirs[0]))
        Put64(si, 88, u64(theirs[1]))
        Put64(si, 96, u64(theirs[2]))
        var attr := raw.Null[u8]()
        if n > 0 {
            attr = raw.Offset(pg, PgAttr)
            var size: uint = Trunc[uint](PgAttrBytes)
            if InitializeProcThreadAttributeList(attr, 1, 0, raw.OfMut(mut size)) == 0 {
                return LastError()
            }
            if UpdateProcThreadAttribute(attr, 0, AttrHandleList, list, Trunc[uint](n * 8), raw.Null[u8](),
                raw.Null[uint]()) == 0 {
                e := LastError()
                DeleteProcThreadAttributeList(attr)
                return e
            }
            Put64(si, 104, u64(raw.Addr(attr)))
        }
        inherits: i32 = if n > 0 { 1 } else { 0 }
        ok := CreateProcessW(app, cmd, raw.Null[u8](), raw.Null[u8](), inherits, ExtendedStartupInfo | UnicodeEnvironment,
            env, raw.Null[u16](), si, pi)
        r := if ok == 0 { LastError() } else { Ok(0) }
        if n > 0 {
            DeleteProcThreadAttributeList(attr)
        }
        if ok == 0 {
            return r
        }
        _ = CloseHandle(Trunc[uint](Get64(pi, 8)))
        return Ok(Trunc[i64](Get64(pi, 0)))
    }
}

// ChildStream makes stream i's handles for mode: theirs the child's
// (inheritable), mine the parent's pipe end (0 unless a pipe). Inherit
// duplicates this process's standard handle as inheritable (none when it
// has none: the child then has none either); Null opens NUL.
ChildStream(i: int, mode: u32, sa: *u8, name: *u16, theirs: mut uint, mine: mut uint): OsResult {
    unsafe "kernel32; sa is an inheritable SECURITY_ATTRIBUTES; name holds 64 units" {
        if mode == core.IoStdInherit {
            h := StdHandle(i)
            if h == 0 || h == InvalidHandle {
                return Ok(0)
            }
            var d: uint = 0
            if DuplicateHandle(GetCurrentProcess(), h, GetCurrentProcess(), raw.OfMut(mut d), 0, 1, DuplicateSameAccess) ==
                0 {
                // A handle that cannot be duplicated (a console pseudo-handle
                // before Windows 8) leaves the child without that stream.
                return Ok(0)
            }
            *theirs = d
            return Ok(0)
        }
        if mode == core.IoStdNull {
            var nul: [4]u16 = [78, 85, 76, 0]
            h := CreateFileW(raw.Data(nul[..]), GenericRead | GenericWrite, ShareAll, sa, OpenExisting, 0, 0)
            if h == InvalidHandle {
                return LastError()
            }
            *theirs = h
            return Ok(0)
        }
        // A pipe: the server end is the parent's (inbound for stdout and
        // stderr; duplex for stdin, only so that end has
        // FILE_READ_ATTRIBUTES and PipeRoom can read its write quota, while
        // the child's end stays read-only), FIRST_PIPE_INSTANCE so no other
        // process can have made the name first; a name collision tries the
        // next number.
        var access := PipeAccessInbound
        var client_access := GenericWrite
        if i == 0 {
            access = PipeAccessDuplex
            client_access = GenericRead
        }
        var server := InvalidHandle
        for _ in 0..16 {
            PipeName(name)
            server = CreateNamedPipeW(name, access | FlagOverlapped | FirstPipeInstance, PipeRejectRemote, 1, PipeBytes,
                PipeBytes, 0, raw.Null[u8]())
            if server != InvalidHandle || GetLastError() != 5 {
                break
            }
        }
        if server == InvalidHandle {
            return LastError()
        }
        client := CreateFileW(name, client_access, 0, sa, OpenExisting, 0, 0)
        if client == InvalidHandle {
            r := LastError()
            _ = CloseHandle(server)
            return r
        }
        if !PollAttach(server) {
            r := LastError()
            _ = CloseHandle(client)
            _ = CloseHandle(server)
            return r
        }
        *theirs = client
        *mine = server
        return Ok(0)
    }
}

/// PipeRoom is the number of bytes a write to the pipe end h buffers at
/// once (FILE_PIPE_LOCAL_INFORMATION.WriteQuotaAvailable, read with ntdll's
/// NtQueryInformationFile, found at run time as hrtimer.mk finds its
/// entries; libuv's pipe.c reads the same field), or -1 when h is not a pipe
/// end this process may query. std.task writes at most that much to a
/// child's stdin at a time, or 1 byte when it is 0: a named-pipe write that
/// a cancel ends after its reader took part of it completes with 995 and a
/// count of 0 (measured on Windows 11), so only a write that fits
/// the quota (it is complete when WriteFile returns) or a 1-byte write (read
/// whole or not at all) has an exact answer when it is cancelled.
/// O(1) system calls; allocates nothing.
export PipeRoom(h: uint): int {
    unsafe "ntdll; the name is a NUL-terminated constant; the out areas are this frame's" {
        // Edge: a cache: every racing finder stores the same address (or 1);
        // the Store publishes it.
        var a := Pipes.query.Load()
        if a == 0 {
            var name: [16]u16 = [0; 16]
            _ = Wide("ntdll.dll", mut name[..])
            m := GetModuleHandleW(raw.Data(name[..]))
            p := if m != 0 { GetProcAddress(m, raw.StrData("NtQueryInformationFile\u{0}")) } else { raw.Null[u8]() }
            a = 1
            if !raw.IsNull(p) {
                a = u64(raw.Addr(p))
            }
            // Edge: publishes the address found above (or 1) to later Loads.
            Pipes.query.Store(a)
        }
        if a == 1 {
            return -1
        }
        f := raw.Read(raw.PtrCast[u64, fn(uint, *u8, *u8, u32, u32): i32](raw.Of(&a)))
        var iosb: [2]u64 = [0; 2]
        var info: [10]u32 = [0; 10]
        st := f(h, raw.PtrCast[u64, u8](raw.Data(iosb[..])), raw.PtrCast[u32, u8](raw.Data(info[..])), 40,
            FilePipeLocalInformation)
        return if st != 0 { -1 } else { int(info[7]) }
    }
}

// PipeName writes \\.\pipe\mk-<pid>-<n> with a fresh n.
PipeName(name: *u16) {
    // Edge: none; a counter that makes each name distinct.
    n := Pipes.next.Add(1)
    unsafe "name holds 64 units: the prefix is 14, each number at most 20" {
        PutUnits(name, "\\\\.\\pipe\\mk-")
        var k := 12
        k = PutDecimal(name, k, Pid())
        raw.Write(raw.Offset(name, k), 45)
        k = PutDecimal(name, k + 1, n)
        raw.Write(raw.Offset(name, k), 0)
    }
}

// PutDecimal writes v in decimal at p[k..] and returns the index after it.
PutDecimal(p: *u16, k: int, v: u64): int {
    var digits: [20]u16 = [0; 20]
    var n := 0
    var x := v
    for {
        digits[n] = Trunc[u16](48 + x % 10)
        n += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    unsafe "the caller's buffer has room for 20 units after k" {
        for j in 0..n {
            raw.Write(raw.Offset(p, k + j), digits[n - 1 - j])
        }
    }
    return k + n
}

// QuoteArg appends a to q at n by CommandLineToArgvW's rules (Go's
// syscall.EscapeArg): as it is when it holds no space, tab or quote and is
// not empty; else in quotes, with each run of backslashes before a quote
// doubled plus one, and a run at the end doubled. q has 2 * len + 3 bytes
// free. The special bytes are ASCII, so quoting UTF-8 bytes is quoting the
// scalars.
QuoteArg(a: []u8, q: *u8, var n: int): int {
    var plain := a.Len() > 0
    for b in a {
        if b == 32 || b == 9 || b == 34 {
            plain = false
        }
    }
    unsafe "q has room, sized by Spawn" {
        if plain {
            for b in a {
                raw.Write(raw.Offset(q, n), b)
                n += 1
            }
            return n
        }
        raw.Write(raw.Offset(q, n), 34)
        n += 1
        var slashes := 0
        for b in a {
            if b == 92 {
                slashes += 1
            } else if b == 34 {
                for _ in 0..slashes * 2 + 1 {
                    raw.Write(raw.Offset(q, n), 92)
                    n += 1
                }
                slashes = 0
            } else {
                for _ in 0..slashes {
                    raw.Write(raw.Offset(q, n), 92)
                    n += 1
                }
                slashes = 0
            }
            if b != 92 {
                raw.Write(raw.Offset(q, n), b)
                n += 1
            }
        }
        for _ in 0..slashes * 2 {
            raw.Write(raw.Offset(q, n), 92)
            n += 1
        }
        raw.Write(raw.Offset(q, n), 34)
        return n + 1
    }
}

// IsBatchName: the full path p (units long) ends in .bat or .cmd, ASCII
// case ignored: what CreateProcessW runs through cmd.exe.
IsBatchName(p: *u16, units: int): bool {
    if units < 4 {
        return false
    }
    var e: [4]u32 = [0; 4]
    unsafe "p holds units units" {
        for k in 0..4 {
            var u := u32(raw.Read(raw.Offset(p, units - 4 + k)))
            if u >= 65 && u <= 90 {
                u += 32
            }
            e[k] = u
        }
    }
    if e[0] != 46 {
        return false
    }
    bat := e[1] == 98 && e[2] == 97 && e[3] == 116
    cmd := e[1] == 99 && e[2] == 109 && e[3] == 100
    return bat || cmd
}

// BatchLine writes the command line of a batch file, Rust's
// make_bat_command_line: cmd.exe /e:ON /v:OFF /d /c ""<script>" <args>"
// (command extensions on for the % rule below, delayed expansion off so !
// is plain, AutoRun off), the script by its full path p (units long), each
// of args[1..] by BatArg; built as UTF-8 at buf, then as UTF-16 into cmd
// (MaxCmdUnits units). A script path holding a quote or ending in a
// backslash, or an argument BatArg refuses, is InvalidPath (Rust's
// InvalidInput); a line past MaxCmdUnits is 206.
BatchLine(p: *u16, units: int, args: []str, buf: *u8, cmd: *u16): OsResult {
    unsafe "p holds units units; buf was sized by SpawnIn for the worst case" {
        for k in 0..units {
            if raw.Read(raw.Offset(p, k)) == 34 {
                return FailWith(core.SysErr.InvalidPath)
            }
        }
        if raw.Read(raw.Offset(p, units - 1)) == 92 {
            return FailWith(core.SysErr.InvalidPath)
        }
        var n := 0
        for b in "cmd.exe /e:ON /v:OFF /d /c \"\"".AsBytes() {
            raw.Write(raw.Offset(buf, n), b)
            n += 1
        }
        nb := NameLen(p, units)
        Narrows(p, units, raw.SliceMut(raw.Offset(buf, n), nb))
        n += nb
        raw.Write(raw.Offset(buf, n), 34)
        n += 1
        for i in 1..args.Len() {
            raw.Write(raw.Offset(buf, n), 32)
            n = BatArg(args[i].AsBytes(), buf, n + 1)
            if n < 0 {
                return FailWith(core.SysErr.InvalidPath)
            }
        }
        raw.Write(raw.Offset(buf, n), 34)
        n += 1
        return if Wide(raw.Str(buf, n), raw.SliceMut(cmd, MaxCmdUnits)) < 0 { Fail(206) } else { Ok(0) }
    }
}

// BatArg appends one batch-file argument a to q at n, Rust's
// append_bat_arg: -1 for a CR or LF (cmd would cut the line there). It is
// quoted when empty, when it ends in a backslash, or when it holds any
// ASCII byte but a letter, a digit or one of #$*+-./:?@\_ (or a C1
// control); inside, a quote is doubled (a run of backslashes before it
// doubled too, for CommandLineToArgvW inside the script's callees), and
// each % becomes %%cd:~,% so no %VAR% expands. Worst case 8 bytes per
// byte plus 2 (SpawnIn sizes for it).
BatArg(a: []u8, q: *u8, var n: int): int {
    var quoted := a.Len() == 0 || a[a.Len() - 1] == 92
    for i, c in a {
        if c == 13 || c == 10 {
            return -1
        }
        if c < 128 {
            alnum := (c >= 97 && c <= 122) || (c >= 65 && c <= 90) || (c >= 48 && c <= 57)
            ok := c == 35 || c == 36 || c == 42 || c == 43 || c == 45 || c == 46 || c == 47 || c == 58 || c == 63 ||
                c == 64 || c == 92 || c == 95
            if !alnum && !ok {
                quoted = true
            }
        } else if c == 0xC2 && i + 1 < a.Len() && a[i + 1] >= 0x80 && a[i + 1] <= 0x9F {
            quoted = true
        }
    }
    unsafe "q has room, sized by SpawnIn" {
        if quoted {
            raw.Write(raw.Offset(q, n), 34)
            n += 1
        }
        var slashes := 0
        for c in a {
            if c == 92 {
                slashes += 1
            } else {
                if c == 34 {
                    for _ in 0..slashes {
                        raw.Write(raw.Offset(q, n), 92)
                        n += 1
                    }
                    raw.Write(raw.Offset(q, n), 34)
                    n += 1
                } else if c == 37 {
                    for b in "%%cd:~,".AsBytes() {
                        raw.Write(raw.Offset(q, n), b)
                        n += 1
                    }
                }
                slashes = 0
            }
            raw.Write(raw.Offset(q, n), c)
            n += 1
        }
        if quoted {
            for _ in 0..slashes {
                raw.Write(raw.Offset(q, n), 92)
                n += 1
            }
            raw.Write(raw.Offset(q, n), 34)
            n += 1
        }
    }
    return n
}

// HasSeparator: s names a path (a slash, a backslash or a drive colon),
// not a program to search for.
HasSeparator(s: []u8): bool {
    for b in s {
        if b == 47 || b == 92 || b == 58 {
            return true
        }
    }
    return false
}

// FindProgram writes the program's path to app (MaxCmdUnits units): a
// name with a separator as it is (with ".exe" when it names nothing and
// has no extension), else the first match in PATH (SearchPathW, ".exe"
// appended to a name without an extension). NotFound when none.
FindProgram(name: str, app: *u16, path: *u16): OsResult {
    b := name.AsBytes()
    if b.Len() == 0 {
        return Fail(2)
    }
    unsafe "app and path hold MaxCmdUnits units each" {
        n := Wide(name, raw.SliceMut(app, MaxCmdUnits - 4))
        if n < 0 {
            return Fail(206)
        }
        if HasSeparator(b) {
            if GetFileAttributesW(app) == InvalidAttributes && !HasExtension(b) {
                PutUnits(raw.Offset(app, n - 1), ".exe")
                raw.Write(raw.Offset(app, n + 3), 0)
            }
            return Ok(0)
        }
        var pname: [8]u16 = [80, 65, 84, 72, 0, 0, 0, 0]
        got := GetEnvironmentVariableW(raw.Data(pname[..]), path, Trunc[u32](MaxCmdUnits))
        if got == 0 || int(got) >= MaxCmdUnits {
            return Fail(2)
        }
        // The name, copied out of app before SearchPathW writes app.
        var small: [260]u16 = [0; 260]
        var big := raw.Null[u8]()
        var src := raw.DataMut(mut small[..])
        if n > 260 {
            big = Reserve(PageRound(n * 2), 4096)
            if raw.IsNull(big) || !Commit(big, PageRound(n * 2)) {
                FreeMap(big, PageRound(n * 2))
                return FailWith(core.SysErr.Other)
            }
            src = raw.PtrCast[u8, u16](big)
        }
        raw.Copy(src, app, n)
        var ext: [5]u16 = [46, 101, 120, 101, 0]
        found := SearchPathW(path, src, raw.Data(ext[..]), Trunc[u32](MaxCmdUnits), app, raw.Null[u8]())
        e := GetLastError()
        if !raw.IsNull(big) {
            FreeMap(big, PageRound(n * 2))
        }
        if found == 0 || int(found) >= MaxCmdUnits {
            return if found == 0 && e != 2 && e != 3 { Fail(e) } else { Fail(2) }
        }
        return Ok(0)
    }
}

// HasExtension: the last path component of s holds a dot.
HasExtension(s: []u8): bool {
    var i := s.Len()
    for i > 0 {
        i -= 1
        c := s[i]
        if c == 46 {
            return true
        }
        if c == 47 || c == 92 || c == 58 {
            return false
        }
    }
    return false
}

// BuildEnv copies the environment block e to out without any MK_INHERIT
// entry (names compare without regard to ASCII case, as Windows compares
// them). out has room for e and one entry of 40 units more.
BuildEnv(e: *u16, out: *u16) {
    var i := 0
    var o := 0
    unsafe "e ends with two NULs; out was sized by Spawn" {
        for raw.Read(raw.Offset(e, i)) != 0 {
            var j := i
            for raw.Read(raw.Offset(e, j)) != 0 {
                j += 1
            }
            if !IsInheritName(raw.Offset(e, i)) {
                raw.Copy(raw.Offset(out, o), raw.Offset(e, i), j - i + 1)
                o += j - i + 1
            }
            i = j + 1
        }
        raw.Write(raw.Offset(out, o), 0)
    }
}

// IsInheritName: the entry at p is MK_INHERIT=..., ASCII case ignored.
// MK_HANDLES=... too: each names handles of this process only, so
// no child is given either from this process's environment.
IsInheritName(p: *u16): bool {
    return HasName(p, "MK_INHERIT=") || HasName(p, "MK_HANDLES=")
}

// HasName: the entry at p starts with name, ASCII case ignored.
HasName(p: *u16, name: str): bool {
    for k, c in name.AsBytes() {
        unsafe "p is a NUL-terminated entry: a NUL unit ends the compare" {
            var u := u32(raw.Read(raw.Offset(p, k)))
            if u >= 97 && u <= 122 {
                u -= 32
            }
            if u != u32(c) {
                return false
            }
        }
    }
    return true
}

// AppendInherit adds MK_INHERIT=<h> at the end of the block BuildEnv
// wrote (CreateProcessW takes an unsorted block; the order is a convention
// of SetEnvironmentVariable, not a rule of process creation).
AppendInherit(out: *u16, h: uint) {
    var o := 0
    unsafe "out ends with an empty entry (one NUL) and has room for 40 units" {
        for raw.Read(raw.Offset(out, o)) != 0 {
            for raw.Read(raw.Offset(out, o)) != 0 {
                o += 1
            }
            o += 1
        }
        PutUnits(raw.Offset(out, o), "MK_INHERIT=")
        k := PutDecimal(out, o + 11, u64(h))
        raw.Write(raw.Offset(out, k), 0)
        raw.Write(raw.Offset(out, k + 1), 0)
    }
}

/// ProcKill ends the process h at once (TerminateProcess, exit code 1). A
/// process that already ended, or whose termination is under way, is
/// success: TerminateProcess then fails with ERROR_ACCESS_DENIED, before the
/// handle is signalled in the second case (which a second Kill right
/// after the first meets), and GetExitCodeProcess already
/// answers a code that is not STILL_ACTIVE (259). Rust's Child::kill makes
/// the same test (ACCESS_DENIED, then try_wait). Any other failure is the
/// OS's. O(1): one system call, three when the process had ended; allocates
/// nothing.
export ProcKill(h: uint): OsResult {
    var code: u32 = 0
    unsafe "kernel32 calls: values and handles, and code, a local the call writes" {
        if TerminateProcess(h, 1) != 0 {
            return Ok(0)
        }
        e := GetLastError()
        if e != 5 {
            return Fail(e)
        }
        if WaitForSingleObject(h, 0) == 0 {
            return Ok(0)
        }
        if GetExitCodeProcess(h, raw.OfMut(mut code)) != 0 && code != 259 {
            return Ok(0)
        }
        return Fail(e)
    }
}

/// Inherited answers the socket this process inherited from a Spawn with
/// inherit (MK_INHERIT in the environment, the handle's value), made not
/// inheritable again, and removes MK_INHERIT from the environment, so this
/// process's own children never see it. os is SharedPort: the socket stays
/// associated with the parent's completion port, so its accepts take the
/// shared-port path (poll_iocp.mk). NotFound when there is none; a value
/// that names no socket is Other.
/// O(1) system calls besides the environment scan; allocates nothing beyond the
/// socket's registration with the poller.
export Inherited(sp: *u8): OsResult {
    iv := InheritValue(sp)
    if iv == -1 {
        return FailWith(core.SysErr.NotFound)
    }
    if iv < 0 {
        return FailWith(core.SysErr.Other)
    }
    v := Trunc[u64](iv)
    var name: [16]u16 = [0; 16]
    _ = Wide("MK_INHERIT", mut name[..])
    unsafe "kernel32; name is NUL-terminated in this frame" {
        _ = SetEnvironmentVariableW(raw.Data(name[..]), raw.Null[u16]())
        h := Trunc[uint](v)
        if h == 0 || HandleKind(h) != 4 {
            return FailWith(core.SysErr.Other)
        }
        _ = SetHandleInformation(h, HandleFlagInherit, 0)
        return OsResult(value: Trunc[i64](v), err: core.SysErr.None, os: SharedPort)
    }
}

// InheritValue parses MK_INHERIT: the handle's value, -1 when it is not
// set, -2 when it is not a decimal of at most 18 digits.
InheritValue(sp: *u8): i64 {
    var buf: [24]u8 = [0; 24]
    n := GetEnvRaw(sp, "MK_INHERIT", mut buf[..])
    if n <= 0 {
        return -1
    }
    if n > 18 {
        return -2
    }
    var v: i64 = 0
    for i in 0..n {
        c := buf[i]
        if c < 48 || c > 57 {
            return -2
        }
        v = v * 10 + i64(c - 48)
    }
    return v
}

/// InheritSeal runs at start-up (std.task's Start): the socket MK_INHERIT
/// names is made not inheritable at once, as Linux makes it close-on-exec.
/// A spawn's handle list already keeps it from a child that does not list
/// it; this keeps the flag off for any other way of starting a process. The handle stays this process's for Inherited.
/// O(1) system calls; allocates nothing.
/// The handles MK_HANDLES names (the handles a Command named, each
/// inherited as inheritable) are checked first (HandlesCheck), before this
/// touches any handle, and the verdict is kept for HandlesValue: a list
/// that passes is made not inheritable; a list that fails is left as it
/// is, every handle it names untouched.
export InheritSeal(sp: *u8) {
    v := InheritValue(sp)
    var hs: [64]uint = [0; 64]
    n := HandlesCheck(v, mut hs[..])
    // Edge: written once by start-up's only thread, before any task or
    // carrier exists; HandlesValue's Load comes after.
    Seal.handles.Store(if n == -1 { SealNone } else if n < 0 { SealRefused } else { SealGood })
    if v > 0 {
        h := Trunc[uint](v)
        if HandleKind(h) == 4 {
            unsafe "a kernel32 call that passes no memory, only values and handles" {
                _ = SetHandleInformation(h, HandleFlagInherit, 0)
            }
        }
    }
    for i in 0..n {
        unsafe "a kernel32 call that passes no memory, only values and handles" {
            _ = SetHandleInformation(hs[i], HandleFlagInherit, 0)
        }
    }
}

// SealState is InheritSeal's verdict on MK_HANDLES.
struct SealState {
    handles: raw.Atomic[u32]
}

// Seal.handles: SealUnsealed until InheritSeal runs (a program with no
// task never runs it), then SealNone, SealGood or SealRefused.
var Seal: SealState = SealState(handles: raw.Atomic[u32].New(0))

const SealUnsealed: u32 = 0
const SealNone: u32 = 1
const SealGood: u32 = 2
const SealRefused: u32 = 3

/// HandlesValue is MK_HANDLES, the handles a Command named for
/// this process, as InheritSeal's HandlesCheck judged it at start-up,
/// before the runtime opened a handle of its own: the inherited handles'
/// values, in order, written to out (room for PassMax), and the count; -1
/// when MK_HANDLES is not set; -2 when the list failed the check (a value
/// named twice, more than out has room for, one that named no open handle
/// or a handle that was not inheritable, which every handle a spawn's
/// handle list gives is and every handle the runtime opens is not, a
/// standard handle, or MK_INHERIT's), so this process was not given those
/// handles by a Spawn and none is to be taken. In a process where
/// InheritSeal never ran, the check runs now: nothing has cleared the
/// named handles' inherit flag. sp is unused. O(bytes of the value plus
/// handles squared); allocates nothing.
export HandlesValue(sp: *u8, out: mut []uint): int {
    // Edge: reads InheritSeal's Store, which start-up's only thread made
    // before any task existed, so the verdict it published is seen whole.
    s := Seal.handles.Load()
    if s == SealUnsealed {
        return HandlesCheck(InheritValue(sp), out)
    }
    if s == SealNone {
        return -1
    }
    if s == SealRefused {
        return -2
    }
    return HandlesList(out)
}

// HandlesCheck is HandlesList, then a list is -2 unless its values are
// distinct multiples of 4, none is inherit (MK_INHERIT's value, or -1) or
// a standard handle's, and each names an open handle whose inherit flag
// is set. The kernel ignores a handle's low two bits (OBJ_HANDLE_TAGBITS),
// so h, h + 1, h + 2 and h + 3 name one handle: a value with either bit
// set is refused, which no Spawn writes (DuplicateHandle and ReOpenFile
// give multiples of 4). The inherit flag is GetHandleInformation's, which
// answers for any live handle without touching its object. Nothing is
// changed. O(bytes of the value plus
// handles squared); allocates nothing.
HandlesCheck(inherit: i64, out: mut []uint): int {
    n := HandlesList(out)
    if n < 0 {
        return n
    }
    for i in 0..n {
        h := out[i]
        if (h & 3) != 0 {
            return -2
        }
        if Trunc[i64](h) == inherit || h == StdHandle(0) || h == StdHandle(1) || h == StdHandle(2) {
            return -2
        }
        for j in 0..i {
            if out[j] == h {
                return -2
            }
        }
        var flags: u32 = 0
        unsafe "a kernel32 call; flags is this frame's" {
            if GetHandleInformation(h, raw.OfMut(mut flags)) == 0 || (flags & HandleFlagInherit) == 0 {
                return -2
            }
        }
    }
    return n
}

// HandlesList parses MK_HANDLES: the count of values written to out, -1
// when it is not set, -2 when it is not a list of at most out.Len()
// decimals of 1 and above.
HandlesList(out: mut []uint): int {
    var buf: [1536]u8 = [0; 1536]
    // GetEnvRaw reads the process's block; its sp is unused here.
    n := GetEnvRaw(raw.Null[u8](), "MK_HANDLES", mut buf[..])
    if n < 0 {
        return -1
    }
    if n > buf.Len() {
        return -2
    }
    return ParseHandles(buf[0..n], 1, out)
}

// ParseHandles parses a comma-separated list of decimal handle values,
// each no less than least and of at most 18 digits, into out: the count,
// or -2 when text is not such a list or holds more than out has room for.
// An empty text is the empty list. O(text's bytes); allocates nothing.
ParseHandles(text: []u8, least: u64, out: mut []uint): int {
    var k := 0
    var v: u64 = 0
    var digits := 0
    for i in 0..text.Len() + 1 {
        if i == text.Len() || text[i] == 44 {
            if i == text.Len() && k == 0 && digits == 0 {
                return 0
            }
            if digits == 0 || v < least || k >= out.Len() {
                return -2
            }
            out[k] = Trunc[uint](v)
            k += 1
            v = 0
            digits = 0
            continue
        }
        c := text[i]
        if c < 48 || c > 57 || digits >= 18 {
            return -2
        }
        v = v * 10 + u64(c - 48)
        digits += 1
    }
    return k
}

/// PassMax is std/sys/linux's: the most handles a Command names.
export const PassMax: int = 64
