// Why: a program that reads a terminal must be testable and replayable
// under std.sim, with the same result from the same seed on every target.
// What: blocks for Open, Read, ReadSecret and Writer under a simulated Io,
// with deadline cuts and seeded crash points.
// How: Sim.TermInput queues a recorded key stream on the simulated clock;
// the blocks compare the events and their times with an uncut run.

// Open opens the Sim's one terminal (80 by 24, writes discarded, modes
// recorded), and no standard stream is a terminal. Term.Read's
// repeatability (a cut read loses nothing) is also tested natively on a
// pseudo-terminal (tests_pty.mk).

import std.sim
import std.time

test "Under std.sim, Open opens the Sim's terminal: 80 by 24, Raw and Restore record a mode, writes are discarded, a queued key is read, and no stream is a terminal" (sys: &System) {
    var s := sim.Sim.New(sys.arena, 7).Must()
    defer s.Free()
    io := s.Io()
    var t := Open(io).Must()
    (cols, rows) := t.Size(io).Must()
    Assert(cols == 80 && rows == 24)
    t.Raw(io).Must()
    // The runtime's restore registry takes no simulated terminal.
    Assert(t.reg == -1)
    var buf: [64]u8 = [0; 64]
    var w := t.Writer(io, mut buf[..])
    AltScreen(mut w, true).Must()
    w.Flush().Must()
    Assert(t.alt)
    s.TermInput("q".AsBytes(), time.Millis(5)).Must()
    ev := t.Read(sys.arena, io).Must()
    Assert(TmIsQuit(ev))
    t.Restore(io).Must()
    Assert(!t.is_raw)
    t.Close(io)
}

test "Under std.sim, IsTerminal and UseColor are false for each stream" (sys: &System) {
    var s := sim.Sim.New(sys.arena, 7).Must()
    defer s.Free()
    io := s.Io()
    for k in [Std.Stdin, Std.Stdout, Std.Stderr] {
        Assert(!IsTerminal(io, k))
        Assert(!UseColor(io, k))
    }
}

// TsQueue queues the recorded key stream on s's terminal, each arrival
// after its delay from the start: a, b; Up split as "\x1b[" then "A"; é and
// U+1F600 (a surrogate pair on Windows); Ctrl+Right split inside its
// parameters; F1 by SS3; Alt+x; a lone escape, z 40 ms after it; a
// bracketed paste split across three arrivals, its end marker cut in two;
// Backspace, Enter and Tab together; q. Each arrival holds whole
// characters (on Windows each call is decoded on its own) and fewer
// than 64 UTF-16 units (one console read).
TsQueue(s: mut sim.Sim) {
    s.TermInput("ab".AsBytes(), time.Millis(0)).Must()
    s.TermInput("\x1b[".AsBytes(), time.Millis(3)).Must()
    s.TermInput("A".AsBytes(), time.Millis(4)).Must()
    s.TermInput("é😀".AsBytes(), time.Millis(6)).Must()
    s.TermInput("\x1b[1;".AsBytes(), time.Millis(9)).Must()
    s.TermInput("5C".AsBytes(), time.Millis(11)).Must()
    s.TermInput("\x1bOP".AsBytes(), time.Millis(14)).Must()
    s.TermInput("\x1bx".AsBytes(), time.Millis(17)).Must()
    s.TermInput("\x1b".AsBytes(), time.Millis(20)).Must()
    s.TermInput("z".AsBytes(), time.Millis(60)).Must()
    s.TermInput("\x1b[200~he".AsBytes(), time.Millis(70)).Must()
    s.TermInput("llo wörld\x1b[20".AsBytes(), time.Millis(74)).Must()
    s.TermInput("1~".AsBytes(), time.Millis(77)).Must()
    s.TermInput("\x7f\r\t".AsBytes(), time.Millis(85)).Must()
    s.TermInput("q".AsBytes(), time.Millis(90)).Must()
}

