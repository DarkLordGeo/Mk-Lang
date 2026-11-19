// verdict: reject E0505
// note: `Pick(t: mut Two): mut Two from t` returns r, a whole loan on t, which goes to the generic `Rot(r)`, which moves a loan between t's fields. Every exclusive whole borrow of a split local collapses it, whatever the callee, so t holds y's loan whole and y's borrow is E0505. M-RV2-1-excl-collapse alone does not flip it: the body also has a call that may take part in a self-reach through a forwarding parameter, so the whole-body fallback rejects it too
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

Pick(t: mut Two): mut Two from t {
    return t
}

Caller() {
    long: i32 = 1
    var t := Two(a: Slot(c: Cell[&i32].New(&long)), b: Slot(c: Cell[&i32].New(&long)))
    if Cond() {
        y: i32 = 2
        t.a = Slot(c: Cell[&i32].New(&y)) // <- E0505
        r := Pick(mut t)
        Rot(r)
        t.a = Slot(c: Cell[&i32].New(&long))
    }
    Use(t.b.Get())
}
