// fmt, Rust, idiomatic std: the task of README.md with write! into a
// String (Display: the shortest digits that round-trip for f64) and
// str::parse::<i64>() and ::<f64>(). Usage: fmt <n> <rounds> <seed>.
use std::fmt::Write;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: fmt <n> <rounds> <seed>");
        std::process::exit(2);
    }
    let n: u64 = a[1].parse().unwrap();
    let rounds: u64 = a[2].parse().unwrap();
    let mut st: u64 = a[3].parse().unwrap();
    let mut s = String::with_capacity(64);
    let mut sum: i64 = 0;
    let mut ok: i64 = 0;
    for _ in 0..rounds {
        for _ in 0..n {
            st = st.wrapping_add(0x9e3779b97f4a7c15);
            let z = mix(st);
            let mut x = (z >> 1) as i64;
            if z & 1 == 1 {
                x = -x;
            }
            st = st.wrapping_add(0x9e3779b97f4a7c15);
            let f = (mix(st) >> 11) as f64 * (1.0 / 9007199254740992.0) * 1e6;
            s.clear();
            write!(s, "{}", x).unwrap();
            let y: i64 = s.parse().unwrap();
            s.clear();
            write!(s, "{}", f).unwrap();
            let g: f64 = s.parse().unwrap();
            sum = sum.wrapping_add(y).wrapping_add(g as i64);
            if g == f {
                ok += 1;
            }
        }
    }
    println!("{}", sum.wrapping_add(ok));
}
