struct Header {
    magic: u32
    count: u32
}

extern var BootHeader: Header
extern var BootFlag: u32

export Set(x: u32) {
    unsafe "the linker script places both symbols in writable data" {
        *(&BootFlag) = x
        p := &BootHeader
        (*p).count = x
    }
}
