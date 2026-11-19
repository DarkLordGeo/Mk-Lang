// math, Rust, idiomatic std: f64's and f32's methods (exp, ln, sin, powf,
// atan2, sinh, cbrt, mul_add; std forwards them to the platform C library
// or LLVM's libm calls) over 4,096 inputs built from splitmix64 bits
// (README.md), reps passes, pass r starting at input r. The checksum adds
// bits >> 32 of each f64 result (bits >> 16 of each f32 result) with
// wrapping adds. One loop per function through a generic helper, so each
// call is direct.
// On Windows these methods reach the static UCRT, which picks an FMA3 and
// AVX2 body at run time on a CPU that has them; the table's one CPU target
// is x86-64-v2 (tests/bench/README.md), so main turns that choice off
// first with the UCRT's switch, as math.c does (fma3_off). That takes the
// row's only `unsafe extern` block, which declares the two UCRT functions
// safe: each takes or returns an int and touches only the UCRT's flag.
// Usage: math <fn> <reps> <seed>.
const N: usize = 4096;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
    z ^ (z >> 31)
}

fn f64of(z: u64, lo: i64, hi: i64, signed: bool) -> f64 {
    let e = (lo + 1023) as u64 + (z >> 53) % ((hi - lo + 1) as u64);
    let sign = if signed { (z & 0x0010000000000000) << 11 } else { 0 };
    f64::from_bits(sign | e << 52 | (z & 0x000FFFFFFFFFFFFF))
}

fn f32of(z: u64, lo: i64, hi: i64, signed: bool) -> f32 {
    let e = (lo + 127) as u64 + (z >> 53) % ((hi - lo + 1) as u64);
    let sign = if signed { (z & 0x0010000000000000) >> 21 } else { 0 };
    f32::from_bits((sign | e << 23 | (z & 0x7FFFFF)) as u32)
}

fn loop1(xs: &[f64], r: usize, f: impl Fn(f64) -> f64) -> u64 {
    let mut acc: u64 = 0;
    for i in 0..N {
        acc = acc.wrapping_add(f(xs[(i + r) & (N - 1)]).to_bits() >> 32);
    }
    acc
}

fn loopf(fs: &[f32], r: usize, f: impl Fn(f32) -> f32) -> u64 {
    let mut acc: u64 = 0;
    for i in 0..N {
        acc = acc.wrapping_add((f(fs[(i + r) & (N - 1)]).to_bits() >> 16) as u64);
    }
    acc
}

#[cfg(all(windows, target_arch = "x86_64", target_env = "msvc"))]
unsafe extern "C" {
    safe fn _set_FMA3_enable(flag: i32) -> i32;
    safe fn _get_FMA3_enable() -> i32;
}

// fma3_off turns off the UCRT's FMA3 paths and exits with status 3 if they
// stay on.
#[cfg(all(windows, target_arch = "x86_64", target_env = "msvc"))]
fn fma3_off() {
    _set_FMA3_enable(0);
    if _get_FMA3_enable() != 0 {
        eprintln!("math: the UCRT's FMA3 paths are still on");
        std::process::exit(3);
    }
}

#[cfg(not(all(windows, target_arch = "x86_64", target_env = "msvc")))]
fn fma3_off() {}

fn main() {
    fma3_off();
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 4 {
        eprintln!("usage: math <fn> <reps> <seed>");
        std::process::exit(2);
    }
    let name = args[1].as_str();
    let reps: usize = args[2].parse().unwrap();
    let mut st: u64 = args[3].parse().unwrap();
    let (lo, hi, sg) = match name {
        "exp" | "expf" => (-10, 5, true),
        "log" | "logf" => (-100, 100, false),
        "sin" | "sinf" => (-10, 2, true),
        "sinlarge" => (20, 60, true),
        "pow" => (-4, 3, false),
        "sinh" => (-10, 4, true),
        "cbrt" => (-200, 200, true),
        "fma" => (-5, 5, true),
        _ => (-10, 10, true), // atan2
    };
    let f32row = matches!(name, "expf" | "logf" | "sinf");
    let mut xs = vec![0f64; N];
    let mut ys = vec![0f64; N];
    let mut zs = vec![0f64; N];
    let mut fs = vec![0f32; N];
    for i in 0..N {
        st = st.wrapping_add(0x9E3779B97F4A7C15);
        let a = mix(st);
        st = st.wrapping_add(0x9E3779B97F4A7C15);
        let b = mix(st);
        st = st.wrapping_add(0x9E3779B97F4A7C15);
        let c = mix(st);
        xs[i] = f64of(a, lo, hi, sg);
        if f32row {
            fs[i] = f32of(a, lo, hi, sg);
        }
        ys[i] = if name == "pow" { f64of(b, -4, 4, true) } else { f64of(b, lo, hi, sg) };
        zs[i] = f64of(c, lo, hi, sg);
    }
    let mut acc: u64 = 0;
    for r in 0..reps {
        let s = match name {
            "exp" => loop1(&xs, r, f64::exp),
            "log" => loop1(&xs, r, f64::ln),
            "sin" | "sinlarge" => loop1(&xs, r, f64::sin),
            "sinh" => loop1(&xs, r, f64::sinh),
            "cbrt" => loop1(&xs, r, f64::cbrt),
            "pow" => {
                let mut s: u64 = 0;
                for i in 0..N {
                    let k = (i + r) & (N - 1);
                    s = s.wrapping_add(xs[k].powf(ys[k]).to_bits() >> 32);
                }
                s
            }
            "atan2" => {
                let mut s: u64 = 0;
                for i in 0..N {
                    let k = (i + r) & (N - 1);
                    s = s.wrapping_add(ys[k].atan2(xs[k]).to_bits() >> 32);
                }
                s
            }
            "fma" => {
                let mut s: u64 = 0;
                for i in 0..N {
                    let k = (i + r) & (N - 1);
                    s = s.wrapping_add(xs[k].mul_add(ys[k], zs[k]).to_bits() >> 32);
                }
                s
            }
            "expf" => loopf(&fs, r, f32::exp),
            "logf" => loopf(&fs, r, f32::ln),
            "sinf" => loopf(&fs, r, f32::sin),
            _ => 0,
        };
        acc = acc.wrapping_add(s);
    }
    println!("{}", acc);
}
