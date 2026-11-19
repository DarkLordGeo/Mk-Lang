// verdict: reject E1002
// rule: D-436
// note: a task cannot print through sys: &System holds &Arena and is not Share, so a task's error goes to a slot the parent owns and the parent prints it (the cookbook's recipe)
Report(sys: &System, n: int) {
    sys.Print("{n}\n", (n)) else {
        return
    }
}

export Main(sys: &System) {
    scope s := sys.io.Scope() {
        s.go Report(sys, 1) // <- E1002
    }
}
