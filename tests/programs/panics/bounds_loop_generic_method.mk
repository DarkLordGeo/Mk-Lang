// Source: a counter loop over xs whose body points xs at a 1-element slice through a mut-self method called with an explicit const argument, xs.Point[0](small). The bounds check on xs[i] stays, because lower sees the method's write to xs, so the loop panics instead of reading past small (types.PanicBounds, every profile).
interface Pointer {
    Point[const K: int](mut self, o: []int into self)
}

impl Pointer for []int {
    Point[const K: int](mut self, o: []int into self) {
        *self = o
    }
}

Sum(v: []int, small: []int): int {
    var xs := v
    var n := 0
    for i in 0..xs.Len() {
        if i == 0 {
            xs.Point[0](small)
        }
        n = n + xs[i]
    }
    return n
}

export Main(sys: &System): int {
    big := [1, 1, 1, 1, 1, 1, 1, 1]
    small := [100]
    return Sum(big[..], small[..]) + sys.args.Len()
}
