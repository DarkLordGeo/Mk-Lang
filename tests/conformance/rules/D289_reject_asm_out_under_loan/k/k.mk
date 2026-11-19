export Overwrite(): u64 {
    var x: u64 = 1
    r := &x
    unsafe "mov writes one register" {
        asm("mov {x}, 7", x: out(reg) x, options(pure, nomem, nostack)) // <- E0502
    }
    return *r
}
