// verdict: reject E0202
// rule: D-919
// note: std.linux is x86_64-linux only: elsewhere its import is E0202 reason target-selected, so a package for several targets imports it only in a module its targets select for linux. The corpus judges units on the host, x86_64-windows; on x86_64-linux the import is accepted (tests/programs/linuxsyscall)
// host: x86_64-windows
import std.linux // <- E0202

One(): int {
    return 1
}
