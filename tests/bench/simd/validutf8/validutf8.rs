// validutf8, Rust, SIMD: the kernel of README.md with std::arch::x86_64
// intrinsics (portable std::simd is nightly-only, B16), the Keiser-Lemire
// lookup validator (Keiser and Lemire, 2021; the algorithm of simdjson and
// simdutf): three 16-entry nibble lookups by pshufb, the third and fourth
// byte check by saturating subtraction, 64-byte chunks with an all-ASCII
// skip. At the build's fixed CPU target and no run-time dispatch: SSE
// 16-byte vectors at x86-64-v2, AVX2 32-byte vectors at x86-64-v3
// (cfg(target_feature = "avx2"), set by -C target-cpu). Each load asserts
// its bound (`assert!(i + W <= p.len())`) before the raw-pointer read the
// intrinsic takes, so the rust and rust-ovf columns are bounds-checked like
// zig-safe's. The buffer starts on a 64-byte boundary, as in every
// column (BS-4). std's own
// std::str::from_utf8 is scalar with a word-at-a-time ASCII skip; this row
// is about each language's SIMD layer. Usage: validutf8 <size> <reps>
// <seed>.
#![allow(unused_unsafe)]
use std::arch::x86_64::*;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

// The 16 encodings of README.md; entry k is 1 + k / 4 bytes long.
const ENC: [[u8; 4]; 16] = [
    [0x61, 0, 0, 0], [0x20, 0, 0, 0], [0x30, 0, 0, 0], [0x7E, 0, 0, 0],
    [0xC2, 0x80, 0, 0], [0xDF, 0xBF, 0, 0], [0xC3, 0xA9, 0, 0], [0xD0, 0x96, 0, 0],
    [0xE0, 0xA0, 0x80, 0], [0xED, 0x9F, 0xBF, 0], [0xEE, 0x80, 0x80, 0], [0xE4, 0xB8, 0x96, 0],
    [0xF0, 0x90, 0x80, 0x80], [0xF4, 0x8F, 0xBF, 0xBF], [0xF0, 0x9F, 0x98, 0x80], [0xF3, 0xA0, 0x80, 0x80],
];

const TOO_SHORT: u8 = 1 << 0;
const TOO_LONG: u8 = 1 << 1;
const OVERLONG_3: u8 = 1 << 2;
const TOO_LARGE: u8 = 1 << 3;
const SURROGATE: u8 = 1 << 4;
const OVERLONG_2: u8 = 1 << 5;
const TOO_LARGE_1000: u8 = 1 << 6;
const OVERLONG_4: u8 = 1 << 6;
const TWO_CONTS: u8 = 1 << 7;
const CARRY: u8 = TOO_SHORT | TOO_LONG | TWO_CONTS;

// The tables by the high nibble of the previous byte, its low nibble, and
// the high nibble of the byte.
const T1H: [u8; 16] = [
    TOO_LONG, TOO_LONG, TOO_LONG, TOO_LONG, TOO_LONG, TOO_LONG, TOO_LONG, TOO_LONG,
    TWO_CONTS, TWO_CONTS, TWO_CONTS, TWO_CONTS,
    TOO_SHORT | OVERLONG_2,
    TOO_SHORT,
    TOO_SHORT | OVERLONG_3 | SURROGATE,
    TOO_SHORT | TOO_LARGE | TOO_LARGE_1000 | OVERLONG_4,
];
const T1L: [u8; 16] = [
    CARRY | OVERLONG_3 | OVERLONG_2 | OVERLONG_4,
    CARRY | OVERLONG_2,
    CARRY,
    CARRY,
    CARRY | TOO_LARGE,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000 | SURROGATE,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
];
const T2H: [u8; 16] = [
    TOO_SHORT, TOO_SHORT, TOO_SHORT, TOO_SHORT, TOO_SHORT, TOO_SHORT, TOO_SHORT, TOO_SHORT,
    TOO_LONG | OVERLONG_2 | TWO_CONTS | OVERLONG_3 | TOO_LARGE_1000 | OVERLONG_4,
    TOO_LONG | OVERLONG_2 | TWO_CONTS | OVERLONG_3 | TOO_LARGE,
    TOO_LONG | OVERLONG_2 | TWO_CONTS | SURROGATE | TOO_LARGE,
    TOO_LONG | OVERLONG_2 | TWO_CONTS | SURROGATE | TOO_LARGE,
    TOO_SHORT, TOO_SHORT, TOO_SHORT, TOO_SHORT,
];

