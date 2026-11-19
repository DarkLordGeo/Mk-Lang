// verdict: reject E0707
interface Weighed {
    Weight(&self): int
}

struct Rock {
    w: int
}

impl Weighed for Rock {
    Weight(&self): i64 { // <- E0707
        return 0
    }
}
