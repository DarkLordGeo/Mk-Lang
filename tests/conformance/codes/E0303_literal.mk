// verdict: reject E0303
// note: a literal out of range of its expected type
Big(): u8 {
    return 300 // <- E0303
}
