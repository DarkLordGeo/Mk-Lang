// verdict: reject E0310
// rule: D-418
// note: an unsafe block and an asm statement are statements, so a value arm that ends in one has no value; the asm sits inside unsafe, so only E0310 is due
import k

Run(): int {
    return k.Pick(true) + k.Spin(true)
}
