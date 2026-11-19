// verdict: reject E0706
// note: the callee's compile-time requires is in a strictly lower module, so the evaluator may run it at the call (the stage rule)
import lib

Zero(): int {
    return lib.Cap(0) // <- E0706
}
