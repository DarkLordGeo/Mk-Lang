// digits, Rust, idiomatic std: write!("{:e}") (variant shortest; core's
// flt2dec, Grisu with a Dragon fallback, shortest round-trip) or "{:.16e}"
// (variant exact17, the exact value correctly rounded) or "{:.6}" (variant
// fixed, the exact value to six places) into a reused
// String over 4,096 positive f64 inputs built from splitmix64 bits
// (README.md), reps passes, pass r starting at input r. The digits and
// exponent are read back from the text, trailing zeros dropped, and folded
// as every column folds them.
// Usage: digits <variant> <reps> <seed>.
use std::fmt::Write;

const N: usize = 4096;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
    z ^ (z >> 31)
}

fn fold(b: &[u8]) -> u64 {
    let k = b.iter().position(|&c| c == b'e').unwrap_or(b.len());
    let mut last = 0;
    for i in 0..k {
        if b[i] != b'.' && b[i] != b'0' {
            last = i + 1;
        }
    }
    let (mut len, mut sum) = (0u64, 0u64);
    for (i, &c) in b[..last].iter().enumerate() {
        if c != b'.' {
            len += 1;
            if i + 1 < last {
                sum += (c - b'0') as u64;
            }
        }
    }
    let e: i64 = std::str::from_utf8(&b[k + 1..]).unwrap().parse().unwrap();
    len.wrapping_mul(1000003).wrapping_add(((e + 400) as u64).wrapping_mul(1009)).wrapping_add(sum)
}

// fold_f reads ddd.dddddd: the digits with leading and trailing zeros
// dropped, the exponent from the point's place.
fn fold_f(b: &[u8]) -> u64 {
    let k = b.iter().position(|&c| c == b'.').unwrap_or(b.len()) as i64;
    let ds: Vec<u8> = b.iter().copied().filter(|&c| c != b'.').collect();
    let lo = ds.iter().take_while(|&&c| c == b'0').count();
    let dp = k - lo as i64;
    let mut hi = ds.len();
    while hi > lo && ds[hi - 1] == b'0' {
        hi -= 1;
    }
    if hi == lo {
        return 400 * 1009;
    }
    let sum: u64 = ds[lo..hi - 1].iter().map(|&c| (c - b'0') as u64).sum();
    ((hi - lo) as u64).wrapping_mul(1000003).wrapping_add(((dp - 1 + 400) as u64).wrapping_mul(1009)).wrapping_add(sum)
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 4 {
        eprintln!("usage: digits <variant> <reps> <seed>");
        std::process::exit(2);
    }
    let exact = args[1] == "exact17";
    let fixed = args[1] == "fixed";
    let reps: usize = args[2].parse().unwrap();
    let mut st: u64 = args[3].parse().unwrap();
    let mut xs = vec![0f64; N];
    for x in xs.iter_mut() {
        st = st.wrapping_add(0x9E3779B97F4A7C15);
        let z = mix(st);
        let e = if fixed { 1003 + (z >> 53) % 81 } else { 723 + (z >> 53) % 601 };
        *x = f64::from_bits(e << 52 | (z & 0x000FFFFFFFFFFFFF));
    }
    let mut acc: u64 = 0;
    let mut s = String::with_capacity(64);
    for r in 0..reps {
        for i in 0..N {
            s.clear();
            let x = xs[(i + r) & (N - 1)];
            if fixed {
                write!(s, "{:.6}", x).unwrap();
                acc = acc.wrapping_add(fold_f(s.as_bytes()));
                continue;
            } else if exact {
                write!(s, "{:.16e}", x).unwrap();
            } else {
                write!(s, "{:e}", x).unwrap();
            }
            acc = acc.wrapping_add(fold(s.as_bytes()));
        }
    }
    println!("{}", acc);
}
