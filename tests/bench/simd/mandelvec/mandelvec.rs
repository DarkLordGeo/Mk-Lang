// mandelvec, Rust, SIMD: the kernel of README.md (same/mandelbrot's
// algorithm, L points at once) with std::arch::x86_64 intrinsics, at the
// build's fixed CPU target and no run-time dispatch: SSE2 2-lane f64
// vectors at x86-64-v2, AVX 4-lane vectors at x86-64-v3
// (cfg(target_feature = "avx2")). Each lane stops counting at its escape;
// the loop ends when no lane is active or at 50 iterations. The intrinsics
// need unsafe; they take and return values, no pointers but the stores into
// local arrays. Usage: mandelvec <n>.
use std::arch::x86_64::*;

const MAX_ITER: i64 = 50;

fn point(cr: f64, ci: f64) -> i64 {
    let (mut zr, mut zi) = (0.0f64, 0.0f64);
    let mut i = 0;
    while i < MAX_ITER {
        let tr = (zr * zr - zi * zi) + cr;
        let ti = (2.0 * zr) * zi + ci;
        zr = tr;
        zi = ti;
        if zr * zr + zi * zi > 4.0 {
            break;
        }
        i += 1;
    }
    i
}

#[cfg(target_feature = "avx2")]
const L: usize = 4;
#[cfg(not(target_feature = "avx2"))]
const L: usize = 2;

#[cfg(target_feature = "avx2")]
fn lanes(crs: &[f64; L], ci: f64) -> [f64; L] {
    let mut c = [0f64; L];
    unsafe {
        let cr = _mm256_loadu_pd(crs.as_ptr());
        let civ = _mm256_set1_pd(ci);
        let (mut zr, mut zi, mut cnt) = (_mm256_setzero_pd(), _mm256_setzero_pd(), _mm256_setzero_pd());
        let mut active = _mm256_castsi256_pd(_mm256_set1_epi64x(-1));
        let (two, four, one) = (_mm256_set1_pd(2.0), _mm256_set1_pd(4.0), _mm256_set1_pd(1.0));
        for _ in 0..MAX_ITER {
            let tr = _mm256_add_pd(_mm256_sub_pd(_mm256_mul_pd(zr, zr), _mm256_mul_pd(zi, zi)), cr);
            let ti = _mm256_add_pd(_mm256_mul_pd(_mm256_mul_pd(two, zr), zi), civ);
            zr = tr;
            zi = ti;
            let esc = _mm256_cmp_pd::<_CMP_GT_OQ>(_mm256_add_pd(_mm256_mul_pd(zr, zr), _mm256_mul_pd(zi, zi)), four);
            active = _mm256_andnot_pd(esc, active);
            if _mm256_movemask_pd(active) == 0 {
                break;
            }
            cnt = _mm256_add_pd(cnt, _mm256_and_pd(active, one));
        }
        _mm256_storeu_pd(c.as_mut_ptr(), cnt);
    }
    c
}

#[cfg(not(target_feature = "avx2"))]
fn lanes(crs: &[f64; L], ci: f64) -> [f64; L] {
    let mut c = [0f64; L];
    unsafe {
        let cr = _mm_loadu_pd(crs.as_ptr());
        let civ = _mm_set1_pd(ci);
        let (mut zr, mut zi, mut cnt) = (_mm_setzero_pd(), _mm_setzero_pd(), _mm_setzero_pd());
        let mut active = _mm_castsi128_pd(_mm_set1_epi64x(-1));
        let (two, four, one) = (_mm_set1_pd(2.0), _mm_set1_pd(4.0), _mm_set1_pd(1.0));
        for _ in 0..MAX_ITER {
            let tr = _mm_add_pd(_mm_sub_pd(_mm_mul_pd(zr, zr), _mm_mul_pd(zi, zi)), cr);
            let ti = _mm_add_pd(_mm_mul_pd(_mm_mul_pd(two, zr), zi), civ);
            zr = tr;
            zi = ti;
            let esc = _mm_cmpgt_pd(_mm_add_pd(_mm_mul_pd(zr, zr), _mm_mul_pd(zi, zi)), four);
            active = _mm_andnot_pd(esc, active);
            if _mm_movemask_pd(active) == 0 {
                break;
            }
            cnt = _mm_add_pd(cnt, _mm_and_pd(active, one));
        }
        _mm_storeu_pd(c.as_mut_ptr(), cnt);
    }
    c
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 2 {
        eprintln!("usage: mandelvec <n>");
        std::process::exit(2);
    }
    let n: i64 = a[1].parse().unwrap();
    let fnn = n as f64;
    let mut total: i64 = 0;
    for y in 0..n {
        let ci = (2.0 * y as f64) / fnn - 1.0;
        let mut x = 0;
        while x + L as i64 <= n {
            let mut crs = [0f64; L];
            for l in 0..L {
                crs[l] = (2.0 * (x + l as i64) as f64) / fnn - 1.5;
            }
            for c in lanes(&crs, ci) {
                total += c as i64;
            }
            x += L as i64;
        }
        while x < n {
            total += point((2.0 * x as f64) / fnn - 1.5, ci);
            x += 1;
        }
    }
    println!("{}", total);
}
