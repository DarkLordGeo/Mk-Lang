// verdict: reject E0905
// note: the emitted function fails to check (a str returned as i32), wrapped as E0905 and cited at the gen line
import gens

struct Point {
    x: i32
    y: i32
}

gen gens.Broken(Point) // <- E0905
