// verdict: reject E0706
// note: an impl method may not have a compile-time requires over its own const generics either: a caller through the interface bound (`x.M[24]()`) judges none of its clauses, while its body may rely on them through a skipped value or a listed set
interface Pick {
    At[const V: int](&self, t: &[4]int): int
}

struct Table {
    a: int
}

impl Pick for Table {
    At[const V: int](&self, t: &[4]int): int
        requires V == 3 || V == 24 // <- E0706
        requires V < 4 // <- E0706
    {
        return t[V]
    }
}

Through[X: Pick](x: &X, t: &[4]int): int {
    return x.At[24](t)
}
