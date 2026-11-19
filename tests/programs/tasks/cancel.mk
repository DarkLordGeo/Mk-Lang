// Cancel: a task sleeps in a loop until its Io returns
// Canceled; the body sleeps 10 ms, then cancels the scope. The task sees Canceled, stops, and
// the join completes. The output does not depend on how many rounds the task slept.
import std.time
import std.atomic

Sleeper(io: Io, stopped: &atomic.AtomicBool) {
    for {
        io.Sleep(time.Millis(1)) else {
            stopped.Store(true)
            return
        }
    }
}

export Main(sys: &System): WriteError!void {
    stopped := atomic.AtomicBool.New(false)
    scope s := sys.io.Scope() {
        s.go Sleeper(sys.io, &stopped)
        sys.io.Sleep(time.Millis(10)) else {
        }
        s.Cancel()
    }
    var out := sys.Stdout()
    try Print(mut out, "canceled {c}\n", (c: stopped.Load()))
}
