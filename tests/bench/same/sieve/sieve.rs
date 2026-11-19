// sieve, Rust: the reference algorithm of README.md.
// A Vec<u8> of n + 1 flags indexed with flags[i] (bounds checked). usize
// indices (the type Rust indexes with), i64 counters like C's int64_t: +
// wraps in "rust" and traps in "rust-ovf". No unsafe, no crates.

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 3 {
        eprintln!("usage: sieve <n> <rounds>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let rounds: usize = a[2].parse().unwrap();
    let mut flags: Vec<u8> = vec![0; n + 1];
    let mut total: i64 = 0;
    let mut marks: u64 = 0;
    for _ in 0..rounds {
        for i in 0..n + 1 {
            flags[i] = 1;
        }
        let mut count: i64 = 0;
        for i in 2..n + 1 {
            if flags[i] != 0 {
                count += 1;
                let mut j = i * i;
                while j <= n {
                    flags[j] = 0;
                    if cfg!(bench_count) {
                        marks += 1;
                    }
                    j += i;
                }
            }
        }
        total += count;
    }
    println!("{}", total);
    if cfg!(bench_count) {
        // --cfg bench_count builds the counting variant.
        println!("counts marks={}", marks);
    }
}
