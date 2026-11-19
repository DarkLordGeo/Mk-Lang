// Why: every hash table needs a hasher that is fast on ordinary keys and,
// when the keys come from untrusted input, one that resists chosen
// collisions; both must give the same order in every run and at compile
// time.
//
// What: HashKey (Of, HashKeyOf, HashKeyWords), Hasher with its two kinds,
// NewHasher and NewHasherKeyed, the word and byte feeders, HashFinish, and
// the one-call string hashes FoldStr, SipStr and SipStrKeyed.
//
// How: New's tables use the fold hash, a fast fixed function (foldhash by
// O. Peters, hashbrown's default since 0.15, in its quality form, with fixed
// seeds), fed two independent lanes so two multiplies are in flight.
// NewKeyed's tables use SipHash-1-3 under the table's HashKey. The Go model
// of the fold hash is std/std-hash_model_test.go, which also checks its
// quality (avalanche and collision counts).

import std.raw

/// A SipHash-1-3 key for the maps and sets made with NewKeyed, which is how
/// to hash keys from untrusted input such as network or file data. Plain,
/// with no public fields. HashKey.New(io) makes a random one from 16 bytes
/// of Io.Random, so a keyed map's iteration order varies from process to
/// process unless io is simulated; compile-time evaluation runs no Io
/// operation, so it never makes one. HashKey.Of(k0, k1) makes one from
/// chosen words. A zero-filled HashKey is the key (0, 0), the key Rust's
/// DefaultHasher uses.
export struct HashKey {
    k0: u64
    k1: u64
}

// SipHash's reference key, the bytes 0x00 to 0x0f: the key of the
// published SipHash vectors and of SipStr. No table uses it.
const FixedK0: u64 = 0x0706050403020100
const FixedK1: u64 = 0x0f0e0d0c0b0a0908

// The fold hash's fixed constants: the first five 64-bit words of pi's
// fraction, F0 to F3 as foldhash's ARBITRARY0 to ARBITRARY3; F4 seeds the
// second lane.
const FoldF0: u64 = 0x243f6a8885a308d3
const FoldF1: u64 = 0x13198a2e03707344
const FoldF2: u64 = 0xa4093822299f31d0
const FoldF3: u64 = 0x082efa98ec4e6c89
const FoldF4: u64 = 0x452821e638d01377

// HashKeyOf is the key whose k0 and k1 are the little-endian words b[0..8]
// and b[8..16] (SipHash's own key layout); a b shorter than 16 bytes is a
// bounds panic (code 2, LoadWord's check).
HashKeyOf(b: []u8): HashKey {
    return HashKey(k0: LoadWord(b, 0), k1: LoadWord(b, 8))
}

/// The key whose SipHash words are k0 and k1, for output that must repeat: a
/// file format, a test vector, a checksum. HashKey.Of(0, 0) is the same key
/// as a zero-filled HashKey. A key whose words are known protects nothing;
/// for keys from untrusted input use HashKey.New(io). O(1); allocates
/// nothing and cannot fail.
export HashKey.Of(k0: u64, k1: u64): HashKey {
    return HashKey(k0: k0, k1: k1)
}

/// The SipHash words (k0, k1) of key, for std.hash.Sip13. Only std's own
/// modules can call it. O(1).
export HashKeyWords(key: &HashKey): (k0: u64, k1: u64) {
    return (k0: key.k0, k1: key.k1)
}

/// The state a Hash impl feeds bytes into, with Bytes and U64. The hash
/// depends only on the byte stream, however Bytes and U64 calls split it.
/// A Hasher is one of two kinds:
/// - a fold hasher: one fixed fast function with no key,
///   which the maps and sets made with New use;
/// - SipHash-1-3 under a HashKey, which the maps and sets made with
///   NewKeyed use.
export struct Hasher {
    v0: u64
    v1: u64
    v2: u64
    v3: u64
    tail: u64
    ntail: int
    total: int
    sip: bool
    half: bool
}

// Fold is the folded multiply: the 128-bit product's high word xor its low
// word (one mul on x86_64).
Fold(x: u64, y: u64): u64 {
    r := raw.Mul128(x, y)
    return r.hi ^ r.lo
}

// FoldEnd finishes a fold hash from the last pair: one fold, then the
// quality fold (without it an integer key fails avalanche).
FoldEnd(x: u64, y: u64): u64 {
    return Fold(Fold(x, y), FoldF3)
}

