// Why: UTS 46's CheckBidi and CheckJoiners need Unicode properties of every
// code point of a label, and must look them up in constant time.
//
// What: BidiClass, JoiningType, BidiJoining and IsVirama, with the Bidi_Class
// and Joining_Type constants, for UTS 46 section 4.1's checks (Bidi_Class,
// RFC 5893 section 2; Joining_Type and Virama, RFC 5892 appendix A.1 and
// A.2).
//
// How: over the generated props_tables.mk: a three-stage table, one byte per
// code point, and a sorted list of the Virama code points. Each lookup is
// O(1) but IsVirama, O(log 80).

// Bidi_Class, as PropData numbers it (bits 0 to 4).
const BidiL: u8 = 0
const BidiR: u8 = 1
const BidiAL: u8 = 2
const BidiEN: u8 = 3
const BidiES: u8 = 4
const BidiET: u8 = 5
const BidiAN: u8 = 6
const BidiCS: u8 = 7
const BidiNSM: u8 = 8
const BidiBN: u8 = 9
const BidiON: u8 = 13

// Joining_Type, as PropData numbers it (bits 5 to 7).
const JoinU: u8 = 0
const JoinC: u8 = 1
const JoinD: u8 = 2
const JoinL: u8 = 3
const JoinR: u8 = 4
const JoinT: u8 = 5

// BidiJoining is the PropData byte of c: Bidi_Class in bits 0 to 4 and
// Joining_Type in bits 5 to 7.
BidiJoining(c: char): u8 {
    cp := u32(c)
    b2 := u32(PropIndex1[int(cp >> (PropShift2 + PropShift3))])
    b3 := u32(PropIndex2[int((b2 << PropShift2) | ((cp >> PropShift3) & ((u32(1) << PropShift2) - 1)))])
    return PropData[int((b3 << PropShift3) | (cp & ((u32(1) << PropShift3) - 1)))]
}

// BidiClass is the Bidi_Class of c.
BidiClass(c: char): u8 {
    return BidiJoining(c) & 0x1F
}

// JoiningType is the Joining_Type of c.
JoiningType(c: char): u8 {
    return BidiJoining(c) >> 5
}

// IsVirama is whether c's Canonical_Combining_Class is 9.
IsVirama(c: char): bool {
    cp := u32(c)
    var lo := 0
    var hi := Virama.Len()
    for lo < hi {
        mid := (lo + hi) / 2
        if Virama[mid] < cp {
            lo = mid + 1
        } else {
            hi = mid
        }
    }
    return lo < Virama.Len() && Virama[lo] == cp
}
