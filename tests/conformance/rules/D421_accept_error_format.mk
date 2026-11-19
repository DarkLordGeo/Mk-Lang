// verdict: ok
// rule: D-421
// note: every error has a compiler-provided Format, and an error-set value formats its member, in a Print hole, a Format bound and a Format call
error NotFound(id: i64)
error Denied
error Named(code: i64, line: int)

type LoadError = NotFound | Denied | Named

Load(id: i64): LoadError!str {
    if id < 0 {
        fail Denied
    }
    if id == 0 {
        fail Named(code: 7, line: 1)
    }
    fail NotFound(id: id)
}

Show[T: Format](w: mut Writer, x: &T): WriteError!void {
    try x.Format(w)
}

Report(sys: &System, id: i64) {
    s := Load(id) else e {
        sys.PrintErr("load {id}: {e}\n", (id, e)) else {}
        return
    }
    _ = s
}

Direct(w: mut Writer): WriteError!void {
    try Show(w, &NotFound(id: 3))
    d := Denied
    try d.Format(w)
}
