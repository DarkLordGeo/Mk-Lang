// verdict: reject E0701
// note: math.Integer is the integer types, computed by the compiler: a float, bool or char does not satisfy it, and a type parameter bounded by it does
import std.math

Add[T: math.Integer](a: T, b: T): bool {
    return math.AddOverflow[T](a, b).overflow
}

const Ok: bool = Add[u16](65535, 1)
const NoFloat: bool = math.AddOverflow[f64](1.0, 2.0).overflow // <- E0701
const NoBool: bool = math.CastSat[bool](1.0) // <- E0701
