// verdict: ok
// note: a precision witness for the sourced fn rule: a generic body that returns a sourced fn value's result is judged for every instance only when no parameter of the fn type holds the result's type. Twice's `fn(T): T`, Via's `fn(x: &T): &T` and ApplyB's `fn(b: &Bx[T]): T` (through the field of an unexported struct, visible wherever Bx can be named; an exported struct with a private field is not walked into) have such a parameter: a function given as either has a parameter of the result's type, which R9 makes its source, so its own boundary refuses a static (E0602 at its return, also for a view passed by value). The three forwarders check clean. With the narrowing cut (mutant M-D756-sfn-supplied), each is E0602 at its return
Id(x: &i32): &i32 {
    return x
}

Twice[T](f: fn(T): T, x: T): T {
    return f(f(x))
}

Via[T](f: fn(x: &T): &T, x: &T): &T from x {
    return f(x)
}

struct Bx[T: Plain] {
    v: T
}

Get(b: &Bx[&i32]): &i32 {
    return b.v
}

ApplyB[T: Plain](f: fn(b: &Bx[T]): T, b: &Bx[T]): T {
    return f(b)
}

export Main(sys: &System): WriteError!void {
    n: i32 = 4
    a := Twice[&i32](Id, &n)
    k: i32 = 5
    b := Via[i32](Id, &k)
    j: i32 = 6
    bx := Bx(v: &j)
    c := ApplyB[&i32](Get, &bx)
    va := *a
    vb := *b
    vc := *c
    try sys.Print("{va} {vb} {vc}\n", (va, vb, vc))
}
