// verdict: reject E0701
// note: mem.OffsetOf's T must be a type whose fields are known at the call, never one built on a type parameter, since an instance could lack the field and the offset is a compile-time constant
import std.mem

struct P {
    a: u8
    b: u64
}

G[T](): int {
    return mem.OffsetOf[T]("b") // <- E0701
}

Closed(): int {
    return mem.OffsetOf[P]("b")
}
