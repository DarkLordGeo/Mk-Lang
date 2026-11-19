// Why: passwords need a hash that is slow and memory-hard, so a stolen
// hash cannot be guessed cheaply on GPUs; Argon2id is std's one
// password-hashing function.
//
// What: Argon2Cost, Argon2id and Argon2idVerify, over A2Hash, A2H0, A2Pass,
// A2Segment, A2Fill and HPrime.
//
// How: RFC 9106 (version 0x13), over the private BLAKE2b of blake2b.mk. The
// memory is m' = 4p *
// floor(m / 4p) blocks of 1024 bytes, from an arena of its own on the heap
// the caller names; it is wiped with Wipe and freed before the call
// returns, on every path past the allocation. So are the copies from
// which a guess could be tested without the memory: H_0 and its buffer,
// every BLAKE2b state (blake2b.mk's Sum and compression wipe theirs), H''s
// chain and the XOR of the last blocks. The block-sized working copies of
// G (A2Fill, A2Perm) are not wiped: each call overwrites the last, so on
// return they hold the last block of the last pass, which only the whole
// memory reaches.
//
// Lanes are computed one after another on the calling task, slice by
// slice as section 3.4 orders them, so p changes the tag and how memory is
// laid out but never runs threads: the work is m' * t block compressions
// whatever p is, and the call never waits, so it holds the calling task's
// carrier from start to end.
//
// Not constant time, by design. The first half of the first pass (slices 0
// and 1) chooses its reference blocks from the public parameters alone
// (Argon2i's addressing), and the rest from the memory's contents (Argon2d's),
// which derive from the password: that half's memory accesses, and the
// remainders that turn a word into a block index, depend on the secret, as
// RFC 9106 section 1 says of every Argon2id. Only the tag comparison of
// Argon2idVerify is constant time (Equal).

/// Argon2Cost is the cost of an Argon2id hash (RFC 9106): memory in
/// kibibytes (from 8 * lanes to 2^32 - 1), passes over it (1 to 2^32 - 1)
/// and lanes (1 to 2^24 - 1). Each one changes the tag, so store the cost
/// beside the hash. RFC 9106 recommends passes 1, lanes 4 and memory
/// 2097152 (2 GiB), or, where memory is short, passes 3, lanes 4 and memory
/// 65536 (64 MiB).
export struct Argon2Cost {
    /// The memory in kibibytes, from 8 * lanes to 2^32 - 1.
    export memory: int
    /// The passes over the memory, 1 to 2^32 - 1.
    export passes: int
    /// The lanes, 1 to 2^24 - 1.
    export lanes: int
}

/// Fills out with the Argon2id tag of password and salt under the cost c,
/// keyed by secret (RFC 9106's K) and bound to ad (its X); an empty secret
/// or ad is absent, as the RFC has it. The tag length is out.Len(). A salt
/// of 16 bytes from Random is the RFC's recommendation; any length the RFC
/// allows is taken. Fails with BadCost when c is outside RFC 9106's limits,
/// with BadLength when out is not 4 to 2^32 - 1 bytes or an input is over
/// 2^32 - 1 bytes, and with OutOfMemory when the heap refuses the memory;
/// either way out is unchanged.
///
/// Not constant time: after the first half of its first pass, it reads
/// memory at positions derived from the password, as RFC 9106 designs
/// Argon2id. The memory, the first hash H_0 and every BLAKE2b state it
/// makes are wiped before it returns.
/// The lanes run one after another on the calling task, so lanes changes
/// the tag but not the time. The call never waits, so it holds the thread
/// running the calling task for the whole hash. O(m * t) block compressions
/// for memory m and passes t; allocates c.memory KiB from h, rounded down
/// to a multiple of 4 * c.lanes, and frees them before it returns.
export Argon2id(h: &Heap, c: Argon2Cost, password: []u8, salt: []u8, secret: []u8, ad: []u8, out: mut []u8): BadLength|BadCost|OutOfMemory!void {
    try A2Check(c, password, salt, secret, ad, out.Len())
    var ar := try Arena.New(h)
    defer ar.Free()
    mem := try ar.Slice[u8](1024 * A2Blocks(c))
    A2Hash(mut mem[..], c, password, salt, secret, ad, mut out[..])
    Wipe(mut mem[..])
}

