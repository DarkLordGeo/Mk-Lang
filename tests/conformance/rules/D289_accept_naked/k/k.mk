const Code: u32 = 7

Spin() {
    for {
    }
}

export extern naked Halt() {
    unsafe "the stub never returns: it stores a code and loops in Spin" {
        asm("mov eax, {c}\ncall {f}\nud2", c: const Code, f: sym Spin, options(att))
    }
}

export Stop() {
    unsafe "Halt never returns, and nothing after the call is live" {
        Halt()
    }
}
