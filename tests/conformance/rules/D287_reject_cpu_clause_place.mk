// verdict: reject E1106
// rule: D-287
// note: an unknown CPU feature name, and a clause on a bodiless extern or an impl method, are E1106
interface Sized {
    Size(&self): int
}

struct Block {
    n: int
}

impl Sized for Block {
    Size(&self): int cpu "avx2" { // <- E1106
        return self.n
    }
}

extern Fast(n: i32): i32 cpu "avx2" // <- E1106

Slow(n: int): int cpu "sse4.2,avx3" { // <- E1106
    return n
}
