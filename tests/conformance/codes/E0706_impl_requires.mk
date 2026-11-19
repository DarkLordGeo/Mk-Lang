// verdict: reject E0706
// note: an impl method may not have a compile-time requires over the impl's const generics, which a caller through the interface bound cannot check
interface Count {
    Get(&self): int
}

struct Lanes[const N: int] {
    a: int
}

impl[const N: int] Count for Lanes[N] {
    Get(&self): int
        requires N > 0 // <- E0706
    {
        return N
    }
}
