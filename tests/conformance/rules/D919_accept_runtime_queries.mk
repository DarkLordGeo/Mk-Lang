// verdict: ok
// rule: D-919
// note: std.runtime's queries and its Waker are safe: no unsafe block is needed for Processors, Carriers, StackBounds, StackRemaining, Blocking or a Waker's New, Wait and Wake, and Atomic.Wait and Wake take any 4-byte word (u32 and i32 here)
import std.runtime
import std.atomic

Inc(x: int): int {
    return x + 1
}

Room(): uint {
    b := runtime.StackBounds()
    if b.hi <= b.lo {
        return 0
    }
    return runtime.StackRemaining()
}

Wide(): int {
    return runtime.Processors() + runtime.Carriers()
}

Once(io: Io, w: &runtime.Waker): Canceled!int {
    w.Wake()
    try w.Wait(io)
    return try runtime.Blocking(io, 1, Inc)
}

Word(io: Io, a: &atomic.Atomic[u32], b: &atomic.Atomic[i32]): Canceled!void {
    a.Wake(true)
    b.Wake(false)
    try a.Wait(io, 1)
    try b.Wait(io, -1)
}
