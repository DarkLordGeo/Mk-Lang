// verdict: reject E1101
// note: every exported function of std.vm is unsafe only: a call outside an unsafe block is E1101 naming vm.PageSize
import std.vm

Page(): int {
    return vm.PageSize() // <- E1101
}
