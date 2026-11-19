// verdict: reject E1223
// note: HB-1: assert(x) and assert!(x) are the assert habit too; the proven fix is Assert(x)
Check(n: int) {
    assert(n == 1) // <- E1223
    assert!(n == 2) // <- E1223
}
