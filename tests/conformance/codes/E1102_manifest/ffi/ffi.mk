export Load(p: *u8): u8 {
    unsafe "the caller passes a valid pointer" {
        return *p
    }
}
