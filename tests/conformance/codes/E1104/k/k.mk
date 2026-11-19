export FirstByte(xs: []u8): u8 {
    var b: u8 = 0
    unsafe "the template reads one byte at the start of xs" {
        asm("mov {b}, byte ptr [{p}]", b: out(reg_byte) b, p: in(reg) xs) // <- E1104
    }
    return b
}
