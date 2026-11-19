// verdict: reject E0301
// rule: D-491
// note: no Scope function names time.Instant; a deadline at an instant is s.CancelAfter(t.Since(time.Now(io)))
import std.time

Until(io: Io, t: time.Instant) {
    scope s := io.Scope() {
        s.CancelAfter(t) // <- E0301
    }
}
