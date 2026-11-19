// verdict: ok
// rule: D-491
// note: the three deadline methods on Scope and Scope[E] (CancelAfter, Remaining, Expired): a deadline armed twice (the earliest counts), a zero and a negative duration, Remaining read through its optional and over an ancestor, the methods as fn values, a task given &s, and a deadline at an instant written with Since
import std.time

error Boom

// Ticks sleeps until its scope is cancelled or its deadline fires.
Ticks(io: Io, s: &Scope) {
    for !s.Expired() {
        io.Sleep(time.Millis(1)) else {
            return
        }
    }
}

// Forms arms, reads and nests deadlines in every position the lines allow.
Forms(io: Io): int {
    var n := 0
    scope outer := io.Scope() {
        outer.CancelAfter(time.Seconds(3_600))
        outer.CancelAfter(time.Seconds(60))
        scope inner := io.Scope() {
            left := inner.Remaining() else {
                time.Seconds(0)
            }
            if left.Nanos() > 0 {
                n += 1
            }
            inner.CancelAfter(time.Millis(0))
            inner.CancelAfter(time.Seconds(-1))
            if inner.Expired() {
                n += 10
            }
        }
        until := time.Now(io)
        outer.CancelAfter(until.Since(time.Now(io)))
        arm := Scope.CancelAfter
        arm(&outer, time.Seconds(1))
        expired := Scope.Expired
        remaining := Scope.Remaining
        if expired(&outer) || remaining(&outer).IsSome() {
            n += 100
        }
        outer.go Ticks(io, &outer)
    }
    return n
}

// Typed arms a deadline on a Scope[E]: it adds no error to E.
Typed(io: Io): Boom!bool {
    var hit := false
    try scope s := io.ScopeOf[Boom]() {
        s.CancelAfter(time.Seconds(30))
        hit = s.Expired() || s.Remaining().IsNone()
    }
    return hit
}
