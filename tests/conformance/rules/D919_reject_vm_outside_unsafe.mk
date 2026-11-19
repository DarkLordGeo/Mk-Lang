// verdict: reject E1101
// rule: D-919
// note: std.vm is unsafe only, every function and method: a CodeRegion made outside an unsafe block is E1101 at the call; its accepting twin is D919_accept_vm_in_unsafe, the same calls in an unsafe block of a module listed in unsafe_modules
import std.vm

Size(): int {
    c := vm.CodeRegion.New(64) else { // <- E1101
        return 0
    }
    n := c.Size() // <- E1101
    c.Release() // <- E1101
    return n
}
