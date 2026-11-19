// Why: SHA-1 still hashes git objects and WebSocket handshakes, and the CPU's
// SHA instructions run it several times faster than portable code.
//
// What: Sha1Blocks, the SHA-1 kernel over std.raw's SHA-1 intrinsics behind
// its simd.Cpu() guard, with Sha1Ni and its round helpers.
//
// How: Sha1Blocks returns false, having written nothing, when the running
// CPU lacks sha on x86_64 or sha2 on aarch64 (always on x86_64-freestanding
// and in the evaluator, where Cpu() is the baseline); the caller then runs
// its portable path, which is the reference. The shape is Intel's SHA
// extensions sample (Gulley et al., "Intel SHA Extensions", 2013) and Go's
// sha1block_amd64 SHA-NI path: the state is held as (d, c, b, a) across the
// block, four rounds per Sha1Rounds4, the next group's e added to its first
// word by Sha1NextE from the state before the rounds, and the schedule four
// words at a time from the last sixteen. Sha1Ni's clause names sse2 too:
// the SHA extensions work in xmm registers (E1106 needs on x86_64; aarch64
// ignores the name).

import std.raw
import std.simd

// Sha1Words is the four big-endian words of data from i in the SHA-1
// intrinsics' lane order, W[t] in lane 3: one reversal of the 16 bytes.
Sha1Words(data: []u8, i: int): [4]u32 {
    v := simd.Vec[u8, 16].FromSlice(data, i)
    return v.Shuffle(v, [15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0]).Reinterpret[u32, 4]().ToArray()
}

// Sha1Add is a + b lanewise, wrapping.
Sha1Add(a: [4]u32, b: [4]u32): [4]u32 {
    return (simd.Vec[u32, 4].FromArray(a) +% simd.Vec[u32, 4].FromArray(b)).ToArray()
}

// Sha1Ni is Sha1Blocks' body. Each step q runs rounds 4q to 4q + 3: it
// takes the next group's e from the state before them (Sha1NextE), runs
// them on we (W[4q .. 4q + 3] with this group's e in W[4q]), and shifts
// the window w0 .. w3, the words of groups q to q + 3. From step 16 no
// word is new, and the window fills with last, (0, 0, 0, h4), so step
// 19's Sha1NextE gives the block's new h4 in lane 3. One loop per round
// group, since the group is Sha1Rounds4's const argument.
Sha1Ni(h: mut [5]u32, data: []u8) cpu "sse2, sha, sha2" {
    var abcd: [4]u32 = [h[3], h[2], h[1], h[0]]
    var e := h[4]
    n := data.Len() / 64 * 64
    var i := 0
    for i < n {
        save := abcd
        last: [4]u32 = [0, 0, 0, e]
        var w0 := Sha1Words(data, i)
        var w1 := Sha1Words(data, i + 16)
        var w2 := Sha1Words(data, i + 32)
        var w3 := Sha1Words(data, i + 48)
        var we := Sha1Add(w0, last) // e into W[0], lane 3
        for _ in 0..5 {
            next := raw.Sha1NextE(abcd, w1)
            abcd = raw.Sha1Rounds4(abcd, we, 0)
            w4 := raw.Sha1Schedule4(w0, w1, w2, w3)
            w0 = w1
            w1 = w2
            w2 = w3
            w3 = w4
            we = next
        }
        for _ in 5..10 {
            next := raw.Sha1NextE(abcd, w1)
            abcd = raw.Sha1Rounds4(abcd, we, 1)
            w4 := raw.Sha1Schedule4(w0, w1, w2, w3)
            w0 = w1
            w1 = w2
            w2 = w3
            w3 = w4
            we = next
        }
        for _ in 10..15 {
            next := raw.Sha1NextE(abcd, w1)
            abcd = raw.Sha1Rounds4(abcd, we, 2)
            w4 := raw.Sha1Schedule4(w0, w1, w2, w3)
            w0 = w1
            w1 = w2
            w2 = w3
            w3 = w4
            we = next
        }
        for q in 15..20 {
            next := raw.Sha1NextE(abcd, w1)
            abcd = raw.Sha1Rounds4(abcd, we, 3)
            w4 := if q < 16 { raw.Sha1Schedule4(w0, w1, w2, w3) } else { last }
            w0 = w1
            w1 = w2
            w2 = w3
            w3 = w4
            we = next
        }
        abcd = Sha1Add(abcd, save)
        e = we[3]
        i += 64
    }
    h[0] = abcd[3]
    h[1] = abcd[2]
    h[2] = abcd[1]
    h[3] = abcd[0]
    h[4] = e
}

/// Sha1Blocks compresses every whole 64-byte block of data into h, in
/// order (FIPS 180-4 section 6.1.2; h is H0 .. H4), and reports true; it
/// reports false, writing nothing, when the CPU lacks sha (sha2 on
/// aarch64). data.Len() % 64 trailing bytes are ignored. O(n); allocates
/// nothing.
export Sha1Blocks(h: mut [5]u32, data: []u8): bool {
    if Target.os == Os.Freestanding {
        return false
    }
    c := simd.Cpu()
    if !c.Has("sha") && !c.Has("sha2") {
        return false
    }
    unsafe "Cpu() reports sha or sha2, the feature Sha1Ni's clause names on this architecture" {
        Sha1Ni(h, data)
    }
    return true
}
