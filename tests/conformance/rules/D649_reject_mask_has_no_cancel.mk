// verdict: reject E0203
// rule: D-649
// note: NoCancel gives an Io, not a scope; the masked Io is cancelled only by the scopes opened from it, so it has no Cancel or deadline of its own
import std.time

Close(io: Io) {
    q := io.NoCancel()
    q.Cancel() // <- E0203
    q.Sleep(time.Millis(1)) else {
    }
}
