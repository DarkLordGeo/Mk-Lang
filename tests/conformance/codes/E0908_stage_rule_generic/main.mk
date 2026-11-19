// verdict: reject E0908
// note: inside a generic, an imported compile-time requires over closed arguments is evaluated at the call, and any evaluation failure is the call's diagnostic, as at a concrete call: here the stage rule, since the evaluation enters Hdr, a type of this module
import lib

struct Hdr {
    a: int
}

H[T](x: &T): int {
    return lib.At[T, 0, Hdr](x) // <- E0908
}

Calls(): int {
    v := 5
    return H[int](&v)
}
