// verdict: reject E1105
// rule: D-289
// note: E1105: a naked function uses no parameter by name, its asm takes no reg operand and no option but att and raw, and it takes no type parameter and no cpu clause
import k

Call() {
    k.Use()
}
