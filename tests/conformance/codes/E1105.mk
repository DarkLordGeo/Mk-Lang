// verdict: reject E1105
// note: a naked body is one unsafe block holding one asm
export extern naked Halt() {
    return // <- E1105
}
