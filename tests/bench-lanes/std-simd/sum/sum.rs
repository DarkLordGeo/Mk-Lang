// sum, Rust, SIMD: the kernel of README.md with std::arch::x86_64
// intrinsics (portable std::simd is nightly-only), at the build's fixed
// CPU target and no run-time dispatch: SSE 4-lane f32 vectors at x86-64-v2
// (four hold the 16 partial sums), AVX 8-lane vectors at x86-64-v3 (two;
// cfg(target_feature = "avx2"), set by -C target-cpu), then the pairwise
// tree and the tail in order, so every column does the same f32 arithmetic
// in the same order. Every load and store takes its pointer from a checked
// slice (`x[i..i + 16]`, then constant sub-slices of it), so the rust and
// rust-ovf columns are bounds-checked like zig-safe's and Mk's loads
// too; unsafe holds only the intrinsics. The buffer starts on a 64-byte
// boundary, as in every column. Usage: sum <n> <reps> <seed>.
use std::arch::x86_64::*;

#[cfg(target_feature = "avx2")]
fn partial(x: &[f32], s: &mut [f32; 16]) -> usize {
    let n = x.len();
    let mut i = 0;
    unsafe {
        let mut a0 = _mm256_setzero_ps();
        let mut a1 = _mm256_setzero_ps();
        while i + 16 <= n {
            let xs = &x[i..i + 16];
            a0 = _mm256_add_ps(a0, _mm256_loadu_ps(xs[0..8].as_ptr()));
            a1 = _mm256_add_ps(a1, _mm256_loadu_ps(xs[8..16].as_ptr()));
            i += 16;
        }
        _mm256_storeu_ps(s[0..8].as_mut_ptr(), a0);
        _mm256_storeu_ps(s[8..16].as_mut_ptr(), a1);
    }
    i
}

#[cfg(not(target_feature = "avx2"))]
fn partial(x: &[f32], s: &mut [f32; 16]) -> usize {
    let n = x.len();
    let mut i = 0;
    unsafe {
        let mut a0 = _mm_setzero_ps();
        let mut a1 = _mm_setzero_ps();
        let mut a2 = _mm_setzero_ps();
        let mut a3 = _mm_setzero_ps();
        while i + 16 <= n {
            let xs = &x[i..i + 16];
            a0 = _mm_add_ps(a0, _mm_loadu_ps(xs[0..4].as_ptr()));
            a1 = _mm_add_ps(a1, _mm_loadu_ps(xs[4..8].as_ptr()));
            a2 = _mm_add_ps(a2, _mm_loadu_ps(xs[8..12].as_ptr()));
            a3 = _mm_add_ps(a3, _mm_loadu_ps(xs[12..16].as_ptr()));
            i += 16;
        }
        _mm_storeu_ps(s[0..4].as_mut_ptr(), a0);
        _mm_storeu_ps(s[4..8].as_mut_ptr(), a1);
        _mm_storeu_ps(s[8..12].as_mut_ptr(), a2);
        _mm_storeu_ps(s[12..16].as_mut_ptr(), a3);
    }
    i
}

fn sum(x: &[f32]) -> f32 {
    let mut s = [0f32; 16];
    let mut i = partial(x, &mut s);
    let mut w = 8;
    while w >= 1 {
        for j in 0..w {
            s[j] = s[j] + s[j + w];
        }
        w /= 2;
    }
    let mut r = s[0];
    while i < x.len() {
        r = r + x[i];
        i += 1;
    }
    r
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: sum <n> <reps> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let reps: u64 = a[2].parse().unwrap();
    let seed: u64 = a[3].parse().unwrap();
    // The buffer starts on a 64-byte boundary in every column.
    let mut xv = vec![0f32; n + 16];
    let xo = xv.as_ptr().align_offset(64);
    let x = &mut xv[xo..xo + n];
    if x.as_ptr() as usize % 64 != 0 {
        eprintln!("sum: buffer not 64-byte aligned");
        std::process::exit(3);
    }
    for (i, a) in x.iter_mut().enumerate() {
        *a = ((i as u64).wrapping_mul(0x9e3779b97f4a7c15).wrapping_add(seed) >> 48) as f32 * (1.0 / 65536.0) - 0.5;
    }
    let mut total: u64 = 0;
    for r in 0..reps {
        x[(r as usize) % n] += 1.0;
        total = total.wrapping_add(sum(x).to_bits() as u64);
    }
    println!("{}", total);
}
