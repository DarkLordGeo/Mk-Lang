// verdict: ok
// rule: D-289
// note: an out operand is a definite assignment, so a local declared with no value and written only by asm may be read after it (mutant M-StAsm-out); inout reads and writes
import k

Run(): u64 {
    return k.Stamp() +% k.Twice(21)
}
