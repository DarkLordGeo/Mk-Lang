// verdict: reject E0706
// rule: D-291
// note: align takes a power of two from 1 to 65,536, and never with packed
align(3) struct Odd { // <- E0706
    x: u8
}

align(131072) struct Huge { // <- E0706
    x: u8
}

align(4096) packed struct Both { // <- E0706
    x: u8
}
