// verdict: ok
// rule: D-910
// note: range patterns are half-open, lo <= x < hi, and exhaustiveness is computed over intervals of the scrutinee type's whole range: ranges and literals covering all 256 values of u8 need no `_` arm, and a char match by ranges compiles
Class(b: u8): int {
    return match b {
        0..32 { 0 }
        32..127 { 1 }
        127 { 2 }
        128..255 { 3 }
        255 { 4 }
    }
}

Letter(c: char): int {
    return match c {
        'a'..'{' { 1 }
        'A'..'[' { 2 }
        _ { 0 }
    }
}
