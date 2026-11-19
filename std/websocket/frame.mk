// Why: every byte a WebSocket peer sends is untrusted, so the frame header
// must be read and checked in full before any payload byte is read or any
// memory reserved for it (RFC 6455 section 5 framing, 7.4 close codes).
//
// What: the opcodes and close statuses, Head, ParseHead, PutHead, Mask,
// ReceivedCode and CloseBody.
//
// How: ParseHead returns none until the header's 2 to 14 bytes are in hand,
// then checks every rule of section 5.2 a header alone can break and the
// connection's frame limit: O(1) time and stack, no loop, no recursion.
// Mask XORs 16 bytes at a time through std.simd's Vec (SSE2 on x86_64, NEON
// on aarch64), with a scalar head and tail; the key's phase carries from
// call to call, so a payload unmasked in pieces equals one unmasked whole.

import std.simd

// The opcodes of RFC 6455 section 5.2.
const OpContinue: u8 = 0
const OpText: u8 = 1
const OpBinary: u8 = 2
const OpClose: u8 = 8
const OpPing: u8 = 9
const OpPong: u8 = 10

// The close statuses std.websocket sends or reports itself (7.4.1).
const CloseNormal: u16 = 1000
const CloseProtocol: u16 = 1002
const CloseNoStatus: u16 = 1005
const CloseAbnormal: u16 = 1006
const CloseBadData: u16 = 1007
const ClosePolicy: u16 = 1008
const CloseTooBig: u16 = 1009

// MaxControl is the longest payload of a control frame (5.5).
const MaxControl: int = 125

// MaxHead is the longest frame header: 2 bytes, a 64-bit length, a key.
const MaxHead: int = 14

// Head is one frame's header (5.2): the payload's length, the mask key
// when masked, and size, the header's own length in bytes.
struct Head {
    fin: bool
    op: u8
    masked: bool
    key: [4]u8
    len: int
    size: int
}

// IsControl: opcodes 8 to 15 are control frames (5.5).
IsControl(op: u8): bool {
    return op >= 8
}

// ParseHead reads the header at the start of b, from the side that sends
// masked frames (a client) when masked is true: none until b holds the
// whole header. Protocol(1002) for a header RFC 6455 forbids, TooLarge for
// a payload over limit. O(1); allocates nothing.
ParseHead(b: []u8, masked: bool, limit: int): Protocol|TooLarge!?Head {
    if b.Len() < 2 {
        return none
    }
    b0 := b[0]
    b1 := b[1]
    // RSV1 to RSV3 (0x70): no extension was agreed, so each must be 0.
    if (b0 & 0x70) != 0 {
        fail Protocol(code: CloseProtocol)
    }
    op := b0 & 0x0F
    fin := (b0 & 0x80) != 0
    if (op > OpBinary && op < OpClose) || op > OpPong {
        fail Protocol(code: CloseProtocol)
    }
    m := (b1 & 0x80) != 0
    if m != masked {
        // 5.1: a server fails a frame its client did not mask, and a client
        // one its server did.
        fail Protocol(code: CloseProtocol)
    }
    short := int(b1 & 0x7F)
    var size := 2
    var len := short
    if short == 126 {
        size = 4
    } else if short == 127 {
        size = 10
    }
    if IsControl(op) && (!fin || short > MaxControl) {
        fail Protocol(code: CloseProtocol)
    }
    if m {
        size += 4
    }
    if b.Len() < size {
        return none
    }
    if short == 126 {
        len = (int(b[2]) << 8) | int(b[3])
    } else if short == 127 {
        if (b[2] & 0x80) != 0 {
            // 5.2: the most significant bit of a 64-bit length is 0.
            fail Protocol(code: CloseProtocol)
        }
        var n: u64 = 0
        for i in 2..10 {
            n = (n << 8) | u64(b[i])
        }
        // Compared as u64, so a length past int's range is TooLarge, never
        // a wrapped int.
        if limit < 0 || n > Cast[u64](limit) {
            fail TooLarge
        }
        len = Trunc[int](n)
    }
    if len > limit {
        fail TooLarge
    }
    var key: [4]u8 = [0; 4]
    if m {
        at := size - 4
        for i in 0..4 {
            key[i] = b[at + i]
        }
    }
    return some(Head(fin: fin, op: op, masked: m, key: key, len: len, size: size))
}

