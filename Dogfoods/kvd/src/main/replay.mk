// Why: a client session can be replayed later with no server, to test the
// client against a recorded conversation.
// What: `kvd record` and `kvd replay`: RunRecord, RunReplay, Session, Save
// and Replay.
// How: the session is recorded at its `Io` with std.sim's Recorder and
// played back with its Replayer. A recording file is the line
// `KVDREC1 <address>`, then the Recorder's log.

import std.fs
import std.log
import std.net
import std.sim
import std.sync
import src.client
import src.server
import src.wal

/// The flags `kvd record` and `kvd replay` take.
const RecordFlags: Accepts = Accepts(values: ["--data"], switches: ["--check"])

/// The session run when no command is given.
const DefaultSession: []str = ["PING", "SET greeting hello", "APPEND greeting  world", "GET greeting", "INCR hits", "INCR hits", "HASH greeting", "KEYS", "QUIT"]

/// The first line of a recording.
const RecordMagic: str = "KVDREC1 "

/// Runs `commands` against the server at `addr` and writes the transcript
/// to `out`.
Session(a: &Arena, io: Io, addr: str, commands: []str, out: mut Writer): client.Error!void {
    conn := try client.Connect(io, addr, 3)
    defer conn.Close()
    var inbuf: [4096]u8 = [0; 4096]
    var outbuf: [4096]u8 = [0; 4096]
    var r := conn.Reader(io, mut inbuf[..])
    var w := conn.Writer(io, mut outbuf[..])
    for c in commands {
        _ = try Exchange(a, mut r, mut w, c, out, true)
    }
}

/// The commands of a record or replay command line, or the default session.
Commands(args: &Args, first: int): []str from args {
    rest := args.Rest(first)
    return if rest.Len() == 0 { DefaultSession } else { rest }
}

/// Stops the server at `addr` with `SHUTDOWN`; false when it cannot.
StopServer(io: Io, addr: str): bool {
    conn := client.Connect(io, addr, 3) else {
        return false
    }
    defer conn.Close()
    _ = conn.Write(io, b"SHUTDOWN\n") else {
        return false
    }
    var buf: [64]u8 = [0; 64]
    _ = conn.Read(io, mut buf[..]) else {
        return false
    }
    return true
}

/// `kvd record LOG [COMMAND...]`: starts a server on a loopback port, runs
/// the session through a Recorder, stops the server and writes LOG. With
/// `--check` it then replays LOG and compares the two transcripts.
RunRecord(sys: &System, rest: []str): int *rest into sys {
    io := sys.io
    a := sys.heap.Alloc()
    args := ParseArgs(sys.arena, rest, &RecordFlags) else e {
        return UsageError(sys, e)
    }
    path := args.Arg(0) else {
        sys.PrintErr("kvd: record needs a file to write; see kvd --help\n") else {}
        return ExitUsage
    }
    commands := Commands(&args, 1)
    p := DataDir(sys.arena, io, args.Str("--data", "kvd-record"), none) else e {
        sys.PrintErr("kvd: data directory: {e}\n", (e)) else {}
        return ExitFailure
    }
    key := HashKey.New(io) else e {
        sys.PrintErr("kvd: no random hash key: {e}\n", (e)) else {}
        return ExitFailure
    }
    opened := server.Open(a, sys.arena, io, p, key, wal.SyncPolicy.Always, wal.NoCrash, 16) else e {
        return OpenFailure(sys, &p, e)
    }
    server.Opened(state: st, ..) := opened
    l := net.Listen(io, "127.0.0.1:0") else e {
        sys.PrintErr("kvd: listen: {e}\n", (e)) else {}
        Discard(sys.heap, io, st)
        return ExitFailure
    }
    addr := sys.arena.Fmt("127.0.0.1:{port}", (port: l.Port())) else {
        l.Close()
        Discard(sys.heap, io, st)
        return ExitMemory
    }
    shared := sync.Shared.New(sys.heap, server.Config.Default()) else {
        l.Close()
        Discard(sys.heap, io, st)
        return ExitMemory
    }
    var recorder := sim.Recorder.New(io) else {
        shared.Free(sys.heap)
        l.Close()
        Discard(sys.heap, io, st)
        return ExitMemory
    }
    var said := Text.New(sys.arena)
    var failure: ?client.Error = none
    scope s := io.Scope() {
        s.go server.Accept(&st, sys.heap, shared.Clone(), &l, io)
        s.go server.Compactor(&s, &st, sys.heap, io)
        s.go server.Watch(&s, &st, io)
        // only this task uses the recorded Io: a log names no task, so a
        // recording replays only one task's calls
        var tw := said.Writer()
        Session(sys.arena, recorder.Io(), addr, commands, mut tw) else e {
            failure = some(e)
        }
        if !StopServer(io, addr) {
            st.Stop(io)
            s.Cancel()
        }
    }
    var errw := sys.Stderr()
    var lg := log.Logger.New(mut errw, log.Level.Warn)
    server.Drain(&st, sys.heap, io, mut lg)
    shared.Free(sys.heap)
    l.Close()
    code := Finish(sys, sys.heap, io, st)
    bytes := recorder.Log(sys.arena) else {
        recorder.Free()
        return ExitMemory
    }
    recorder.Free()
    if e := failure {
        sys.PrintErr("kvd: session: {e}\n", (e)) else {}
        return ExitFailure
    }
    if code != 0 {
        return code
    }
    sys.Print("{t}", (t: said.Str())) else {}
    if !Save(sys, io, path, addr, bytes) {
        return ExitFailure
    }
    if !args.Has("--check") {
        return 0
    }
    var again := Text.New(sys.arena)
    var aw := again.Writer()
    if !Replay(sys, bytes, addr, commands, mut aw) {
        return ExitFailure
    }
    if again.Str() != said.Str() {
        sys.PrintErr("kvd: the replay's transcript differs from the recording's\n") else {}
        return ExitFailure
    }
    sys.Print("replay matched the recording: {n} bytes of transcript\n", (n: said.Str().Len())) else {}
    return 0
}

