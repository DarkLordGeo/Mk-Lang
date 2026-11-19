// verdict: reject E0901
// note: math.DivWide panics unless hi < d: a zero divisor panics with divide by zero, and hi >= d, whose quotient does not fit a u64, with overflow
import std.math

const Fits: u64 = math.DivWide(1, 0, 2).q
const ByZero: u64 = math.DivWide(0, 5, 0).q // <- E0901
const TooWide: u64 = math.DivWide(2, 0, 2).q // <- E0901
