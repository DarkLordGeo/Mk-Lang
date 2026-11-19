export Carry(): bool {
    var c := false
    unsafe "stc sets the carry flag and setc copies it" {
        asm("stc\nsetc {c}", c: out(reg_byte) c) // <- E1104
    }
    return c
}