/// Writes a recording file: the magic line with the address, then the log.
Save(sys: &System, io: Io, path: str, addr: str, log: []u8): bool {
    head := sys.arena.Fmt("{m}{addr}\n", (m: RecordMagic, addr)) else {
        return false
    }
    bytes := sys.arena.Slice[u8](head.Len() + log.Len()) else {
        return false
    }
    (mut bytes[..head.Len()]).CopyFrom(head.AsBytes())
    (mut bytes[head.Len()..]).CopyFrom(log)
    fs.WriteAll(io, path, bytes[..]) else e {
        sys.PrintErr("kvd: {path}: {e}\n", (path, e)) else {}
        return false
    }
    return true
}

/// Plays a recorded session back and writes its transcript to `out`.
/// False, with a message, when it diverges or does not use the whole log.
Replay(sys: &System, log: []u8, addr: str, commands: []str, out: mut Writer): bool {
    var player := sim.Replayer.New(log) else {
        return false
    }
    defer player.Free()
    Session(sys.arena, player.Io(), addr, commands, out) else e {
        sys.PrintErr("kvd: replay: {e}\n", (e)) else {}
        return false
    }
    if player.Diverged() {
        sys.PrintErr("kvd: replay diverged from the recording\n") else {}
        return false
    }
    if !player.Done() {
        sys.PrintErr("kvd: replay ended before the recording did\n") else {}
        return false
    }
    return true
}

/// `kvd replay LOG [COMMAND...]`: plays a recording back with no server.
RunReplay(sys: &System, rest: []str): int *rest into sys {
    args := ParseArgs(sys.arena, rest, &RecordFlags) else e {
        return UsageError(sys, e)
    }
    path := args.Arg(0) else {
        sys.PrintErr("kvd: replay needs a recording; see kvd --help\n") else {}
        return ExitUsage
    }
    data := fs.ReadBytes(sys.arena, sys.io, path) else e {
        sys.PrintErr("kvd: {path}: {e}\n", (path, e)) else {}
        return ExitFailure
    }
    var end := 0
    for end < data.Len() && data[end] != b'\n' {
        end += 1
    }
    head := str.From(data[..end]) else { "" }
    if end == data.Len() || !head.StartsWith(RecordMagic) {
        sys.PrintErr("kvd: {path}: not a kvd recording\n", (path)) else {}
        return ExitFailure
    }
    addr := head[RecordMagic.Len()..]
    var out := sys.Stdout()
    if !Replay(sys, data[end + 1..], addr, Commands(&args, 1), mut out) {
        return ExitFailure
    }
    return 0
}