#[cfg(target_feature = "avx2")]
mod v {
    use std::arch::x86_64::*;
    pub type V = __m256i;
    pub const W: usize = 32;
    pub fn splat(b: u8) -> V { unsafe { _mm256_set1_epi8(b as i8) } }
    pub fn load(p: &[u8], i: usize) -> V {
        assert!(i + W <= p.len());
        unsafe { _mm256_loadu_si256(p.as_ptr().add(i) as *const V) }
    }
    pub fn table(t: &[u8; 16]) -> V { unsafe { _mm256_broadcastsi128_si256(_mm_loadu_si128(t.as_ptr() as *const __m128i)) } }
    pub fn lookup(t: V, idx: V) -> V { unsafe { _mm256_shuffle_epi8(t, idx) } }
    pub fn shr4(x: V) -> V { unsafe { _mm256_and_si256(_mm256_srli_epi16::<4>(x), splat(0x0F)) } }
    pub fn and(a: V, b: V) -> V { unsafe { _mm256_and_si256(a, b) } }
    pub fn or(a: V, b: V) -> V { unsafe { _mm256_or_si256(a, b) } }
    pub fn xor(a: V, b: V) -> V { unsafe { _mm256_xor_si256(a, b) } }
    pub fn subs(a: V, b: V) -> V { unsafe { _mm256_subs_epu8(a, b) } }
    pub fn any_high(x: V) -> bool { unsafe { _mm256_movemask_epi8(x) != 0 } }
    pub fn nonzero(x: V) -> bool { unsafe { _mm256_testz_si256(x, x) == 0 } }
    // The input shifted by N bytes, the gap filled from the previous vector.
    pub fn prev1(cur: V, prev: V) -> V { unsafe { _mm256_alignr_epi8::<15>(cur, _mm256_permute2x128_si256::<0x21>(prev, cur)) } }
    pub fn prev2(cur: V, prev: V) -> V { unsafe { _mm256_alignr_epi8::<14>(cur, _mm256_permute2x128_si256::<0x21>(prev, cur)) } }
    pub fn prev3(cur: V, prev: V) -> V { unsafe { _mm256_alignr_epi8::<13>(cur, _mm256_permute2x128_si256::<0x21>(prev, cur)) } }
}

#[cfg(not(target_feature = "avx2"))]
mod v {
    use std::arch::x86_64::*;
    pub type V = __m128i;
    pub const W: usize = 16;
    pub fn splat(b: u8) -> V { unsafe { _mm_set1_epi8(b as i8) } }
    pub fn load(p: &[u8], i: usize) -> V {
        assert!(i + W <= p.len());
        unsafe { _mm_loadu_si128(p.as_ptr().add(i) as *const V) }
    }
    pub fn table(t: &[u8; 16]) -> V { unsafe { _mm_loadu_si128(t.as_ptr() as *const V) } }
    pub fn lookup(t: V, idx: V) -> V { unsafe { _mm_shuffle_epi8(t, idx) } }
    pub fn shr4(x: V) -> V { unsafe { _mm_and_si128(_mm_srli_epi16::<4>(x), splat(0x0F)) } }
    pub fn and(a: V, b: V) -> V { unsafe { _mm_and_si128(a, b) } }
    pub fn or(a: V, b: V) -> V { unsafe { _mm_or_si128(a, b) } }
    pub fn xor(a: V, b: V) -> V { unsafe { _mm_xor_si128(a, b) } }
    pub fn subs(a: V, b: V) -> V { unsafe { _mm_subs_epu8(a, b) } }
    pub fn any_high(x: V) -> bool { unsafe { _mm_movemask_epi8(x) != 0 } }
    pub fn nonzero(x: V) -> bool { unsafe { _mm_testz_si128(x, x) == 0 } }
    pub fn prev1(cur: V, prev: V) -> V { unsafe { _mm_alignr_epi8::<15>(cur, prev) } }
    pub fn prev2(cur: V, prev: V) -> V { unsafe { _mm_alignr_epi8::<14>(cur, prev) } }
    pub fn prev3(cur: V, prev: V) -> V { unsafe { _mm_alignr_epi8::<13>(cur, prev) } }
}

