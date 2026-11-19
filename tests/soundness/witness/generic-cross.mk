// verdict: reject E0602
// note: the clause of a generic struct is checked field by field in the generic body
struct Two[T] {
    a: T
    b: &i32
}

F[T](x: T, y: &i32, z: T): Two[T] from (a: x, b: y) {
    Take(x)
    return Two(a: z, b: y) // <- E0602
}