/// Reports whether tag is the Argon2id tag of password and salt under c,
/// secret and ad, the tag length being tag.Len(). It computes the tag as
/// Argon2id does and compares the two with Equal, in time that depends only
/// on the length. Fails as Argon2id does, BadLength for a tag outside 4 to
/// 2^32 - 1 bytes included; a wrong password is false, never an error. O(m
/// * t) block compressions; allocates as Argon2id does, plus tag.Len()
/// bytes, all freed before it returns.
export Argon2idVerify(h: &Heap, c: Argon2Cost, password: []u8, salt: []u8, secret: []u8, ad: []u8, tag: []u8): BadLength|BadCost|OutOfMemory!bool {
    try A2Check(c, password, salt, secret, ad, tag.Len())
    var ar := try Arena.New(h)
    defer ar.Free()
    mem := try ar.Slice[u8](1024 * A2Blocks(c))
    got := try ar.Slice[u8](tag.Len())
    A2Hash(mut mem[..], c, password, salt, secret, ad, mut got[..])
    ok := Equal(got[..], tag)
    Wipe(mut mem[..])
    Wipe(mut got[..])
    return ok
}

// A2Max is RFC 9106's bound on every length and on m and t, 2^32 - 1.
const A2Max: int = 0xFFFFFFFF

// A2Check refuses a cost or a length outside RFC 9106 section 3.1's
// limits; tlen is the tag's length.
A2Check(c: Argon2Cost, password: []u8, salt: []u8, secret: []u8, ad: []u8, tlen: int): BadLength|BadCost!void {
    if c.lanes < 1 || c.lanes > 0xFFFFFF || c.passes < 1 || c.passes > A2Max || c.memory < 8 * c.lanes || c.memory > A2Max {
        fail BadCost
    }
    if tlen < 4 || tlen > A2Max || password.Len() > A2Max || salt.Len() > A2Max || secret.Len() > A2Max || ad.Len() > A2Max {
        fail BadLength
    }
}

// A2Blocks is m', the number of 1024-byte blocks c's memory makes.
A2Blocks(c: Argon2Cost): int {
    return 4 * c.lanes * (c.memory / (4 * c.lanes))
}

// A2Hash computes the tag into out over mem, A2Blocks(c) blocks, after
// A2Check accepted every argument: RFC 9106 section 3.2's steps 1 to 8.
A2Hash(mem: mut []u8, c: Argon2Cost, password: []u8, salt: []u8, secret: []u8, ad: []u8, out: mut []u8) {
    var h0: [72]u8 = [0; 72]
    A2H0(mut h0, c, password, salt, secret, ad, out.Len())
    A2Start(mut mem[..], c, mut h0)
    Wipe(mut h0[..])
    for r in 0..c.passes {
        A2Pass(mut mem[..], c, r)
    }
    A2Tag(mem[..], c, mut out[..])
}

// A2H0 writes step 1's H_0 to h0[0..64], with the m the caller gave, not
// m', and tlen the tag's length.
A2H0(h0: mut [72]u8, c: Argon2Cost, password: []u8, salt: []u8, secret: []u8, ad: []u8, tlen: int) {
    var s := B2New(64)
    A2Word(mut s, c.lanes)
    A2Word(mut s, tlen)
    A2Word(mut s, c.memory)
    A2Word(mut s, c.passes)
    A2Word(mut s, 0x13)
    A2Word(mut s, 2)
    A2Word(mut s, password.Len())
    s.Bytes(password)
    A2Word(mut s, salt.Len())
    s.Bytes(salt)
    A2Word(mut s, secret.Len())
    s.Bytes(secret)
    A2Word(mut s, ad.Len())
    s.Bytes(ad)
    s.Sum(mut h0[0..64])
}

// A2Start is steps 3 and 4: the first two blocks of each lane, H'^1024 of
// H_0 || LE32(column) || LE32(lane), written into h0[64..72] in turn.
A2Start(mem: mut []u8, c: Argon2Cost, h0: mut [72]u8) {
    q := A2Blocks(c) / c.lanes
    var sc := A2Chain(v: [0; 64], w: [0; 64])
    for l in 0..c.lanes {
        PutLe32(mut h0[..], 68, Trunc[u32](l))
        for j in 0..2 {
            PutLe32(mut h0[..], 64, Trunc[u32](j))
            o := 1024 * (l * q + j)
            HPrime(mut mem[o..o + 1024], h0[..], mut sc)
        }
    }
}

// A2Pass is pass r of steps 5 and 6, slice by slice, each slice lane by
// lane.
A2Pass(mem: mut []u8, c: Argon2Cost, r: int) {
    q := A2Blocks(c) / c.lanes
    for sl in 0..4 {
        for l in 0..c.lanes {
            A2Segment(mut mem[..], c, q, r, sl, l)
        }
    }
}

