// verdict: ok
// rule: D-933
// note: ForeignIo and ForeignContext in an unsafe block of a module pkg.mk lists in unsafe_modules (k); a Foreign is linear, so it is released on every path, and an Io copied from it is safe to pass on; D933_reject_foreign_io_outside_unsafe is the refusal
import k

Run(): bool {
    return k.RoundTrip()
}
