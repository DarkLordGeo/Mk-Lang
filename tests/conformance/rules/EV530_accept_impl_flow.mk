// verdict: ok
// rule: EV530
// rule: R11
// note: an `impl` method's flows may not exceed the interface's; here they are equal
interface Keeper {
    Keep(mut self, x: &i32 into self)
}

struct Slot {
    v: ?&i32
}

impl Keeper for Slot {
    Keep(mut self, x: &i32 into self) {
        self.v = some(x)
    }
}
