// verdict: reject E0101
// note: lexical, uppercase number prefix 0X; write 0x
Mask(): int {
    return 0X1F // <- E0101
}
