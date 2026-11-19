// indexany, Rust, SIMD: the kernel of README.md with std::arch::x86_64
// intrinsics (portable std::simd is nightly-only, B16), at the build's fixed
// CPU target and no run-time dispatch: 16-byte vectors at x86-64-v2, AVX2
// 32-byte vectors at x86-64-v3 (cfg(target_feature = "avx2"), set by
// -C target-cpu). Hyperscan's Truffle, as indexany.c and std.simd.IndexAny
// for sets of four or more bytes: two 16-byte tables by low nibble
// (_mm*_shuffle_epi8, the second on the byte XOR 0x80), ANDed with a bit
// looked up by the high nibble; one vector a step, the first member by
// trailing_zeros of the movemask, then bytes. Every load takes its pointer
// from a checked slice (`s[i..i + 32]`), so the rust and rust-ovf columns
// are bounds-checked like zig-safe's and Mk's loads; unsafe holds
// only the intrinsics. The buffer starts on a 64-byte boundary, as in every
// column (BS-4). Usage: indexany <size> <reps> <seed>.
use std::arch::x86_64::*;

const SET: [u8; 8] = [b'\t', b'\n', b'\r', b' ', b'"', b',', b':', b'='];

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn tables(set: &[u8]) -> ([u8; 16], [u8; 16]) {
    let (mut lo, mut hi) = ([0u8; 16], [0u8; 16]);
    for &x in set {
        if x < 0x80 {
            lo[(x & 15) as usize] |= 1 << (x >> 4);
        } else {
            hi[(x & 15) as usize] |= 1 << ((x >> 4) - 8);
        }
    }
    (lo, hi)
}

fn scalar(s: &[u8], mut i: usize, lo: &[u8; 16], hi: &[u8; 16]) -> i64 {
    while i < s.len() {
        let x = s[i];
        let row = if x < 0x80 { lo[(x & 15) as usize] } else { hi[(x & 15) as usize] };
        if (row >> ((x >> 4) & 7)) & 1 != 0 {
            return i as i64;
        }
        i += 1;
    }
    -1
}

#[cfg(target_feature = "avx2")]
fn index_any(s: &[u8], set: &[u8]) -> i64 {
    let (lo, hi) = tables(set);
    let n = s.len();
    let mut i = 0;
    unsafe {
        let tlo = _mm256_broadcastsi128_si256(_mm_loadu_si128(lo.as_ptr() as *const __m128i));
        let thi = _mm256_broadcastsi128_si256(_mm_loadu_si128(hi.as_ptr() as *const __m128i));
        let bits = _mm256_setr_epi8(
            1, 2, 4, 8, 16, 32, 64, -128, 1, 2, 4, 8, 16, 32, 64, -128, 1, 2, 4, 8, 16, 32, 64, -128, 1, 2, 4, 8, 16, 32,
            64, -128,
        );
        let top = _mm256_set1_epi8(-128);
        let low4 = _mm256_set1_epi8(15);
        let z = _mm256_setzero_si256();
        while i + 32 <= n {
            let v = _mm256_loadu_si256(s[i..i + 32].as_ptr() as *const __m256i);
            let row = _mm256_or_si256(_mm256_shuffle_epi8(tlo, v), _mm256_shuffle_epi8(thi, _mm256_xor_si256(v, top)));
            let bit = _mm256_shuffle_epi8(bits, _mm256_and_si256(_mm256_srli_epi16::<4>(v), low4));
            let m = !(_mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_and_si256(row, bit), z)) as u32);
            if m != 0 {
                return (i + m.trailing_zeros() as usize) as i64;
            }
            i += 32;
        }
    }
    scalar(s, i, &lo, &hi)
}

#[cfg(not(target_feature = "avx2"))]
fn index_any(s: &[u8], set: &[u8]) -> i64 {
    let (lo, hi) = tables(set);
    let n = s.len();
    let mut i = 0;
    unsafe {
        let tlo = _mm_loadu_si128(lo.as_ptr() as *const __m128i);
        let thi = _mm_loadu_si128(hi.as_ptr() as *const __m128i);
        let bits = _mm_setr_epi8(1, 2, 4, 8, 16, 32, 64, -128, 1, 2, 4, 8, 16, 32, 64, -128);
        let top = _mm_set1_epi8(-128);
        let low4 = _mm_set1_epi8(15);
        let z = _mm_setzero_si128();
        while i + 16 <= n {
            let v = _mm_loadu_si128(s[i..i + 16].as_ptr() as *const __m128i);
            let row = _mm_or_si128(_mm_shuffle_epi8(tlo, v), _mm_shuffle_epi8(thi, _mm_xor_si128(v, top)));
            let bit = _mm_shuffle_epi8(bits, _mm_and_si128(_mm_srli_epi16::<4>(v), low4));
            let m = 0xFFFF & !(_mm_movemask_epi8(_mm_cmpeq_epi8(_mm_and_si128(row, bit), z)) as u32);
            if m != 0 {
                return (i + m.trailing_zeros() as usize) as i64;
            }
            i += 16;
        }
    }
    scalar(s, i, &lo, &hi)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: indexany <size> <reps> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let reps: u64 = a[2].parse().unwrap();
    let mut st: u64 = a[3].parse().unwrap();
    // BS-4: the buffer starts on a 64-byte boundary in every column.
    let mut v = vec![0u8; n + 64];
    let off = v.as_ptr().align_offset(64);
    let buf = &mut v[off..off + n];
    if buf.as_ptr() as usize % 64 != 0 {
        eprintln!("indexany: buffer not 64-byte aligned");
        std::process::exit(3);
    }
    for (i, c) in buf.iter_mut().enumerate() {
        *c = 0x40 | ((i as u64).wrapping_mul(131).wrapping_add(st) & 0x3F) as u8;
    }
    let span = if n < 64 { n } else { 64 } as u64;
    let mut total: i64 = 0;
    for _ in 0..reps {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let m = mix(st);
        let pos = n - 1 - (m % span) as usize;
        let old = buf[pos];
        buf[pos] = SET[((m >> 32) & 7) as usize];
        total += index_any(buf, &SET);
        buf[pos] = old;
    }
    println!("{}", total);
}
