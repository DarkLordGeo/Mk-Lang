// verdict: reject E0404
// rule: MKH-147
// note: an export extern var is no Mk var even in the module that defines it: a write goes through its address
export struct JitDesc {
    export version: u32
}

export extern var __jit_debug_descriptor: JitDesc

Run() {
    unsafe "the descriptor is this module's" {
        __jit_debug_descriptor = JitDesc(version: 1) // <- E0404
    }
}
