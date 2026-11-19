// verdict: reject E0801
// rule: D-491
// note: a task maps Canceled to time.TimedOut only with TimedOut in the scope's E; a deadline adds no error to Scope[E] by itself
import std.time

error Busy

Wait(io: Io, s: &Scope[Busy]): time.TimedOut!void {
    io.Sleep(time.Seconds(3_600)) else {
        fail time.TimedOut
    }
}

Run(io: Io): Busy!void {
    try scope s := io.ScopeOf[Busy]() {
        s.CancelAfter(time.Millis(10))
        s.go Wait(io, &s) // <- E0801
    }
}
