// verdict: ok
// rule: D-289
// note: a naked function's body is one unsafe block holding one asm, with const and sym operands only; calling one needs unsafe
import k

Stop() {
    k.Stop()
}
