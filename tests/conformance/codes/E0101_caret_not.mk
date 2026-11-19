// verdict: reject E0101
// note: foreign form, prefix ^x; ^ is infix only
Flip(x: u8): u8 {
    return ^x // <- E0101
}
