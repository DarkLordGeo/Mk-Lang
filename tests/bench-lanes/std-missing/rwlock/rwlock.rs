// rwlock, Rust, idiomatic std: the workload of README.md with
// std::sync::RwLock and t OS threads (std::thread::scope; Rust's std has
// threads, not tasks, so the cell is labelled threads). Each thread does n
// operations on one shared pair: every tenth takes the write lock and adds
// one to both fields, the others take the read lock and count a pair whose
// fields differ. Prints the final first field and the mismatches.
// Usage: rwlock <n> <t>.
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::RwLock;

struct Pair {
    a: u64,
    b: u64,
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: rwlock <n> <t>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let t: usize = args[2].parse().unwrap();
    let l = RwLock::new(Pair { a: 0, b: 0 });
    let bad = AtomicI64::new(0);
    std::thread::scope(|s| {
        for _ in 0..t {
            s.spawn(|| {
                let mut miss: i64 = 0;
                for i in 0..n {
                    if i % 10 == 0 {
                        let mut g = l.write().unwrap();
                        g.a += 1;
                        g.b += 1;
                    } else {
                        let g = l.read().unwrap();
                        if g.a != g.b {
                            miss += 1;
                        }
                    }
                }
                bad.fetch_add(miss, Ordering::Relaxed);
            });
        }
    });
    let p = l.into_inner().unwrap();
    println!("{} {}", p.a, bad.load(Ordering::Relaxed));
}
