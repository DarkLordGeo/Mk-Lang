// mapiter, Rust, idiomatic std: the task of README.md with
// std::collections::HashMap<u64, u64> (hashbrown, SipHash-1-3 under a
// random key; iteration does not hash), filled once with insert, then
// iterated <passes> times with `for (k, v) in &m`; the sum is order-free.
// Usage: mapiter <n> <passes> <seed>.
use std::collections::HashMap;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: mapiter <n> <passes> <seed>");
        std::process::exit(2);
    }
    let n: u64 = a[1].parse().unwrap();
    let passes: u64 = a[2].parse().unwrap();
    let mut st: u64 = a[3].parse().unwrap();
    let mut m: HashMap<u64, u64> = HashMap::new();
    for i in 0..n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        m.insert(mix(st), i);
    }
    let mut total: u64 = 0;
    for _ in 0..passes {
        for (k, v) in &m {
            total = total.wrapping_add(k.wrapping_mul(v.wrapping_add(1)));
        }
        total = total.wrapping_add(m.len() as u64);
    }
    println!("{}", total);
}
