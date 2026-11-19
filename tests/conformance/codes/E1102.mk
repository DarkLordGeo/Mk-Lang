// verdict: reject E1102
// needs: imports
// note: single-file and directory mode permit no module; anchored at the unsafe keyword
Peek(p: *u8): u8 {
    unsafe "the caller passes a valid pointer" { // <- E1102
        return *p
    }
}
