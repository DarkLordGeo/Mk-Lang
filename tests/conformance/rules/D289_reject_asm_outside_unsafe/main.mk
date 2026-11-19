// verdict: reject E1101
// rule: D-289
// note: asm is allowed only inside unsafe, and so is a call of a naked function or naming one as a value, even in a module pkg.mk lists
import k

Stop() {
    k.Stop()
}
