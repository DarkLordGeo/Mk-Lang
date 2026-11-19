import std.raw

const Header: [3]u32 = [0x1BADB002, 0, 0xE4524FFE] section ".multiboot"

var Ticks: u64 = 0 section ".data.ticks"

Magic(): u32 section ".text.boot" {
    return Header[0]
}

extern var KernelEnd: u8
extern var BootFlag: u32

export End(): uint {
    _ = Magic()
    _ = Ticks
    unsafe "the linker script defines KernelEnd after the last section" {
        return raw.Addr(&KernelEnd)
    }
}

export Flag(): u32 {
    unsafe "the boot stub writes BootFlag before the entry" {
        old := BootFlag
        *(&BootFlag) = 0
        return old
    }
}
