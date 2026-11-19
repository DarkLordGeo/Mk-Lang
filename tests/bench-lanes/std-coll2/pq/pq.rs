// pq, Rust, std::collections::BinaryHeap with Reverse (a min-heap): the
// workload of README.md.
use std::cmp::Reverse;
use std::collections::BinaryHeap;
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
    let mut q = BinaryHeap::new();
    let mut s = seed;
    for _ in 0..n {
        s = s.wrapping_add(0x9E3779B97F4A7C15);
        q.push(Reverse(mix(s)));
    }
    let mut sum: u64 = 0;
    while let Some(Reverse(x)) = q.pop() {
        sum = sum.wrapping_mul(31).wrapping_add(x);
    }
    let mut out = std::io::stdout().lock();
    writeln!(out, "{}", sum).unwrap();
}