/// Feeds the bytes of b into the hash: first completing the pending tail,
/// then whole 8-byte little-endian words, then keeping the rest as the new
/// tail. O(n); allocates nothing, and reads 8 bytes per step.
export Hasher.Bytes(mut self, b: []u8) {
    n := b.Len()
    self.total = self.total +% n
    var i := 0
    if self.ntail > 0 {
        var k := 8 -% self.ntail
        if n < k {
            k = n
        }
        self.tail = self.tail | (ReadShort(b, 0, k) << ((8 *% self.ntail) & 63))
        self.ntail = self.ntail +% k
        if self.ntail < 8 {
            return
        }
        self.Word(self.tail)
        self.tail = 0
        self.ntail = 0
        i = k
    }
    if n -% i >= 8 {
        if self.sip {
            // The state in a Sip value (registers), stored back once.
            var st := Sip(v0: self.v0, v1: self.v1, v2: self.v2, v3: self.v3)
            for n -% i >= 8 {
                st = SipCompress(st, LoadWord(b, i))
                i = i +% 8
            }
            self.v0 = st.v0
            self.v1 = st.v1
            self.v2 = st.v2
            self.v3 = st.v3
        } else {
            // Pairs of words, one fold each, alternating between the two
            // lanes, both accumulators in locals: the waiting word's pair
            // first, then one pair if lane 1 is next, so that the loop
            // takes lane 0's pair and lane 1's, two multiplies in flight.
            var a0 := self.v0
            var a1 := self.v2
            var lane := self.v3
            if self.half {
                if lane == 0 {
                    a0 = Fold(self.v1 ^ a0, LoadWord(b, i) ^ FoldF1)
                } else {
                    a1 = Fold(self.v1 ^ a1, LoadWord(b, i) ^ FoldF1)
                }
                lane = lane ^ 1
                self.half = false
                i = i +% 8
            }
            if lane == 1 && n -% i >= 16 {
                a1 = Fold(LoadWord(b, i) ^ a1, LoadWord(b, i +% 8) ^ FoldF1)
                lane = 0
                i = i +% 16
            }
            for n -% i >= 32 {
                a0 = Fold(LoadWord(b, i) ^ a0, LoadWord(b, i +% 8) ^ FoldF1)
                a1 = Fold(LoadWord(b, i +% 16) ^ a1, LoadWord(b, i +% 24) ^ FoldF1)
                i = i +% 32
            }
            if n -% i >= 16 {
                a0 = Fold(LoadWord(b, i) ^ a0, LoadWord(b, i +% 8) ^ FoldF1)
                lane = 1
                i = i +% 16
            }
            if n -% i >= 8 {
                self.v1 = LoadWord(b, i)
                self.half = true
                i = i +% 8
            }
            self.v0 = a0
            self.v2 = a1
            self.v3 = lane
        }
    }
    if i < n {
        self.tail = ReadShort(b, i, n -% i)
        self.ntail = n -% i
    }
}

// ReadShort is b[i..i + k], 0 <= k < 8 bytes, as a little-endian word with
// the upper bytes zero, from a fixed number of loads (after
// foldhash's hash_bytes_short; FoldStr inlines the same read): 4 to 7
// bytes from two overlapping 4-byte reads shifted together (the bytes
// both cover are equal, so the or is exact); 1 to 3 bytes from the first,
// the middle and the last byte, which cover them all. Needs
// i + k <= b.Len(); every index is checked.
ReadShort(b: []u8, i: int, k: int): u64 {
    if k >= 4 {
        j := i +% k -% 4
        lo := u64(b[i]) | (u64(b[i +% 1]) << 8) | (u64(b[i +% 2]) << 16) | (u64(b[i +% 3]) << 24)
        hi := u64(b[j]) | (u64(b[j +% 1]) << 8) | (u64(b[j +% 2]) << 16) | (u64(b[j +% 3]) << 24)
        return lo | (hi << ((8 *% (k -% 4)) & 63))
    }
    if k <= 0 {
        return 0
    }
    m := k >> 1
    e := k -% 1
    return u64(b[i]) | (u64(b[i +% m]) << ((8 *% m) & 63)) | (u64(b[i +% e]) << ((8 *% e) & 63))
}

