// A c_sources file on the freestanding target:
// c/sum.c, compiled by the driver with -mno-red-zone and, in this
// general-regs-only bin, -mgeneral-regs-only, called
// through extern.

import std.raw

extern SumBytes(p: *u8, n: uint): uint

// CSum is the byte sum of s, computed in C.
CSum(s: str): u64 {
    var r: uint = 0
    unsafe "SumBytes reads exactly n bytes of s" {
        r = SumBytes(raw.StrData(s), Cast[uint](s.Len()))
    }
    return u64(r)
}