use v::*;

const NV: usize = 64 / W;

struct State {
    t1h: V,
    t1l: V,
    t2h: V,
    maxv: V,
    err: V,
    prev: V,
    prev_incomplete: V,
}

// check is the error vector of one input vector against the one before.
fn check(s: &State, input: V, prev: V) -> V {
    let p1 = prev1(input, prev);
    let sc = and(and(lookup(s.t1h, shr4(p1)), lookup(s.t1l, and(p1, splat(0x0F)))), lookup(s.t2h, shr4(input)));
    let p2 = prev2(input, prev);
    let p3 = prev3(input, prev);
    let must23 = or(subs(p2, splat(0xE0 - 0x80)), subs(p3, splat(0xF0 - 0x80)));
    xor(and(must23, splat(0x80)), sc)
}

fn chunk(s: &mut State, p: &[u8], i: usize) {
    let mut v = [splat(0); NV];
    let mut all = splat(0);
    for j in 0..NV {
        v[j] = load(p, i + j * W);
        all = or(all, v[j]);
    }
    if !any_high(all) {
        s.err = or(s.err, s.prev_incomplete);
        return;
    }
    let mut prev = s.prev;
    for j in 0..NV {
        s.err = or(s.err, check(s, v[j], prev));
        prev = v[j];
    }
    s.prev_incomplete = subs(v[NV - 1], s.maxv);
    s.prev = v[NV - 1];
}

fn valid_utf8(b: &[u8]) -> bool {
    let mut maxb = [0xFFu8; W];
    maxb[W - 3] = 0xF0 - 1;
    maxb[W - 2] = 0xE0 - 1;
    maxb[W - 1] = 0xC0 - 1;
    let mut s = State {
        t1h: table(&T1H),
        t1l: table(&T1L),
        t2h: table(&T2H),
        maxv: load(&maxb, 0),
        err: splat(0),
        prev: splat(0),
        prev_incomplete: splat(0),
    };
    let n = b.len();
    let mut i = 0;
    while i + 64 <= n {
        chunk(&mut s, b, i);
        i += 64;
    }
    if i < n {
        let mut tail = [0u8; 64];
        tail[..n - i].copy_from_slice(&b[i..]);
        chunk(&mut s, &tail, 0);
    }
    s.err = or(s.err, s.prev_incomplete);
    !nonzero(s.err)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: validutf8 <size> <reps> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let reps: u64 = a[2].parse().unwrap();
    let mut st: u64 = a[3].parse().unwrap();
    let blen = if n < 65536 { n } else { 65536 };
    let mut blk = vec![0u8; blen];
    let mut p = 0;
    loop {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let k = (mix(st) & 15) as usize;
        let l = 1 + k / 4;
        if p + l > blen {
            break;
        }
        blk[p..p + l].copy_from_slice(&ENC[k][..l]);
        p += l;
    }
    for c in &mut blk[p..] {
        *c = 0x61;
    }
    // BS-4: the buffer starts on a 64-byte boundary in every column.
    let mut bv = vec![0u8; n + 64];
    let off = bv.as_ptr().align_offset(64);
    let buf = &mut bv[off..off + n];
    if buf.as_ptr() as usize % 64 != 0 {
        eprintln!("validutf8: buffer not 64-byte aligned");
        std::process::exit(3);
    }
    let mut i = 0;
    while i + blen <= n {
        buf[i..i + blen].copy_from_slice(&blk);
        i += blen;
    }
    for c in &mut buf[i..] {
        *c = 0x61;
    }
    let span = if n < 64 { n } else { 64 } as u64;
    let mut total: u64 = 0;
    for _ in 0..reps {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let z = mix(st);
        let pos = n - 1 - ((z >> 1) % span) as usize;
        let old = buf[pos];
        if z & 1 == 1 {
            buf[pos] = (z >> 56) as u8;
        }
        total = total.wrapping_mul(31).wrapping_add(valid_utf8(buf) as u64);
        buf[pos] = old;
    }
    println!("{}", total);
}
