export extern naked ByName(x: u64) {
    unsafe "x would be read from a register the stub has not saved" {
        asm("mov rax, {x}\nret", x: in(reg) x) // <- E1105
    }
}

export extern naked Flags() {
    unsafe "the stub returns at once" {
        asm("ret", options(nomem)) // <- E1105
    }
}

export extern naked Fast() cpu "avx2" { // <- E1105
    unsafe "the stub returns at once" {
        asm("ret")
    }
}

export Use() {
}
