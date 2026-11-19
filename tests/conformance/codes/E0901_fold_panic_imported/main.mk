// verdict: reject E0901
// note: inside a generic, an imported compile-time requires over closed const arguments, evaluated with a stand-in for an open type argument it does not read, reports the evaluator's panic at the call, as at a concrete call
import lib

H[T](): int {
    a := lib.At[T, 16, -9223372036854775800]() // <- E0901
    return a + lib.At[T, 4, 2]()
}

Calls(): int {
    return H[int]()
}
