// Why: BLAKE3 is a fast cryptographic hash for content addressing, file
// checksums and key derivation, and parallel over long inputs.
//
// What: Blake3 (one-shot) and Blake3State (incremental: New, Bytes, Sum),
// BLAKE3's hash mode with a 32-byte output.
//
// How: after the BLAKE3 specification (O'Connor, Aumasson, Neves,
// Wilcox-O'Hearn 2020) and its reference_impl.rs: 1024-byte chunks of
// 64-byte blocks, a 7-round compression, and a stack of chaining values
// merged by the trailing zero bits of the chunk count. Every word operation
// is mod 2^32 (+%). No allocation; the state is plain. The one-shot Blake3
// builds no state: one chunk, or the subtree recursion of the blake3 crate.

import std.raw

// Blake3IV is BLAKE3's IV (SHA-256's).
const Blake3IV: [8]u32 = [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19]

const Blake3ChunkStart: u32 = 1
const Blake3ChunkEnd: u32 = 2
const Blake3Parent: u32 = 4
const Blake3Root: u32 = 8

// Blake3G is the quarter-round on state words a, b, c, d with message
// words x and y.
Blake3G(s: mut [16]u32, a: int, b: int, c: int, d: int, x: u32, y: u32) {
    s[a] = s[a] +% s[b] +% x
    s[d] = RotR32(s[d] ^ s[a], 16)
    s[c] = s[c] +% s[d]
    s[b] = RotR32(s[b] ^ s[c], 12)
    s[a] = s[a] +% s[b] +% y
    s[d] = RotR32(s[d] ^ s[a], 8)
    s[c] = s[c] +% s[d]
    s[b] = RotR32(s[b] ^ s[c], 7)
}

// Blake3Round is one round: the four column G's, then the four diagonal
// G's, on the message words x0 to x15 in their order for this round.
Blake3Round(s: mut [16]u32, x0: u32, x1: u32, x2: u32, x3: u32, x4: u32, x5: u32, x6: u32, x7: u32,
    x8: u32, x9: u32, x10: u32, x11: u32, x12: u32, x13: u32, x14: u32, x15: u32) {
    Blake3G(s, 0, 4, 8, 12, x0, x1)
    Blake3G(s, 1, 5, 9, 13, x2, x3)
    Blake3G(s, 2, 6, 10, 14, x4, x5)
    Blake3G(s, 3, 7, 11, 15, x6, x7)
    Blake3G(s, 0, 5, 10, 15, x8, x9)
    Blake3G(s, 1, 6, 11, 12, x10, x11)
    Blake3G(s, 2, 7, 8, 13, x12, x13)
    Blake3G(s, 3, 4, 9, 14, x14, x15)
}

// Blake3Compress is the compression function's 16-word output for the
// chaining value cv, the block's words m, the counter, the block's length
// and the flags: seven rounds written out, each reading the message words
// in its own order (the reference's MSG_SCHEDULE, the permutation 2 6 3 10
// 7 0 4 13 1 11 12 5 9 14 15 8 applied r times), so no word is copied
// between rounds and every index is a constant.
Blake3Compress(cv: &[8]u32, m: &[16]u32, counter: u64, blen: u32, flags: u32): [16]u32 {
    var s: [16]u32 = [cv[0], cv[1], cv[2], cv[3], cv[4], cv[5], cv[6], cv[7],
        Blake3IV[0], Blake3IV[1], Blake3IV[2], Blake3IV[3],
        Trunc[u32](counter), Trunc[u32](counter >> 32), blen, flags]
    Blake3Round(mut s, m[0], m[1], m[2], m[3], m[4], m[5], m[6], m[7], m[8], m[9], m[10], m[11], m[12], m[13], m[14], m[15])
    Blake3Round(mut s, m[2], m[6], m[3], m[10], m[7], m[0], m[4], m[13], m[1], m[11], m[12], m[5], m[9], m[14], m[15], m[8])
    Blake3Round(mut s, m[3], m[4], m[10], m[12], m[13], m[2], m[7], m[14], m[6], m[5], m[9], m[0], m[11], m[15], m[8], m[1])
    Blake3Round(mut s, m[10], m[7], m[12], m[9], m[14], m[3], m[13], m[15], m[4], m[0], m[11], m[2], m[5], m[8], m[1], m[6])
    Blake3Round(mut s, m[12], m[13], m[9], m[11], m[15], m[10], m[14], m[8], m[7], m[2], m[5], m[3], m[0], m[1], m[6], m[4])
    Blake3Round(mut s, m[9], m[14], m[11], m[5], m[8], m[12], m[15], m[1], m[13], m[3], m[0], m[10], m[2], m[6], m[4], m[7])
    Blake3Round(mut s, m[11], m[15], m[5], m[0], m[1], m[9], m[8], m[6], m[14], m[10], m[2], m[12], m[3], m[4], m[7], m[13])
    for i in 0..8 {
        s[i] = s[i] ^ s[i + 8]
        s[i + 8] = s[i + 8] ^ cv[i]
    }
    return s
}

