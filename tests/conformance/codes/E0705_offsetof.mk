// verdict: reject E0705
// note: mem.OffsetOf's field is a string literal, since the offset is a compile-time constant
import std.mem

struct P {
    a: u8
}

Run(name: str): int {
    return mem.OffsetOf[P](name) // <- E0705
}

// As a fn value it could not be given a compile-time field either.
Value(): fn(str): int {
    return mem.OffsetOf[P] // <- E0705
}
