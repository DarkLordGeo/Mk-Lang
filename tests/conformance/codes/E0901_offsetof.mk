// verdict: reject E0901
// note: mem.OffsetOf[T]("field") is a compile-time offset, E0901 for a field T lacks
import std.mem

struct P {
    a: u8
    b: u64
}

const Good: int = mem.OffsetOf[P]("b")
const Bad: int = mem.OffsetOf[P]("c") // <- E0901

// Wherever it is called, not only in a const.
Run(): int {
    return mem.OffsetOf[P]("a") + mem.OffsetOf[P]("d") // <- E0901
}
