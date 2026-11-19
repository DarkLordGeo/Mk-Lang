// verdict: reject E0505
// note: The writable-by-type condition on a shared whole borrow (t stays split under `&t` only when its type holds no Cell, Mutex, Chan, Arena or view inline) matters for a callee no Mk body stands behind. C2 holds a Cell inline, so `f(&t)` must make t whole: f holds an extern, whose C body may store t.a's reference into t.c through the shared reference, which a Cell allows; Use(t.c.Load()) would then read y after its scope. Mutant M-RV2-a6-writable (every shared whole borrow keeps the split) keeps t split and accepts it. Naming the extern as a value outside `unsafe` is E1101, a check-phase error that hides every flow diagnostic, so the naming sits inside an `unsafe` block (a witness is judged in an unsafe module); the borrow program is unchanged
struct C2 {
    a: &i32
    c: Cell[&i32]
}

extern Shuffle(v: &C2)

Caller() {
    unsafe "D-587: Mix is named as a value" {
        long: i32 = 1
        f: fn(&C2) = Shuffle
        var t := C2(a: &long, c: Cell[&i32].New(&long))
        if Cond() {
            y: i32 = 2
            t.a = &y // <- E0505
            f(&t)
        }
        Use(t.c.Load())
    }
}
