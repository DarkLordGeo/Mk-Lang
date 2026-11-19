// Why: Cycles must count forward.
// What: one test that it is monotonic on one thread, run by
// `mk test --std=std.runtime`.
// How: reads it in a loop. It lives here, not beside std.mem's intrinsics,
// because std.mem builds on x86_64-freestanding and std.runtime does not.

test "Cycles is monotonic on one thread" {
    var last := Cycles()
    for _ in 0..1000 {
        c := Cycles()
        Assert(c >= last)
        last = c
    }
}
