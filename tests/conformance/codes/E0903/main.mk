// verdict: reject E0903
// note: Gen.Fail records E0903 at the gen line; the generator lives in a strictly imported module
import gens

struct Point {
    x: i32
    y: i32
}

gen gens.Refuse(Point) // <- E0903
