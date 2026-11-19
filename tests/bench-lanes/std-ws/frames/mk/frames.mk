// ws-frames, Mk: std.websocket's receive path written once per language
// (README.md): decode a buffer of masked client frames into messages,
// fragments joined, each payload unmasked a byte at a time into one
// message buffer, each text message checked as UTF-8 by RFC 3629's table
// (a branch per lead byte), rounds passes. The buffer is batches copies of
// one batch of seven messages (binary 0, 125, 65,535 and 70,000 bytes; text
// 126 bytes, 30,000 bytes in three fragments split inside a character, and
// 1,000 bytes ending in a byte that is not UTF-8), each frame masked with
// the four low bytes of (f + 1) * 0x9E3779B97F4A7C15, f the frame's index
// in the buffer; binary payloads are splitmix64 bytes (seed 1), text the
// 10-byte unit "a", U+00E9, U+20AC, U+1F600 repeated. Per message the
// checksum adds its length, 7 when it is binary or valid text, and its
// byte at (r * 7919 + m) % length, m its index in the pass. The checksum
// wraps; every other operation is checked. Usage: ws-frames <batches>
// <rounds>.

// MaxMsg is the longest message, the message buffer's size.
const MaxMsg: int = 70000

// The batch's messages: opcode, length and fragments of each.
const Ops: [7]u8 = [2, 2, 1, 2, 2, 1, 1]
const Lens: [7]int = [0, 125, 126, 65535, 70000, 30000, 1000]
const Frags: [7]int = [1, 1, 1, 1, 1, 3, 1]

const Unit: [10]u8 = [0x61, 0xC3, 0xA9, 0xE2, 0x82, 0xAC, 0xF0, 0x9F, 0x98, 0x80]

// Build writes the frames of batches copies of the batch to out and
// returns their length.
Build(out: mut []u8, pool: mut []u8, text: mut []u8, payload: mut []u8, batches: int): int {
    var st: u64 = 1
    var i := 0
    for i < MaxMsg {
        st = st +% 0x9E3779B97F4A7C15
        var z := st
        z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
        z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
        z = z ^ (z >> 31)
        var k := 0
        for k < 8 && i < MaxMsg {
            pool[i] = Trunc[u8](z >> Trunc[u64](8 * k))
            k += 1
            i += 1
        }
    }
    for t in 0..MaxMsg {
        text[t] = Unit[t % 10]
    }
    var o := 0
    var f: u64 = 0
    for _ in 0..batches {
        for mi in 0..7 {
            op := Ops[mi]
            n := Lens[mi]
            frags := Frags[mi]
            for p in 0..n {
                payload[p] = if op == 1 { text[p] } else { pool[p] }
            }
            if n == 1000 {
                payload[999] = 0xFF
            }
            // Fragments: the first n/3 - 1 bytes, the next n/3 + 1, the rest.
            var at := 0
            for j in 0..frags {
                var l := n - at
                if frags == 3 && j == 0 {
                    l = n / 3 - 1
                } else if frags == 3 && j == 1 {
                    l = n / 3 + 1
                }
                var b0: u8 = 0
                if j == 0 {
                    b0 = op
                }
                if j == frags - 1 {
                    b0 = b0 | 0x80
                }
                out[o] = b0
                o += 1
                if l <= 125 {
                    out[o] = 0x80 | Trunc[u8](l)
                    o += 1
                } else if l <= 65535 {
                    out[o] = 0x80 | 126
                    out[o + 1] = Trunc[u8](l >> 8)
                    out[o + 2] = Trunc[u8](l)
                    o += 3
                } else {
                    out[o] = 0x80 | 127
                    out[o + 1] = 0
                    out[o + 2] = 0
                    out[o + 3] = 0
                    out[o + 4] = 0
                    out[o + 5] = Trunc[u8](l >> 24)
                    out[o + 6] = Trunc[u8](l >> 16)
                    out[o + 7] = Trunc[u8](l >> 8)
                    out[o + 8] = Trunc[u8](l)
                    o += 9
                }
                kw := (f +% 1) *% 0x9E3779B97F4A7C15
                key: [4]u8 = [Trunc[u8](kw), Trunc[u8](kw >> 8), Trunc[u8](kw >> 16), Trunc[u8](kw >> 24)]
                for k in 0..4 {
                    out[o + k] = key[k]
                }
                o += 4
                for q in 0..l {
                    out[o + q] = payload[at + q] ^ key[q & 3]
                }
                o += l
                at += l
                f += 1
            }
        }
    }
    return o
}

