// verdict: reject E1002
// case: H42
Reader2(m: &Mutex[&Cell[i32]]) {
}

Racy(c: &Cell[i32], io: Io) {
    m := Mutex.New(c)
    scope s := io.Scope() {
        s.go Reader2(&m) // <- E1002
        s.go Reader2(&m) // <- E1002
    }
}
