// runtime.StackBounds and StackRemaining: the stack queries turn deep recursion into an
// ordinary error before the guard page.
// A recursion that is not a tail call checks
// StackRemaining on every call and fails with its own error, StackLow, once less than 64 KiB is
// left, so it ends with an error its caller handles, never at the guard page. It runs on Main's
// task and on a spawned task. The output names no depth or address (they differ by profile and
// target), only what holds: the bounds were known, the error stopped the recursion, and the
// stack had room again after it. Expected output: stackquery.out.
import std.runtime

error StackLow

// Low is the bytes the recursion keeps in hand: it stops before less than this is left.
const Low: uint = 65536

// Cap ends a recursion the query failed to stop, so a failure prints a line of its own.
const Cap: int = 100_000_000

// Depth recurses until StackRemaining is below Low and fails with StackLow there; it returns how
// deep it went when Cap stopped it instead. The work after the call keeps it from being a tail
// call.
Depth(n: int): StackLow!int {
    if n >= Cap {
        return n
    }
    if runtime.StackRemaining() < Low {
        fail StackLow
    }
    var pad: [32]u8 = [0; 32]
    pad[n % 32] = 1
    d := try Depth(n + 1)
    return d + Cast[int](pad[n % 32]) - 1
}

// Seen is what the queries said on one stack and how the recursion ended.
struct Seen {
    known: bool
    ordered: bool
    room: bool
    stopped: bool
    after: bool
}

// Probe asks the queries on this stack and runs the recursion.
Probe(): Seen {
    b := runtime.StackBounds()
    room := runtime.StackRemaining() > Low
    var stopped := false
    _ = Depth(0) else {
        stopped = true
        0
    }
    return Seen(known: b.lo != 0 && b.hi != 0, ordered: b.lo < b.hi, room: room, stopped: stopped,
        after: runtime.StackRemaining() > Low)
}

// Task runs Probe on a spawned task's stack.
Task(seen: mut Seen) {
    *seen = Probe()
}

// Show prints what Probe saw on one stack.
Show(w: mut Writer, on: str, s: Seen): WriteError!void {
    try Print(w, "{on}: bounds known {k}, lo below hi {o}, room at the start {r}\n", (on: on, k: s.known, o: s.ordered, r: s.room))
    if s.stopped {
        try Print(w, "{on}: deep recursion ended with its own error StackLow, before the guard page\n", (on: on))
    } else {
        try Print(w, "{on}: the recursion was not stopped\n", (on: on))
    }
    try Print(w, "{on}: room again after it {a}\n", (on: on, a: s.after))
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    m := Probe()
    var t := Seen(known: false, ordered: false, room: false, stopped: false, after: false)
    scope s := sys.io.Scope() {
        s.go Task(mut t)
    }
    try Show(mut out, "Main", m)
    try Show(mut out, "spawned task", t)
}