/// Feeds x into the hash as 8 little-endian bytes. O(1); allocates nothing.
export Hasher.U64(mut self, x: u64) {
    if self.sip || self.ntail != 0 {
        self.U64Rest(x)
        return
    }
    self.total = self.total +% 8
    if !self.half {
        self.v1 = x
        self.half = true
        return
    }
    if self.v3 == 0 {
        self.v0 = Fold(self.v1 ^ self.v0, x ^ FoldF1)
    } else {
        self.v2 = Fold(self.v1 ^ self.v2, x ^ FoldF1)
    }
    self.v3 = self.v3 ^ 1
    self.half = false
}

// U64Rest is U64 for SipHash or with a tail pending: a compression at a
// word boundary, else the 8 bytes through Bytes.
Hasher.U64Rest(mut self, x: u64) {
    if self.ntail == 0 {
        self.total += 8
        self.Word(x)
        return
    }
    var b: [8]u8 = [0; 8]
    for k in 0..8 {
        b[k] = Trunc[u8](x >> (8 * k))
    }
    self.Bytes(b[..])
}

// Word absorbs one whole word of the stream: a SipHash compression, or for
// the fold hash the first of a pair (kept) or the second (folded into the
// pair's lane).
Hasher.Word(mut self, m: u64) {
    if self.sip {
        self.Compress(m)
    } else if self.half {
        if self.v3 == 0 {
            self.v0 = Fold(self.v1 ^ self.v0, m ^ FoldF1)
        } else {
            self.v2 = Fold(self.v1 ^ self.v2, m ^ FoldF1)
        }
        self.v3 = self.v3 ^ 1
        self.half = false
    } else {
        self.v1 = m
        self.half = true
    }
}

// Compress is one SipHash-1-3 compression of the word m (c = 1).
Hasher.Compress(mut self, m: u64) {
    self.v3 = self.v3 ^ m
    self.Round()
    self.v0 = self.v0 ^ m
}

Hasher.Round(mut self) {
    self.v0 = self.v0 +% self.v1
    self.v1 = raw.RotL64(self.v1, 13)
    self.v1 = self.v1 ^ self.v0
    self.v0 = raw.RotL64(self.v0, 32)
    self.v2 = self.v2 +% self.v3
    self.v3 = raw.RotL64(self.v3, 16)
    self.v3 = self.v3 ^ self.v2
    self.v0 = self.v0 +% self.v3
    self.v3 = raw.RotL64(self.v3, 21)
    self.v3 = self.v3 ^ self.v0
    self.v2 = self.v2 +% self.v1
    self.v1 = raw.RotL64(self.v1, 17)
    self.v1 = self.v1 ^ self.v2
    self.v2 = raw.RotL64(self.v2, 32)
}

/// A new fold hasher, the one the maps and sets made with New use. Its
/// starting state is fixed, so hashes, and so a map's iteration order, are
/// the same in every run and at compile time. The function may change
/// between releases. A fixed function resists nothing: whoever picks n keys
/// can give them one probe sequence and make each map operation O(n), so
/// keys from untrusted input belong in a map made with NewKeyed. O(1).
export NewHasher(): Hasher {
    return Hasher(v0: FoldF0, v1: 0, v2: FoldF4, v3: 0, tail: 0, ntail: 0, total: 0, sip: false, half: false)
}

// NewHasherKeyed is the initial SipHash-1-3 state under the key (k0, k1),
// the hasher of NewKeyed's tables.
NewHasherKeyed(k0: u64, k1: u64): Hasher {
    return Hasher(v0: 0x736f6d6570736575 ^ k0, v1: 0x646f72616e646f6d ^ k1,
        v2: 0x6c7967656e657261 ^ k0, v3: 0x7465646279746573 ^ k1,
        tail: 0, ntail: 0, total: 0, sip: true, half: false)
}

/// Finishes h and returns the 64-bit hash. O(1).
export HashFinish(h: mut Hasher): u64 {
    if !h.sip {
        return FoldFinish(&*h)
    }
    b := ((Trunc[u64](h.total) & 0xFF) << 56) | h.tail
    h.Compress(b)
    h.v2 = h.v2 ^ 0xFF
    h.Round()
    h.Round()
    h.Round()
    return h.v0 ^ h.v1 ^ h.v2 ^ h.v3
}

