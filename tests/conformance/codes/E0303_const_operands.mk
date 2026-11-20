// verdict: reject E0303
// note: an operator whose operands are all literals panics at run time when its value at the operator's type overflows or divides by zero; a wrapping operator and a variable operand stay legal
Sum(): u8 {
    return 200 + 100 // <- E0303
}

Product(): i32 {
    return 70000 * 70000 // <- E0303
}

Under(): u32 {
    return 0 - 1 // <- E0303
}

Ratio(): int {
    return 7 / 0 // <- E0303
}

Wrapped(): u8 {
    return 200 +% 100
}

Late(v: u8): u8 {
    return v + 100
}
