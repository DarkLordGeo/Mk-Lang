// atomicadd, Rust, idiomatic std: the workload of README.md with
// std::sync::atomic::AtomicI64 over t OS threads (std::thread::scope;
// labelled threads). Each thread does n fetch_add(1, SeqCst) on one shared
// counter (SeqCst, as Mk's AtomicInt; on x86-64 every order of an RMW is
// the same lock-prefixed instruction). Prints the counter (t * n).
// Usage: atomicadd <n> <t>.
use std::sync::atomic::{AtomicI64, Ordering};

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: atomicadd <n> <t>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let t: usize = args[2].parse().unwrap();
    let c = AtomicI64::new(0);
    std::thread::scope(|s| {
        for _ in 0..t {
            let c = &c;
            s.spawn(move || {
                for _ in 0..n {
                    c.fetch_add(1, Ordering::SeqCst);
                }
            });
        }
    });
    println!("{}", c.load(Ordering::SeqCst));
}
