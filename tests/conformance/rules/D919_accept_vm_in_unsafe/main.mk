// verdict: ok
// rule: D-919
// note: std.vm's calls in an unsafe block of a module that pkg.mk lists in unsafe_modules are accepted (k), and a module that only calls k's safe exports needs nothing; D919_reject_vm_outside_unsafe is the refusal
import k

Run(): int {
    return k.Page() + k.CodeSize()
}