// FoldFinish finishes a fold Hasher: the waiting word and the tail, or the
// tail alone, with the length and lane 1 (its accumulator xor its seed,
// so 0 for a stream of fewer than 32 bytes, which never reaches lane 1),
// through FoldEnd.
FoldFinish(h: &Hasher): u64 {
    n := Trunc[u64](h.total) ^ h.v2 ^ FoldF4
    if h.half {
        return FoldEnd(h.v1 ^ h.v0, h.tail ^ FoldF2 ^ n)
    }
    return FoldEnd(h.tail ^ h.v0, FoldF2 ^ n)
}

// FoldFinishK is FoldFinish instantiated in the caller's pack, so that
// HashOf's Hash impl path makes no call to finish.
FoldFinishK[K](h: &Hasher): u64 {
    n := Trunc[u64](h.total) ^ h.v2 ^ FoldF4
    if h.half {
        return FoldEnd(h.v1 ^ h.v0, h.tail ^ FoldF2 ^ n)
    }
    return FoldEnd(h.tail ^ h.v0, FoldF2 ^ n)
}

// FoldU64K is the fold hash of the 8 bytes of x, the value NewHasher, U64(x)
// and HashFinish give, in straight line: two multiplies. Generic over the
// key type only, so it is instantiated, and inlined, in the caller's pack,
// as a non-generic std function is not.
FoldU64K[K](x: u64): u64 {
    return FoldEnd(x ^ FoldF0, FoldF2 ^ 8)
}

// FoldStr is the fold hash a NewHasher gives after str's Hash impl: s's
// bytes then one 0xFF byte, so s.Len() + 1 bytes in all. Whole words
// are read with one align 1 load each (LoadU64), 32 bytes per step, one
// pair into each lane; the rest, the 0xFF included, is at most 32
// bytes: a pair into lane 0 when 16 or more bytes of s are left, then a
// waiting word when 8 or more, then the tail. A word the 0xFF completes is
// absorbed as Hasher.Word would. No loop runs per byte. The index
// arithmetic wraps (-%, +%) because 0 <= i <= n holds throughout, so it
// never does, and each shift amount is masked to 0..63, which it already
// is: neither needs a check, and LoadU64 keeps its bounds check. Generic
// over the key type only, so it is instantiated, and can be inlined, in
// the caller's pack (FoldU64K).
FoldStr[K](s: str): u64 {
    n := s.Len()
    var a0 := FoldF0
    var a1 := FoldF4
    var i := 0
    for n -% i >= 32 {
        a0 = Fold(LoadU64(s, i) ^ a0, LoadU64(s, i +% 8) ^ FoldF1)
        a1 = Fold(LoadU64(s, i +% 16) ^ a1, LoadU64(s, i +% 24) ^ FoldF1)
        i = i +% 32
    }
    // lane is the lane of the next pair.
    var lane := 0
    if n -% i >= 16 {
        a0 = Fold(LoadU64(s, i) ^ a0, LoadU64(s, i +% 8) ^ FoldF1)
        lane = 1
        i = i +% 16
    }
    var w: u64 = 0
    var half := false
    if n -% i >= 8 {
        w = LoadU64(s, i)
        half = true
        i = i +% 8
    }
    // The r = n - i < 8 bytes left: from one load of s's last 8 bytes when
    // s has 8, shifted down (the bytes before i are already absorbed), else
    // they are all of s, read with a fixed number of loads and no loop
    // (after foldhash's hash_bytes_short): 4 to 7 bytes from two
    // overlapping 4-byte reads, b[0..4] and b[n - 4..n] shifted up by n - 4
    // bytes (the bytes both cover are equal, so the or is exact, and LLVM
    // combines each four byte loads into one); 1 to 3 bytes from the
    // first, the middle and the last byte, which cover them all (n = 1
    // reads one byte three times, n = 2 the second twice). Every index is
    // below n, so no load reads past s.
    r := n -% i
    var t: u64 = 0
    if n >= 8 {
        if r > 0 {
            t = LoadU64(s, n -% 8) >> ((64 -% 8 *% r) & 63)
        }
    } else if n >= 4 {
        b := s.AsBytes()
        j := n -% 4
        lo := u64(b[0]) | (u64(b[1]) << 8) | (u64(b[2]) << 16) | (u64(b[3]) << 24)
        hi := u64(b[j]) | (u64(b[j +% 1]) << 8) | (u64(b[j +% 2]) << 16) | (u64(b[j +% 3]) << 24)
        t = lo | (hi << ((8 *% j) & 63))
    } else if n > 0 {
        b := s.AsBytes()
        m := n >> 1
        e := n -% 1
        t = u64(b[0]) | (u64(b[m]) << ((8 *% m) & 63)) | (u64(b[e]) << ((8 *% e) & 63))
    }
    t = t | (Trunc[u64](0xFF) << ((8 *% r) & 63))
    if r == 7 {
        if !half {
            w = t
            half = true
        } else if lane == 0 {
            a0 = Fold(w ^ a0, t ^ FoldF1)
            half = false
        } else {
            a1 = Fold(w ^ a1, t ^ FoldF1)
            half = false
        }
        t = 0
    }
    total := Trunc[u64](n +% 1) ^ a1 ^ FoldF4
    if half {
        return FoldEnd(w ^ a0, t ^ FoldF2 ^ total)
    }
    return FoldEnd(t ^ a0, FoldF2 ^ total)
}

