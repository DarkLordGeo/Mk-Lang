// verdict: reject E0608
// rule: EV530
// rule: R11
// note: the `impl` stores `x`, which the interface's signature does not allow
interface Keeper {
    Keep(mut self, x: &i32)
}

struct Slot {
    v: ?&i32
}

impl Keeper for Slot {
    Keep(mut self, x: &i32 into self) { // <- E0608
        self.v = some(x)
    }
}
