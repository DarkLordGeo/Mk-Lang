export struct JitDesc {
    export version: u32
    export action: u32
    export relevant: u64
    export first: u64
}

export extern var __jit_debug_descriptor: JitDesc

export Register(v: u32): u32 {
    unsafe "invariant: the descriptor is this module's own definition" {
        p := &__jit_debug_descriptor
        (*p).version = v
        return __jit_debug_descriptor.version
    }
}
