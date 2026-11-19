export extern naked Halt() {
    unsafe "hlt stops the core" {
        asm("hlt\njmp {h}", h: sym Halt, options(att))
    }
}

Pause() {
    asm("pause", options(nomem, nostack, preserves_flags)) // <- E1101
}

export Stop() {
    Halt() // <- E1101
}

Entry(): fn() {
    return Halt // <- E1101
}