// TsEvents is the stream's events as Read gives them, each with the
// simulated millisecond it is read at: the key of an arrival when it
// arrives, the escape 25 ms after it (EscapeWait), the paste when its end
// marker is whole. The model is the decoder's own blocks (tests_input.mk)
// and the escape wait (EscapeWait).
const TsEvents: str = "0 key Char a\n0 key Char b\n4 key Up\n6 key Char é\n6 key Char 😀\n11 key Right+c\n14 key F1\n17 key Char x+a\n45 key Escape\n60 key Char z\n77 paste hello wörld\n85 key Bs\n85 key Enter\n85 key Tab\n90 key Char q\n"

// TsCut5 is TsRun's whole run under 5 ms deadlines: a deadline cuts a read
// only where the stream has a gap of 5 ms or more, and the escape's 25 ms
// wait goes on across the five cuts in it. It is the same on every target
// and every seed: a simulated terminal makes the same Sim operations and
// tasks on Linux and Windows (Fill skips the Linux-only steps for it).
const TsCut5: str = "0 0 key Char a\n0 0 key Char b\n0 4 key Up\n0 6 key Char é\n0 6 key Char 😀\n0 11 key Right+c\n0 14 key F1\n0 17 key Char x+a\n5 45 key Escape\n2 60 key Char z\n3 77 paste hello wörld\n1 85 key Bs\n0 85 key Enter\n0 85 key Tab\n0 90 key Char q\n"

// TsRun reads TsQueue's stream under a Sim of seed until q, each Read under
// a deadline of cut ms (0: none). It returns the events, one line each
// with the millisecond it was read at, the whole run, where each event
// line starts with how many reads a deadline cut before it, and the cuts.
TsRun(a: &Arena, seed: u64, cut: i64): (events: str, run: str, cuts: int) from a {
    var s := sim.Sim.New(a, seed).Must()
    defer s.Free()
    io := s.Io()
    var t := Open(io).Must()
    t.Raw(io).Must()
    TsQueue(mut s)
    start := Now(io)
    var events := ""
    var run := ""
    var cuts := 0
    var since := 0
    var quit := false
    for _ in 0..1000 {
        if quit {
            break
        }
        var line := ""
        scope sc := io.Scope() {
            if cut > 0 {
                sc.CancelAfter(time.Millis(cut))
            }
            var read := true
            ev := t.Read(a, io) else e {
                // Only the deadline ends a Read here.
                Assert(cut > 0 && sc.Expired())
                Assert(match e {
                    Canceled { true }
                    _ { false }
                })
                read = false
                Event.Focus(on: false)
            }
            if read {
                ms := (Now(io) - start) / 1_000_000
                d := TmDescribe(a, ev)
                line = a.Fmt("{ms} {d}", (ms, d)).Must()
                quit = TmIsQuit(ev)
            }
        }
        if line.Len() == 0 {
            cuts += 1
            since += 1
        } else {
            events = a.Fmt("{events}{line}\n", (events, line)).Must()
            run = a.Fmt("{run}{since} {line}\n", (run, since, line)).Must()
            since = 0
        }
    }
    Assert(quit)
    t.Restore(io).Must()
    t.Close(io)
    return (events, run, cuts)
}

test "Under std.sim: a recorded key stream read with Term.Read cut by deadlines of 1 to 26 ms gives the uncut run's events at the uncut run's times, and each seed's run is the same twice (seeds 0 to 3)" (sys: &System) {
    a := sys.arena
    for seed in 0..4 {
        (events: whole, run: _, cuts: uncut) := TsRun(a, Cast[u64](seed), 0)
        Assert(whole == TsEvents && uncut == 0)
        for cut in [1, 2, 3, 5, 8, 13, 26] {
            (events, run, cuts) := TsRun(a, Cast[u64](seed), Cast[i64](cut))
            Assert(events == whole)
            // Each deadline cuts at least one read: the stream has a gap
            // longer than every deadline. At 1 ms, each idle millisecond's
            // read is cut: 81 cuts, the same on every target.
            Assert(cuts > 0)
            Assert(cut != 1 || cuts == 81)
            Assert(cut != 5 || run == TsCut5)
            (events: _, run: again, cuts: _) := TsRun(a, Cast[u64](seed), Cast[i64](cut))
            Assert(run == again)
        }
    }
}

