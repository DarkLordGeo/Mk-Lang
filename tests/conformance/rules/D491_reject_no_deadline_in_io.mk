// verdict: reject E0203
// rule: D-491
// note: there is no per-operation timeout argument and no deadline in Io; a deadline belongs to a scope
import std.time

Nap(io: Io): Canceled!void {
    io.CancelAfter(time.Seconds(1)) // <- E0203
    try io.Sleep(time.Millis(5))
}
