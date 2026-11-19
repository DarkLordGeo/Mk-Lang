// verdict: ok
// rule: MKH-147
// note: an export extern var defines its symbol, zero-initialized, under its C name used as written, leading underscores included (GDB's `__jit_debug_descriptor`); another module reads and writes it through its address in unsafe
import k

Run(): u64 {
    var first: u64 = 0
    unsafe "invariant: k defines the descriptor; nothing else writes it" {
        first = k.__jit_debug_descriptor.first
        p := &k.__jit_debug_descriptor
        (*p).first = first + u64(k.Register(41)) + 1
        first = k.__jit_debug_descriptor.first
    }
    return first
}

export Main(sys: &System): WriteError!void {
    if Run() != 42 {
        Panic("the descriptor did not start at zero or did not keep its writes")
    }
}
