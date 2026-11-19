// compare, Rust, SIMD: the kernel of README.md with std::arch::x86_64
// intrinsics (portable std::simd is nightly-only, B16), at the build's fixed
// CPU target and no run-time dispatch: SSE2 16-byte vectors at x86-64-v2,
// AVX2 32-byte vectors at x86-64-v3 (cfg(target_feature = "avx2"), set by
// -C target-cpu). Four vector compares ANDed and tested once a step, then
// one vector a step, the first differing byte found by trailing_zeros of
// the inverted movemask and ordered unsigned, then bytes, then the lengths:
// compare.c's method and std.simd.Compare's. Every load takes its pointer
// from a checked slice (`a[i..i + 128]`, then constant sub-slices of it),
// so the rust and rust-ovf columns are bounds-checked like zig-safe's and
// Mk's loads; unsafe holds only the intrinsics. Both buffers start
// on a 64-byte boundary, as in every column (BS-4). Usage: compare <size>
// <reps> <seed>.
use std::arch::x86_64::*;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn order(x: u8, y: u8) -> i64 {
    if x < y { -1 } else { 1 }
}

#[cfg(target_feature = "avx2")]
fn compare(a: &[u8], b: &[u8]) -> i64 {
    let n = a.len().min(b.len());
    let mut i = 0;
    unsafe {
        while i + 128 <= n {
            let (x, y) = (&a[i..i + 128], &b[i..i + 128]);
            let e0 = _mm256_cmpeq_epi8(_mm256_loadu_si256(x[0..32].as_ptr() as *const __m256i), _mm256_loadu_si256(y[0..32].as_ptr() as *const __m256i));
            let e1 = _mm256_cmpeq_epi8(_mm256_loadu_si256(x[32..64].as_ptr() as *const __m256i), _mm256_loadu_si256(y[32..64].as_ptr() as *const __m256i));
            let e2 = _mm256_cmpeq_epi8(_mm256_loadu_si256(x[64..96].as_ptr() as *const __m256i), _mm256_loadu_si256(y[64..96].as_ptr() as *const __m256i));
            let e3 = _mm256_cmpeq_epi8(_mm256_loadu_si256(x[96..128].as_ptr() as *const __m256i), _mm256_loadu_si256(y[96..128].as_ptr() as *const __m256i));
            let all = _mm256_and_si256(_mm256_and_si256(e0, e1), _mm256_and_si256(e2, e3));
            if _mm256_movemask_epi8(all) as u32 != u32::MAX {
                break;
            }
            i += 128;
        }
        while i + 32 <= n {
            let m = _mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_loadu_si256(a[i..i + 32].as_ptr() as *const __m256i), _mm256_loadu_si256(b[i..i + 32].as_ptr() as *const __m256i))) as u32;
            if m != u32::MAX {
                let j = i + (!m).trailing_zeros() as usize;
                return order(a[j], b[j]);
            }
            i += 32;
        }
    }
    while i < n {
        if a[i] != b[i] {
            return order(a[i], b[i]);
        }
        i += 1;
    }
    (a.len() > b.len()) as i64 - (a.len() < b.len()) as i64
}

#[cfg(not(target_feature = "avx2"))]
fn compare(a: &[u8], b: &[u8]) -> i64 {
    let n = a.len().min(b.len());
    let mut i = 0;
    unsafe {
        while i + 64 <= n {
            let (x, y) = (&a[i..i + 64], &b[i..i + 64]);
            let e0 = _mm_cmpeq_epi8(_mm_loadu_si128(x[0..16].as_ptr() as *const __m128i), _mm_loadu_si128(y[0..16].as_ptr() as *const __m128i));
            let e1 = _mm_cmpeq_epi8(_mm_loadu_si128(x[16..32].as_ptr() as *const __m128i), _mm_loadu_si128(y[16..32].as_ptr() as *const __m128i));
            let e2 = _mm_cmpeq_epi8(_mm_loadu_si128(x[32..48].as_ptr() as *const __m128i), _mm_loadu_si128(y[32..48].as_ptr() as *const __m128i));
            let e3 = _mm_cmpeq_epi8(_mm_loadu_si128(x[48..64].as_ptr() as *const __m128i), _mm_loadu_si128(y[48..64].as_ptr() as *const __m128i));
            let all = _mm_and_si128(_mm_and_si128(e0, e1), _mm_and_si128(e2, e3));
            if _mm_movemask_epi8(all) != 0xFFFF {
                break;
            }
            i += 64;
        }
        while i + 16 <= n {
            let m = _mm_movemask_epi8(_mm_cmpeq_epi8(_mm_loadu_si128(a[i..i + 16].as_ptr() as *const __m128i), _mm_loadu_si128(b[i..i + 16].as_ptr() as *const __m128i))) as u32;
            if m != 0xFFFF {
                let j = i + (!m).trailing_zeros() as usize;
                return order(a[j], b[j]);
            }
            i += 16;
        }
    }
    while i < n {
        if a[i] != b[i] {
            return order(a[i], b[i]);
        }
        i += 1;
    }
    (a.len() > b.len()) as i64 - (a.len() < b.len()) as i64
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 4 {
        eprintln!("usage: compare <size> <reps> <seed>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let reps: u64 = args[2].parse().unwrap();
    let mut st: u64 = args[3].parse().unwrap();
    // BS-4: both buffers start on a 64-byte boundary in every column.
    let mut va = vec![0u8; n + 64];
    let mut vb = vec![0u8; n + 64];
    let (ao, bo) = (va.as_ptr().align_offset(64), vb.as_ptr().align_offset(64));
    let a = &mut va[ao..ao + n];
    let b = &mut vb[bo..bo + n];
    if a.as_ptr() as usize % 64 != 0 || b.as_ptr() as usize % 64 != 0 {
        eprintln!("compare: buffer not 64-byte aligned");
        std::process::exit(3);
    }
    for (i, (x, y)) in a.iter_mut().zip(b.iter_mut()).enumerate() {
        *x = ((i as u64).wrapping_mul(0x9e3779b97f4a7c15).wrapping_add(st) >> 56) as u8;
        *y = *x;
    }
    let a = &*a;
    let span = if n < 64 { n } else { 64 } as u64;
    let mut total: u64 = 0;
    for _ in 0..reps {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let m = mix(st);
        let pos = n - 1 - (m % span) as usize;
        let d = ((m >> 32) & 1) as u8;
        b[pos] ^= d;
        total = total.wrapping_mul(31).wrapping_add((compare(a, b) + 1) as u64);
        b[pos] ^= d;
    }
    println!("{}", total);
}
