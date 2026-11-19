// Why: zone files are untrusted input read from disk, so their reader must
// check every count before it trusts it.
//
// What: Zone.FromTzif, the TZif reader for RFC 8536 versions 1 to 4, with
// ReadHeader, ReadBlock and the limit MaxTzif.
//
// How: a version 2+ file is read from its second header and 64-bit data block
// (the version 1 block is skipped, as RFC 8536 section 4 asks), then the
// POSIX TZ footer. Every count is checked against the bytes before anything
// is read or allocated, the reader holds no recursion, and it allocates 12
// bytes per transition in the caller's arena; input over 1 MiB is refused.

// MaxTzif is the largest zone data read: 1 MiB. The largest zone of
// tzdata 2026c is 3,535 bytes.
const MaxTzif: int = 1 << 20

// HeaderLen is the length of a TZif header (RFC 8536 3.1).
const HeaderLen: int = 44

// The utoff range RFC 8536 3.2 recommends (-24:59:59 to 25:59:59); the
// reader refuses others, so every offset is within ResolveWindow.
const MinUtoff: i64 = -89_999
const MaxUtoff: i64 = 93_599

// Tzif is a header's counts (RFC 8536 3.1).
struct Tzif {
    version: u8
    isutcnt: int
    isstdcnt: int
    leapcnt: int
    timecnt: int
    typecnt: int
    charcnt: int
}

// Be32 is the big-endian u32 at data[i..i + 4].
Be32(data: []u8, i: int): u32 {
    return (u32(data[i]) << 24) | (u32(data[i + 1]) << 16) | (u32(data[i + 2]) << 8) | u32(data[i + 3])
}

// Be64 is the big-endian i64 at data[i..i + 8].
Be64(data: []u8, i: int): i64 {
    hi := u64(Be32(data, i))
    lo := u64(Be32(data, i + 4))
    return Trunc[i64]((hi << 32) | lo)
}

// ReadHeader reads the header at data[at..]; none when it is short, not
// TZif, of another version, or counts more than data holds.
ReadHeader(data: []u8, at: int): ?Tzif {
    if data.Len() - at < HeaderLen {
        return none
    }
    if data[at] != b'T' || data[at + 1] != b'Z' || data[at + 2] != b'i' || data[at + 3] != b'f' {
        return none
    }
    v := data[at + 4]
    if v != 0 && v != b'2' && v != b'3' && v != b'4' {
        return none
    }
    var c: [6]int = [0; 6]
    for k in 0..6 {
        n := Be32(data, at + 20 + 4 * k)
        // Every count names at least one byte, so none can exceed data.
        if Cast[int](n) > data.Len() {
            return none
        }
        c[k] = int(n)
    }
    return some(Tzif(version: v, isutcnt: c[0], isstdcnt: c[1], leapcnt: c[2], timecnt: c[3], typecnt: c[4], charcnt: c[5]))
}

// BlockLen is the length of the data block after h with times of tb bytes.
Tzif.BlockLen(&self, tb: int): int {
    return self.timecnt * tb + self.timecnt + self.typecnt * 6 + self.charcnt + self.leapcnt * (tb + 4) + self.isstdcnt + self.isutcnt
}

/// The zone that TZif bytes describe (RFC 8536 versions 1 to 4, the POSIX
/// footer of version 2+ for instants past the table). name is the
/// program's name for the zone; it is not checked or kept. Fails with
/// BadData when data is not valid TZif, its footer is not a valid POSIX TZ
/// string, an offset is outside -24:59:59 to 25:59:59, or data is over
/// 1 MiB; OutOfMemory when a cannot hold 12 bytes per transition. O(n) in
/// the bytes; allocates the transition table in a.
export Zone.FromTzif(a: &Arena, name: str, data: []u8): Error|OutOfMemory!Zone from a {
    _ = name
    if data.Len() > MaxTzif {
        fail BadData
    }
    h1 := ReadHeader(data, 0) else {
        fail BadData
    }
    if h1.version == 0 {
        end := HeaderLen + h1.BlockLen(4)
        if end != data.Len() {
            fail BadData
        }
        return try ReadBlock(a, data, HeaderLen, &h1, 4, NoRule())
    }
    at := HeaderLen + h1.BlockLen(4)
    h2 := ReadHeader(data, at) else {
        fail BadData
    }
    if h2.version != h1.version {
        fail BadData
    }
    body := at + HeaderLen
    foot := body + h2.BlockLen(8)
    // The footer is "\n", the TZ string, "\n", and nothing after it.
    if foot >= data.Len() || data[foot] != 0x0A || data[data.Len() - 1] != 0x0A || data.Len() - foot < 2 {
        fail BadData
    }
    tz := data[foot + 1..data.Len() - 1]
    for c in tz {
        if c == 0x0A {
            fail BadData
        }
    }
    r := ParseRule(tz) else {
        fail BadData
    }
    return try ReadBlock(a, data, body, &h2, 8, r)
}

// ReadBlock reads the data block at data[at..] whose header is h and whose
// times are tb bytes, checking RFC 8536 3.2's constraints, and builds the
// zone with rule r.
ReadBlock(a: &Arena, data: []u8, at: int, h: &Tzif, tb: int, r: Rule): Error|OutOfMemory!Zone from a {
    if h.typecnt < 1 || h.typecnt > 256 || h.charcnt < 1 {
        fail BadData
    }
    if (h.isutcnt != 0 && h.isutcnt != h.typecnt) || (h.isstdcnt != 0 && h.isstdcnt != h.typecnt) {
        fail BadData
    }
    if data.Len() - at < h.BlockLen(tb) {
        fail BadData
    }
    n := h.timecnt
    idx := at + n * tb
    types := idx + n
    // The offset of each local time type, checked.
    var offs: [256]i32 = [0; 256]
    for k in 0..h.typecnt {
        p := types + 6 * k
        utoff := Cast[i64](Trunc[i32](Be32(data, p)))
        if utoff < MinUtoff || utoff > MaxUtoff {
            fail BadData
        }
        if data[p + 4] > 1 || int(data[p + 5]) >= h.charcnt {
            fail BadData
        }
        offs[k] = Cast[i32](utoff)
    }
    // isstd and isut indicators are 0 or 1, and isut needs isstd.
    ind := types + 6 * h.typecnt + h.charcnt + h.leapcnt * (tb + 4)
    for k in 0..h.isstdcnt {
        if data[ind + k] > 1 {
            fail BadData
        }
    }
    for k in 0..h.isutcnt {
        u := data[ind + h.isstdcnt + k]
        if u > 1 || (u == 1 && (h.isstdcnt == 0 || data[ind + k] != 1)) {
            fail BadData
        }
    }
    trans := try a.Slice[i64](n)
    zoff := try a.Slice[i32](n)
    for i in 0..n {
        t := if tb == 8 { Be64(data, at + 8 * i) } else { Cast[i64](Trunc[i32](Be32(data, at + 4 * i))) }
        if i > 0 && t <= trans[i - 1] {
            fail BadData
        }
        k := int(data[idx + i])
        if k >= h.typecnt {
            fail BadData
        }
        trans[i] = t
        zoff[i] = offs[k]
    }
    return Zone(trans: trans[..], offs: zoff[..], first: offs[0], rule: r)
}