// A2Tag is steps 7 and 8: H'^T of the XOR of each lane's last block, T
// being out.Len().
A2Tag(mem: []u8, c: Argon2Cost, out: mut []u8) {
    q := A2Blocks(c) / c.lanes
    var last: [1024]u8 = [0; 1024]
    for l in 0..c.lanes {
        o := 1024 * (l * q + q - 1)
        for i in 0..1024 {
            last[i] = last[i] ^ mem[o + i]
        }
    }
    var sc := A2Chain(v: [0; 64], w: [0; 64])
    HPrime(mut out[..], last[..], mut sc)
    Wipe(mut last[..])
}

// A2Word absorbs x as LE32(x), 0 <= x <= 2^32 - 1.
A2Word(s: mut B2b, x: int) {
    var b: [4]u8 = [0; 4]
    PutLe32(mut b[..], 0, Trunc[u32](x))
    s.Bytes(b[..])
}

// HPrime is RFC 9106 section 3.3's H' with T = out.Len() >= 4: H^T(LE32(T)
// || input) up to 64 bytes, else the first 32 bytes of each of the chain
// V_1 = H^64(LE32(T) || input), V_(i + 1) = H^64(V_i), r = ceil(T / 32) -
// 2 of them, then H^(T - 32r)(V_r) whole. The chain's copies are sc's,
// the caller's, and are wiped before it returns.
HPrime(out: mut []u8, input: []u8, sc: mut A2Chain) {
    t := out.Len()
    var lt: [4]u8 = [0; 4]
    PutLe32(mut lt[..], 0, Trunc[u32](t))
    if t <= 64 {
        var s := B2New(t)
        s.Bytes(lt[..])
        s.Bytes(input)
        s.Sum(mut out[..])
        return
    }
    r := (t + 31) / 32 - 2
    var s := B2New(64)
    s.Bytes(lt[..])
    s.Bytes(input)
    s.Sum(mut sc.v[..])
    (mut out[0..32]).CopyFrom(sc.v[0..32])
    for i in 1..r {
        B2Hash(mut sc.w[..], sc.v[..])
        sc.v = sc.w
        Wipe(mut sc.w[..])
        (mut out[32 * i..32 * i + 32]).CopyFrom(sc.v[0..32])
    }
    B2Hash(mut out[32 * r..t], sc.v[..])
    Wipe(mut sc.v[..])
}

// A2Chain is H''s chain, V_i in v and V_(i + 1) in w, held by HPrime's
// caller so that a test sees HPrime wipe both.
struct A2Chain {
    v: [64]u8
    w: [64]u8
}

// A2Segment computes lane l's segment of slice sl in pass r (RFC 9106
// section 3.4, as the reference implementation's fill_segment orders it):
// q blocks a lane, q / 4 a segment. Argon2i's addressing for pass 0's
// slices 0 and 1, Argon2d's after; pass 0 writes each block, later passes
// XOR into it.
A2Segment(mem: mut []u8, c: Argon2Cost, q: int, r: int, sl: int, l: int) {
    seg := q / 4
    indep := r == 0 && sl < 2
    var input: [128]u64 = [0; 128]
    var addr: [128]u64 = [0; 128]
    if indep {
        input[0] = Trunc[u64](r)
        input[1] = Trunc[u64](l)
        input[2] = Trunc[u64](sl)
        input[3] = Trunc[u64](q * c.lanes)
        input[4] = Trunc[u64](c.passes)
        input[5] = 2
    }
    var start := 0
    if r == 0 && sl == 0 {
        start = 2
        if indep {
            A2NextAddr(mut input, mut addr)
        }
    }
    for i in start..seg {
        j := sl * seg + i
        cur := l * q + j
        prev := if j == 0 { cur + q - 1 } else { cur - 1 }
        var rnd: u64 = 0
        if indep {
            if i % 128 == 0 {
                A2NextAddr(mut input, mut addr)
            }
            rnd = addr[i % 128]
        } else {
            rnd = Le64(mem[..], 1024 * prev)
        }
        var rl := Trunc[int](rnd >> 32) % c.lanes
        if r == 0 && sl == 0 {
            rl = l
        }
        z := A2Index(q, r, sl, i, rnd & 0xFFFFFFFF, rl == l)
        A2Fill(mut mem[..], prev, rl * q + z, cur, r > 0)
    }
}

