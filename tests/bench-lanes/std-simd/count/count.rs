// count, Rust, SIMD: the kernel of README.md with std::arch::x86_64
// intrinsics (portable std::simd is nightly-only, B16), at the build's fixed
// CPU target and no run-time dispatch: SSE2 16-byte vectors at x86-64-v2,
// AVX2 32-byte vectors at x86-64-v3 (cfg(target_feature = "avx2"), set by
// -C target-cpu). Each compare's mask is subtracted from one of four byte
// counters, a step of four vectors, and every 255 steps the counters are
// summed with _mm*_sad_epu8; then one vector a step (the movemask's
// count_ones), then bytes: count.c's method and std.simd.Count's. Every
// load takes its pointer from a checked slice (`s[i..i + 128]`, then
// constant sub-slices of it), so the rust and rust-ovf columns are
// bounds-checked like zig-safe's and Mk's loads; unsafe holds only
// the intrinsics. The buffer starts on a 64-byte boundary, as in every
// column (BS-4). Usage: count <size> <reps> <seed>.
use std::arch::x86_64::*;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

#[cfg(target_feature = "avx2")]
fn count(s: &[u8], b: u8) -> i64 {
    let n = s.len();
    let mut i = 0;
    let mut total: i64 = 0;
    unsafe {
        let nd = _mm256_set1_epi8(b as i8);
        let z = _mm256_setzero_si256();
        while i + 128 <= n {
            let (mut c0, mut c1, mut c2, mut c3) = (z, z, z, z);
            let mut steps = 0;
            while steps < 255 && i + 128 <= n {
                let w = &s[i..i + 128];
                c0 = _mm256_sub_epi8(c0, _mm256_cmpeq_epi8(_mm256_loadu_si256(w[0..32].as_ptr() as *const __m256i), nd));
                c1 = _mm256_sub_epi8(c1, _mm256_cmpeq_epi8(_mm256_loadu_si256(w[32..64].as_ptr() as *const __m256i), nd));
                c2 = _mm256_sub_epi8(c2, _mm256_cmpeq_epi8(_mm256_loadu_si256(w[64..96].as_ptr() as *const __m256i), nd));
                c3 = _mm256_sub_epi8(c3, _mm256_cmpeq_epi8(_mm256_loadu_si256(w[96..128].as_ptr() as *const __m256i), nd));
                steps += 1;
                i += 128;
            }
            let t = _mm256_add_epi64(
                _mm256_add_epi64(_mm256_sad_epu8(c0, z), _mm256_sad_epu8(c1, z)),
                _mm256_add_epi64(_mm256_sad_epu8(c2, z), _mm256_sad_epu8(c3, z)),
            );
            total += _mm256_extract_epi64::<0>(t)
                + _mm256_extract_epi64::<1>(t)
                + _mm256_extract_epi64::<2>(t)
                + _mm256_extract_epi64::<3>(t);
        }
        while i + 32 <= n {
            let m = _mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_loadu_si256(s[i..i + 32].as_ptr() as *const __m256i), nd)) as u32;
            total += m.count_ones() as i64;
            i += 32;
        }
    }
    while i < n {
        total += (s[i] == b) as i64;
        i += 1;
    }
    total
}

#[cfg(not(target_feature = "avx2"))]
fn count(s: &[u8], b: u8) -> i64 {
    let n = s.len();
    let mut i = 0;
    let mut total: i64 = 0;
    unsafe {
        let nd = _mm_set1_epi8(b as i8);
        let z = _mm_setzero_si128();
        while i + 64 <= n {
            let (mut c0, mut c1, mut c2, mut c3) = (z, z, z, z);
            let mut steps = 0;
            while steps < 255 && i + 64 <= n {
                let w = &s[i..i + 64];
                c0 = _mm_sub_epi8(c0, _mm_cmpeq_epi8(_mm_loadu_si128(w[0..16].as_ptr() as *const __m128i), nd));
                c1 = _mm_sub_epi8(c1, _mm_cmpeq_epi8(_mm_loadu_si128(w[16..32].as_ptr() as *const __m128i), nd));
                c2 = _mm_sub_epi8(c2, _mm_cmpeq_epi8(_mm_loadu_si128(w[32..48].as_ptr() as *const __m128i), nd));
                c3 = _mm_sub_epi8(c3, _mm_cmpeq_epi8(_mm_loadu_si128(w[48..64].as_ptr() as *const __m128i), nd));
                steps += 1;
                i += 64;
            }
            let t = _mm_add_epi64(
                _mm_add_epi64(_mm_sad_epu8(c0, z), _mm_sad_epu8(c1, z)),
                _mm_add_epi64(_mm_sad_epu8(c2, z), _mm_sad_epu8(c3, z)),
            );
            total += _mm_cvtsi128_si64(t) + _mm_cvtsi128_si64(_mm_unpackhi_epi64(t, t));
        }
        while i + 16 <= n {
            let m = _mm_movemask_epi8(_mm_cmpeq_epi8(_mm_loadu_si128(s[i..i + 16].as_ptr() as *const __m128i), nd)) as u32;
            total += m.count_ones() as i64;
            i += 16;
        }
    }
    while i < n {
        total += (s[i] == b) as i64;
        i += 1;
    }
    total
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: count <size> <reps> <seed>");
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
        eprintln!("count: buffer not 64-byte aligned");
        std::process::exit(3);
    }
    for (i, c) in buf.iter_mut().enumerate() {
        *c = ((i as u64).wrapping_mul(0x9e3779b97f4a7c15).wrapping_add(st) >> 60) as u8;
    }
    let buf = &*buf;
    let mut total: u64 = 0;
    for _ in 0..reps {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        total = total.wrapping_mul(31).wrapping_add(count(buf, (mix(st) & 15) as u8) as u64);
    }
    println!("{}", total);
}
