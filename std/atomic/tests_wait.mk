// Why: Wait must sleep until the word changes and Wake must reach every
// waiter it promises to.
// What: tests of Atomic[T].Wait and Wake.
// How: run by `mk test --std=std.atomic -run "Wait "` with 1 and 8 carriers
// (std/std-missing_blocks_test.go). A wake from a foreign thread is
// std.runtime's Waker test (std/runtime/tests_runtime.mk); a wrong-width T
// is E0706 (TestRt_AtomicWaitWidth).

// WaitFor parks until the word is not 0, rereading it after every return,
// since Wait may return early; counts itself into seen.
WaitFor(io: Io, a: &Atomic[u32], seen: &Atomic[i64]) {
    for a.Load(Acquire) == 0 {
        a.Wait(io, 0) else {
            return
        }
    }
    _ = seen.FetchAdd(1, Relaxed)
}

test "Wait Wake(true) wakes every one of 8 waiting tasks" (sys: &System) {
    a := Atomic[u32].New(0)
    seen := Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go WaitFor(sys.io, &a, &seen)
        }
        for _ in 0..64 {
            sys.io.Yield() else {}
        }
        a.Store(1, Release)
        a.Wake(true)
    }
    Assert(seen.Load(SeqCst) == 8)
}

// The word goes to 1, then one Wake(false) at a time: a woken waiter finds
// 1 and leaves, so the loop wakes until all four are out.
test "Wait Wake(false) wakes waiters one at a time until all are out" (sys: &System) {
    a := Atomic[u32].New(0)
    seen := Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..4 {
            s.go WaitFor(sys.io, &a, &seen)
        }
        for _ in 0..64 {
            sys.io.Yield() else {}
        }
        a.Store(1, Release)
        for seen.Load(Acquire) < 4 {
            a.Wake(false)
            sys.io.Yield() else {}
        }
    }
    Assert(seen.Load(SeqCst) == 4)
}

test "Wait returns at once when the word does not hold expected" (sys: &System) {
    a := Atomic[u32].New(7)
    a.Wait(sys.io, 3) else {
        Assert(false)
    }
    b := Atomic[i32].New(-1)
    b.Wait(sys.io, 0) else {
        Assert(false)
    }
}

// An i32 word compares bits: -1 is 0xFFFFFFFF.
WaitNeg(io: Io, a: &Atomic[i32], seen: &Atomic[i64]) {
    for a.Load(Acquire) == -1 {
        a.Wait(io, -1) else {
            return
        }
    }
    _ = seen.FetchAdd(1, Relaxed)
}

test "Wait an i32 word holding -1 parks until a store and a Wake" (sys: &System) {
    a := Atomic[i32].New(-1)
    seen := Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        s.go WaitNeg(sys.io, &a, &seen)
        s.go WaitNeg(sys.io, &a, &seen)
        for _ in 0..64 {
            sys.io.Yield() else {}
        }
        a.Store(5, Release)
        a.Wake(true)
    }
    Assert(seen.Load(SeqCst) == 2)
}

// WaitCanceled waits on a word nobody changes; only its scope's cancel ends
// the wait, with Canceled.
WaitCanceled(io: Io, a: &Atomic[u32], out: &Atomic[i64]) {
    for {
        a.Wait(io, 0) else {
            _ = out.FetchAdd(1, Relaxed)
            return
        }
    }
}

test "Wait a cancelled scope ends the wait with Canceled" (sys: &System) {
    a := Atomic[u32].New(0)
    out := Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        s.go WaitCanceled(sys.io, &a, &out)
        s.go WaitCanceled(sys.io, &a, &out)
        for _ in 0..64 {
            sys.io.Yield() else {}
        }
        s.Cancel()
    }
    Assert(out.Load(SeqCst) == 2)
    Assert(a.Load(SeqCst) == 0)
}

test "Wait a Wake with no waiter does nothing" {
    a := Atomic[u32].New(0)
    a.Wake(false)
    a.Wake(true)
    Assert(a.Load(SeqCst) == 0)
}
