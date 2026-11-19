// verdict: reject E0602
// case: H23
// note: the model encoded `if T is &X` as a plain branch; the Go checker narrows `T`, so the case uses `if T is &i32`
struct Wrap[T: Plain] {
    v: T
    f: i32
}

Wrap[T].Get(&self): T {
    if T is &i32 {
        return &self.f // <- E0602
    }
    return self.v
}
