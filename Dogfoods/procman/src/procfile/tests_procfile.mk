// Why: every bad Procfile line must get the right error.
//
// What: tests of words, quotes, expansion, options and errors.
//
// How: parses small Procfiles with a fixed variable Lookup.

// Knows one variable, as an environment with HOME set would.
Lookup(name: str, out: mut Text): OutOfMemory!bool {
    if name == "HOME" {
        try out.Push("/home/ann")
        return true
    }
    return false
}

test "a line splits into words as a shell splits a simple command" (sys: &System) {
    entries := try Parse(sys.arena, "web: ./srv 'a b' \"c  d\" e\\ f $HOME ${HOME}/x $$1\n", Lookup)
    w := entries[0].argv
    Assert(w.Len() == 7)
    Assert(w[1] == "a b")
    Assert(w[2] == "c  d")
    Assert(w[3] == "e f")
    Assert(w[4] == "/home/ann")
    Assert(w[5] == "/home/ann/x")
    Assert(w[6] == "$1")
}

test "options in brackets set the restart policy, retries, back-off, limit and main" (sys: &System) {
    entries := try Parse(sys.arena, "w [restart=on-failure retries=7 backoff=2s limit=64k main]: ./w\n", Lookup)
    e := entries[0]
    Assert(e.restart == Restart.OnFailure)
    Assert(e.retries == 7)
    Assert(e.backoff_ms == 2000)
    Assert(e.limit == 65536)
    Assert(e.main)
}

test "a task line declares a task, and comments and blank lines are skipped" (sys: &System) {
    entries := try Parse(sys.arena, "# a comment\n\ntask migrate: ./m up # trailing\nweb: ./w a#b\n", Lookup)
    Assert(entries.Len() == 2)
    Assert(entries[0].kind == Kind.Task)
    Assert(entries[0].line == 3)
    Assert(entries[0].argv.Len() == 2)
    Assert(entries[1].argv[1] == "a#b")
}

test "a bad line fails with its line and column" (sys: &System) {
    col := match Parse(sys.arena, "web: ok\nworker [retries=lots]: ./w\n", Lookup) {
        ok(_) { 0 }
        err(e) {
            match e {
                BadValue(line, col) { if line == 2 { col } else { -1 } }
                _ { -2 }
            }
        }
    }
    Assert(col == 17)
}

test "an unknown variable is an error, not an empty word" (sys: &System) {
    unknown := match Parse(sys.arena, "web: ./w $NOT_SET\n", Lookup) {
        ok(_) { false }
        err(e) {
            match e {
                UnknownVariable(line, col) { line == 1 && col == 10 }
                _ { false }
            }
        }
    }
    Assert(unknown)
}

test "sizes and durations take their units and refuse what is out of range" {
    Assert(ParseSize("1m").Must() == 1_048_576)
    Assert(ParseSize("4096").Must() == 4096)
    Assert(ParseSize("2048m").IsNone())
    Assert(ParseSize("-1").IsNone())
    Assert(ParseMillis("250ms").Must() == 250)
    Assert(ParseMillis("1m").Must() == 60_000)
    Assert(ParseMillis("2h").IsNone())
    Assert(ParseCount("+3").IsNone())
}
