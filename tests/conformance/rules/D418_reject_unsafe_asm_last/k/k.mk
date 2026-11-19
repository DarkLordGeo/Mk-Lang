export Pick(c: bool): int {
    x := if c {
        1
    } else {
        unsafe "no value here" { // <- E0310
            _ = 0
        }
    }
    return x
}

export Spin(c: bool): int {
    var y := 0
    unsafe "pause hints the core" {
        y = match c {
            true { 2 }
            false {
                asm("pause", options(nomem, nostack, preserves_flags)) // <- E0310
            }
        }
    }
    return y
}
