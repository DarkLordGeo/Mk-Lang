// verdict: reject E0202
// note: std.linux exists only on x86_64-linux: on every other target its import is reason target-selected, with a message naming the target. The corpus judges units on the host target, x86_64-windows; on an x86_64-linux host the import is accepted (tests/programs/linuxsyscall runs it there)
// host: x86_64-windows
import std.linux // <- E0202

One(): int {
    return 1
}
