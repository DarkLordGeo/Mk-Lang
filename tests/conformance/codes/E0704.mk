// verdict: reject E0704
interface Weighed {
    Weight(&self): int
}

struct Rock {
    w: int
}

impl Weighed for Rock {
    Weight(&self): int {
        return self.w
    }
}

impl Weighed for Rock { // <- E0704
    Weight(&self): int {
        return 0
    }
}
