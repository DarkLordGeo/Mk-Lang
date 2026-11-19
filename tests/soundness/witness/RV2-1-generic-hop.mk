// verdict: reject E0505
// note: `Outer(t: mut Two) { Rot(t) }` hands t whole to the generic `Rot[T: Fields[B]](x: mut T)`, which rotates a loan from t.a's Cell into t.b's through a `fields` loop: safe Mk, no extern, no unsafe. If t stayed split, `t.b.Get()` would read y after its scope. Every exclusive whole borrow of a split local collapses it, whatever the callee, so t holds y's loan whole and y's borrow is E0505. Mutant M-RV2-1-excl-collapse (an exclusive whole borrow keeps the root split) accepts it
interface B {
    Get(&self): &i32 from self
    Put(&self, v: &i32 into self)
}

export struct Slot {
    export c: Cell[&i32]
}

impl B for Slot {
    Get(&self): &i32 from self {
        return self.c.Load()
    }

    Put(&self, v: &i32 into self) {
        self.c.Store(v)
    }
}

export struct Two {
    export a: Slot
    export b: Slot
}

Rot[T: Fields[B]](x: mut T) {
    var last: ?&i32 = none
    for _, f in fields(&*x) {
        if l := last {
            f.Put(l)
        }
        last = some(f.Get())
    }
}

Outer(t: mut Two) {
    Rot(t)
}

Caller() {
    long: i32 = 1
    var t := Two(a: Slot(c: Cell[&i32].New(&long)), b: Slot(c: Cell[&i32].New(&long)))
    if Cond() {
        y: i32 = 2
        t.a = Slot(c: Cell[&i32].New(&y)) // <- E0505
        Outer(mut t)
        t.a = Slot(c: Cell[&i32].New(&long))
    }
    Use(t.b.Get())
}