// ValidUtf8 reports whether s is UTF-8 by RFC 3629's table of well-formed
// sequences: a branch per lead byte, the second byte's range by the lead,
// the rest continuation bytes.
ValidUtf8(s: []u8): bool {
    n := s.Len()
    var i := 0
    for i < n {
        c := s[i]
        if c < 0x80 {
            i += 1
            continue
        }
        var need := 0
        var lo: u8 = 0x80
        var hi: u8 = 0xBF
        if c < 0xC2 {
            return false
        } else if c < 0xE0 {
            need = 1
        } else if c < 0xF0 {
            need = 2
            if c == 0xE0 {
                lo = 0xA0
            } else if c == 0xED {
                hi = 0x9F
            }
        } else if c < 0xF5 {
            need = 3
            if c == 0xF0 {
                lo = 0x90
            } else if c == 0xF4 {
                hi = 0x8F
            }
        } else {
            return false
        }
        if i + need >= n {
            return false
        }
        b := s[i + 1]
        if b < lo || b > hi {
            return false
        }
        var k := 2
        for k <= need {
            if s[i + k] & 0xC0 != 0x80 {
                return false
            }
            k += 1
        }
        i += need + 1
    }
    return true
}

// Decode is one pass over buf: the checksum it adds for round r.
Decode(buf: []u8, msg: mut []u8, r: u64): u64 {
    len := buf.Len()
    var sum: u64 = 0
    var pos := 0
    var mlen := 0
    var mop: u8 = 0
    var count: u64 = 0
    for pos + 2 <= len {
        b0 := buf[pos]
        b1 := buf[pos + 1]
        var l := int(b1 & 0x7F)
        var hl := 2
        if l == 126 {
            if pos + 4 > len {
                return sum +% 1000003
            }
            l = (int(buf[pos + 2]) << 8) | int(buf[pos + 3])
            hl = 4
        } else if l == 127 {
            if pos + 10 > len {
                return sum +% 1000003
            }
            var x: u64 = 0
            for k in 2..10 {
                x = (x << 8) | u64(buf[pos + k])
            }
            if x > Cast[u64](MaxMsg) {
                return sum +% 1000003
            }
            l = Cast[int](x)
            hl = 10
        }
        op := b0 & 0x0F
        if b0 & 0x70 != 0 || b1 & 0x80 == 0 || l > len - pos - hl - 4 {
            return sum +% 1000003
        }
        if op != 0 {
            mop = op
            mlen = 0
        }
        if l > MaxMsg - mlen {
            return sum +% 1000003
        }
        k := pos + hl
        key: [4]u8 = [buf[k], buf[k + 1], buf[k + 2], buf[k + 3]]
        pos = k + 4
        for i in 0..l {
            msg[mlen + i] = buf[pos + i] ^ key[i & 3]
        }
        mlen += l
        pos += l
        if b0 & 0x80 != 0 {
            sum = sum +% Cast[u64](mlen)
            if mop != 1 || ValidUtf8(msg[0..mlen]) {
                sum = sum +% 7
            }
            if mlen > 0 {
                at := Cast[int]((r *% 7919 +% count) % Cast[u64](mlen))
                sum = sum +% u64(msg[at])
            }
            count += 1
        }
    }
    return sum
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    batches := Cast[int](try sys.args[1].ParseInt())
    rounds := try sys.args[2].ParseInt()
    pool := try sys.arena.Slice[u8](MaxMsg)
    text := try sys.arena.Slice[u8](MaxMsg)
    payload := try sys.arena.Slice[u8](MaxMsg)
    out := try sys.arena.Slice[u8](batches * (166786 + 9 * 14))
    len := Build(mut out[..], mut pool[..], mut text[..], mut payload[..], batches)
    msg := try sys.arena.Slice[u8](MaxMsg)
    var total: u64 = 0
    var r: i64 = 0
    for r < rounds {
        total = total +% Decode(out[0..len], mut msg[..], Cast[u64](r))
        r += 1
    }
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total))
}
