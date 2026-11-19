// verdict: reject E0706
// rule: D-919
// note: Atomic[T].Wait and Wake require SizeOf[T]() == 4, since a futex and WaitOnAddress compare 4 bytes: a u64 word is refused at each call; D919_accept_runtime_queries waits on u32 and i32 words
import std.atomic

Word(io: Io, a: &atomic.Atomic[u64]): Canceled!void {
    a.Wake(true) // <- E0706
    try a.Wait(io, 0) // <- E0706
}
