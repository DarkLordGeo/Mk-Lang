extern var KernelEnd: u8

export End(): *u8 {
    return &KernelEnd // <- E1101
}

export First(): u8 {
    return KernelEnd // <- E1101
}