// Blake3Words is the 16 little-endian words of a 64-byte block.
Blake3Words(block: []u8): [16]u32 {
    b := block[0..64]
    var m: [16]u32 = [0; 16]
    for i in 0..16 {
        m[i] = Le32(b, 4 * i)
    }
    return m
}

// Blake3Cv is the chaining value of a compression: its first 8 words.
Blake3Cv(cv: &[8]u32, m: &[16]u32, counter: u64, blen: u32, flags: u32): [8]u32 {
    s := Blake3Compress(cv, m, counter, blen, flags)
    return [s[0], s[1], s[2], s[3], s[4], s[5], s[6], s[7]]
}

// Blake3ParentWords is the block of a parent node: the left and right
// children's chaining values.
Blake3ParentWords(l: &[8]u32, r: &[8]u32): [16]u32 {
    return [l[0], l[1], l[2], l[3], l[4], l[5], l[6], l[7], r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7]]
}

/// Blake3State is a BLAKE3 hash in progress. It is not zero-valid: a
/// zero-filled state is not the start of a hash, so
/// Arena.Slice[Blake3State] is E0701. Make one with New. It holds a stack
/// of up to 54 chaining values (1,728 bytes), enough for 2^64 bytes of
/// input.
export struct Blake3State {
    cv: [8]u32
    block: [64]u8
    blen: int
    blocks: int
    chunk: u64
    stack: [54][8]u32
    depth: int
    invariant blen >= 0 && blen <= 64 && blocks >= 0 && blocks < 16 && depth >= 0 && depth <= 54
}

/// Returns the state of the empty input. O(1); allocates nothing.
export Blake3State.New(): Blake3State {
    return Blake3State(cv: Blake3IV, block: [0; 64], blen: 0, blocks: 0, chunk: 0, stack: [[0; 8]; 54], depth: 0)
}

// Blake3StartFlag is CHUNK_START when no block of the chunk is compressed.
Blake3StartFlag(blocks: int): u32 {
    return if blocks == 0 { Blake3ChunkStart } else { 0 }
}

// Blake3State.EndChunk closes the full current chunk: its chaining value
// joins the stack, merged with every completed left sibling (one per
// trailing zero bit of the new chunk count), and a fresh chunk starts.
Blake3State.EndChunk(mut self) {
    m := Blake3Words(self.block[..])
    self.PushCv(Blake3Cv(&self.cv, &m, self.chunk, 64, Blake3StartFlag(self.blocks) | Blake3ChunkEnd))
    self.cv = Blake3IV
    self.blen = 0
    self.blocks = 0
}

// Blake3State.PushCv adds the chaining value of the completed chunk whose
// counter is self.chunk to the stack, merged with every completed left
// sibling (one per trailing zero bit of the new chunk count), and counts
// the chunk. The depth stays at most the popcount of the chunk count.
Blake3State.PushCv(mut self, chunk_cv: [8]u32) {
    var cv := chunk_cv
    var total := self.chunk +% 1
    for total & 1 == 0 {
        self.depth -= 1
        p := Blake3ParentWords(&self.stack[self.depth], &cv)
        cv = Blake3Cv(&Blake3IV, &p, 0, 64, Blake3Parent)
        total = total >> 1
    }
    self.stack[self.depth] = cv
    self.depth += 1
    self.chunk = self.chunk +% 1
}

/// Absorbs data. Bytes in any split gives the same hash as Blake3 of the
/// concatenation. Runs of four whole 1 KiB chunks are hashed side by side
/// with SIMD. O(data.Len()); allocates nothing.
export Blake3State.Bytes(mut self, data: []u8) {
    n := data.Len()
    var i := 0
    for i < n {
        if self.blen == 64 {
            if self.blocks == 15 {
                self.EndChunk()
            } else {
                m := Blake3Words(self.block[..])
                self.cv = Blake3Cv(&self.cv, &m, self.chunk, 64, Blake3StartFlag(self.blocks))
                self.blocks += 1
                self.blen = 0
            }
        }
        // The vector leaf is not instantiated on freestanding, whose
        // general-regs-only bins may hold no vector (E1108), as in
        // Blake3Subtree. Each chunk's chaining value joins the stack by
        // itself, so the chunk count needs no alignment.
        if Target.os != Os.Freestanding && self.blen == 0 && self.blocks == 0 {
            for n - i > 4096 {
                cvs := Blake3Chunks4(data[i..i + 4096], self.chunk)
                for j in 0..4 {
                    self.PushCv(cvs[j])
                }
                i += 4096
            }
        }
        if self.blen == 0 {
            // Whole blocks straight from data, while more input follows each.
            for n - i > 64 && self.blocks < 15 {
                m := Blake3Words(data[i..n])
                self.cv = Blake3Cv(&self.cv, &m, self.chunk, 64, Blake3StartFlag(self.blocks))
                self.blocks += 1
                i += 64
            }
        }
        k := CopyBytes(mut self.block[self.blen..64], data[i..n])
        self.blen += k
        i += k
    }
}