// TsCrashes reads TsQueue's stream, each Read under a 1 ms deadline,
// under a Sim of each seed 0 to 15
// whose run ends at a seeded crash point (Sim.CrashAfterRandomOps, armed
// after Raw), and gives, per seed, how many reads (events and cuts) came
// before the crash's Canceled, or before q when q came first. Every Sim entry
// counts toward the crash point, so the counts are the same on Linux and
// Windows only when a Read makes the same Sim operations on both.
TsCrashes(a: &Arena): str from a {
    var out := ""
    for seed in 0..16 {
        var s := sim.Sim.New(a, Cast[u64](seed)).Must()
        defer s.Free()
        io := s.Io()
        var t := Open(io).Must()
        t.Raw(io).Must()
        TsQueue(mut s)
        s.CrashAfterRandomOps()
        var n := 0
        var done := false
        for _ in 0..1000 {
            if done {
                break
            }
            scope sc := io.Scope() {
                sc.CancelAfter(time.Millis(1))
                ev := t.Read(a, io) else e {
                    Assert(match e {
                        Canceled { true }
                        _ { false }
                    })
                    // A cut is a read; Canceled with the deadline not
                    // passed is the crash.
                    done = !sc.Expired()
                    Event.Focus(on: false)
                }
                done = done || TmIsQuit(ev)
            }
            if !done {
                n += 1
            }
        }
        // The crash makes Restore's mode call fail too; Close only
        // releases.
        t.Restore(io) else {}
        t.Close(io)
        out = a.Fmt("{out}{n} ", (out, n)).Must()
    }
    return out
}

// TsCrashCounts is TsCrashes' answer, measured on Windows and pinned: it
// must be the same on x86_64-linux. A
// change to the Sim operations a Read makes changes it on both targets
// alike, and is pinned again from either.
const TsCrashCounts: str = "101 101 80 101 101 67 101 101 101 24 101 8 88 85 101 101 "

test "Under std.sim: Term.Read makes the same Sim operations on every target, so a seeded crash point cuts the recorded stream at the same read on Linux and Windows (seeds 0 to 15)" (sys: &System) {
    Assert(TsCrashes(sys.arena) == TsCrashCounts)
}

test "Under std.sim, Term.Read with nothing queued, no deadline and no task to queue more (a deadlock) is Canceled, not a spin, and a key queued after it is read" (sys: &System) {
    var s := sim.Sim.New(sys.arena, 3).Must()
    defer s.Free()
    io := s.Io()
    var t := Open(io).Must()
    var canceled := false
    _ = t.Read(sys.arena, io) else e {
        canceled = match e {
            Canceled { true }
            _ { false }
        }
        Event.Focus(on: false)
    }
    Assert(canceled)
    s.TermInput("q".AsBytes(), time.Millis(2)).Must()
    Assert(TmIsQuit(t.Read(sys.arena, io).Must()))
    t.Close(io)
}

// TsSecret reads a secret under a Sim of seed: "ab" at 0 ms, a bracketed
// paste "c" with a line end at 5 ms, Enter at 9 ms, each ReadSecret under
// a 1 ms deadline until the line ends. It returns the line and how many
// calls a deadline cut.
TsSecret(a: &Arena, seed: u64): (line: str, cuts: int) from a {
    var s := sim.Sim.New(a, seed).Must()
    defer s.Free()
    io := s.Io()
    var t := Open(io).Must()
    s.TermInput("ab".AsBytes(), time.Millis(0)).Must()
    s.TermInput("\x1b[200~c\r\n\x1b[201~".AsBytes(), time.Millis(5)).Must()
    s.TermInput("\r".AsBytes(), time.Millis(9)).Must()
    var line := ""
    var cuts := 0
    for _ in 0..400 {
        var done := false
        scope sc := io.Scope() {
            sc.CancelAfter(time.Millis(1))
            got := t.ReadSecret(a, io) else e {
                Assert(match e {
                    Canceled { true }
                    _ { false }
                })
                cuts += 1
                ""
            }
            if got.Len() > 0 {
                line = got
                done = true
            }
        }
        if done {
            break
        }
    }
    // The line was wiped once it was returned, and the mode put back.
    Assert(t.secret_len == 0 && !t.is_raw)
    t.Close(io)
    return (line, cuts)
}

