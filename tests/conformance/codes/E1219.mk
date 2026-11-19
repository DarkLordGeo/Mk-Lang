// verdict: reject E1219
struct Point {
    x: i32
    y: i32
}

impl Point { // <- E1219
    Sum(&self): i32 {
        return self.x + self.y
    }
}
