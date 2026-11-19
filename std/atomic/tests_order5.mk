// Why: Unordered (order 5) is a load or store order only;
// Fence and every read-modify-write must refuse it.
// What: tests of the Order clauses on order 5 and that they fold at compile
// time.
// How: the value is spelled here, inside std.atomic, where the private field
// can be written. Run by `mk test --std=std.atomic -run "Order5 "`.

test "Order5 a load or a store may be unordered, a fence or read-modify-write may not" {
    u := Order(v: 5)
    Assert(u.CanLoad())
    Assert(u.CanStore())
    Assert(!u.CanFence())
    Assert(!u.CanModify())
}

test "Order5 every C++20 order may be a read-modify-write's" {
    Assert(Relaxed.CanModify())
    Assert(Acquire.CanModify())
    Assert(Release.CanModify())
    Assert(AcqRel.CanModify())
    Assert(SeqCst.CanModify())
    Assert(!Relaxed.CanFence())
    Assert(Acquire.CanFence() && Release.CanFence() && AcqRel.CanFence() && SeqCst.CanFence())
}

// The clauses are const-evaluable, so a requires over them folds: a const
// initializer reads each as the E0706 check at a call does.
const FenceRefuses: bool = !Order(v: 5).CanFence()
const ModifyRefuses: bool = !Order(v: 5).CanModify()

test "Order5 the clauses fold at compile time" {
    Assert(FenceRefuses)
    Assert(ModifyRefuses)
}
