// verdict: reject E0301
// rule: D-491
// note: Remaining is none when no scope of the chain has a deadline, so it is ?time.Duration and is read through its optional
import std.time

Left(io: Io): i64 {
    var n: i64 = 0
    scope s := io.Scope() {
        d: time.Duration = s.Remaining() // <- E0301
        n = d.Nanos()
    }
    return n
}
