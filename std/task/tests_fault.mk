// Why: a stack overflow in a task and an invalid scope limit must end the
// process with a panic report; that cannot be asserted from inside the run.
// What: two tests, a stack overflow on several carriers and Limits.tasks 0.
// How: a name ending in "[exit <code>: <text>]" tells
// std/rt-sched_harness_test.go to expect that exit status and that text in
// standard error.

import std.raw

// FDeep recurses without bound, keeping a 256-byte array live across the
// call so no frame is elided; it never returns.
FDeep(n: int): int {
    var buf: [256]u8 = [0; 256]
    buf[n % 256] = Trunc[u8](n)
    r := FDeep(n + 1)
    return r + int(buf[(n * 3) % 256])
}

// FOverflow computes 5 ms by the clock (so the tasks spread over the
// carriers), then overflows its stack.
FOverflow(io: Io, out: &raw.Atomic[u32]) {
    t0 := BenchNow()
    for BenchNow() - t0 < 5_000_000 {
    }
    out.Store(Trunc[u32](FDeep(0)))
}

test "RT-Fault-StackTaskMN tasks overflow on several carriers [exit 101: panic: stack limit in task]" (sys: &System) {
    // Under M:N a task runs on whichever carrier took it: the guard page
    // and, on Windows, the NT_TIB words mk_switch installs for the task
    // must work on carrier threads 1 and up, as on carrier 0.
    out := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go FOverflow(sys.io, &out)
        }
    }
    Assert(false)
}

test "RT-R17-LimitsZero Limits.tasks 0 panics at the scope [exit 101: panic: Limits.tasks must be at least 1 at std/task/tests_fault.mk:]" (sys: &System) {
    scope s := sys.io.ScopeWith(Limits(tasks: 0, stack: StackSize.Default)) {
        s.Cancel()
    }
    Assert(false)
}
