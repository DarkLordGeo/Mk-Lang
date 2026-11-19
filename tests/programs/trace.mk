// Error-return trace (doc/reference.md: "Dev builds record try sites as an error return
// trace"). Usage: trace deep|scope|early. Each mode
// ends Main with an error, so standard error is `error: main.<Name>` and the exit status is 1;
// a dev build adds one `    at trace.mk:<line>:<col>` row per recorded site, the `fail` first.
// The sites are the columns of the `fail` and `try` keywords. Release builds print no rows.
//   deep:  Down(20) fails at depth 0 and 20 `try` edges in Down plus 1 in Main carry it up: a
//          task keeps 12 rows, the fail and 11 tries, and counts the other 10.
//   scope: Main first fails and handles Handled (its row must not appear), then a `try scope`
//          raises a child's Child: the trace starts at the scope's site, then the `try` edges
//          that carry it up.
//   early: the scope body itself fails Body, an early exit that discards whatever the child
//          recorded, so the first row is the body's own `fail`.

error Deep(n: i64)
error Handled
error Child
error Body

Down(n: i64): Deep!void {
    if n == 0 {
        fail Deep(n: 0)
    }
    try Down(n - 1)
}

Maybe(): Handled!void {
    fail Handled
}

Fails(): Child!void {
    fail Child
}

RunScope(io: Io): Child!void {
    Maybe() else {
    }
    try scope s := io.ScopeOf[Child]() {
        s.go Fails()
    }
}

Early(io: Io): Child|Body!void {
    try scope s := io.ScopeOf[Child]() {
        s.go Fails()
        fail Body
    }
}

export Main(sys: &System): Deep|Child|Body|WriteError!void {
    mode := sys.args[1]
    if mode == "deep" {
        try Down(20)
    } else if mode == "scope" {
        try RunScope(sys.io)
    } else if mode == "early" {
        try Early(sys.io)
    }
    var out := sys.Stdout()
    try Print(mut out, "no error\n")
}
