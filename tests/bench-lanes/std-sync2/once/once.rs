// once, Rust, idiomatic std: the workload of README.md with
// std::sync::OnceLock and t OS threads (std::thread::scope; labelled
// threads). Each thread does n rounds of get_or_init, whose closure
// returns 7 the first time, and adds the value. Prints the sum.
// Usage: once <n> <t>.
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::OnceLock;

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: once <n> <t>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let t: usize = args[2].parse().unwrap();
    let o: OnceLock<i64> = OnceLock::new();
    let sum = AtomicI64::new(0);
    std::thread::scope(|s| {
        for _ in 0..t {
            s.spawn(|| {
                let mut k: i64 = 0;
                for _ in 0..n {
                    k += *o.get_or_init(|| 7);
                }
                sum.fetch_add(k, Ordering::Relaxed);
            });
        }
    });
    println!("{}", sum.load(Ordering::Relaxed));
}
