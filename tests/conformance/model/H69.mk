// verdict: ok
// case: H69
struct L2 {
    src: &Point
}

L2.Get(&self): &Point {
    return &*self.src
}