// A2Index is section 3.4.2's z: the column, in a lane of q blocks, that
// block i of segment sl in pass r takes its reference from, j1 being J_1
// and same whether the reference lane is the current one.
A2Index(q: int, r: int, sl: int, i: int, j1: u64, same: bool): int {
    seg := q / 4
    // A different lane's segment in progress is out of reach, and so is the
    // block before this one when it ends a finished segment there.
    first := if i == 0 { 1 } else { 0 }
    var area := 0
    if r == 0 {
        if sl == 0 {
            area = i - 1
        } else if same {
            area = sl * seg + i - 1
        } else {
            area = sl * seg - first
        }
    } else if same {
        area = q - seg + i - 1
    } else {
        area = q - seg - first
    }
    a := Trunc[u64](area)
    x := (j1 * j1) >> 32
    y := (a * x) >> 32
    rel := Trunc[int](a - 1 - y)
    start := if r != 0 && sl != 3 { (sl + 1) * seg } else { 0 }
    return (start + rel) % q
}

// A2NextAddr makes the next block of Argon2i addresses: input's counter
// (word 6) goes up by one, and addr = G(0, G(0, input)).
A2NextAddr(input: mut [128]u64, addr: mut [128]u64) {
    input[6] = input[6] +% 1
    var t := *input
    A2Perm(mut t)
    for i in 0..128 {
        t[i] = t[i] ^ input[i]
    }
    var u := t
    A2Perm(mut u)
    for i in 0..128 {
        addr[i] = u[i] ^ t[i]
    }
}

// A2Fill is section 3.5's G into block cur: G(B[prev], B[ref]), XORed into
// the block's old value when xor is set (passes after the first).
A2Fill(mem: mut []u8, prev: int, ref: int, cur: int, xor: bool) {
    var rr: [128]u64 = [0; 128]
    var t: [128]u64 = [0; 128]
    po := 1024 * prev
    ro := 1024 * ref
    co := 1024 * cur
    for i in 0..128 {
        w := Le64(mem[..], po + 8 * i) ^ Le64(mem[..], ro + 8 * i)
        rr[i] = w
        t[i] = w
    }
    if xor {
        for i in 0..128 {
            t[i] = t[i] ^ Le64(mem[..], co + 8 * i)
        }
    }
    A2Perm(mut rr)
    for i in 0..128 {
        PutLe64(mut mem[..], co + 8 * i, t[i] ^ rr[i])
    }
}

// A2Perm is G's core on a block of 128 words: P on each row of eight
// 16-byte registers, then on each column (section 3.5, Figure 15).
A2Perm(b: mut [128]u64) {
    for i in 0..8 {
        A2P(b, 16 * i, 2)
    }
    for i in 0..8 {
        A2P(b, 2 * i, 16)
    }
}

// A2P is section 3.6's P on the sixteen words b[base + (k & 1) + step * (k
// >> 1)], k = 0 to 15: a row of registers for step 2, a column for 16.
A2P(b: mut [128]u64, base: int, step: int) {
    var v: [16]u64 = [0; 16]
    for k in 0..16 {
        v[k] = b[base + (k & 1) + step * (k >> 1)]
    }
    A2Gb(mut v, 0, 4, 8, 12)
    A2Gb(mut v, 1, 5, 9, 13)
    A2Gb(mut v, 2, 6, 10, 14)
    A2Gb(mut v, 3, 7, 11, 15)
    A2Gb(mut v, 0, 5, 10, 15)
    A2Gb(mut v, 1, 6, 11, 12)
    A2Gb(mut v, 2, 7, 8, 13)
    A2Gb(mut v, 3, 4, 9, 14)
    for k in 0..16 {
        b[base + (k & 1) + step * (k >> 1)] = v[k]
    }
}

// A2Gb is section 3.6's GB: BLAKE2b's G with each addition a + b replaced
// by a + b + 2 * trunc(a) * trunc(b), mod 2^64, and no message words.
A2Gb(v: mut [16]u64, a: int, b: int, c: int, d: int) {
    v[a] = A2Mix(v[a], v[b])
    v[d] = B2Rot(v[d] ^ v[a], 32)
    v[c] = A2Mix(v[c], v[d])
    v[b] = B2Rot(v[b] ^ v[c], 24)
    v[a] = A2Mix(v[a], v[b])
    v[d] = B2Rot(v[d] ^ v[a], 16)
    v[c] = A2Mix(v[c], v[d])
    v[b] = B2Rot(v[b] ^ v[c], 63)
}

// A2Mix is x + y + 2 * trunc(x) * trunc(y) mod 2^64, trunc taking the low
// 32 bits.
A2Mix(x: u64, y: u64): u64 {
    return x +% y +% ((x & 0xFFFFFFFF) * (y & 0xFFFFFFFF)) *% 2
}
