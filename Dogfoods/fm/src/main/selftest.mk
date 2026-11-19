// Why: the interactive UI can be tested with no terminal and no real disk.
// What: SelfTest, Setup and the Script of keys it types.
// How: runs the file manager on a std.sim machine seeded from `--seed`, with
// a fault injected into one directory read, and prints the transcript.

import std.fs
import std.sim
import std.time
import src.clock
import src.ui

// The keys of the scripted session, as a terminal sends them: down, copy
// and copy again over it, the other pane, view the copy and close the
// pager, delete it, back to the left pane, up to `docs`, delete it (a
// directory that holds a file, so its name is typed again), go to the
// right pane's directory by a paste, and quit.
const Script: str = "jcycy\tvqdy\tkdydocs\r\x1b[200~../right\x1b[201~q"

// selftest: the file manager on a simulated machine (std.sim) whose seed
// is --seed: a small tree is made on the simulated disk, the next read
// is set to fail with Denied (the right pane's first listing takes it,
// and fm shows the error), and Script is typed on the simulated terminal.
// The transcript goes to standard output, then the exit code.
// The same seed gives the same run.
SelfTest(sys: &System, args: &Args): int {
    var s := sim.Sim.New(sys.arena, Cast[u64](args.seed)) else {
        sys.PrintErr("fm: selftest: out of memory\n") else {}
        return 1
    }
    defer s.Free()
    io := s.Io()
    Setup(io) else e {
        sys.PrintErr("fm: selftest: setting up the simulated disk: {e}\n", (e)) else {}
        return 1
    }
    s.TermInput(Script.AsBytes(), time.Millis(5)) else {
        sys.PrintErr("fm: selftest: out of memory\n") else {}
        return 1
    }
    // the disk fills up under the scripted copy: its write fails, and fm says so
    s.FailNext(sim.Op.Write, sim.Fault.NoSpace)
    o := ui.Options(left: "demo/left", right: "demo/right", zone: clock.ZoneChoice.Utc)
    var out := sys.Stdout()
    code := ui.Run(sys.heap, io, &o, mut out) else e {
        sys.PrintErr("fm: selftest: {e}\n", (e)) else {}
        return 1
    }
    sys.Print("selftest: exit {code}\n", (code)) else {
        return 1
    }
    return 0
}

Setup(io: Io): fs.Error!void {
    try fs.MakeDirAll(io, "demo/left/docs")
    try fs.MakeDirAll(io, "demo/right")
    try fs.WriteAll(io, "demo/left/notes.txt", b"Notes kept on a simulated disk, wrapped by the pager at word boundaries.\n")
    try fs.WriteAll(io, "demo/left/todo.txt", b"- copy\n- delete\n")
    try fs.WriteAll(io, "demo/left/docs/readme.txt", b"inside docs\n")
}
