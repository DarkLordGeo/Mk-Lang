// indexbyte, Rust, SIMD: the kernel of README.md with std::arch::x86_64
// intrinsics (portable std::simd is nightly-only, B16), at the build's fixed
// CPU target and no run-time dispatch: SSE2 16-byte vectors at x86-64-v2,
// AVX2 32-byte vectors at x86-64-v3 (cfg(target_feature = "avx2"), set by
// -C target-cpu). Four vectors a step, their compares ORed, one movemask
// test; then one vector a step; then bytes. Every load takes its pointer
// from a checked slice (`s[i..i + 128]`, then constant sub-slices of it), so
// the rust and rust-ovf columns are bounds-checked like zig-safe's and Mk's
// loads; unsafe holds only the intrinsics. The buffer starts on a
// 64-byte boundary, as in every column (BS-4). Usage: indexbyte <size>
// <reps> <seed>.
use std::arch::x86_64::*;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

#[cfg(target_feature = "avx2")]
fn index_byte(s: &[u8], b: u8) -> i64 {
    let n = s.len();
    let mut i = 0;
    unsafe {
        let nb = _mm256_set1_epi8(b as i8);
        while i + 128 <= n {
            let w = &s[i..i + 128];
            let e0 = _mm256_cmpeq_epi8(_mm256_loadu_si256(w[0..32].as_ptr() as *const __m256i), nb);
            let e1 = _mm256_cmpeq_epi8(_mm256_loadu_si256(w[32..64].as_ptr() as *const __m256i), nb);
            let e2 = _mm256_cmpeq_epi8(_mm256_loadu_si256(w[64..96].as_ptr() as *const __m256i), nb);
            let e3 = _mm256_cmpeq_epi8(_mm256_loadu_si256(w[96..128].as_ptr() as *const __m256i), nb);
            let any = _mm256_or_si256(_mm256_or_si256(e0, e1), _mm256_or_si256(e2, e3));
            if _mm256_movemask_epi8(any) != 0 {
                break;
            }
            i += 128;
        }
        while i + 32 <= n {
            let m = _mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_loadu_si256(s[i..i + 32].as_ptr() as *const __m256i), nb)) as u32;
            if m != 0 {
                return (i + m.trailing_zeros() as usize) as i64;
            }
            i += 32;
        }
    }
    while i < n {
        if s[i] == b {
            return i as i64;
        }
        i += 1;
    }
    -1
}

#[cfg(not(target_feature = "avx2"))]
fn index_byte(s: &[u8], b: u8) -> i64 {
    let n = s.len();
    let mut i = 0;
    unsafe {
        let nb = _mm_set1_epi8(b as i8);
        while i + 64 <= n {
            let w = &s[i..i + 64];
            let e0 = _mm_cmpeq_epi8(_mm_loadu_si128(w[0..16].as_ptr() as *const __m128i), nb);
            let e1 = _mm_cmpeq_epi8(_mm_loadu_si128(w[16..32].as_ptr() as *const __m128i), nb);
            let e2 = _mm_cmpeq_epi8(_mm_loadu_si128(w[32..48].as_ptr() as *const __m128i), nb);
            let e3 = _mm_cmpeq_epi8(_mm_loadu_si128(w[48..64].as_ptr() as *const __m128i), nb);
            let any = _mm_or_si128(_mm_or_si128(e0, e1), _mm_or_si128(e2, e3));
            if _mm_movemask_epi8(any) != 0 {
                break;
            }
            i += 64;
        }
        while i + 16 <= n {
            let m = _mm_movemask_epi8(_mm_cmpeq_epi8(_mm_loadu_si128(s[i..i + 16].as_ptr() as *const __m128i), nb)) as u32;
            if m != 0 {
                return (i + m.trailing_zeros() as usize) as i64;
            }
            i += 16;
        }
    }
    while i < n {
        if s[i] == b {
            return i as i64;
        }
        i += 1;
    }
    -1
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: indexbyte <size> <reps> <seed>");
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
        eprintln!("indexbyte: buffer not 64-byte aligned");
        std::process::exit(3);
    }
    for (i, c) in buf.iter_mut().enumerate() {
        *c = ((i as u64).wrapping_mul(131).wrapping_add(st) & 0x7F) as u8;
    }
    let span = if n < 64 { n } else { 64 } as u64;
    let mut total: i64 = 0;
    for _ in 0..reps {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let pos = n - 1 - (mix(st) % span) as usize;
        let old = buf[pos];
        buf[pos] = 0xFF;
        total += index_byte(buf, 0xFF);
        buf[pos] = old;
    }
    println!("{}", total);
}
