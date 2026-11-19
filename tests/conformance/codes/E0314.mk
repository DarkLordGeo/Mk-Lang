// verdict: reject E0314
// note: a method through an interface bound is chosen per instance
interface Shape {
    Area(&self): f64
}

Measure[T: Shape](s: &T): f64 {
    become s.Area() // <- E0314
}
