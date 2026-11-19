// verdict: reject E0908
// note: an evaluation in a type enters a same-module body (the stage rule)
Four(): int {
    return 4
}

Zero(): int {
    buf: [Four()]u8 = [0; 4] // <- E0908
    return int(buf[0])
}
