// verdict: ok
// rule: D-418
// note: a value if takes every condition form: a binding condition, a type test in a generic, and a constant Target test, whose dead arm is still checked
Or0(w: ?int): int {
    x := if v := w { v } else { 0 }
    return x
}

Size[T: Plain](v: T): int {
    n := if T is i64 { 8 } else { 0 }
    _ = v
    return n
}

Slash(): str {
    s := if Target.os == Os.Windows { "\\" } else { "/" }
    return s
}
