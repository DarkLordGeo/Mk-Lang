// Why: Db.Close cannot fail, so it takes its lock through a NoCancel Io
// (sqlite.mk's Hold) and retries; a dead Sim run must not make it retry forever.
// What: TestSqliteCloseUnderDivergedReplayer and
// TestSqliteCloseAfterDeadSimRun.
// How: a dead run (restarted or crashed) is not a scope cancellation, so the
// masked check goes on and Close returns; the programs print that it did.

package std

import "testing"

// d699SqliteSrc opens an in-memory database under a Sim, kills the run
// (Restart, then in a second Sim a crash point), and closes the database
// with the dead run's Io, printing a line after each Close returns.
const d699SqliteSrc = `import std.db.sqlite
import std.fs
import std.sim

// Dead: a counted operation answers Canceled (the run crashed).
Dead(io: Io): bool {
    fs.Remove(io, "d699-none") else e {
        return match e {
            Canceled { true }
            _ { false }
        }
    }
    return false
}

Probe(sys: &System, crash: bool): WriteError!void {
    var s := sim.Sim.New(sys.arena, 3) else {
        try sys.Print("no sim\n")
        return
    }
    defer s.Free()
    io := s.Io()
    db := sqlite.Open(sys.arena, io, ":memory:") else {
        try sys.Print("open failed\n")
        return
    }
    if crash {
        s.CrashAfterRandomOps()
        var k := 0
        for k < 100000 && !Dead(io) {
            k += 1
        }
        if k == 100000 {
            sys.Print("no crash\n") else {}
        }
    } else {
        _ = s.Restart()
    }
    db.Close()
    if crash {
        try sys.Print("closed after a crash\n")
    } else {
        try sys.Print("closed after a restart\n")
    }
}

export Main(sys: &System): WriteError!void {
    try Probe(sys, false)
    try Probe(sys, true)
}
`

// d699SqliteReplaySrc records sqlite.Open alone through a Recorder over the
// native Io, then replays Open and closes, which the log does not hold: the
// Replayer diverges at Close's masked check, which goes on, so
// Close returns.
const d699SqliteReplaySrc = `import std.db.sqlite
import std.sim

export Main(sys: &System): WriteError!void {
    var rec := sim.Recorder.New(sys.io) else {
        try sys.Print("no recorder\n")
        return
    }
    db0 := sqlite.Open(sys.arena, rec.Io(), ":memory:") else {
        rec.Free()
        try sys.Print("recorded open failed\n")
        return
    }
    log := rec.Log(sys.arena) else {
        db0.Close()
        rec.Free()
        try sys.Print("no log\n")
        return
    }
    db0.Close()
    rec.Free()
    var rep := sim.Replayer.New(log) else {
        try sys.Print("no replayer\n")
        return
    }
    db := sqlite.Open(sys.arena, rep.Io(), ":memory:") else {
        rep.Free()
        try sys.Print("replayed open failed\n")
        return
    }
    before := rep.Diverged()
    db.Close()
    after := rep.Diverged()
    rep.Free()
    try sys.Print("closed, diverged before {b} after {a}\n", (b: before, a: after))
}
`

// TestSqliteCloseUnderDivergedReplayer: Db.Close under a Replayer
// whose log ends before Close returns.
func TestSqliteCloseUnderDivergedReplayer(t *testing.T) {
	r := newSqliteRoot(t, d699SqliteReplaySrc, true, "[]")
	got := r.run(t)
	want := "closed, diverged before false after true\n"
	if got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}

// TestSqliteCloseAfterDeadSimRun: Db.Close with the Io of a Sim run
// that was restarted or crashed returns.
func TestSqliteCloseAfterDeadSimRun(t *testing.T) {
	r := newSqliteRoot(t, d699SqliteSrc, true, "[]")
	got := r.run(t)
	want := "closed after a restart\nclosed after a crash\n"
	if got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}
