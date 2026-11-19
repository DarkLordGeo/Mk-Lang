// verdict: reject E0505 E0602
// note: in the generic caller Run, p of type Two2[T] (T: B) must not stay split across a whole shared borrow: at T = Slot, the clause-free Rot moves field a's short loan into field b's Cell, and a checker that keeps p split lets the run read Run's dead local through p.b. A whole borrow of a type that mentions a type parameter or Self makes the value whole (locals.go collapseFor). p is rebuilt with a Slot holding &n in field a (the next comment line). A fn value's result in a generic body is judged for every instance even when its type names a source, unless a parameter of the fn type holds the result's type (`fn(v: &i32): T from v` has none), so Run's `return p`, which holds mk(&n)'s T, is E0602 too: the cost of that rule to a generic forwarder of a sourced fn value
// &p goes to the clause-free Rot, which copies a's loan into b's Slot; p is taken apart, a dropped, rebuilt with s3
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

export struct Two2[T] {
    export a: T
    export b: T
}

Rot[T: Fields[B]](x: &T) {
    var last: ?&i32 = none
    for _, f in fields(&*x) {
        if l := last {
            f.Put(l)
        }
        last = some(f.Get())
    }
}

Run[T: B](c: bool, s1: T, s2: T, s3: T, mk: fn(v: &i32): T from v, drop: fn(T)): Two2[T] from s1, s2, s3 {
    var p := Two2(a: s1, b: s2)
    if c {
        n: i32 = 4242
        Two2(a: x0, b: y0) := p
        drop(x0)
        p = Two2(a: mk(&n), b: y0) // <- E0505
        Rot(&p)
        Two2(a: x, b: y) := p
        drop(x)
        p = Two2(a: s3, b: y)
    } else {
        drop(s3)
    }
    return p // <- E0602
}

MkSlot(v: &i32): Slot from v {
    return Slot(c: Cell[&i32].New(v))
}

DropSlot(s: Slot) {
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    keep: i32 = 0
    p := Run(sys.args.Len() >= 0, Slot(c: Cell[&i32].New(&keep)), Slot(c: Cell[&i32].New(&keep)), Slot(c: Cell[&i32].New(&keep)), MkSlot, DropSlot)
    w := p.b.Get()
    v0 := *w
    k := Clobber(5)
    v := *w
    try Print(mut out, "keep={keep} r0={v0} r={v} k={k}\n", (keep, v0, v, k))
}

Clobber(n: i32): i32 {
    var acc: i32 = n
    var w0: i32 = n + 1001
    var w1: i32 = n + 2002
    var w2: i32 = n + 3003
    var w3: i32 = n + 4004
    for _ in 0..4 {
        acc = acc + w0 + w1 + w2 + w3
        w0 = w0 + 1
        w1 = w1 + 1
        w2 = w2 + 1
        w3 = w3 + 1
    }
    return acc
}
