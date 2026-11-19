extern var BootFlag: u32

Set(x: mut u32) {
    *x = 1
}

export Clear() {
    unsafe "the linker script places BootFlag in writable data" {
        BootFlag = 0      // <- E0404
        Set(mut BootFlag) // <- E0404
    }
}
