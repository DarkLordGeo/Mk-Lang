// spectralnorm, Rust: the reference algorithm of README.md (the Benchmarks
// Game's spectral-norm, scalar and single-threaded). A(i, j) computed from
// i64 on every use (+ and * wrap in "rust", trap in "rust-ovf"), Vec<f64>
// vectors indexed v[j] (bounds checked), f64::sqrt. No unsafe, no crates.

fn a(i: i64, j: i64) -> f64 {
    1.0 / ((i + j) * (i + j + 1) / 2 + i + 1) as f64
}

fn mul_av(v: &[f64], out: &mut [f64]) {
    let n = v.len();
    for i in 0..n {
        let mut s = 0.0;
        for j in 0..n {
            s = s + a(i as i64, j as i64) * v[j];
        }
        out[i] = s;
    }
}

fn mul_atv(v: &[f64], out: &mut [f64]) {
    let n = v.len();
    for i in 0..n {
        let mut s = 0.0;
        for j in 0..n {
            s = s + a(j as i64, i as i64) * v[j];
        }
        out[i] = s;
    }
}

fn mul_atav(v: &[f64], out: &mut [f64], tmp: &mut [f64]) {
    mul_av(v, tmp);
    mul_atv(tmp, out);
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 2 {
        eprintln!("usage: spectralnorm <n>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let mut u = vec![1.0f64; n];
    let mut v = vec![0.0f64; n];
    let mut tmp = vec![0.0f64; n];
    for _ in 0..10 {
        mul_atav(&u, &mut v, &mut tmp);
        mul_atav(&v, &mut u, &mut tmp);
    }
    let mut vbv = 0.0;
    let mut vv = 0.0;
    for i in 0..n {
        vbv = vbv + u[i] * v[i];
        vv = vv + v[i] * v[i];
    }
    println!("{}", ((vbv / vv).sqrt() * 1e9) as i64);
}
