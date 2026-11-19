// verdict: reject E1220
// note: 255 may be the type's maximum, so the fix is an alternative, not proven
Class(x: u8): int {
    return match x {
        0..=255 { 1 } // <- E1220
    }
}