// Sip is SipHash's state as a plain value, so the one-shot hashes below
// keep it in locals (registers, once inlined) instead of a Hasher in memory.
struct Sip {
    v0: u64
    v1: u64
    v2: u64
    v3: u64
}

// SipRound is Hasher.Round on a value.
SipRound(s: Sip): Sip {
    var v0 := s.v0
    var v1 := s.v1
    var v2 := s.v2
    var v3 := s.v3
    v0 = v0 +% v1
    v1 = raw.RotL64(v1, 13)
    v1 = v1 ^ v0
    v0 = raw.RotL64(v0, 32)
    v2 = v2 +% v3
    v3 = raw.RotL64(v3, 16)
    v3 = v3 ^ v2
    v0 = v0 +% v3
    v3 = raw.RotL64(v3, 21)
    v3 = v3 ^ v0
    v2 = v2 +% v1
    v1 = raw.RotL64(v1, 17)
    v1 = v1 ^ v2
    v2 = raw.RotL64(v2, 32)
    return Sip(v0: v0, v1: v1, v2: v2, v3: v3)
}

// SipCompress is Hasher.Compress on a value (c = 1).
SipCompress(s: Sip, m: u64): Sip {
    r := SipRound(Sip(v0: s.v0, v1: s.v1, v2: s.v2, v3: s.v3 ^ m))
    return Sip(v0: r.v0 ^ m, v1: r.v1, v2: r.v2, v3: r.v3)
}

// SipEnd is HashFinish on a value whose pending tail t holds total's last
// total % 8 bytes (d = 3).
SipEnd(s: Sip, t: u64, total: int): u64 {
    c := SipCompress(s, ((Trunc[u64](total) & 0xFF) << 56) | t)
    var r := Sip(v0: c.v0, v1: c.v1, v2: c.v2 ^ 0xFF, v3: c.v3)
    r = SipRound(r)
    r = SipRound(r)
    r = SipRound(r)
    return r.v0 ^ r.v1 ^ r.v2 ^ r.v3
}

/// The SipHash-1-3 hash of s as a str key (its bytes, then one 0xFF byte)
/// under SipHash's reference key, the bytes 0x00 to 0x0f. O(n).
export SipStr(s: str): u64 {
    return SipStrKeyed(FixedK0, FixedK1, s)
}

// SipStrKeyed is the hash NewHasherKeyed(k0, k1) gives after str's Hash
// impl: s's bytes then one 0xFF byte, so s.Len() + 1 bytes in all.
// Whole words are read with one align 1 load each (LoadU64); the tail and
// the 0xFF fill the last word.
SipStrKeyed(k0: u64, k1: u64, s: str): u64 {
    n := s.Len()
    var st := Sip(v0: 0x736f6d6570736575 ^ k0, v1: 0x646f72616e646f6d ^ k1, v2: 0x6c7967656e657261 ^ k0, v3: 0x7465646279746573 ^ k1)
    var i := 0
    for n - i >= 8 {
        st = SipCompress(st, LoadU64(s, i))
        i += 8
    }
    b := s.AsBytes()
    var t: u64 = 0
    var k := 0
    for i < n {
        t = t | (u64(b[i]) << (8 * k))
        k += 1
        i += 1
    }
    t = t | (Trunc[u64](0xFF) << (8 * k))
    k += 1
    if k == 8 {
        st = SipCompress(st, t)
        t = 0
    }
    return SipEnd(st, t, n + 1)
}
