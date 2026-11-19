// verdict: reject E0704
// note: two `gen` lines emitting one `impl` conflict, E0704 at the second `gen` line citing the first, not wrapped as E0905
import std.derive

struct Point {
    x: i32
    y: i32
}

gen derive.Eq(Point)
gen derive.Eq(Point) // <- E0704