test "Under std.sim: Term.ReadSecret cut by a 1 ms deadline again and again keeps what was typed, and each seed's run is the same twice (seeds 0 to 3)" (sys: &System) {
    for seed in 0..4 {
        (line, cuts) := TsSecret(sys.arena, Cast[u64](seed))
        Assert(line == "abc" && cuts >= 2)
        (line: again, cuts: cuts2) := TsSecret(sys.arena, Cast[u64](seed))
        Assert(again == line && cuts2 == cuts)
    }
}

// TsCut is t.ReadSecret under a deadline of ms on the simulated clock,
// which must cut it (Canceled).
TsCut(t: mut Term, a: &Arena, io: Io, ms: i64) {
    scope sc := io.Scope() {
        sc.CancelAfter(time.Millis(ms))
        _ = t.ReadSecret(a, io) else e {
            Assert(match e {
                Canceled { true }
                _ { false }
            })
            ""
        }
    }
}

test "under std.sim, a ReadSecret cut on a Term that was not raw leaves raw mode in force with the line, and the call that ends the line, Restore or Close puts the mode back; Raw takes it over" (sys: &System) {
    a := sys.arena
    var s := sim.Sim.New(a, 4).Must()
    defer s.Free()
    io := s.Io()
    var t := Open(io).Must()
    s.TermInput("ab".AsBytes(), time.Millis(0)).Must()
    s.TermInput("c\r".AsBytes(), time.Millis(20)).Must()
    TsCut(mut t, a, io, 5)
    Assert(t.is_raw && t.secret_raw && t.secret_len == 2)
    Assert(t.ReadSecret(a, io).Must() == "abc")
    Assert(!t.is_raw && !t.secret_raw && t.saved_len == 0)
    // Restore puts the mode back too.
    s.TermInput("x".AsBytes(), time.Millis(1)).Must()
    TsCut(mut t, a, io, 5)
    Assert(t.is_raw && t.secret_raw)
    t.Restore(io).Must()
    Assert(!t.is_raw && !t.secret_raw)
    // Raw takes the mode over: the line's end then leaves it raw.
    s.TermInput("\r".AsBytes(), time.Millis(1)).Must()
    TsCut(mut t, a, io, 0)
    Assert(t.is_raw && t.secret_raw)
    t.Raw(io).Must()
    Assert(t.ReadSecret(a, io).Must() == "x")
    Assert(t.is_raw && !t.secret_raw)
    t.Close(io)
}

test "under std.sim, ReadSecret ends its line on a line feed as on Enter" (sys: &System) {
    a := sys.arena
    var s := sim.Sim.New(a, 5).Must()
    defer s.Free()
    io := s.Io()
    var t := Open(io).Must()
    s.TermInput("pw\n".AsBytes(), time.Millis(1)).Must()
    Assert(t.ReadSecret(a, io).Must() == "pw")
    s.TermInput("x\r".AsBytes(), time.Millis(1)).Must()
    Assert(t.ReadSecret(a, io).Must() == "x")
    t.Close(io)
}

test "Under std.sim: a Term.Writer flush in a cancelled scope (Prefix) fails WriteError and records the alternate screen and mouse capture it may have set, so Close leaves both" (sys: &System) {
    var s := sim.Sim.New(sys.arena, 6).Must()
    defer s.Free()
    io := s.Io()
    var t := Open(io).Must()
    t.Raw(io).Must()
    var buf: [64]u8 = [0; 64]
    var w := t.Writer(io, mut buf[..])
    var failed := false
    scope sc := io.Scope() {
        sc.Cancel()
        AltScreen(mut w, true).Must()
        MouseCapture(mut w, true).Must()
        w.Str("text").Must()
        w.Flush() else {
            failed = true
        }
    }
    Assert(failed)
    Assert(t.alt && t.mouse)
    // A flush that goes through after it is recorded exactly.
    var w2 := t.Writer(io, mut buf[..])
    AltScreen(mut w2, false).Must()
    w2.Flush().Must()
    Assert(!t.alt && t.mouse)
    t.Close(io)
}
