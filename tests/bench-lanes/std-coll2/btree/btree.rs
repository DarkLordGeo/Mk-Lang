// btree, Rust, std::collections::BTreeMap: the workload of README.md.
use std::collections::BTreeMap;
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
    let mut t: BTreeMap<u64, u64> = BTreeMap::new();
    let mut s = seed;
    for _ in 0..n {
        s = s.wrapping_add(0x9E3779B97F4A7C15);
        let k = mix(s) >> 1;
        t.insert(k, k.wrapping_mul(3));
    }
    let mut sum: u64 = 0;
    let mut q = seed;
    for i in 0..n {
        q = q.wrapping_add(0x9E3779B97F4A7C15);
        let mut k = mix(q) >> 1;
        if i & 1 == 1 {
            k = mix(q.wrapping_add(7)) >> 1;
        }
        if let Some(v) = t.get(&k) {
            sum = sum.wrapping_add(*v);
        }
    }
    for (k, _) in t.iter() {
        sum = sum.wrapping_mul(31).wrapping_add(*k);
    }
    s = seed;
    for _ in 0..n {
        s = s.wrapping_add(0x9E3779B97F4A7C15);
        let k = mix(s) >> 1;
        if let Some(v) = t.remove(&k) {
            sum = sum.wrapping_add(v);
        }
    }
    let mut out = std::io::stdout().lock();
    writeln!(out, "{} {}", sum, t.len()).unwrap();
}
