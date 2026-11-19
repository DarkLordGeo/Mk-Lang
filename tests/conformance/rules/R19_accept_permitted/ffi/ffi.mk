export Load(p: *i32): i32 {
    unsafe "the caller passes a pointer to a live i32" {
        return *p
    }
}
