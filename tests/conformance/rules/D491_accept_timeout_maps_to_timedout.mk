// verdict: ok
// rule: D-491
// note: under ScopeOf[E] a task given &s maps its Canceled to time.TimedOut when s.Expired(), with TimedOut in E, so try scope receives it; and the nested form, the deadline on an outer scope, the tasks in an inner one, Expired read after the inner block
import std.time

error Busy

type Error = Busy | time.TimedOut | Canceled

// Wait sleeps an hour unless its scope ends first, and says why it ended.
Wait(io: Io, s: &Scope[Error]): Error!void {
    io.Sleep(time.Seconds(3_600)) else {
        if s.Expired() {
            fail time.TimedOut
        }
        fail Canceled
    }
}

// Mapped gives up after d with time.TimedOut.
Mapped(io: Io, d: time.Duration): Error!void {
    try scope s := io.ScopeOf[Error]() {
        s.CancelAfter(d)
        s.go Wait(io, &s)
    }
}

// Sleeper sleeps an hour unless cancelled.
Sleeper(io: Io) {
    io.Sleep(time.Seconds(3_600)) else {
    }
}

// Nested tells a timeout of a group of tasks from their end.
Nested(io: Io, d: time.Duration): bool {
    var timed_out := false
    scope outer := io.Scope() {
        outer.CancelAfter(d)
        scope inner := io.Scope() {
            inner.go Sleeper(io)
            inner.go Sleeper(io)
        }
        timed_out = outer.Expired()
    }
    return timed_out
}
