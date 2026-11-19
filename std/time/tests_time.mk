// Why: Duration constructors and the monotonic clock must be exact and never
// go backwards.
// What: tests of Seconds and Millis at the i64 edge, and of Now and Since.
// How: run by `mk test --std=std.time`; the overflow panics are a Go test
// (TestTimeDurations), since a test that must panic cannot be a test block.

test "Seconds and Millis are exact up to the i64 edge" {
    Assert(Seconds(0).Nanos() == 0)
    Assert(Seconds(1).Nanos() == 1_000_000_000)
    Assert(Seconds(-3).Nanos() == -3_000_000_000)
    Assert(Millis(1).Nanos() == 1_000_000)
    Assert(Millis(-250).Nanos() == -250_000_000)
    // The largest whole seconds and milliseconds an i64 of nanoseconds holds.
    Assert(Seconds(9_223_372_036).Nanos() == 9_223_372_036_000_000_000)
    Assert(Seconds(-9_223_372_036).Nanos() == -9_223_372_036_000_000_000)
    Assert(Millis(9_223_372_036_854).Nanos() == 9_223_372_036_854_000_000)
}

test "Now is monotonic and Since measures a sleep" (sys: &System) {
    io := sys.io
    a := Now(io)
    b := Now(io)
    Assert(b.Since(a).Nanos() >= 0)
    io.Sleep(Millis(20)) else {
        Assert(false)
        return
    }
    c := Now(io)
    // At least the 20 ms asked; Since is negative the other way round.
    Assert(c.Since(a).Nanos() >= 20_000_000)
    Assert(a.Since(c).Nanos() <= -20_000_000)
    Assert(c.Since(c).Nanos() == 0)
}

test "Now never goes backwards across tasks and carriers" (sys: &System) {
    io := sys.io
    var prev := Now(io)
    for _ in 0..10_000 {
        io.Yield() else {
            Assert(false)
            return
        }
        n := Now(io)
        Assert(n.Since(prev).Nanos() >= 0)
        prev = n
    }
}
