// Why: seeding a generator from the OS needs Io, which the rest of std.math
// does not; keeping it apart lets a freestanding build without Io leave it
// out.
//
// What: Rng.FromIo.
//
// How: one Io.Random call fills the four state words; the rest is rng.mk's.

/// A generator seeded with 32 random bytes from io, for a sequence that differs
/// on every run. If the bytes are all zero, the one state the generator cannot
/// leave, it starts from New(0)'s state instead. One Io call (Io.Random), so it
/// fails with Canceled when the task's scope is cancelled. It allocates
/// nothing.
export Rng.FromIo(io: Io): Canceled!Rng {
    var b: [32]u8 = [0; 32]
    try io.Random(mut b[..])
    return RngFromBytes(&b)
}
