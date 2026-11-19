// deque, Rust, std::collections::VecDeque: the workload of README.md.
use std::collections::VecDeque;
use std::io::Write;

fn mix(x: u64) -> u64 {
    let mut z = x;
    z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
    z ^ (z >> 31)
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let n: usize = args[1].parse().unwrap();
    let seed: u64 = args[2].parse().unwrap();
    let mut d: VecDeque<u64> = VecDeque::new();
    let mut s = seed;
    let mut sum: u64 = 0;
    for i in 0..n {
        s = s.wrapping_add(0x9E3779B97F4A7C15);
        let x = mix(s);
        if i & 15 == 15 {
            d.push_front(x);
            if d.len() > 1000 {
                sum = sum.wrapping_mul(31).wrapping_add(d.pop_back().unwrap_or(0));
            }
        } else {
            d.push_back(x);
            if d.len() > 1000 {
                sum = sum.wrapping_mul(31).wrapping_add(d.pop_front().unwrap_or(0));
            }
        }
    }
    let mut out = std::io::stdout().lock();
    writeln!(out, "{} {}", sum, d.len()).unwrap();
}
