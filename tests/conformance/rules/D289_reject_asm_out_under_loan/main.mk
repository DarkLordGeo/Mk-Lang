// verdict: reject E0502
// rule: D-289
// note: the loan checker treats an out operand as a write of its place, so writing x while a shared loan of it is live and used after is E0502 (mutant M-StAsm-out)
import k

Run(): u64 {
    return k.Overwrite()
}