/// Returns the 32-byte hash of everything absorbed. It works on a copy of
/// the state, so the state can keep absorbing. O(stack depth) compressions;
/// allocates nothing.
export Blake3State.Sum(self): [32]u8 {
    var cv := self.cv
    var tail: [64]u8 = [0; 64]
    _ = CopyBytes(mut tail[..], self.block[0..self.blen])
    var m := Blake3Words(tail[..])
    var counter := self.chunk
    var blen := Trunc[u32](self.blen)
    var flags := Blake3StartFlag(self.blocks) | Blake3ChunkEnd
    var k := self.depth
    for k > 0 {
        k -= 1
        child := Blake3Cv(&cv, &m, counter, blen, flags)
        m = Blake3ParentWords(&self.stack[k], &child)
        cv = Blake3IV
        counter = 0
        blen = 64
        flags = Blake3Parent
    }
    s := Blake3Compress(&cv, &m, counter, blen, flags | Blake3Root)
    var out: [32]u8 = [0; 32]
    for i in 0..8 {
        PutLe32(mut out[..], 4 * i, s[i])
    }
    return out
}

// Blake3Chunk is the chaining value of one chunk, data of 1 to 1,024
// bytes (0 only for the empty input), whose chunk counter is counter; root
// is Blake3Root when the chunk is the whole input, else 0, so a lone
// chunk's result is the hash's first 8 words. Every full block is
// compressed straight from data; only a partial last block is copied, into
// a 64-byte buffer.
Blake3Chunk(data: []u8, counter: u64, root: u32): [8]u32 {
    n := data.Len()
    var cv := Blake3IV
    var flags := Blake3ChunkStart
    var i := 0
    for n - i > 64 {
        m := Blake3Words(data[i..n])
        cv = Blake3Cv(&cv, &m, counter, 64, flags)
        flags = 0
        i += 64
    }
    last := flags | Blake3ChunkEnd | root
    if n - i == 64 {
        m := Blake3Words(data[i..n])
        return Blake3Cv(&cv, &m, counter, 64, last)
    }
    var tail: [64]u8 = [0; 64]
    _ = CopyBytes(mut tail[..], data[i..n])
    m := Blake3Words(tail[..])
    return Blake3Cv(&cv, &m, counter, Trunc[u32](n - i), last)
}

// Blake3Subtree is the chaining value of the subtree over data whose first
// chunk has the counter chunk, after the specification's section 5.1.2 and
// the blake3 crate's compress_subtree_wide: at most 1,024 bytes is one
// chunk; above, the left subtree takes the largest power of two of chunks
// that leaves at least one byte to the right, and the node is the parent
// of the two. root is Blake3Root on the top node only. The recursion is
// ceil(log2(chunks)) deep, at most 54 (2^54 chunks is 2^64 bytes), and
// each level holds two chaining values and a parent block on the stack.
Blake3Subtree(data: []u8, chunk: u64, root: u32): [8]u32 {
    n := data.Len()
    if n <= 1024 {
        return Blake3Chunk(data, chunk, root)
    }
    // The vector leaf is not instantiated on freestanding, whose
    // general-regs-only bins may hold no vector (E1108; std.simd's words.mk).
    if Target.os != Os.Freestanding && n == 4096 {
        return Blake3Subtree4(data, chunk, root)
    }
    // n - 1 >= 1024, so q >= 1 and Clz64(q) <= 63.
    q := Cast[u64]((n - 1) / 1024)
    left := 1024 << (63 - raw.Clz64(q))
    lcv := Blake3Subtree(data[0..left], chunk, 0)
    rcv := Blake3Subtree(data[left..n], chunk +% Cast[u64](left / 1024), 0)
    p := Blake3ParentWords(&lcv, &rcv)
    return Blake3Cv(&Blake3IV, &p, 0, 64, Blake3Parent | root)
}

/// Returns the 32-byte BLAKE3 hash of data. It builds no Blake3State, so a
/// short input does not pay for the state's 1,728-byte stack.
/// O(data.Len()); allocates nothing.
export Blake3(data: []u8): [32]u8 {
    cv := Blake3Subtree(data, 0, Blake3Root)
    var out: [32]u8 = [0; 32]
    for i in 0..8 {
        PutLe32(mut out[..], 4 * i, cv[i])
    }
    return out
}
