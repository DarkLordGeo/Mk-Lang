// verdict: reject E1104
// note: no bool comes out of asm, which could leave a byte other than 0 or 1
import k

Flag(): bool {
    return k.Carry()
}