// PutHead writes a final frame's header for a payload of len bytes into
// dst, masked with key when masked, and returns its length (2 to 14).
// The length takes the fewest bytes 5.2 allows. dst holds at least
// MaxHead bytes.
PutHead(dst: mut []u8, op: u8, len: int, masked: bool, key: [4]u8): int {
    dst[0] = 0x80 | op
    mbit: u8 = if masked { 0x80 } else { 0 }
    var size := 2
    if len <= 125 {
        dst[1] = mbit | Trunc[u8](len)
    } else if len <= 0xFFFF {
        dst[1] = mbit | 126
        dst[2] = Trunc[u8](len >> 8)
        dst[3] = Trunc[u8](len)
        size = 4
    } else {
        dst[1] = mbit | 127
        for i in 0..8 {
            dst[2 + i] = Trunc[u8](len >> (8 * (7 - i)))
        }
        size = 10
    }
    if masked {
        for i in 0..4 {
            dst[size + i] = key[i]
        }
        size += 4
    }
    return size
}

// Mask XORs b in place with key, starting at the key's byte phase (0 to
// 3), and returns the phase after b (5.3: octet i of the payload is XORed
// with octet i mod 4 of the key). O(b.Len()), 16 bytes a step; allocates
// nothing.
Mask(b: mut []u8, key: [4]u8, phase: int): int {
    n := b.Len()
    var i := 0
    var p := phase & 3
    // A scalar head, so the vector loop starts at phase 0 and its key
    // pattern is the key itself, repeated.
    for i < n && p != 0 {
        b[i] = b[i] ^ key[p]
        p = (p + 1) & 3
        i += 1
    }
    if n - i >= 16 {
        k := simd.Vec[u8, 16].FromArray([key[0], key[1], key[2], key[3], key[0], key[1], key[2], key[3], key[0], key[1],
            key[2], key[3], key[0], key[1], key[2], key[3]])
        for n - i >= 16 {
            v := simd.Vec[u8, 16].FromSlice(b[..], i) ^ k
            v.CopyTo(b, i)
            i += 16
        }
    }
    for i < n {
        b[i] = b[i] ^ key[p]
        p = (p + 1) & 3
        i += 1
    }
    return p
}

// ReceivedCode reports whether a Close frame may carry code (7.4): the
// statuses RFC 6455 and the IANA registry define for the wire (1000 to
// 1003, 1007 to 1014) and the ranges for libraries and applications (3000
// to 4999). 1004 is reserved, and 1005, 1006 and 1015 are never sent.
ReceivedCode(code: u16): bool {
    if code >= 3000 && code <= 4999 {
        return true
    }
    return (code >= 1000 && code <= 1003) || (code >= 1007 && code <= 1014)
}

// CloseBody reads a Close frame's body (5.5.1): empty is 1005, one byte is
// Protocol(1002), else a status ReceivedCode allows and a UTF-8 reason
// (Protocol(1007) otherwise).
CloseBody(body: []u8): Protocol!u16 {
    if body.Len() == 0 {
        return CloseNoStatus
    }
    if body.Len() == 1 {
        fail Protocol(code: CloseProtocol)
    }
    code := (u16(body[0]) << 8) | u16(body[1])
    if !ReceivedCode(code) {
        fail Protocol(code: CloseProtocol)
    }
    if !simd.ValidUtf8(body[2..]) {
        fail Protocol(code: CloseBadData)
    }
    return code
}
