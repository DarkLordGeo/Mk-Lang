const Header: [3]u32 = [0x1BADB002, 0, 0xE4524FFE] section ".multiboot" // <- E1102

extern var KernelEnd: u8 // <- E1102

export Magic(): u32 {
    return Header[0]
}
