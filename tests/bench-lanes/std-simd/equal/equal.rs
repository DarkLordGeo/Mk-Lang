// equal, Rust, SIMD: the kernel of README.md with std::arch::x86_64
// intrinsics (portable std::simd is nightly-only, B16), at the build's fixed
// CPU target and no run-time dispatch: 16-byte vectors at x86-64-v2, AVX2
// 32-byte vectors at x86-64-v3 (cfg(target_feature = "avx2"), set by
// -C target-cpu). Four vector XORs ORed and tested once a step
// (_mm*_testz_si*), then one vector a step, then bytes: equal.c's method and
// std.simd.Equal's. Every load takes its pointer from a checked slice
// (`a[i..i + 128]`, then constant sub-slices of it), so the rust and
// rust-ovf columns are bounds-checked like zig-safe's and Mk's loads;
// unsafe holds only the intrinsics. Both buffers start on a 64-byte
// boundary, as in every column (BS-4). Usage: equal <size> <reps> <seed>.
use std::arch::x86_64::*;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

#[cfg(target_feature = "avx2")]
fn equal(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    let n = a.len();
    let mut i = 0;
    unsafe {
        while i + 128 <= n {
            let (x, y) = (&a[i..i + 128], &b[i..i + 128]);
            let x0 = _mm256_xor_si256(_mm256_loadu_si256(x[0..32].as_ptr() as *const __m256i), _mm256_loadu_si256(y[0..32].as_ptr() as *const __m256i));
            let x1 = _mm256_xor_si256(_mm256_loadu_si256(x[32..64].as_ptr() as *const __m256i), _mm256_loadu_si256(y[32..64].as_ptr() as *const __m256i));
            let x2 = _mm256_xor_si256(_mm256_loadu_si256(x[64..96].as_ptr() as *const __m256i), _mm256_loadu_si256(y[64..96].as_ptr() as *const __m256i));
            let x3 = _mm256_xor_si256(_mm256_loadu_si256(x[96..128].as_ptr() as *const __m256i), _mm256_loadu_si256(y[96..128].as_ptr() as *const __m256i));
            let o = _mm256_or_si256(_mm256_or_si256(x0, x1), _mm256_or_si256(x2, x3));
            if _mm256_testz_si256(o, o) == 0 {
                return false;
            }
            i += 128;
        }
        while i + 32 <= n {
            let x = _mm256_xor_si256(_mm256_loadu_si256(a[i..i + 32].as_ptr() as *const __m256i), _mm256_loadu_si256(b[i..i + 32].as_ptr() as *const __m256i));
            if _mm256_testz_si256(x, x) == 0 {
                return false;
            }
            i += 32;
        }
    }
    while i < n {
        if a[i] != b[i] {
            return false;
        }
        i += 1;
    }
    true
}

#[cfg(not(target_feature = "avx2"))]
fn equal(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    let n = a.len();
    let mut i = 0;
    unsafe {
        while i + 64 <= n {
            let (x, y) = (&a[i..i + 64], &b[i..i + 64]);
            let x0 = _mm_xor_si128(_mm_loadu_si128(x[0..16].as_ptr() as *const __m128i), _mm_loadu_si128(y[0..16].as_ptr() as *const __m128i));
            let x1 = _mm_xor_si128(_mm_loadu_si128(x[16..32].as_ptr() as *const __m128i), _mm_loadu_si128(y[16..32].as_ptr() as *const __m128i));
            let x2 = _mm_xor_si128(_mm_loadu_si128(x[32..48].as_ptr() as *const __m128i), _mm_loadu_si128(y[32..48].as_ptr() as *const __m128i));
            let x3 = _mm_xor_si128(_mm_loadu_si128(x[48..64].as_ptr() as *const __m128i), _mm_loadu_si128(y[48..64].as_ptr() as *const __m128i));
            let o = _mm_or_si128(_mm_or_si128(x0, x1), _mm_or_si128(x2, x3));
            if _mm_testz_si128(o, o) == 0 {
                return false;
            }
            i += 64;
        }
        while i + 16 <= n {
            let x = _mm_xor_si128(_mm_loadu_si128(a[i..i + 16].as_ptr() as *const __m128i), _mm_loadu_si128(b[i..i + 16].as_ptr() as *const __m128i));
            if _mm_testz_si128(x, x) == 0 {
                return false;
            }
            i += 16;
        }
    }
    while i < n {
        if a[i] != b[i] {
            return false;
        }
        i += 1;
    }
    true
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 4 {
        eprintln!("usage: equal <size> <reps> <seed>");
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
        eprintln!("equal: buffer not 64-byte aligned");
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
        total = total.wrapping_mul(31).wrapping_add(equal(a, b) as u64);
        b[pos] ^= d;
    }
    println!("{}", total);
}
