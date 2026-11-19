// dot, Rust, SIMD: the kernel of README.md with std::arch::x86_64
// intrinsics (portable std::simd is nightly-only), at the build's fixed
// CPU target and no run-time dispatch: SSE 4-lane f32 vectors at x86-64-v2
// (four hold the 16 partial sums), AVX 8-lane vectors at x86-64-v3 (two;
// cfg(target_feature = "avx2"), set by -C target-cpu). Products and sums are
// separate intrinsics, so every column does the same f32 arithmetic in the
// same order. Every load and store takes its pointer from a checked slice
// (`x[i..i + 16]`, then constant sub-slices of it), so the rust and rust-ovf
// columns are bounds-checked like zig-safe's and Mk's loads; unsafe
// holds only the intrinsics. Both buffers start on a 64-byte boundary, as
// in every column. Usage: dot <n> <reps> <seed>.
use std::arch::x86_64::*;

#[cfg(target_feature = "avx2")]
fn partial(x: &[f32], y: &[f32], s: &mut [f32; 16]) -> usize {
    let n = x.len().min(y.len());
    let mut i = 0;
    unsafe {
        let mut a0 = _mm256_setzero_ps();
        let mut a1 = _mm256_setzero_ps();
        while i + 16 <= n {
            let (xs, ys) = (&x[i..i + 16], &y[i..i + 16]);
            a0 = _mm256_add_ps(a0, _mm256_mul_ps(_mm256_loadu_ps(xs[0..8].as_ptr()), _mm256_loadu_ps(ys[0..8].as_ptr())));
            a1 = _mm256_add_ps(a1, _mm256_mul_ps(_mm256_loadu_ps(xs[8..16].as_ptr()), _mm256_loadu_ps(ys[8..16].as_ptr())));
            i += 16;
        }
        _mm256_storeu_ps(s[0..8].as_mut_ptr(), a0);
        _mm256_storeu_ps(s[8..16].as_mut_ptr(), a1);
    }
    i
}

#[cfg(not(target_feature = "avx2"))]
fn partial(x: &[f32], y: &[f32], s: &mut [f32; 16]) -> usize {
    let n = x.len().min(y.len());
    let mut i = 0;
    unsafe {
        let mut a0 = _mm_setzero_ps();
        let mut a1 = _mm_setzero_ps();
        let mut a2 = _mm_setzero_ps();
        let mut a3 = _mm_setzero_ps();
        while i + 16 <= n {
            let (xs, ys) = (&x[i..i + 16], &y[i..i + 16]);
            a0 = _mm_add_ps(a0, _mm_mul_ps(_mm_loadu_ps(xs[0..4].as_ptr()), _mm_loadu_ps(ys[0..4].as_ptr())));
            a1 = _mm_add_ps(a1, _mm_mul_ps(_mm_loadu_ps(xs[4..8].as_ptr()), _mm_loadu_ps(ys[4..8].as_ptr())));
            a2 = _mm_add_ps(a2, _mm_mul_ps(_mm_loadu_ps(xs[8..12].as_ptr()), _mm_loadu_ps(ys[8..12].as_ptr())));
            a3 = _mm_add_ps(a3, _mm_mul_ps(_mm_loadu_ps(xs[12..16].as_ptr()), _mm_loadu_ps(ys[12..16].as_ptr())));
            i += 16;
        }
        _mm_storeu_ps(s[0..4].as_mut_ptr(), a0);
        _mm_storeu_ps(s[4..8].as_mut_ptr(), a1);
        _mm_storeu_ps(s[8..12].as_mut_ptr(), a2);
        _mm_storeu_ps(s[12..16].as_mut_ptr(), a3);
    }
    i
}

fn dot(x: &[f32], y: &[f32]) -> f32 {
    let mut s = [0f32; 16];
    let mut i = partial(x, y, &mut s);
    let mut w = 8;
    while w >= 1 {
        for j in 0..w {
            s[j] = s[j] + s[j + w];
        }
        w /= 2;
    }
    let mut r = s[0];
    while i < x.len() {
        let p = x[i] * y[i];
        r = r + p;
        i += 1;
    }
    r
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: dot <n> <reps> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let reps: u64 = a[2].parse().unwrap();
    let seed: u64 = a[3].parse().unwrap();
    // Both buffers start on a 64-byte boundary in every column.
    let mut xv = vec![0f32; n + 16];
    let mut yv = vec![0f32; n + 16];
    let (xo, yo) = (xv.as_ptr().align_offset(64), yv.as_ptr().align_offset(64));
    let x = &mut xv[xo..xo + n];
    let y = &mut yv[yo..yo + n];
    if x.as_ptr() as usize % 64 != 0 || y.as_ptr() as usize % 64 != 0 {
        eprintln!("dot: buffer not 64-byte aligned");
        std::process::exit(3);
    }
    for (i, (a, b)) in x.iter_mut().zip(y.iter_mut()).enumerate() {
        *a = ((i as u64).wrapping_mul(0x9e3779b97f4a7c15).wrapping_add(seed) >> 48) as f32 * (1.0 / 65536.0) - 0.5;
        *b = ((i as u64).wrapping_mul(0xbf58476d1ce4e5b9).wrapping_add(seed) >> 48) as f32 * (1.0 / 65536.0) - 0.5;
    }
    let y = &*y;
    let mut total: u64 = 0;
    for r in 0..reps {
        x[(r as usize) % n] += 1.0;
        total = total.wrapping_add(dot(x, y).to_bits() as u64);
    }
    println!("{}", total);
}
